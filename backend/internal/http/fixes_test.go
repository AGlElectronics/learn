package http_test

// Session booking honours prerequisites (403 locked, staff exempt), and
// inactive enrolments are never overdue. Same DB-backed setup as
// courses_test.go.

import (
	"testing"
	"time"
)

func TestSessionBookingRespectsPrerequisites(t *testing.T) {
	h := setupRouter(t)
	ids := signIn(t, h, trainerA, learner)

	code, course := call(t, h, trainerA, "POST", "/api/v1/courses", map[string]any{"title": "Locked session"})
	mustStatus(t, code, 201, "create course")
	courseID := str(course, "id")
	code, section := call(t, h, trainerA, "POST", "/api/v1/courses/"+courseID+"/sections", map[string]any{"title": "S"})
	mustStatus(t, code, 201, "section")
	code, page := call(t, h, trainerA, "POST", "/api/v1/sections/"+str(section, "id")+"/activities",
		map[string]any{"title": "Safety intro", "body": "Read first"})
	mustStatus(t, code, 201, "page")
	code, session := call(t, h, trainerA, "POST", "/api/v1/sections/"+str(section, "id")+"/activities",
		map[string]any{"kind": "session", "title": "Practical", "body": ""})
	mustStatus(t, code, 201, "session")
	code, _ = call(t, h, trainerA, "PUT", "/api/v1/activities/"+str(session, "id")+"/prerequisites",
		map[string]any{"requires": []string{str(page, "id")}})
	mustStatus(t, code, 200, "session requires the page")
	now := time.Now()
	code, slot := call(t, h, trainerA, "POST", "/api/v1/activities/"+str(session, "id")+"/session-slots",
		map[string]any{"starts_at": rfc(now.Add(2 * time.Hour)), "ends_at": rfc(now.Add(3 * time.Hour)), "capacity": 5})
	mustStatus(t, code, 201, "slot")
	slotID := str(slot, "id")
	code, _ = call(t, h, trainerA, "POST", "/api/v1/courses/"+courseID+"/publish", nil)
	mustStatus(t, code, 200, "publish")
	code, _ = call(t, h, trainerA, "POST", "/api/v1/courses/"+courseID+"/enrolments", map[string]any{"user_id": ids[learner.user]})
	mustStatus(t, code, 201, "enrol")

	code, out := call(t, h, learner, "POST", "/api/v1/slots/"+slotID+"/booking", nil)
	mustStatus(t, code, 403, "book a locked session")
	if errCode(out) != "locked" {
		t.Fatalf("error code = %q, want locked", errCode(out))
	}
	code, _ = call(t, h, trainerA, "POST", "/api/v1/slots/"+slotID+"/booking", nil)
	mustStatus(t, code, 201, "staff are exempt from the lock")

	code, _ = call(t, h, learner, "PUT", "/api/v1/activities/"+str(page, "id")+"/completion", map[string]any{"completed": true})
	mustStatus(t, code, 200, "complete the prerequisite")
	code, _ = call(t, h, learner, "POST", "/api/v1/slots/"+slotID+"/booking", nil)
	mustStatus(t, code, 201, "book once unlocked")
}

func TestInactiveEnrolmentsAreNotOverdue(t *testing.T) {
	h := setupRouter(t)
	ids := signIn(t, h, trainerA, learner)

	code, course := call(t, h, trainerA, "POST", "/api/v1/courses", map[string]any{"title": "Due course"})
	mustStatus(t, code, 201, "create course")
	courseID := str(course, "id")
	code, section := call(t, h, trainerA, "POST", "/api/v1/courses/"+courseID+"/sections", map[string]any{"title": "S"})
	mustStatus(t, code, 201, "section")
	code, _ = call(t, h, trainerA, "POST", "/api/v1/sections/"+str(section, "id")+"/activities", map[string]any{"title": "P", "body": ""})
	mustStatus(t, code, 201, "page")
	code, _ = call(t, h, trainerA, "POST", "/api/v1/courses/"+courseID+"/publish", nil)
	mustStatus(t, code, 200, "publish")
	code, e := call(t, h, trainerA, "POST", "/api/v1/courses/"+courseID+"/enrolments", map[string]any{"user_id": ids[learner.user]})
	mustStatus(t, code, 201, "enrol")
	past := time.Now().Add(-48 * time.Hour).UTC().Format(time.RFC3339)
	code, e = call(t, h, trainerA, "PATCH", "/api/v1/enrolments/"+str(e, "id"), map[string]any{"due_at": past})
	mustStatus(t, code, 200, "set a past due date")
	if e["overdue"] != true {
		t.Fatalf("active enrolment should be overdue: %v", e)
	}

	// The learner leaves the synced group: the enrolment goes inactive.
	if _, err := testDB(t).Exec(`UPDATE enrolments SET status = 'inactive', left_group_at = now() WHERE id = $1`, str(e, "id")); err != nil {
		t.Fatalf("deactivate: %v", err)
	}
	code, list := call(t, h, trainerA, "GET", "/api/v1/courses/"+courseID+"/enrolments", nil)
	mustStatus(t, code, 200, "enrolments")
	for _, x := range arr(list, "enrolments") {
		if str(obj(x), "id") == str(e, "id") && obj(x)["overdue"] != false {
			t.Fatalf("inactive enrolment listed as overdue: %v", x)
		}
	}
	code, mine := call(t, h, learner, "GET", "/api/v1/courses/mine", nil)
	mustStatus(t, code, 200, "mine")
	for _, c := range arr(mine, "courses") {
		if str(obj(c), "id") == courseID && obj(c)["overdue"] != false {
			t.Fatalf("inactive course shown overdue: %v", c)
		}
	}
	code, g := call(t, h, trainerA, "GET", "/api/v1/courses/"+courseID+"/gradebook", nil)
	mustStatus(t, code, 200, "gradebook")
	if len(arr(g, "rows")) != 0 {
		t.Fatalf("inactive learner in gradebook: %v", g)
	}
}
