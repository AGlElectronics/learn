package http_test

// Learning paths (phase 10): management access, enrolment fan-out to the
// path's courses, sequential course locks, path completion awarding the
// path's qualification, and Keycloak group sync for paths (inactive, never
// deleted). Same DB-backed setup as courses_test.go.

import (
	"context"
	nethttp "net/http"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func enrolmentState(t *testing.T, pool *pgxpool.Pool, courseID, userID string) (source, status string) {
	t.Helper()
	err := pool.QueryRow(context.Background(), `SELECT source, status FROM enrolments WHERE course_id = $1 AND user_id = $2`,
		courseID, userID).Scan(&source, &status)
	if err != nil {
		return "", ""
	}
	return source, status
}

func completeCourse(t *testing.T, h nethttp.Handler, id identity, courseID string) {
	t.Helper()
	code, d := call(t, h, id, "GET", "/api/v1/courses/"+courseID, nil)
	mustStatus(t, code, 200, "detail before completing")
	for _, s := range arr(d, "sections") {
		for _, a := range arr(obj(s), "activities") {
			code, _ := call(t, h, id, "PUT", "/api/v1/activities/"+str(obj(a), "id")+"/completion", map[string]any{"completed": true})
			mustStatus(t, code, 200, "complete activity")
		}
	}
}

func publishedCourse(t *testing.T, h nethttp.Handler, title string) (string, string) {
	t.Helper()
	courseID, sectionID := newCourse(t, h, title)
	pageID := newActivity(t, h, sectionID, "page", title+" page")
	code, _ := call(t, h, trainerA, "POST", "/api/v1/courses/"+courseID+"/publish", nil)
	mustStatus(t, code, 200, "publish "+title)
	return courseID, pageID
}

func TestPaths(t *testing.T) {
	h, pool := setupP8Router(t, nil)
	ids := signIn(t, h, admin, trainerA, learner, outsider)
	lou := ids["learner-lou"]

	c1, _ := publishedCourse(t, h, "Stash 1")
	c2, p2 := publishedCourse(t, h, "Stash 2")
	c3, _ := publishedCourse(t, h, "Stash 3")
	code, q := call(t, h, admin, "POST", "/api/v1/qualifications", map[string]any{"key": "stash-crew", "name": "Stash crew"})
	mustStatus(t, code, 201, "qualification")

	code, path := call(t, h, admin, "POST", "/api/v1/paths", map[string]any{
		"name": "New Stash crew", "sequential": true, "awards_qualification_id": str(q, "id"),
	})
	mustStatus(t, code, 201, "create path")
	pathID := str(path, "id")

	t.Run("management needs course.manage", func(t *testing.T) {
		code, _ := call(t, h, trainerA, "GET", "/api/v1/paths", nil)
		mustStatus(t, code, 403, "trainer lists paths")
		code, _ = call(t, h, trainerA, "POST", "/api/v1/paths", map[string]any{"name": "x"})
		mustStatus(t, code, 403, "trainer creates path")
		code, _ = call(t, h, trainerA, "GET", "/api/v1/paths/"+pathID, nil)
		mustStatus(t, code, 404, "trainer reads path")
		code, _ = call(t, h, learner, "POST", "/api/v1/paths/"+pathID+"/enrol", nil)
		mustStatus(t, code, 403, "self-enrol on a closed path")
	})

	t.Run("courses: set, add, reorder", func(t *testing.T) {
		code, _ := call(t, h, admin, "PUT", "/api/v1/paths/"+pathID+"/courses", map[string]any{"course_ids": []string{c2, c1}})
		mustStatus(t, code, 200, "set courses")
		code, _ = call(t, h, admin, "POST", "/api/v1/paths/"+pathID+"/courses", map[string]any{"course_id": c3})
		mustStatus(t, code, 201, "add course")
		code, _ = call(t, h, admin, "POST", "/api/v1/paths/"+pathID+"/courses", map[string]any{"course_id": c3})
		mustStatus(t, code, 409, "add twice")
		code, out := call(t, h, admin, "PUT", "/api/v1/paths/"+pathID+"/courses", map[string]any{"course_ids": []string{c1, c2, c3}})
		mustStatus(t, code, 200, "reorder")
		cs := arr(out, "courses")
		if len(cs) != 3 || str(obj(cs[0]), "course_id") != c1 || str(obj(cs[2]), "course_id") != c3 {
			t.Fatalf("path courses: %v", cs)
		}
	})

	code, enr := call(t, h, admin, "POST", "/api/v1/paths/"+pathID+"/enrolments", map[string]any{"user_id": lou})
	mustStatus(t, code, 201, "enrol learner on path")

	t.Run("enrolling fans out to every course", func(t *testing.T) {
		for _, c := range []string{c1, c2, c3} {
			if src, st := enrolmentState(t, pool, c, lou); src != "manual" || st != "active" {
				t.Fatalf("course enrolment on %s: %s/%s", c, src, st)
			}
		}
		code, _ := call(t, h, admin, "POST", "/api/v1/paths/"+pathID+"/enrolments", map[string]any{"user_id": lou})
		mustStatus(t, code, 409, "enrol twice")
	})

	t.Run("sequential path locks later courses", func(t *testing.T) {
		code, mine := call(t, h, learner, "GET", "/api/v1/courses/mine", nil)
		mustStatus(t, code, 200, "mine")
		locked := map[string]bool{}
		for _, c := range arr(mine, "courses") {
			locked[str(obj(c), "id")] = obj(c)["locked"] == true
		}
		if locked[c1] || !locked[c2] || !locked[c3] {
			t.Fatalf("locks in mine: %v", locked)
		}
		code, out := call(t, h, learner, "GET", "/api/v1/courses/"+c2, nil)
		mustStatus(t, code, 403, "locked course detail")
		if errCode(out) != "locked" {
			t.Fatalf("locked error: %v", out)
		}
		if needs := arr(obj(obj(out["error"])["details"]), "needs"); len(needs) != 1 || needs[0] != "Stash 1" {
			t.Fatalf("needs: %v", out)
		}
		code, _ = call(t, h, learner, "PUT", "/api/v1/activities/"+p2+"/completion", map[string]any{"completed": true})
		mustStatus(t, code, 403, "completing an activity of a locked course")
		code, _ = call(t, h, trainerA, "GET", "/api/v1/courses/"+c2, nil)
		mustStatus(t, code, 200, "staff are never locked")

		completeCourse(t, h, learner, c1)
		code, _ = call(t, h, learner, "GET", "/api/v1/courses/"+c2, nil)
		mustStatus(t, code, 200, "course 2 opens after course 1")
		code, _ = call(t, h, learner, "GET", "/api/v1/courses/"+c3, nil)
		mustStatus(t, code, 403, "course 3 still locked")

		code, me := call(t, h, learner, "GET", "/api/v1/me/paths", nil)
		mustStatus(t, code, 200, "my paths")
		p := obj(arr(me, "paths")[0])
		if num(p, "courses_done") != 1 || num(p, "courses_total") != 3 || p["complete"] != false {
			t.Fatalf("my path progress: %v", p)
		}
		if obj(arr(p, "courses")[2])["locked"] != true {
			t.Fatalf("course 3 should show locked: %v", p)
		}
	})

	t.Run("finishing every course awards the path qualification", func(t *testing.T) {
		completeCourse(t, h, learner, c2)
		if n := countQ(t, pool, `SELECT count(*) FROM certificates WHERE path_id = $1`, pathID); n != 0 {
			t.Fatalf("certificate too early: %d", n)
		}
		completeCourse(t, h, learner, c3)
		if n := countQ(t, pool, `SELECT count(*) FROM certificates WHERE path_id = $1 AND user_id = $2 AND course_id IS NULL`, pathID, lou); n != 1 {
			t.Fatalf("path certificates: %d", n)
		}
		code, me := call(t, h, learner, "GET", "/api/v1/me/paths", nil)
		mustStatus(t, code, 200, "my paths")
		p := obj(arr(me, "paths")[0])
		if p["complete"] != true || p["certificate_id"] == nil {
			t.Fatalf("completed path: %v", p)
		}
		mq := myQualification(t, h, learner, "stash-crew")
		if mq == nil || str(mq, "course_title") != "New Stash crew" {
			t.Fatalf("my qualification from the path: %v", mq)
		}
		// Unmarking and re-marking doesn't issue a second certificate.
		code, _ = call(t, h, learner, "PUT", "/api/v1/activities/"+p2+"/completion", map[string]any{"completed": false})
		mustStatus(t, code, 200, "unmark")
		code, _ = call(t, h, learner, "PUT", "/api/v1/activities/"+p2+"/completion", map[string]any{"completed": true})
		mustStatus(t, code, 200, "re-mark")
		if n := countQ(t, pool, `SELECT count(*) FROM certificates WHERE path_id = $1`, pathID); n != 1 {
			t.Fatalf("certificate should be idempotent: %d", n)
		}
	})

	t.Run("self-enrol and removal keep course enrolments", func(t *testing.T) {
		code, _ := call(t, h, admin, "PATCH", "/api/v1/paths/"+pathID, map[string]any{"self_enrol": true})
		mustStatus(t, code, 200, "open self-enrol")
		code, me := call(t, h, outsider, "GET", "/api/v1/me/paths", nil)
		mustStatus(t, code, 200, "outsider paths")
		if len(arr(me, "available")) != 1 {
			t.Fatalf("available paths: %v", me)
		}
		code, _ = call(t, h, outsider, "POST", "/api/v1/paths/"+pathID+"/enrol", nil)
		mustStatus(t, code, 201, "self-enrol")
		if src, st := enrolmentState(t, pool, c3, ids["outsider-otto"]); src != "manual" || st != "active" {
			t.Fatalf("self-enrol fan-out: %s/%s", src, st)
		}
		code, _ = call(t, h, admin, "DELETE", "/api/v1/path-enrolments/"+str(enr, "id"), nil)
		mustStatus(t, code, 204, "remove path enrolment")
		if _, st := enrolmentState(t, pool, c1, lou); st != "active" {
			t.Fatalf("manual course enrolments stay after leaving the path: %s", st)
		}
	})
}

func TestPathGroupSync(t *testing.T) {
	realm := &fakeRealm{groups: map[string][]string{"crew": {"ann", "bob"}, "cohort": {}}, failing: map[string]bool{}}
	h, pool, _ := setupGroupRouter(t, realm)
	signIn(t, h, admin, trainerA)
	userID := func(subject string) string {
		var id string
		if err := pool.QueryRow(context.Background(), `SELECT id::text FROM users WHERE issuer = 'dev' AND subject = $1`, subject).Scan(&id); err != nil {
			t.Fatalf("user %s: %v", subject, err)
		}
		return id
	}

	c1, _ := publishedCourse(t, h, "Crew 1")
	c2, _ := publishedCourse(t, h, "Crew 2")
	code, path := call(t, h, admin, "POST", "/api/v1/paths", map[string]any{"name": "Crew path"})
	mustStatus(t, code, 201, "create path")
	pathID := str(path, "id")
	code, _ = call(t, h, admin, "PUT", "/api/v1/paths/"+pathID+"/courses", map[string]any{"course_ids": []string{c1, c2}})
	mustStatus(t, code, 200, "set courses")
	// c1 also has its own (empty) group link, so its course sync must not
	// deactivate learners the path covers.
	code, _ = call(t, h, trainerA, "POST", "/api/v1/courses/"+c1+"/group-links", map[string]any{"group_name": "cohort"})
	mustStatus(t, code, 201, "course group link")

	code, out := call(t, h, admin, "POST", "/api/v1/paths/"+pathID+"/group-links", map[string]any{"group_name": "crew"})
	mustStatus(t, code, 201, "link crew to the path")
	if res := obj(out["sync"]); num(res, "enrolled") != 2 || num(res, "users_created") != 2 {
		t.Fatalf("first path sync: %v", out)
	}
	ann, bob := userID("ann"), userID("bob")
	for _, c := range []string{c1, c2} {
		for _, u := range []string{ann, bob} {
			if src, st := enrolmentState(t, pool, c, u); src != "group" || st != "active" {
				t.Fatalf("group fan-out on %s for %s: %s/%s", c, u, src, st)
			}
		}
	}

	t.Run("course sync leaves path-covered enrolments alone", func(t *testing.T) {
		code, res := call(t, h, trainerA, "POST", "/api/v1/courses/"+c1+"/group-links/sync", nil)
		mustStatus(t, code, 200, "course sync")
		if num(res, "deactivated") != 0 {
			t.Fatalf("course sync deactivated path members: %v", res)
		}
	})

	t.Run("leaving the group: path and course enrolments go inactive, not deleted", func(t *testing.T) {
		realm.set("crew", "bob")
		code, res := call(t, h, admin, "POST", "/api/v1/paths/"+pathID+"/group-links/sync", nil)
		mustStatus(t, code, 200, "path sync")
		if num(res, "deactivated") != 1 {
			t.Fatalf("path sync: %v", res)
		}
		code, enr := call(t, h, admin, "GET", "/api/v1/paths/"+pathID+"/enrolments", nil)
		mustStatus(t, code, 200, "path enrolments")
		if len(arr(enr, "enrolments")) != 2 {
			t.Fatalf("path enrolments must not be deleted: %v", enr)
		}
		for _, c := range []string{c1, c2} {
			if _, st := enrolmentState(t, pool, c, ann); st != "inactive" {
				t.Fatalf("ann on %s: %s", c, st)
			}
			if _, st := enrolmentState(t, pool, c, bob); st != "active" {
				t.Fatalf("bob on %s: %s", c, st)
			}
		}
	})

	t.Run("login groups join, leave and rejoin a path", func(t *testing.T) {
		dan := identity{user: "dan", groups: "crew"}
		code, _ := call(t, h, dan, "GET", "/api/v1/me", nil)
		mustStatus(t, code, 200, "dan signs in with crew")
		danID := userID("dan")
		if src, st := enrolmentState(t, pool, c2, danID); src != "group" || st != "active" {
			t.Fatalf("dan after login: %s/%s", src, st)
		}
		code, _ = call(t, h, identity{user: "dan", groups: ""}, "GET", "/api/v1/me", nil)
		mustStatus(t, code, 200, "dan signs in without crew")
		if _, st := enrolmentState(t, pool, c2, danID); st != "inactive" {
			t.Fatalf("dan after leaving: %s", st)
		}
		if n := countQ(t, pool, `SELECT count(*) FROM path_enrolments WHERE user_id = $1 AND status = 'inactive'`, danID); n != 1 {
			t.Fatalf("dan's path enrolment should be inactive: %d", n)
		}
		code, _ = call(t, h, dan, "GET", "/api/v1/me", nil)
		mustStatus(t, code, 200, "dan rejoins")
		if _, st := enrolmentState(t, pool, c1, danID); st != "active" {
			t.Fatalf("dan after rejoining: %s", st)
		}
	})

	t.Run("unlinking the group deactivates", func(t *testing.T) {
		code, out := call(t, h, admin, "DELETE", "/api/v1/paths/"+pathID+"/group-links?group=crew", nil)
		mustStatus(t, code, 200, "unlink")
		if num(out, "deactivated") != 2 { // bob and dan
			t.Fatalf("unlink: %v", out)
		}
		if _, st := enrolmentState(t, pool, c2, bob); st != "inactive" {
			t.Fatalf("bob after unlink: %s", st)
		}
	})
}
