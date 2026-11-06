package http_test

// Qualifications, certificates, renewal, public verify, the qualification
// catalog and HR holdings, and reminders. Same DB-backed setup (and the
// same TEST_DATABASE_URL warning) as courses_test.go.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	nethttp "net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pressly/goose/v3"
	"github.com/rs/zerolog"

	"github.com/AGlElectronics/learn/backend/internal/auth"
	"github.com/AGlElectronics/learn/backend/internal/config"
	"github.com/AGlElectronics/learn/backend/internal/db"
	httpx "github.com/AGlElectronics/learn/backend/internal/http"
	"github.com/AGlElectronics/learn/backend/internal/mail"
	"github.com/AGlElectronics/learn/backend/internal/notify"
	"github.com/AGlElectronics/learn/backend/migrations"
)

var (
	quinn = identity{user: "quinn-qual", groups: ""}
	rhea  = identity{user: "rhea-qual", groups: ""}
)

// setupQualRouter is setupRouter with a config hook and a notifier, and it
// also returns the pool so tests can move certificate dates around.
func setupQualRouter(t *testing.T, mutate func(*config.Config), notifier *notify.Notifier) (nethttp.Handler, *pgxpool.Pool) {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping DB-backed tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	t.Cleanup(cancel)

	sqlDB, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if _, err := sqlDB.ExecContext(ctx, `DROP SCHEMA public CASCADE; CREATE SCHEMA public;`); err != nil {
		t.Fatalf("reset schema: %v", err)
	}
	goose.SetBaseFS(migrations.FS)
	goose.SetTableName("goose_db_version")
	goose.SetLogger(goose.NopLogger())
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatalf("goose dialect: %v", err)
	}
	if err := goose.UpContext(ctx, sqlDB, "."); err != nil {
		t.Fatalf("goose up: %v", err)
	}
	pool, err := db.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	t.Cleanup(pool.Close)

	accessPath := filepath.Join(t.TempDir(), "access-config.toml")
	if err := os.WriteFile(accessPath, []byte(accessConfigTOML), 0o600); err != nil {
		t.Fatalf("write access config: %v", err)
	}
	access, err := auth.LoadAccessConfig(accessPath)
	if err != nil {
		t.Fatalf("load access config: %v", err)
	}
	cfg := &config.Config{
		AppEnv:         "test",
		HTTPAddr:       ":0",
		DatabaseURL:    dsn,
		AuthMode:       config.AuthModeDev,
		DevDefaultUser: "dev-admin",
		PublicBaseURL:  "https://learn.test",
	}
	if mutate != nil {
		mutate(cfg)
	}
	return httpx.NewRouter(httpx.Deps{
		Config:   cfg,
		Pool:     pool,
		Logger:   zerolog.Nop(),
		Access:   access,
		Notifier: notifier,
	}, "test"), pool
}

func exec(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatalf("exec %q: %v", sql, err)
	}
}

// rawCall performs an unauthenticated request with optional headers.
func rawCall(t *testing.T, h nethttp.Handler, method, path string, headers map[string]string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	out := map[string]any{}
	if rec.Body.Len() > 0 {
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("%s %s: invalid JSON (%d): %s", method, path, rec.Code, rec.Body.String())
		}
	}
	return rec.Code, out
}

// newCourse creates a course with one section as trainer A and returns
// the course and section ids.
func newCourse(t *testing.T, h nethttp.Handler, title string) (string, string) {
	t.Helper()
	code, course := call(t, h, trainerA, "POST", "/api/v1/courses", map[string]any{"title": title})
	mustStatus(t, code, 201, "create course "+title)
	code, section := call(t, h, trainerA, "POST", "/api/v1/courses/"+str(course, "id")+"/sections", map[string]any{"title": "Part 1"})
	mustStatus(t, code, 201, "create section")
	return str(course, "id"), str(section, "id")
}

func newActivity(t *testing.T, h nethttp.Handler, sectionID, kind, title string) string {
	t.Helper()
	code, a := call(t, h, trainerA, "POST", "/api/v1/sections/"+sectionID+"/activities",
		map[string]any{"kind": kind, "title": title, "body": "Body of " + title})
	mustStatus(t, code, 201, "create "+kind+" "+title)
	return str(a, "id")
}

func publishAndEnrol(t *testing.T, h nethttp.Handler, courseID string, userIDs ...string) {
	t.Helper()
	code, _ := call(t, h, trainerA, "POST", "/api/v1/courses/"+courseID+"/publish", nil)
	mustStatus(t, code, 200, "publish")
	for _, id := range userIDs {
		code, _ := call(t, h, trainerA, "POST", "/api/v1/courses/"+courseID+"/enrolments", map[string]any{"user_id": id, "role": "learner"})
		mustStatus(t, code, 201, "enrol")
	}
}

func setQualification(t *testing.T, h nethttp.Handler, courseID, qualificationID string) {
	t.Helper()
	code, _ := call(t, h, trainerA, "PUT", "/api/v1/courses/"+courseID+"/qualification", map[string]any{"qualification_id": qualificationID})
	mustStatus(t, code, 200, "set course qualification")
}

func myQualifications(t *testing.T, h nethttp.Handler, id identity) []any {
	t.Helper()
	code, out := call(t, h, id, "GET", "/api/v1/me/qualifications", nil)
	mustStatus(t, code, 200, "my qualifications")
	return arr(out, "qualifications")
}

// myQualification returns the caller's latest certificate of a
// qualification (nil when they hold none).
func myQualification(t *testing.T, h nethttp.Handler, id identity, key string) map[string]any {
	t.Helper()
	for _, q := range myQualifications(t, h, id) {
		if str(obj(q), "qualification_key") == key {
			return obj(q)
		}
	}
	return nil
}

func certCount(t *testing.T, pool *pgxpool.Pool, userID string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM certificates WHERE user_id = $1`, userID).Scan(&n); err != nil {
		t.Fatalf("count certificates: %v", err)
	}
	return n
}

func progressOf(t *testing.T, h nethttp.Handler, id identity, courseID string) (float64, float64) {
	t.Helper()
	code, detail := call(t, h, id, "GET", "/api/v1/courses/"+courseID, nil)
	mustStatus(t, code, 200, "course detail")
	p := obj(detail["progress"])
	return num(p, "completed"), num(p, "total")
}

func TestQualificationsAndCertificates(t *testing.T) {
	h, pool := setupQualRouter(t, nil, nil)
	ids := signIn(t, h, admin, trainerA, trainerB, quinn, rhea)

	code, q := call(t, h, admin, "POST", "/api/v1/qualifications",
		map[string]any{"key": "first-aid", "name": "First aid", "description": "Basic first aid", "valid_months": 12})
	mustStatus(t, code, 201, "admin creates qualification")
	qualID := str(q, "id")
	code, q2 := call(t, h, admin, "POST", "/api/v1/qualifications", map[string]any{"key": "forklift", "name": "Forklift"})
	mustStatus(t, code, 201, "admin creates second qualification")

	t.Run("qualification management needs settings.manage", func(t *testing.T) {
		code, _ := call(t, h, trainerA, "POST", "/api/v1/qualifications", map[string]any{"key": "x", "name": "X"})
		mustStatus(t, code, 403, "trainer creates qualification")
		code, _ = call(t, h, quinn, "POST", "/api/v1/qualifications", map[string]any{"key": "x", "name": "X"})
		mustStatus(t, code, 403, "learner creates qualification")
		code, _ = call(t, h, admin, "POST", "/api/v1/qualifications", map[string]any{"key": "first-aid", "name": "Dup"})
		mustStatus(t, code, 409, "duplicate key")
		code, _ = call(t, h, admin, "POST", "/api/v1/qualifications", map[string]any{"key": "Bad Key!", "name": "Bad"})
		mustStatus(t, code, 422, "invalid key")
		code, _ = call(t, h, trainerA, "PATCH", "/api/v1/qualifications/"+qualID, map[string]any{"name": "Hijack"})
		mustStatus(t, code, 403, "trainer edits qualification")
		code, list := call(t, h, trainerA, "GET", "/api/v1/qualifications", nil)
		mustStatus(t, code, 200, "trainer lists qualifications for the picker")
		if len(arr(list, "qualifications")) != 2 {
			t.Fatalf("qualifications: %v", list)
		}
		code, _ = call(t, h, quinn, "GET", "/api/v1/qualifications", nil)
		mustStatus(t, code, 403, "learner lists qualifications")
		code, upd := call(t, h, admin, "PATCH", "/api/v1/qualifications/"+str(q2, "id"), map[string]any{"valid_months": 24})
		mustStatus(t, code, 200, "admin edits qualification")
		if num(upd, "valid_months") != 24 {
			t.Fatalf("valid_months not updated: %v", upd)
		}
		code, _ = call(t, h, admin, "DELETE", "/api/v1/qualifications/"+str(q2, "id"), nil)
		mustStatus(t, code, 204, "delete unused qualification")
	})

	// Page path: two pages; the certificate comes with the second.
	pageCourse, pageSection := newCourse(t, h, "First aid theory")
	page1 := newActivity(t, h, pageSection, "page", "CPR")
	page2 := newActivity(t, h, pageSection, "page", "Recovery position")
	publishAndEnrol(t, h, pageCourse, ids[quinn.user], ids[rhea.user])

	t.Run("only course staff set the awarded qualification", func(t *testing.T) {
		code, _ := call(t, h, trainerB, "PUT", "/api/v1/courses/"+pageCourse+"/qualification", map[string]any{"qualification_id": qualID})
		mustStatus(t, code, 403, "other trainer sets qualification")
		code, _ = call(t, h, quinn, "PUT", "/api/v1/courses/"+pageCourse+"/qualification", map[string]any{"qualification_id": qualID})
		mustStatus(t, code, 403, "learner sets qualification")
	})
	setQualification(t, h, pageCourse, qualID)

	t.Run("finishing the last page awards the certificate once", func(t *testing.T) {
		code, _ := call(t, h, quinn, "PUT", "/api/v1/activities/"+page1+"/completion", map[string]any{"completed": true})
		mustStatus(t, code, 200, "complete page 1")
		if n := certCount(t, pool, ids[quinn.user]); n != 0 {
			t.Fatalf("certificate after one page: %d", n)
		}
		code, _ = call(t, h, quinn, "PUT", "/api/v1/activities/"+page2+"/completion", map[string]any{"completed": true})
		mustStatus(t, code, 200, "complete page 2")
		if n := certCount(t, pool, ids[quinn.user]); n != 1 {
			t.Fatalf("certificates after finishing: %d, want 1", n)
		}
		// Unmark and mark again: still one certificate (idempotent per cycle).
		call(t, h, quinn, "PUT", "/api/v1/activities/"+page2+"/completion", map[string]any{"completed": false})
		call(t, h, quinn, "PUT", "/api/v1/activities/"+page2+"/completion", map[string]any{"completed": true})
		if n := certCount(t, pool, ids[quinn.user]); n != 1 {
			t.Fatalf("certificates after re-completing: %d, want 1", n)
		}
		mq := myQualification(t, h, quinn, "first-aid")
		if mq == nil || str(mq, "status") != "valid" || mq["expires_at"] == nil || mq["renewable"] != false {
			t.Fatalf("my qualification: %v", mq)
		}
		if !strings.HasPrefix(str(mq, "verify_url"), "https://learn.test/verify/") {
			t.Fatalf("verify_url: %q", str(mq, "verify_url"))
		}
		code, detail := call(t, h, quinn, "GET", "/api/v1/courses/"+pageCourse, nil)
		mustStatus(t, code, 200, "detail")
		if str(obj(detail["awards_qualification"]), "key") != "first-aid" || num(detail, "cycle") != 1 {
			t.Fatalf("detail qualification/cycle: %v %v", detail["awards_qualification"], detail["cycle"])
		}
	})

	cert := myQualification(t, h, quinn, "first-aid")
	certID, verifyCode := str(cert, "id"), str(cert, "verify_code")

	t.Run("certificate page: holder and staff only", func(t *testing.T) {
		code, _ := call(t, h, quinn, "GET", "/api/v1/certificates/"+certID, nil)
		mustStatus(t, code, 200, "holder")
		code, _ = call(t, h, rhea, "GET", "/api/v1/certificates/"+certID, nil)
		mustStatus(t, code, 404, "other learner")
		code, _ = call(t, h, trainerB, "GET", "/api/v1/certificates/"+certID, nil)
		mustStatus(t, code, 404, "trainer of another course")
		code, _ = call(t, h, trainerA, "GET", "/api/v1/certificates/"+certID, nil)
		mustStatus(t, code, 200, "course trainer")
		code, _ = call(t, h, admin, "GET", "/api/v1/certificates/"+certID, nil)
		mustStatus(t, code, 200, "admin")
	})

	t.Run("public verify needs no login", func(t *testing.T) {
		code, v := rawCall(t, h, "GET", "/api/public/verify/"+verifyCode, nil)
		mustStatus(t, code, 200, "verify")
		if str(v, "status") != "valid" || str(v, "qualification") != "First aid" || str(v, "display_name") == "" {
			t.Fatalf("verify: %v", v)
		}
		if _, leaked := v["email"]; leaked {
			t.Fatalf("verify leaks email: %v", v)
		}
		code, _ = rawCall(t, h, "GET", "/api/public/verify/"+strings.ToLower(verifyCode), nil)
		mustStatus(t, code, 200, "verify lower-case")
		code, _ = rawCall(t, h, "GET", "/api/public/verify/AAAAAAAAAAAA", nil)
		mustStatus(t, code, 404, "unknown code")
		exec(t, pool, `UPDATE certificates SET expires_at = now() - interval '1 day' WHERE id = $1`, certID)
		_, v = rawCall(t, h, "GET", "/api/public/verify/"+verifyCode, nil)
		if str(v, "status") != "expired" {
			t.Fatalf("verify after expiry: %v", v)
		}
		exec(t, pool, `UPDATE certificates SET expires_at = now() + interval '1 year' WHERE id = $1`, certID)
	})

	t.Run("renewal opens 60 days before expiry and resets progress", func(t *testing.T) {
		code, body := call(t, h, quinn, "POST", "/api/v1/courses/"+pageCourse+"/renew", nil)
		mustStatus(t, code, 409, "renew a year early")
		if !strings.Contains(str(obj(body["error"]), "message"), "renewal opens") {
			t.Fatalf("renew error: %v", body)
		}
		code, _ = call(t, h, rhea, "POST", "/api/v1/courses/"+pageCourse+"/renew", nil)
		mustStatus(t, code, 409, "renew without a certificate")
		code, _ = call(t, h, trainerB, "POST", "/api/v1/courses/"+pageCourse+"/renew", nil)
		mustStatus(t, code, 403, "renew without an enrolment")

		exec(t, pool, `UPDATE certificates SET expires_at = now() + interval '20 days' WHERE id = $1`, certID)
		if mq := myQualification(t, h, quinn, "first-aid"); mq["renewable"] != true || mq["expiring_soon"] != true {
			t.Fatalf("expected renewable + expiring soon: %v", mq)
		}
		code, res := call(t, h, quinn, "POST", "/api/v1/courses/"+pageCourse+"/renew", nil)
		mustStatus(t, code, 200, "renew inside the window")
		if num(res, "cycle") != 2 || res["due_at"] == nil {
			t.Fatalf("renew result: %v", res)
		}
		if done, total := progressOf(t, h, quinn, pageCourse); done != 0 || total != 2 {
			t.Fatalf("progress after renewal: %v/%v, want 0/2", done, total)
		}
		mq := myQualification(t, h, quinn, "first-aid")
		if mq["renewal_in_progress"] != true || mq["renewable"] != false || str(mq, "status") != "valid" {
			t.Fatalf("my qualification during renewal: %v", mq)
		}
		code, _ = call(t, h, quinn, "POST", "/api/v1/courses/"+pageCourse+"/renew", nil)
		mustStatus(t, code, 409, "renew twice")

		for _, p := range []string{page1, page2} {
			code, _ := call(t, h, quinn, "PUT", "/api/v1/activities/"+p+"/completion", map[string]any{"completed": true})
			mustStatus(t, code, 200, "complete in cycle 2")
		}
		if n := certCount(t, pool, ids[quinn.user]); n != 2 {
			t.Fatalf("certificates after cycle 2: %d, want 2", n)
		}
		mq = myQualification(t, h, quinn, "first-aid")
		if num(mq, "cycle") != 2 || mq["renewal_in_progress"] != false || str(mq, "id") == certID {
			t.Fatalf("latest certificate after renewal: %v", mq)
		}
		// History is kept: cycle 1 completions are still stored.
		var old int
		if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM completions WHERE user_id = $1 AND cycle = 1`,
			ids[quinn.user]).Scan(&old); err != nil || old != 2 {
			t.Fatalf("cycle 1 completions kept: %d (%v)", old, err)
		}
	})

	t.Run("revoke needs settings.manage", func(t *testing.T) {
		code, _ := call(t, h, trainerA, "POST", "/api/v1/certificates/"+certID+"/revoke", map[string]any{"reason": "nope"})
		mustStatus(t, code, 403, "trainer revokes")
		code, _ = call(t, h, admin, "POST", "/api/v1/certificates/"+certID+"/revoke", map[string]any{"reason": ""})
		mustStatus(t, code, 422, "revoke without reason")
		code, rev := call(t, h, admin, "POST", "/api/v1/certificates/"+certID+"/revoke", map[string]any{"reason": "Issued by mistake"})
		mustStatus(t, code, 200, "admin revokes")
		if str(rev, "status") != "revoked" || str(rev, "revoked_reason") != "Issued by mistake" {
			t.Fatalf("revoked certificate: %v", rev)
		}
		code, _ = call(t, h, admin, "POST", "/api/v1/certificates/"+certID+"/revoke", map[string]any{"reason": "again"})
		mustStatus(t, code, 409, "revoke twice")
		_, v := rawCall(t, h, "GET", "/api/public/verify/"+verifyCode, nil)
		if str(v, "status") != "revoked" {
			t.Fatalf("verify after revoke: %v", v)
		}
		code, list := call(t, h, admin, "GET", "/api/v1/qualifications/"+qualID+"/certificates", nil)
		mustStatus(t, code, 200, "admin lists certificates")
		if len(arr(list, "certificates")) != 2 {
			t.Fatalf("certificates of qualification: %v", list)
		}
		code, _ = call(t, h, admin, "DELETE", "/api/v1/qualifications/"+qualID, nil)
		mustStatus(t, code, 409, "delete a qualification people hold")
	})

	t.Run("quiz pass awards; renewal resets the quiz", func(t *testing.T) {
		courseID, sectionID := newCourse(t, h, "First aid test")
		quizID := newActivity(t, h, sectionID, "quiz", "Exam")
		code, cat := call(t, h, trainerA, "POST", "/api/v1/courses/"+courseID+"/question-categories", map[string]any{"name": "Exam"})
		mustStatus(t, code, 201, "category")
		question := createQuestion(t, h, str(cat, "id"), "single", "Call which number?", opt("112", true), opt("999 999", false))
		code, _ = call(t, h, trainerA, "POST", "/api/v1/activities/"+quizID+"/slots", map[string]any{"question_id": str(question, "id")})
		mustStatus(t, code, 201, "slot")
		publishAndEnrol(t, h, courseID, ids[rhea.user])
		setQualification(t, h, courseID, qualID)

		pass := func(what string) {
			t.Helper()
			code, attempt := call(t, h, rhea, "POST", "/api/v1/activities/"+quizID+"/attempts", nil)
			if code != 200 && code != 201 {
				t.Fatalf("%s: start attempt: %d", what, code)
			}
			aq := attemptQuestion(t, attempt, "Call which number?")
			code, res := call(t, h, rhea, "POST", "/api/v1/attempts/"+str(attempt, "id")+"/submit", map[string]any{
				"answers": map[string]any{str(aq, "question_id"): []string{optionByLabel(t, aq, "112")}},
			})
			mustStatus(t, code, 200, what+": submit")
			if res["passed"] != true {
				t.Fatalf("%s: not passed: %v", what, res)
			}
		}
		pass("first pass")
		mq := myQualification(t, h, rhea, "first-aid")
		if mq == nil || num(mq, "cycle") != 1 {
			t.Fatalf("certificate from quiz: %v", mq)
		}
		exec(t, pool, `UPDATE certificates SET expires_at = now() - interval '3 days' WHERE id = $1`, str(mq, "id"))
		if mq := myQualification(t, h, rhea, "first-aid"); str(mq, "status") != "expired" || mq["renewable"] != true {
			t.Fatalf("expired certificate should be renewable: %v", mq)
		}
		code, _ = call(t, h, rhea, "POST", "/api/v1/courses/"+courseID+"/renew", nil)
		mustStatus(t, code, 200, "renew after expiry")
		code, info := call(t, h, rhea, "GET", "/api/v1/activities/"+quizID+"/quiz", nil)
		mustStatus(t, code, 200, "quiz info after renewal")
		st := obj(info["standing"])
		if num(st, "attempts_used") != 0 || st["passed"] != false || st["best_percent"] != nil || len(arr(info, "attempts")) != 0 {
			t.Fatalf("quiz standing after renewal: %v", info)
		}
		pass("pass again")
		if mq := myQualification(t, h, rhea, "first-aid"); num(mq, "cycle") != 2 || str(mq, "status") != "valid" {
			t.Fatalf("certificate after second pass: %v", mq)
		}
	})

	t.Run("session attendance awards; a new cycle needs re-attending", func(t *testing.T) {
		courseID, sectionID := newCourse(t, h, "First aid practice")
		sessionID := newActivity(t, h, sectionID, "session", "Hands-on")
		now := time.Now()
		code, slot := call(t, h, trainerA, "POST", "/api/v1/activities/"+sessionID+"/session-slots",
			map[string]any{"starts_at": rfc(now.Add(48 * time.Hour)), "ends_at": rfc(now.Add(50 * time.Hour)), "capacity": 5})
		mustStatus(t, code, 201, "slot")
		slotID := str(slot, "id")
		publishAndEnrol(t, h, courseID, ids[quinn.user])
		setQualification(t, h, courseID, qualID)

		code, signup := call(t, h, quinn, "POST", "/api/v1/slots/"+slotID+"/booking", nil)
		mustStatus(t, code, 201, "book")
		code, _ = call(t, h, trainerA, "PUT", "/api/v1/signups/"+str(signup, "id"), map[string]any{"status": "attended"})
		mustStatus(t, code, 200, "mark attended")
		var n int
		if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM certificates WHERE course_id = $1`, courseID).Scan(&n); err != nil || n != 1 {
			t.Fatalf("certificate from session: %d (%v)", n, err)
		}
		exec(t, pool, `UPDATE certificates SET expires_at = now() + interval '5 days' WHERE course_id = $1`, courseID)
		code, _ = call(t, h, quinn, "POST", "/api/v1/courses/"+courseID+"/renew", nil)
		mustStatus(t, code, 200, "renew session course")
		if sessionCompleted(t, h, quinn, courseID, sessionID) {
			t.Fatalf("session still complete in the new cycle")
		}
		code, info := call(t, h, quinn, "GET", "/api/v1/activities/"+sessionID+"/session", nil)
		mustStatus(t, code, 200, "session info")
		if info["my_signup"] != nil {
			t.Fatalf("old attendance still shown as my signup: %v", info["my_signup"])
		}
		code, _ = call(t, h, quinn, "POST", "/api/v1/slots/"+slotID+"/booking", nil)
		mustStatus(t, code, 201, "book again in the new cycle")
	})

	t.Run("linking a qualification awards learners who already finished", func(t *testing.T) {
		courseID, sectionID := newCourse(t, h, "Already done")
		pageID := newActivity(t, h, sectionID, "page", "Only page")
		publishAndEnrol(t, h, courseID, ids[rhea.user])
		code, _ := call(t, h, rhea, "PUT", "/api/v1/activities/"+pageID+"/completion", map[string]any{"completed": true})
		mustStatus(t, code, 200, "complete")
		setQualification(t, h, courseID, qualID)
		var n int
		if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM certificates WHERE course_id = $1`, courseID).Scan(&n); err != nil || n != 1 {
			t.Fatalf("backfilled certificate: %d (%v)", n, err)
		}
	})
}

func TestQualificationCatalogIsPublic(t *testing.T) {
	h, _ := setupQualRouter(t, nil, nil)
	signIn(t, h, admin)
	code, _ := call(t, h, admin, "POST", "/api/v1/qualifications",
		map[string]any{"key": "first-aid", "name": "First aid", "description": "Basic", "valid_months": 24})
	mustStatus(t, code, 201, "create qualification")
	code, _ = call(t, h, admin, "POST", "/api/v1/qualifications", map[string]any{"key": "radio", "name": "Radio"})
	mustStatus(t, code, 201, "create qualification")

	code, out := rawCall(t, h, "GET", "/api/qualification-catalog", nil)
	mustStatus(t, code, 200, "catalog without auth")
	if str(out, "module") != "learn" || str(out, "display_name") != "Learn" {
		t.Fatalf("catalog header: %v", out)
	}
	list := arr(out, "qualifications")
	if len(list) != 2 {
		t.Fatalf("catalog entries: %v", list)
	}
	first, second := obj(list[0]), obj(list[1])
	if str(first, "key") != "first-aid" || str(first, "name") != "First aid" || str(first, "description") != "Basic" || num(first, "valid_months") != 24 {
		t.Fatalf("catalog entry: %v", first)
	}
	if str(second, "key") != "radio" || second["valid_months"] != nil {
		t.Fatalf("catalog entry without validity: %v", second)
	}
}

func TestVerifyIsRateLimited(t *testing.T) {
	h, _ := setupQualRouter(t, nil, nil)
	for i := 0; i < 30; i++ {
		code, _ := rawCall(t, h, "GET", "/api/public/verify/AAAAAAAAAAAA", nil)
		mustStatus(t, code, 404, "unknown code within the limit")
	}
	code, out := rawCall(t, h, "GET", "/api/public/verify/AAAAAAAAAAAA", nil)
	mustStatus(t, code, 429, "31st request in a minute")
	if str(obj(out["error"]), "code") != "rate_limited" {
		t.Fatalf("rate limit body: %v", out)
	}
}

func TestHoldings(t *testing.T) {
	t.Run("route is absent without a token", func(t *testing.T) {
		h, _ := setupQualRouter(t, nil, nil)
		code, _ := rawCall(t, h, "GET", "/api/internal/holdings?subjects=a", map[string]string{"Authorization": "Bearer anything"})
		mustStatus(t, code, 404, "holdings without LEARN_SERVICE_TOKEN")
	})

	h, pool := setupQualRouter(t, func(c *config.Config) { c.LearnServiceToken = "hr-to-learn" }, nil)
	ids := signIn(t, h, admin, quinn, rhea)
	code, fa := call(t, h, admin, "POST", "/api/v1/qualifications", map[string]any{"key": "first-aid", "name": "First aid", "valid_months": 12})
	mustStatus(t, code, 201, "qualification")
	code, radio := call(t, h, admin, "POST", "/api/v1/qualifications", map[string]any{"key": "radio", "name": "Radio"})
	mustStatus(t, code, 201, "qualification")
	code, fork := call(t, h, admin, "POST", "/api/v1/qualifications", map[string]any{"key": "forklift", "name": "Forklift"})
	mustStatus(t, code, 201, "qualification")
	insert := func(user, qual, code string, expires string, revoked bool) {
		t.Helper()
		exec(t, pool, `
			INSERT INTO certificates (user_id, qualification_id, verify_code, expires_at, revoked_at)
			VALUES ($1, $2, $3, now() + $4::interval, CASE WHEN $5 THEN now() END)`, user, qual, code, expires, revoked)
	}
	insert(ids[quinn.user], str(fa, "id"), "AAAAAAAAAAA2", "200 days", false)
	insert(ids[quinn.user], str(radio, "id"), "AAAAAAAAAAA3", "-1 day", false) // expired
	insert(ids[quinn.user], str(fork, "id"), "AAAAAAAAAAA4", "200 days", true) // revoked
	exec(t, pool, `
		INSERT INTO certificates (user_id, qualification_id, verify_code, expires_at)
		VALUES ($1, $2, 'AAAAAAAAAAA5', NULL)`, ids[rhea.user], str(radio, "id"))

	path := "/api/internal/holdings?subjects=" + quinn.user + "," + rhea.user + ",nobody"
	code, _ = rawCall(t, h, "GET", path, nil)
	mustStatus(t, code, 401, "no token")
	code, _ = rawCall(t, h, "GET", path, map[string]string{"Authorization": "Bearer wrong"})
	mustStatus(t, code, 401, "wrong token")
	code, _ = call(t, h, admin, "GET", path, nil)
	mustStatus(t, code, 401, "a user session is not the service token")

	code, out := rawCall(t, h, "GET", path, map[string]string{"Authorization": "Bearer hr-to-learn"})
	mustStatus(t, code, 200, "holdings")
	holdings := obj(out["holdings"])
	q := arr(holdings, quinn.user)
	if len(q) != 1 || str(obj(q[0]), "key") != "first-aid" || obj(q[0])["expires_at"] == nil {
		t.Fatalf("quinn holdings (expired and revoked must be left out): %v", q)
	}
	r := arr(holdings, rhea.user)
	if len(r) != 1 || str(obj(r[0]), "key") != "radio" || obj(r[0])["expires_at"] != nil {
		t.Fatalf("rhea holdings: %v", r)
	}
	if nobody, ok := holdings["nobody"].([]any); !ok || len(nobody) != 0 {
		t.Fatalf("unknown subject should map to []: %v", holdings["nobody"])
	}
}

// fakeMailer records sent mail; fail makes every send fail.
type fakeMailer struct {
	mu   sync.Mutex
	fail bool
	sent []mail.Message
}

func (f *fakeMailer) Send(_ context.Context, m mail.Message) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail {
		return errors.New("smtp down")
	}
	f.sent = append(f.sent, m)
	return nil
}

func (f *fakeMailer) messages() []mail.Message {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]mail.Message(nil), f.sent...)
}

func TestReminders(t *testing.T) {
	mailer := &fakeMailer{}
	notifier := notify.New(zerolog.Nop(), notify.NewEmail(mailer))
	h, pool := setupQualRouter(t, nil, notifier)

	due7 := identity{user: "dana-due7"}
	due1 := identity{user: "dina-due1"}
	late := identity{user: "lars-late"}
	done := identity{user: "dora-done"}
	gone := identity{user: "gus-gone"}
	booked := identity{user: "bea-booked"}
	expiring := identity{user: "eve-expiring"}
	expired := identity{user: "ezra-expired"}
	ids := signIn(t, h, admin, trainerA, due7, due1, late, done, gone, booked, expiring, expired)

	courseID, sectionID := newCourse(t, h, "Safety induction")
	pageID := newActivity(t, h, sectionID, "page", "Read me")
	sessionID := newActivity(t, h, sectionID, "session", "Site tour")
	now := time.Now()
	code, slot := call(t, h, trainerA, "POST", "/api/v1/activities/"+sessionID+"/session-slots",
		map[string]any{"starts_at": rfc(now.Add(20 * time.Hour)), "ends_at": rfc(now.Add(21 * time.Hour)), "location": "Gate 3", "capacity": 10})
	mustStatus(t, code, 201, "slot tomorrow")
	all := []string{}
	for _, id := range []identity{due7, due1, late, done, gone, booked, expiring, expired} {
		all = append(all, ids[id.user])
	}
	publishAndEnrol(t, h, courseID, all...)

	setDue := func(id identity, d time.Duration) {
		t.Helper()
		exec(t, pool, `UPDATE enrolments SET due_at = $3 WHERE course_id = $1 AND user_id = $2`, courseID, ids[id.user], now.Add(d))
	}
	setDue(due7, 3*24*time.Hour)
	setDue(due1, 12*time.Hour)
	setDue(late, -2*time.Hour)
	setDue(done, 2*24*time.Hour)
	setDue(gone, -2*time.Hour)
	setDue(booked, 30*24*time.Hour) // outside every due window
	exec(t, pool, `INSERT INTO completions (activity_id, user_id) VALUES ($1, $3), ($2, $3)`, pageID, sessionID, ids[done.user])
	exec(t, pool, `UPDATE enrolments SET status = 'inactive' WHERE course_id = $1 AND user_id = $2`, courseID, ids[gone.user])
	code, _ = call(t, h, booked, "POST", "/api/v1/slots/"+str(slot, "id")+"/booking", nil)
	mustStatus(t, code, 201, "book tomorrow's session")

	code, qual := call(t, h, admin, "POST", "/api/v1/qualifications", map[string]any{"key": "induction", "name": "Site induction", "valid_months": 12})
	mustStatus(t, code, 201, "qualification")
	exec(t, pool, `
		INSERT INTO certificates (user_id, qualification_id, course_id, verify_code, expires_at)
		VALUES ($1, $3, $4, 'BBBBBBBBBBB2', now() + interval '10 days'),
		       ($2, $3, $4, 'BBBBBBBBBBB3', now() - interval '2 days')`,
		ids[expiring.user], ids[expired.user], str(qual, "id"), courseID)

	t.Run("only settings.manage may trigger a run", func(t *testing.T) {
		code, _ := call(t, h, due7, "POST", "/api/v1/reminders/run", nil)
		mustStatus(t, code, 403, "learner runs reminders")
	})

	want := map[string]int{
		"due_7d": 1, "due_1d": 1, "overdue": 1, "session_tomorrow": 1,
		"certificate_expiring_30d": 1, "certificate_expired": 1,
	}
	total := 0
	for _, n := range want {
		total += n
	}

	t.Run("failed deliveries are retried on the next run", func(t *testing.T) {
		mailer.fail = true
		code, res := call(t, h, admin, "POST", "/api/v1/reminders/run", nil)
		mustStatus(t, code, 200, "run with mail down")
		if res["ran"] != true || num(res, "failed") != float64(total) || len(obj(res["sent"])) != 0 {
			t.Fatalf("run with failing mail: %v", res)
		}
		mailer.fail = false
	})

	t.Run("each reminder goes out once", func(t *testing.T) {
		code, res := call(t, h, admin, "POST", "/api/v1/reminders/run", nil)
		mustStatus(t, code, 200, "run")
		sent := obj(res["sent"])
		for kind, n := range want {
			if num(sent, kind) != float64(n) {
				t.Fatalf("sent %s = %v, want %d (all: %v)", kind, sent[kind], n, res)
			}
		}
		if len(sent) != len(want) || num(res, "failed") != 0 {
			t.Fatalf("unexpected reminders: %v", res)
		}
		msgs := mailer.messages()
		if len(msgs) != total {
			t.Fatalf("mails: %d, want %d", len(msgs), total)
		}
		byTo := map[string]mail.Message{}
		for _, m := range msgs {
			byTo[m.To] = m
		}
		for _, skipped := range []identity{done, gone} {
			if _, ok := byTo[skipped.user+"@learn.local"]; ok {
				t.Fatalf("%s should get no reminder", skipped.user)
			}
		}
		m := byTo[booked.user+"@learn.local"]
		if !strings.Contains(m.Body, "Gate 3") || !strings.Contains(m.Body, "https://learn.test/courses/"+courseID) {
			t.Fatalf("session reminder body: %q", m.Body)
		}
		if m := byTo[late.user+"@learn.local"]; !strings.HasPrefix(m.Subject, "Overdue") {
			t.Fatalf("overdue subject: %q", m.Subject)
		}
		if m := byTo[expired.user+"@learn.local"]; !strings.Contains(m.Body, "Site induction") || !strings.Contains(m.Body, "Renew") {
			t.Fatalf("expired body: %q", m.Body)
		}

		code, res = call(t, h, admin, "POST", "/api/v1/reminders/run", nil)
		mustStatus(t, code, 200, "second run")
		if len(obj(res["sent"])) != 0 || num(res, "already_sent") != float64(total) {
			t.Fatalf("second run should send nothing: %v", res)
		}
		if len(mailer.messages()) != total {
			t.Fatalf("mails after second run: %d", len(mailer.messages()))
		}
	})

	t.Run("a new due date is reminded again", func(t *testing.T) {
		setDue(due7, 5*24*time.Hour)
		code, res := call(t, h, admin, "POST", "/api/v1/reminders/run", nil)
		mustStatus(t, code, 200, "run after due change")
		if num(obj(res["sent"]), "due_7d") != 1 {
			t.Fatalf("due change: %v", res)
		}
	})
}
