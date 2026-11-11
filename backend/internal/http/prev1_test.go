package http_test

// Course roles, people search scope, archive, quiz reset, erasure,
// retention, settings, rate limits, media sweeping and path-based group
// links. Same DB-backed setup (and TEST_DATABASE_URL warning) as
// courses_test.go.

import (
	"context"
	"encoding/json"
	nethttp "net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/AGlElectronics/learn/backend/internal/audit"
	"github.com/AGlElectronics/learn/backend/internal/config"
	"github.com/AGlElectronics/learn/backend/internal/denlink"
	httpx "github.com/AGlElectronics/learn/backend/internal/http"
	"github.com/AGlElectronics/learn/backend/internal/keycloakadmin"
	"github.com/AGlElectronics/learn/backend/internal/learn"
)

var (
	manager = identity{user: "tina-manager", groups: "learn-manager"}
	cora    = identity{user: "cora-coordinator", groups: "learn-coordinator"}
)

// auditActions returns the actions of the audit entries the caller sees
// for query (e.g. "course=<id>").
func auditActions(t *testing.T, h nethttp.Handler, query string) map[string]int {
	t.Helper()
	code, out := call(t, h, admin, "GET", "/api/v1/audit?limit=200&"+query, nil)
	mustStatus(t, code, 200, "read audit log")
	got := map[string]int{}
	for _, e := range arr(out, "entries") {
		got[str(obj(e), "action")]++
	}
	return got
}

func newSectionedCourse(t *testing.T, h nethttp.Handler, by identity, title string) (courseID, sectionID string) {
	t.Helper()
	code, course := call(t, h, by, "POST", "/api/v1/courses", map[string]any{"title": title})
	mustStatus(t, code, 201, "create course "+title)
	code, section := call(t, h, by, "POST", "/api/v1/courses/"+str(course, "id")+"/sections", map[string]any{"title": "S"})
	mustStatus(t, code, 201, "create section")
	return str(course, "id"), str(section, "id")
}

func TestCourseRoles(t *testing.T) {
	h, _ := setupRouterWith(t, nil)
	ids := signIn(t, h, admin, trainerA, trainerB, learner, manager, cora)
	courseID, _ := newSectionedCourse(t, h, trainerA, "Stage safety")
	enrolments := "/api/v1/courses/" + courseID + "/enrolments"

	code, _ := call(t, h, trainerA, "POST", enrolments, map[string]any{"user_id": ids[trainerB.user], "role": "trainer"})
	mustStatus(t, code, 403, "a trainer makes someone a trainer")
	code, lou := call(t, h, trainerA, "POST", enrolments, map[string]any{"user_id": ids[learner.user]})
	mustStatus(t, code, 201, "a trainer enrols a learner")
	if _, has := lou["email"]; has {
		t.Fatalf("a trainer sees the learner's email: %v", lou)
	}

	code, staff := call(t, h, manager, "GET", "/api/v1/course-staff?q=stage", nil)
	mustStatus(t, code, 200, "manager lists course staff")
	courses := arr(staff, "courses")
	if len(courses) != 1 || len(arr(obj(courses[0]), "trainers")) != 1 {
		t.Fatalf("course staff = %v", staff)
	}
	code, _ = call(t, h, trainerA, "GET", "/api/v1/course-staff", nil)
	mustStatus(t, code, 403, "a trainer lists course staff")

	code, bee := call(t, h, manager, "POST", enrolments, map[string]any{"user_id": ids[trainerB.user], "role": "trainer"})
	mustStatus(t, code, 201, "manager adds a trainer")
	if str(bee, "role") != "trainer" {
		t.Fatalf("enrolment = %v", bee)
	}
	code, _ = call(t, h, manager, "PATCH", "/api/v1/courses/"+courseID, map[string]any{"title": "Hijack"})
	if code == 200 {
		t.Fatal("a training manager edits course content")
	}

	code, list := call(t, h, trainerA, "GET", enrolments, nil)
	mustStatus(t, code, 200, "trainer lists enrolments")
	var trainerAEnrolment string
	for _, e := range arr(list, "enrolments") {
		if _, has := obj(e)["email"]; has {
			t.Fatalf("a trainer sees emails in the enrolment list: %v", e)
		}
		if str(obj(e), "user_id") == ids[trainerA.user] {
			trainerAEnrolment = str(obj(e), "id")
		}
	}
	code, list = call(t, h, admin, "GET", enrolments, nil)
	mustStatus(t, code, 200, "admin lists enrolments")
	for _, e := range arr(list, "enrolments") {
		if str(obj(e), "email") == "" {
			t.Fatalf("admin should see emails: %v", e)
		}
	}

	code, _ = call(t, h, trainerA, "DELETE", "/api/v1/enrolments/"+str(bee, "id"), nil)
	mustStatus(t, code, 403, "a trainer removes a co-trainer")
	code, _ = call(t, h, manager, "DELETE", "/api/v1/enrolments/"+str(bee, "id"), nil)
	mustStatus(t, code, 204, "manager removes a trainer")
	code, out := call(t, h, manager, "DELETE", "/api/v1/enrolments/"+trainerAEnrolment, nil)
	mustStatus(t, code, 409, "removing the last trainer")
	if !strings.Contains(str(obj(out["error"]), "message"), "last trainer") {
		t.Fatalf("last trainer error: %v", out)
	}

	code, promoted := call(t, h, manager, "POST", enrolments, map[string]any{"user_id": ids[learner.user], "role": "trainer"})
	mustStatus(t, code, 201, "manager makes a learner a trainer")
	if str(promoted, "id") != str(lou, "id") || str(promoted, "role") != "trainer" {
		t.Fatalf("promotion = %v, want the same enrolment as trainer", promoted)
	}
	code, _ = call(t, h, manager, "DELETE", "/api/v1/enrolments/"+trainerAEnrolment, nil)
	mustStatus(t, code, 204, "remove a trainer who is no longer the last")

	code, _ = call(t, h, trainerA, "GET", "/api/v1/audit", nil)
	mustStatus(t, code, 403, "a trainer reads the audit log")
	code, _ = call(t, h, cora, "GET", "/api/v1/audit", nil)
	mustStatus(t, code, 403, "a coordinator reads the audit log")
	got := auditActions(t, h, "course="+courseID)
	if got[audit.EnrolmentAdded] != 3 || got[audit.EnrolmentRemoved] != 2 {
		t.Fatalf("audit = %v, want 3 added and 2 removed", got)
	}
	code, page := call(t, h, admin, "GET", "/api/v1/audit?course="+courseID+"&limit=2", nil)
	mustStatus(t, code, 200, "first audit page")
	if len(arr(page, "entries")) != 2 || page["next_before"] == nil {
		t.Fatalf("audit page = %v", page)
	}
}

func TestCoordinatorManagesQualifications(t *testing.T) {
	h, _ := setupRouterWith(t, nil)
	signIn(t, h, admin, trainerA, cora)
	code, q := call(t, h, cora, "POST", "/api/v1/qualifications", map[string]any{"key": "hv", "name": "High voltage"})
	mustStatus(t, code, 201, "coordinator creates a qualification")
	code, _ = call(t, h, cora, "PATCH", "/api/v1/qualifications/"+str(q, "id"), map[string]any{"name": "HV"})
	mustStatus(t, code, 200, "coordinator edits it")
	code, _ = call(t, h, cora, "GET", "/api/v1/settings", nil)
	mustStatus(t, code, 403, "coordinator opens module settings")
	code, _ = call(t, h, trainerA, "PATCH", "/api/v1/qualifications/"+str(q, "id"), map[string]any{"name": "X"})
	mustStatus(t, code, 403, "trainer edits a qualification")
	if got := auditActions(t, h, "target_type=qualification"); got[audit.QualificationSaved] != 2 {
		t.Fatalf("audit = %v", got)
	}
}

func TestPeopleSearchScope(t *testing.T) {
	h, pool := setupRouterWith(t, nil)
	ids := signIn(t, h, admin, trainerA, learner)

	code, out := call(t, h, trainerA, "GET", "/api/v1/users?q=lou", nil)
	mustStatus(t, code, 200, "trainer searches people")
	people := arr(out, "people")
	if len(people) != 1 || str(obj(people[0]), "display_name") != "Learner Lou" {
		t.Fatalf("people = %v", out)
	}
	if _, has := obj(people[0])["email"]; has {
		t.Fatalf("a trainer sees emails in search: %v", people[0])
	}
	if str(out, "source") != "learn" {
		t.Fatalf("source = %v, want learn (no directory)", out["source"])
	}
	code, out = call(t, h, trainerA, "GET", "/api/v1/users?q=@learn.local", nil)
	mustStatus(t, code, 200, "trainer searches by email")
	if len(arr(out, "people")) != 0 {
		t.Fatalf("a trainer can search by email: %v", out)
	}
	code, out = call(t, h, admin, "GET", "/api/v1/users?q=lou", nil)
	mustStatus(t, code, 200, "admin searches people")
	if str(obj(arr(out, "people")[0]), "email") != "learner-lou@learn.local" {
		t.Fatalf("admin people = %v", out)
	}
	code, _ = call(t, h, learner, "GET", "/api/v1/users?q=", nil)
	mustStatus(t, code, 403, "a learner searches people")

	var strangerID string
	if err := pool.QueryRow(context.Background(), `
		WITH o AS (INSERT INTO organizations (slug, display_name) VALUES ('other-org', 'Other') RETURNING id),
		     u AS (INSERT INTO users (issuer, subject, display_name) VALUES ('dev', 'stranger', 'Stranger Sam') RETURNING id)
		INSERT INTO user_organizations (user_id, organization_id) SELECT u.id, o.id FROM u, o RETURNING user_id::text`).
		Scan(&strangerID); err != nil {
		t.Fatalf("other org: %v", err)
	}
	code, out = call(t, h, admin, "GET", "/api/v1/users?q=stranger", nil)
	mustStatus(t, code, 200, "search another org's person")
	if len(arr(out, "people")) != 0 {
		t.Fatalf("people of another organization are visible: %v", out)
	}
	courseID, _ := newSectionedCourse(t, h, trainerA, "Scope")
	code, _ = call(t, h, trainerA, "POST", "/api/v1/courses/"+courseID+"/enrolments", map[string]any{"user_id": strangerID})
	mustStatus(t, code, 404, "enrol a person of another organization")
	code, _ = call(t, h, trainerA, "POST", "/api/v1/courses/"+courseID+"/enrolments", map[string]any{"user_id": ids[learner.user]})
	mustStatus(t, code, 201, "enrol a person of the organization")
}

func TestCourseArchive(t *testing.T) {
	h, _ := setupRouterWith(t, nil)
	ids := signIn(t, h, admin, trainerA, learner)
	courseID, sectionID := newSectionedCourse(t, h, trainerA, "Old edition")
	code, _ := call(t, h, trainerA, "POST", "/api/v1/sections/"+sectionID+"/activities", map[string]any{"title": "P", "body": "B"})
	mustStatus(t, code, 201, "page")
	code, _ = call(t, h, trainerA, "POST", "/api/v1/courses/"+courseID+"/publish", nil)
	mustStatus(t, code, 200, "publish")
	code, _ = call(t, h, trainerA, "POST", "/api/v1/courses/"+courseID+"/enrolments", map[string]any{"user_id": ids[learner.user]})
	mustStatus(t, code, 201, "enrol")

	code, _ = call(t, h, learner, "POST", "/api/v1/courses/"+courseID+"/archive", nil)
	mustStatus(t, code, 403, "a learner archives")
	code, c := call(t, h, trainerA, "POST", "/api/v1/courses/"+courseID+"/archive", nil)
	mustStatus(t, code, 200, "trainer archives")
	if c["archived_at"] == nil {
		t.Fatalf("archived course = %v", c)
	}
	code, _ = call(t, h, learner, "GET", "/api/v1/courses/"+courseID, nil)
	mustStatus(t, code, 404, "a learner opens an archived course")
	_, mine := call(t, h, learner, "GET", "/api/v1/courses/mine", nil)
	if len(arr(mine, "courses")) != 0 {
		t.Fatalf("archived course in my courses: %v", mine)
	}
	code, _ = call(t, h, trainerA, "GET", "/api/v1/courses/"+courseID, nil)
	mustStatus(t, code, 200, "staff read an archived course")
	code, out := call(t, h, trainerA, "PATCH", "/api/v1/courses/"+courseID, map[string]any{"title": "New"})
	mustStatus(t, code, 409, "edit an archived course")
	if errCode(out) != "archived" {
		t.Fatalf("error = %v, want archived", out)
	}
	code, _ = call(t, h, trainerA, "POST", "/api/v1/sections/"+sectionID+"/activities", map[string]any{"title": "Q", "body": "B"})
	mustStatus(t, code, 409, "add content to an archived course")
	_, teach := call(t, h, trainerA, "GET", "/api/v1/courses/teach", nil)
	if len(arr(teach, "courses")) != 0 {
		t.Fatalf("archived course in the teach list: %v", teach)
	}
	_, teach = call(t, h, trainerA, "GET", "/api/v1/courses/teach?archived=true", nil)
	if len(arr(teach, "courses")) != 1 {
		t.Fatalf("archived list: %v", teach)
	}

	code, _ = call(t, h, trainerA, "POST", "/api/v1/courses/"+courseID+"/unarchive", nil)
	mustStatus(t, code, 200, "unarchive")
	code, _ = call(t, h, learner, "GET", "/api/v1/courses/"+courseID, nil)
	mustStatus(t, code, 200, "the learner is back in")
	got := auditActions(t, h, "course="+courseID)
	if got[audit.CourseArchived] != 1 || got[audit.CourseUnarchived] != 1 || got[audit.CoursePublished] != 1 {
		t.Fatalf("audit = %v", got)
	}
}

func TestQuizResetKeepsPastCycles(t *testing.T) {
	h, pool := setupRouterWith(t, nil)
	ids := signIn(t, h, admin, trainerA, learner)
	courseID, sectionID := newSectionedCourse(t, h, trainerA, "Recert")
	code, quiz := call(t, h, trainerA, "POST", "/api/v1/sections/"+sectionID+"/activities",
		map[string]any{"kind": "quiz", "title": "Final check"})
	mustStatus(t, code, 201, "quiz")
	quizID := str(quiz, "id")
	code, _ = call(t, h, trainerA, "POST", "/api/v1/courses/"+courseID+"/enrolments", map[string]any{"user_id": ids[learner.user]})
	mustStatus(t, code, 201, "enrol")

	lou := ids[learner.user]
	exec(t, pool, `INSERT INTO quiz_attempts (activity_id, user_id, cycle, submitted_at, score, max_score, passed)
	               VALUES ($1, $2, 1, now(), 1, 1, true)`, quizID, lou)
	exec(t, pool, `INSERT INTO completions (activity_id, user_id, cycle) VALUES ($1, $2, 1)`, quizID, lou)
	exec(t, pool, `UPDATE enrolments SET cycle = 2 WHERE course_id = $1 AND user_id = $2`, courseID, lou)
	exec(t, pool, `INSERT INTO quiz_attempts (activity_id, user_id, cycle, submitted_at, score, max_score, passed)
	               VALUES ($1, $2, 2, now(), 1, 1, true)`, quizID, lou)
	exec(t, pool, `INSERT INTO completions (activity_id, user_id, cycle) VALUES ($1, $2, 2)`, quizID, lou)
	exec(t, pool, `INSERT INTO quiz_attempts (activity_id, user_id) VALUES ($1, $2)`, quizID, lou)

	code, out := call(t, h, admin, "POST", "/api/v1/activities/"+quizID+"/reset", map[string]any{"confirm": "Final check"})
	mustStatus(t, code, 200, "reset")
	if num(out, "attempts") != 2 || num(out, "completions") != 1 {
		t.Fatalf("reset = %v, want the cycle-2 and the open attempt, and one completion", out)
	}
	var attempts, completions int
	if err := pool.QueryRow(context.Background(), `
		SELECT (SELECT count(*) FROM quiz_attempts WHERE activity_id = $1 AND cycle = 1 AND submitted_at IS NOT NULL),
		       (SELECT count(*) FROM completions WHERE activity_id = $1 AND cycle = 1)`, quizID).
		Scan(&attempts, &completions); err != nil {
		t.Fatal(err)
	}
	if attempts != 1 || completions != 1 {
		t.Fatalf("cycle 1 kept %d attempts and %d completions, want 1 and 1", attempts, completions)
	}
	if got := auditActions(t, h, "action=quiz.reset"); got[audit.QuizReset] != 1 {
		t.Fatalf("audit = %v", got)
	}
}

func TestEraseKeepsCertificatesVerifying(t *testing.T) {
	h, pool := setupRouterWith(t, nil)
	quinn := identity{user: "quinn-q", groups: ""}
	ids := signIn(t, h, admin, trainerA, quinn)

	courseID, sectionID := newSectionedCourse(t, h, trainerA, "Fire safety")
	code, page := call(t, h, trainerA, "POST", "/api/v1/sections/"+sectionID+"/activities", map[string]any{"title": "Exits", "body": "B"})
	mustStatus(t, code, 201, "page")
	code, q := call(t, h, admin, "POST", "/api/v1/qualifications", map[string]any{"key": "fire", "name": "Fire warden"})
	mustStatus(t, code, 201, "qualification")
	code, _ = call(t, h, trainerA, "PUT", "/api/v1/courses/"+courseID+"/qualification", map[string]any{"qualification_id": str(q, "id")})
	mustStatus(t, code, 200, "award it")
	code, _ = call(t, h, trainerA, "PATCH", "/api/v1/courses/"+courseID, map[string]any{"self_enrol": true})
	mustStatus(t, code, 200, "self enrol")
	code, _ = call(t, h, trainerA, "POST", "/api/v1/courses/"+courseID+"/publish", nil)
	mustStatus(t, code, 200, "publish")
	code, _ = call(t, h, quinn, "POST", "/api/v1/courses/"+courseID+"/enrol", nil)
	mustStatus(t, code, 201, "quinn enrols herself")
	code, _ = call(t, h, quinn, "PUT", "/api/v1/activities/"+str(page, "id")+"/completion", map[string]any{"completed": true})
	mustStatus(t, code, 200, "complete")
	_, mine := call(t, h, quinn, "GET", "/api/v1/me/qualifications", nil)
	quals := arr(mine, "qualifications")
	if len(quals) != 1 {
		t.Fatalf("quinn's qualifications = %v", mine)
	}
	verifyCode := str(obj(quals[0]), "verify_code")

	erase := "/api/v1/users/" + ids[quinn.user] + "/erase"
	code, _ = call(t, h, trainerA, "POST", erase, map[string]any{"confirm": "ERASE"})
	mustStatus(t, code, 403, "a trainer erases someone")
	code, _ = call(t, h, admin, "POST", erase, map[string]any{"confirm": "yes"})
	mustStatus(t, code, 422, "erase without the confirmation")
	code, _ = call(t, h, admin, "POST", erase, map[string]any{"confirm": "ERASE"})
	mustStatus(t, code, 200, "admin erases quinn")

	code, v := rawCall(t, h, "GET", "/api/public/verify/"+verifyCode, nil)
	mustStatus(t, code, 200, "verify after erasure")
	if str(v, "status") != "valid" || str(v, "display_name") != learn.ErasedDisplayName {
		t.Fatalf("verify = %v", v)
	}
	var email *string
	var subject, actorName string
	if err := pool.QueryRow(context.Background(), `
		SELECT u.email, u.subject, (SELECT actor_name FROM audit_log WHERE actor_user_id = u.id AND action = 'enrolment.self')
		  FROM users u WHERE u.id = $1`, ids[quinn.user]).Scan(&email, &subject, &actorName); err != nil {
		t.Fatal(err)
	}
	if email != nil || !strings.HasPrefix(subject, "erased:") || actorName != learn.ErasedActorName {
		t.Fatalf("erased row: email %v, subject %q, audit actor %q", email, subject, actorName)
	}
	again := signIn(t, h, quinn)
	if again[quinn.user] == ids[quinn.user] {
		t.Fatal("signing in again reused the erased person")
	}
	if got := auditActions(t, h, "action=user.erased"); got[audit.UserErased] != 1 {
		t.Fatalf("audit = %v", got)
	}
}

func TestSettingsAndRetention(t *testing.T) {
	h, pool := setupRouterWith(t, nil)
	ids := signIn(t, h, admin, trainerA, learner)

	code, s := call(t, h, admin, "GET", "/api/v1/settings", nil)
	mustStatus(t, code, 200, "settings")
	ret := obj(s["retention"])
	if str(ret, "source") != "default" || ret["pending_legal_review"] != true || num(ret, "inactive_user_months") != 36 {
		t.Fatalf("default retention = %v", ret)
	}
	if obj(s["telegram"])["configured"] != false {
		t.Fatalf("telegram = %v", s["telegram"])
	}
	code, _ = call(t, h, trainerA, "GET", "/api/v1/settings", nil)
	mustStatus(t, code, 403, "a trainer opens settings")
	code, _ = call(t, h, admin, "PUT", "/api/v1/settings", map[string]any{"retention": map[string]any{"inactive_user_months": -1, "audit_log_months": 1}})
	mustStatus(t, code, 422, "negative retention")
	code, s = call(t, h, admin, "PUT", "/api/v1/settings", map[string]any{
		"telegram_enabled": false, "retention": map[string]any{"inactive_user_months": 12, "audit_log_months": 6}})
	mustStatus(t, code, 200, "save settings")
	if ret := obj(s["retention"]); str(ret, "source") != "learn" || num(ret, "inactive_user_months") != 12 {
		t.Fatalf("saved retention = %v", ret)
	}
	if obj(s["telegram"])["enabled"] != false {
		t.Fatalf("telegram switch = %v", s["telegram"])
	}

	exec(t, pool, `UPDATE users SET last_login_at = now() - interval '13 months' WHERE id = $1`, ids[learner.user])
	exec(t, pool, `INSERT INTO audit_log (action, at) VALUES ('old.entry', now() - interval '7 months')`)
	res, err := learn.NewService(pool).ApplyRetention(context.Background(), audit.New(pool, zerolog.Nop()), nil, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if res.Anonymised != 1 || res.AuditDeleted < 1 || res.Source != "learn" {
		t.Fatalf("retention = %+v", res)
	}
	var name string
	if err := pool.QueryRow(context.Background(), `SELECT display_name FROM users WHERE id = $1`, ids[learner.user]).Scan(&name); err != nil || name != learn.ErasedDisplayName {
		t.Fatalf("inactive learner = %q (%v)", name, err)
	}
	if got := auditActions(t, h, "action=users.anonymised"); got[audit.UsersAnonymised] != 1 {
		t.Fatalf("audit = %v", got)
	}
}

func TestRetentionFromDen(t *testing.T) {
	h, _ := setupRouterWith(t, func(d *httpx.Deps) {
		bundle := `{"brand":{"organization_name":"Con"},"settings":{"data":{"retention":{"inactive_user_months":18,"audit_log_months":3}}}}`
		if _, err := d.Pool.Exec(context.Background(), `
			INSERT INTO den_link (den_base_url, credential, bundle) VALUES ('https://den.test', 'cred', $1)`, bundle); err != nil {
			t.Fatal(err)
		}
		den := denlink.New(d.Pool, denlink.Config{Slug: "learn"}, zerolog.Nop())
		if err := den.Restore(context.Background()); err != nil {
			t.Fatal(err)
		}
		d.Den = den
	})
	signIn(t, h, admin)
	code, s := call(t, h, admin, "GET", "/api/v1/settings", nil)
	mustStatus(t, code, 200, "settings")
	ret := obj(s["retention"])
	if str(ret, "source") != "den" || ret["editable"] != false || num(ret, "inactive_user_months") != 18 || num(ret, "audit_log_months") != 3 {
		t.Fatalf("retention from Den = %v", ret)
	}
	code, _ = call(t, h, admin, "PUT", "/api/v1/settings", map[string]any{"retention": map[string]any{"inactive_user_months": 1, "audit_log_months": 1}})
	mustStatus(t, code, 409, "change retention while Den decides")
	code, _ = call(t, h, admin, "PUT", "/api/v1/settings", map[string]any{"telegram_enabled": false})
	mustStatus(t, code, 200, "the Telegram switch stays Learn's")
}

// countLimiter allows the first n requests per key.
type countLimiter struct {
	mu   sync.Mutex
	n    int
	seen map[string]int
}

func (l *countLimiter) Allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.seen[key]++
	return l.seen[key] <= l.n
}

func TestRateLimitHooks(t *testing.T) {
	limiter := &countLimiter{n: 1, seen: map[string]int{}}
	h, _ := setupRouterWith(t, func(d *httpx.Deps) {
		d.Limiters = map[httpx.LimitName]httpx.Limiter{httpx.LimitPeopleSearch: limiter, httpx.LimitSelfEnrol: limiter}
	})
	signIn(t, h, trainerA, admin)
	code, _ := call(t, h, trainerA, "GET", "/api/v1/users?q=a", nil)
	mustStatus(t, code, 200, "first search")
	code, out := call(t, h, trainerA, "GET", "/api/v1/users?q=b", nil)
	mustStatus(t, code, 429, "second search in the window")
	if errCode(out) != "rate_limited" {
		t.Fatalf("error = %v", out)
	}
	code, _ = call(t, h, admin, "GET", "/api/v1/users?q=a", nil)
	mustStatus(t, code, 200, "limits are per person")
}

func TestMediaSweeper(t *testing.T) {
	store := newFakeStore()
	h, pool := setupRouterWith(t, func(d *httpx.Deps) { d.Files = store })
	signIn(t, h, trainerA)
	courseID, _ := newSectionedCourse(t, h, trainerA, "Sweep")
	code, ticket := call(t, h, trainerA, "POST", "/api/v1/courses/"+courseID+"/media/uploads",
		map[string]any{"filename": "never.png", "content_type": "image/png", "size_bytes": 10})
	mustStatus(t, code, 201, "ticket")
	key := str(obj(ticket["fields"]), "key")
	id := str(obj(ticket["media"]), "id")
	svc := learn.NewMediaService(learn.NewService(pool), store)
	if n, err := svc.SweepPending(context.Background(), 24*time.Hour); err != nil || n != 0 {
		t.Fatalf("fresh upload swept: %d %v", n, err)
	}
	exec(t, pool, `UPDATE media SET created_at = now() - interval '25 hours' WHERE id = $1`, id)
	if n, err := svc.SweepPending(context.Background(), 24*time.Hour); err != nil || n != 1 {
		t.Fatalf("sweep = %d %v, want 1", n, err)
	}
	if len(store.removed) != 1 || store.removed[0] != key {
		t.Fatalf("removed objects = %v", store.removed)
	}
	code, _ = call(t, h, trainerA, "GET", "/api/v1/media/"+id+"/url", nil)
	mustStatus(t, code, 404, "swept upload is gone")
}

// fakeDirectory is a Keycloak realm with nested groups and people.
type fakeDirectory struct {
	// groups: full path -> member usernames (ids equal usernames).
	groups map[string][]string
	people map[string]string // username -> first name
}

func (f *fakeDirectory) handler() nethttp.Handler {
	write := func(w nethttp.ResponseWriter, r *nethttp.Request, v any) {
		if first := r.URL.Query().Get("first"); first != "" && first != "0" {
			v = []any{}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(v)
	}
	group := func(path string) map[string]any {
		children := 0
		for p := range f.groups {
			if strings.HasPrefix(p, path+"/") && !strings.Contains(p[len(path)+1:], "/") {
				children++
			}
		}
		return map[string]any{"id": strings.ReplaceAll(strings.Trim(path, "/"), "/", "."), "name": path[strings.LastIndex(path, "/")+1:],
			"path": path, "subGroupCount": children}
	}
	user := func(u string) map[string]any {
		return map[string]any{"id": u, "username": u, "email": u + "@kc.test", "firstName": f.people[u], "lastName": "Dir", "enabled": true}
	}
	mux := nethttp.NewServeMux()
	mux.HandleFunc("POST /realms/conservices/protocol/openid-connect/token", func(w nethttp.ResponseWriter, r *nethttp.Request) {
		_, _ = w.Write([]byte(`{"access_token":"tok","expires_in":300}`))
	})
	mux.HandleFunc("GET /admin/realms/conservices/groups", func(w nethttp.ResponseWriter, r *nethttp.Request) {
		out := []any{}
		for p := range f.groups {
			if strings.Count(p, "/") == 1 {
				out = append(out, group(p))
			}
		}
		write(w, r, out)
	})
	mux.HandleFunc("GET /admin/realms/conservices/groups/{id}/children", func(w nethttp.ResponseWriter, r *nethttp.Request) {
		parent := "/" + strings.ReplaceAll(r.PathValue("id"), ".", "/")
		out := []any{}
		for p := range f.groups {
			if strings.HasPrefix(p, parent+"/") && !strings.Contains(p[len(parent)+1:], "/") {
				out = append(out, group(p))
			}
		}
		write(w, r, out)
	})
	mux.HandleFunc("GET /admin/realms/conservices/groups/{id}/members", func(w nethttp.ResponseWriter, r *nethttp.Request) {
		out := []any{}
		for _, u := range f.groups["/"+strings.ReplaceAll(r.PathValue("id"), ".", "/")] {
			out = append(out, user(u))
		}
		write(w, r, out)
	})
	mux.HandleFunc("GET /admin/realms/conservices/users", func(w nethttp.ResponseWriter, r *nethttp.Request) {
		q := strings.ToLower(r.URL.Query().Get("search"))
		out := []any{}
		for u := range f.people {
			if strings.Contains(u, q) {
				out = append(out, user(u))
			}
		}
		write(w, r, out)
	})
	mux.HandleFunc("GET /admin/realms/conservices/users/{id}", func(w nethttp.ResponseWriter, r *nethttp.Request) {
		if _, ok := f.people[r.PathValue("id")]; !ok {
			w.WriteHeader(nethttp.StatusNotFound)
			return
		}
		write(w, r, user(r.PathValue("id")))
	})
	return mux
}

func setupDirectoryRouter(t *testing.T, dir *fakeDirectory, linkable []string) (nethttp.Handler, *pgxpool.Pool, *keycloakadmin.Client) {
	t.Helper()
	srv := httptest.NewServer(dir.handler())
	t.Cleanup(srv.Close)
	kc, err := keycloakadmin.New(keycloakadmin.Config{BaseURL: srv.URL, Realm: "conservices", ClientID: "learn-provisioner", ClientSecret: "s3cret"})
	if err != nil {
		t.Fatal(err)
	}
	h, pool := setupRouterWith(t, func(d *httpx.Deps) {
		d.Keycloak = kc
		d.Config.OIDCIssuerURL = "dev"
		d.Config.LinkableGroups = linkable
		d.Config.GroupDirectory = config.GroupDirectoryKeycloak
	})
	return h, pool, kc
}

func TestGroupLinksByPath(t *testing.T) {
	dir := &fakeDirectory{
		groups: map[string][]string{
			"/learn": {}, "/learn/cohort-a": {"ann", "bob"}, "/staff": {"cat"},
			"/other": {}, "/other/cohort-a": {"dan"},
		},
		people: map[string]string{"ann": "Ann", "bob": "Bob", "cat": "Cat", "dan": "Dan", "gil": "Gil"},
	}
	h, pool, kc := setupDirectoryRouter(t, dir, []string{"/learn/*"})
	signIn(t, h, admin, trainerA)
	courseID, _ := newSectionedCourse(t, h, trainerA, "Cohorts")
	code, _ := call(t, h, trainerA, "POST", "/api/v1/courses/"+courseID+"/publish", nil)
	mustStatus(t, code, 200, "publish")
	links := "/api/v1/courses/" + courseID + "/group-links"

	t.Run("trainers only see and link linkable groups", func(t *testing.T) {
		code, out := call(t, h, trainerA, "GET", "/api/v1/groups", nil)
		mustStatus(t, code, 200, "trainer lists groups")
		groups := arr(out, "groups")
		if len(groups) != 1 || str(obj(groups[0]), "path") != "/learn/cohort-a" || out["limited"] != true {
			t.Fatalf("trainer groups = %v", out)
		}
		code, out = call(t, h, admin, "GET", "/api/v1/groups", nil)
		mustStatus(t, code, 200, "admin lists groups")
		if len(arr(out, "groups")) != 5 {
			t.Fatalf("admin groups = %v", out)
		}
		code, _ = call(t, h, trainerA, "POST", links, map[string]any{"group_path": "/staff"})
		mustStatus(t, code, 403, "trainer links a group outside LEARN_LINKABLE_GROUPS")
		code, _ = call(t, h, trainerA, "POST", links, map[string]any{"group_path": "cohort-a"})
		mustStatus(t, code, 403, "a plain name is not the full path")
	})

	t.Run("links store the full path and sync the right group", func(t *testing.T) {
		code, out := call(t, h, trainerA, "POST", links, map[string]any{"group_path": "/Learn/Cohort-A"})
		mustStatus(t, code, 201, "trainer links /learn/cohort-a")
		link := obj(out["link"])
		if str(link, "group_path") != "/learn/cohort-a" || str(link, "group_name") != "learn/cohort-a" || link["path_resolved"] != true {
			t.Fatalf("link = %v", link)
		}
		if num(obj(out["sync"]), "enrolled") != 2 {
			t.Fatalf("sync = %v, want ann and bob", out["sync"])
		}
		code, _ = call(t, h, trainerA, "POST", links, map[string]any{"group_path": "/learn/nope"})
		mustStatus(t, code, 422, "a path that is not in the directory")
		code, out = call(t, h, admin, "POST", links, map[string]any{"group_path": "/other/cohort-a"})
		mustStatus(t, code, 201, "admin links a group with the same name elsewhere")
		if num(obj(out["sync"]), "enrolled") != 1 {
			t.Fatalf("sync = %v, want only dan", out["sync"])
		}
		code, out = call(t, h, trainerA, "GET", links, nil)
		mustStatus(t, code, 200, "list links")
		if out["directory"] != "keycloak" || len(arr(out, "links")) != 2 {
			t.Fatalf("links = %v", out)
		}
	})

	t.Run("token groups match by full path only", func(t *testing.T) {
		eve := identity{user: "eve", groups: "/learn/cohort-a"}
		_, mine := call(t, h, eve, "GET", "/api/v1/courses/mine", nil)
		if len(arr(mine, "courses")) != 1 {
			t.Fatalf("full-path claim: %v", mine)
		}
		fay := identity{user: "fay", groups: "cohort-a"}
		_, mine = call(t, h, fay, "GET", "/api/v1/courses/mine", nil)
		if len(arr(mine, "courses")) != 0 {
			t.Fatalf("plain-name claim matched a nested link: %v", mine)
		}
	})

	t.Run("legacy name links are resolved once", func(t *testing.T) {
		otherID, _ := newSectionedCourse(t, h, admin, "Legacy")
		exec(t, pool, `INSERT INTO course_group_links (course_id, group_name, group_path, path_resolved)
		               VALUES ($1, 'staff', '/staff', false), ($1, 'cohort-a', '/cohort-a', false)`, otherID)
		gs := learn.NewGroupSync(learn.NewService(pool), kc, "dev", zerolog.Nop())
		n, err := gs.ResolveLegacyLinks(context.Background())
		if err != nil || n != 1 {
			t.Fatalf("resolved %d (%v), want 1", n, err)
		}
		var resolved bool
		var syncErr string
		if err := pool.QueryRow(context.Background(), `
			SELECT path_resolved, last_sync_error FROM course_group_links WHERE course_id = $1 AND group_name = 'cohort-a'`, otherID).
			Scan(&resolved, &syncErr); err != nil {
			t.Fatal(err)
		}
		if resolved || !strings.Contains(syncErr, "several") {
			t.Fatalf("ambiguous legacy link: resolved %v, error %q", resolved, syncErr)
		}
	})

	t.Run("people from the directory", func(t *testing.T) {
		code, out := call(t, h, trainerA, "GET", "/api/v1/users?q=gil", nil)
		mustStatus(t, code, 200, "search the directory")
		people := arr(out, "people")
		if str(out, "source") != "learn+directory" || len(people) != 1 || str(obj(people[0]), "directory_id") != "gil" {
			t.Fatalf("people = %v", out)
		}
		if _, has := obj(people[0])["email"]; has {
			t.Fatalf("a trainer sees a directory email: %v", people[0])
		}
		code, e := call(t, h, trainerA, "POST", "/api/v1/courses/"+courseID+"/enrolments", map[string]any{"directory_id": "gil"})
		mustStatus(t, code, 201, "enrol a directory person")
		var idp string
		if err := pool.QueryRow(context.Background(), `SELECT idp_user_id FROM users WHERE id = $1`, str(e, "user_id")).Scan(&idp); err != nil || idp != "gil" {
			t.Fatalf("directory person stored with idp_user_id %q (%v)", idp, err)
		}
		_, out = call(t, h, trainerA, "GET", "/api/v1/users?q=gil", nil)
		if people := arr(out, "people"); len(people) != 1 || str(obj(people[0]), "directory_id") != "" {
			t.Fatalf("a known person shows twice or as a directory hit: %v", out)
		}
		code, _ = call(t, h, trainerA, "POST", "/api/v1/courses/"+courseID+"/enrolments", map[string]any{"directory_id": "nobody"})
		mustStatus(t, code, 404, "unknown directory person")
	})

	if got := auditActions(t, h, "action=group_link.added"); got[audit.GroupLinkAdded] != 2 {
		t.Fatalf("audit = %v", got)
	}
}
