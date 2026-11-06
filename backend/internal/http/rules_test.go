package http_test

// Activity prerequisites (locks, cycles) and due dates. Same DB-backed
// setup (and TEST_DATABASE_URL warning) as courses_test.go.

import (
	"testing"
	"time"
)

func TestPrerequisitesAndDueDates(t *testing.T) {
	store := newFakeStore()
	h := setupRouterWithFiles(t, store)
	for _, id := range []identity{admin, trainerA, trainerB, learner, outsider} {
		code, _ := call(t, h, id, "GET", "/api/v1/me", nil)
		mustStatus(t, code, 200, "me for "+id.user)
	}
	code, course := call(t, h, trainerA, "POST", "/api/v1/courses", map[string]any{"title": "Stewarding"})
	mustStatus(t, code, 201, "create course")
	courseID := str(course, "id")
	code, section := call(t, h, trainerA, "POST", "/api/v1/courses/"+courseID+"/sections", map[string]any{"title": "S"})
	mustStatus(t, code, 201, "create section")
	sectionID := str(section, "id")
	mk := func(body map[string]any) string {
		t.Helper()
		code, a := call(t, h, trainerA, "POST", "/api/v1/sections/"+sectionID+"/activities", body)
		mustStatus(t, code, 201, "create activity")
		return str(a, "id")
	}
	pageA := mk(map[string]any{"title": "Intro", "body": "Read this first"})
	pageB := mk(map[string]any{"title": "Radio", "body": "Secret radio codes"})
	quizQ := mk(map[string]any{"kind": "quiz", "title": "Final check", "body": ""})
	code, cat := call(t, h, trainerA, "POST", "/api/v1/courses/"+courseID+"/question-categories", map[string]any{"name": "C"})
	mustStatus(t, code, 201, "category")
	q := createQuestion(t, h, str(cat, "id"), "single", "Channel 1 is for?", opt("Ops", true), opt("Chat", false))
	code, _ = call(t, h, trainerA, "POST", "/api/v1/activities/"+quizQ+"/slots", map[string]any{"question_id": str(q, "id")})
	mustStatus(t, code, 201, "slot")

	// Page B shows an audio clip; the clip must stay locked with the page.
	clip := uploadMedia(t, h, store, courseID, "codes.mp3", "audio/mpeg", "")
	code, _ = call(t, h, trainerA, "POST", "/api/v1/activities/"+pageB+"/media", map[string]any{"media_id": str(clip, "id")})
	mustStatus(t, code, 201, "attach clip to B")

	code, _ = call(t, h, trainerA, "PUT", "/api/v1/activities/"+pageB+"/prerequisites", map[string]any{"requires": []string{pageA}})
	mustStatus(t, code, 200, "B requires A")
	code, out := call(t, h, trainerA, "PUT", "/api/v1/activities/"+quizQ+"/prerequisites", map[string]any{"requires": []string{pageB, pageB}})
	mustStatus(t, code, 200, "Q requires B (deduplicated)")
	if len(arr(out, "prerequisites")) != 1 {
		t.Fatalf("prerequisites: %v", out)
	}

	t.Run("rules are validated on save", func(t *testing.T) {
		code, _ := call(t, h, trainerA, "PUT", "/api/v1/activities/"+pageA+"/prerequisites", map[string]any{"requires": []string{quizQ}})
		mustStatus(t, code, 422, "A requires Q closes a cycle A→Q→B→A")
		code, _ = call(t, h, trainerA, "PUT", "/api/v1/activities/"+pageA+"/prerequisites", map[string]any{"requires": []string{pageA}})
		mustStatus(t, code, 422, "self-prerequisite")
		code, other := call(t, h, trainerA, "POST", "/api/v1/courses", map[string]any{"title": "Other"})
		mustStatus(t, code, 201, "other course")
		code, os := call(t, h, trainerA, "POST", "/api/v1/courses/"+str(other, "id")+"/sections", map[string]any{"title": "S"})
		mustStatus(t, code, 201, "other section")
		code, oa := call(t, h, trainerA, "POST", "/api/v1/sections/"+str(os, "id")+"/activities", map[string]any{"title": "X", "body": ""})
		mustStatus(t, code, 201, "other activity")
		code, _ = call(t, h, trainerA, "PUT", "/api/v1/activities/"+pageA+"/prerequisites", map[string]any{"requires": []string{str(oa, "id")}})
		mustStatus(t, code, 422, "cross-course prerequisite")
		code, _ = call(t, h, trainerB, "PUT", "/api/v1/activities/"+pageA+"/prerequisites", map[string]any{"requires": []string{}})
		mustStatus(t, code, 404, "other trainer edits rules of a draft")
		// The rejected saves left the existing rules alone.
		code, detail := call(t, h, trainerA, "GET", "/api/v1/courses/"+courseID, nil)
		mustStatus(t, code, 200, "staff detail")
		if len(arr(findActivity(t, detail, pageB), "prerequisites")) != 1 || len(arr(findActivity(t, detail, pageA), "prerequisites")) != 0 {
			t.Fatalf("rules changed by a rejected save: %v", detail)
		}
	})

	t.Run("course due days", func(t *testing.T) {
		code, _ := call(t, h, trainerA, "PATCH", "/api/v1/courses/"+courseID, map[string]any{"due_days": 0})
		mustStatus(t, code, 422, "due_days must be positive")
		code, _ = call(t, h, trainerA, "PATCH", "/api/v1/courses/"+courseID, map[string]any{"due_days": 7, "self_enrol": true})
		mustStatus(t, code, 200, "set due_days")
		code, detail := call(t, h, trainerA, "GET", "/api/v1/courses/"+courseID, nil)
		mustStatus(t, code, 200, "detail")
		if num(detail, "due_days") != 7 {
			t.Fatalf("due_days not stored: %v", detail["due_days"])
		}
	})

	code, _ = call(t, h, trainerA, "POST", "/api/v1/courses/"+courseID+"/publish", nil)
	mustStatus(t, code, 200, "publish")
	code, people := call(t, h, trainerA, "GET", "/api/v1/users?q=lou", nil)
	mustStatus(t, code, 200, "people")
	learnerID := str(obj(arr(people, "people")[0]), "id")
	code, _ = call(t, h, trainerA, "POST", "/api/v1/courses/"+courseID+"/enrolments", map[string]any{"user_id": learnerID})
	mustStatus(t, code, 201, "enrol learner")
	code, _ = call(t, h, trainerB, "POST", "/api/v1/courses/"+courseID+"/enrol", nil)
	mustStatus(t, code, 201, "trainer B self-enrols")

	var learnerEnrolment string
	t.Run("due_at is set at enrolment, staff and self-enrol alike", func(t *testing.T) {
		code, list := call(t, h, trainerA, "GET", "/api/v1/courses/"+courseID+"/enrolments", nil)
		mustStatus(t, code, 200, "enrolments")
		want := time.Now().Add(7 * 24 * time.Hour)
		for _, e := range arr(list, "enrolments") {
			e := obj(e)
			due, _ := time.Parse(time.RFC3339Nano, str(e, "due_at"))
			switch str(e, "role") {
			case "learner":
				if due.IsZero() || due.Sub(want).Abs() > time.Minute {
					t.Fatalf("learner %s due_at = %v, want about %v", str(e, "display_name"), e["due_at"], want)
				}
				if str(e, "user_id") == learnerID {
					learnerEnrolment = str(e, "id")
				}
			case "trainer":
				if e["due_at"] != nil {
					t.Fatalf("trainers get no due date: %v", e)
				}
			}
		}
		code, mine := call(t, h, learner, "GET", "/api/v1/courses/mine", nil)
		mustStatus(t, code, 200, "mine")
		c := obj(arr(mine, "courses")[0])
		if str(c, "due_at") == "" || c["overdue"] != false {
			t.Fatalf("mine due: %v", c)
		}
	})

	t.Run("locked activities for the learner", func(t *testing.T) {
		code, detail := call(t, h, learner, "GET", "/api/v1/courses/"+courseID, nil)
		mustStatus(t, code, 200, "learner detail")
		a, b, qz := findActivity(t, detail, pageA), findActivity(t, detail, pageB), findActivity(t, detail, quizQ)
		if a["locked"] != false || b["locked"] != true || qz["locked"] != true {
			t.Fatalf("lock flags: A=%v B=%v Q=%v", a["locked"], b["locked"], qz["locked"])
		}
		if str(b, "body") != "" {
			t.Fatalf("locked page body leaked: %q", str(b, "body"))
		}
		if needs := arr(b, "needs"); len(needs) != 1 || needs[0] != "Intro" {
			t.Fatalf("B needs: %v", b["needs"])
		}
		code, out := call(t, h, learner, "PUT", "/api/v1/activities/"+pageB+"/completion", map[string]any{"completed": true})
		mustStatus(t, code, 403, "complete a locked page")
		if errCode(out) != "locked" {
			t.Fatalf("error code = %q, want locked", errCode(out))
		}
		code, out = call(t, h, learner, "POST", "/api/v1/activities/"+quizQ+"/attempts", nil)
		mustStatus(t, code, 403, "start a locked quiz")
		if errCode(out) != "locked" {
			t.Fatalf("error code = %q, want locked", errCode(out))
		}
		code, out = call(t, h, learner, "GET", "/api/v1/media/"+str(clip, "id")+"/url", nil)
		mustStatus(t, code, 403, "media of a locked page")
		if errCode(out) != "locked" {
			t.Fatalf("error code = %q, want locked", errCode(out))
		}
		code, _ = call(t, h, learner, "GET", "/api/v1/activities/"+pageB+"/media", nil)
		mustStatus(t, code, 403, "attachments of a locked page")
	})

	t.Run("staff are never locked", func(t *testing.T) {
		code, detail := call(t, h, trainerA, "GET", "/api/v1/courses/"+courseID, nil)
		mustStatus(t, code, 200, "staff detail")
		if b := findActivity(t, detail, pageB); b["locked"] != false || str(b, "body") == "" {
			t.Fatalf("staff see B locked: %v", b)
		}
		code, _ = call(t, h, trainerA, "POST", "/api/v1/activities/"+quizQ+"/attempts", nil)
		mustStatus(t, code, 201, "trainer starts the quiz")
		code, _ = call(t, h, admin, "POST", "/api/v1/activities/"+quizQ+"/attempts", nil)
		mustStatus(t, code, 201, "admin starts the quiz")
		code, _ = call(t, h, trainerA, "GET", "/api/v1/media/"+str(clip, "id")+"/url", nil)
		mustStatus(t, code, 200, "staff view media of the locked page")
	})

	t.Run("completing prerequisites unlocks in order", func(t *testing.T) {
		code, _ := call(t, h, learner, "PUT", "/api/v1/activities/"+pageA+"/completion", map[string]any{"completed": true})
		mustStatus(t, code, 200, "complete A")
		code, detail := call(t, h, learner, "GET", "/api/v1/courses/"+courseID, nil)
		mustStatus(t, code, 200, "detail")
		if b := findActivity(t, detail, pageB); b["locked"] != false || str(b, "body") != "Secret radio codes" {
			t.Fatalf("B after A: %v", b)
		}
		if findActivity(t, detail, quizQ)["locked"] != true {
			t.Fatal("Q still needs B")
		}
		code, _ = call(t, h, learner, "GET", "/api/v1/media/"+str(clip, "id")+"/url", nil)
		mustStatus(t, code, 200, "clip opens with B")
		code, _ = call(t, h, learner, "PUT", "/api/v1/activities/"+pageB+"/completion", map[string]any{"completed": true})
		mustStatus(t, code, 200, "complete B")
		code, _ = call(t, h, learner, "POST", "/api/v1/activities/"+quizQ+"/attempts", nil)
		mustStatus(t, code, 201, "start Q")
	})

	t.Run("staff override due dates; overdue shows everywhere", func(t *testing.T) {
		past := time.Now().Add(-48 * time.Hour).UTC().Format(time.RFC3339)
		code, _ := call(t, h, learner, "PATCH", "/api/v1/enrolments/"+learnerEnrolment, map[string]any{"due_at": past})
		mustStatus(t, code, 403, "learner moves their own due date")
		code, e := call(t, h, trainerA, "PATCH", "/api/v1/enrolments/"+learnerEnrolment, map[string]any{"due_at": past})
		mustStatus(t, code, 200, "staff sets a past due date")
		if e["overdue"] != true {
			t.Fatalf("enrolment overdue: %v", e)
		}
		code, mine := call(t, h, learner, "GET", "/api/v1/courses/mine", nil)
		mustStatus(t, code, 200, "mine")
		if obj(arr(mine, "courses")[0])["overdue"] != true {
			t.Fatalf("mine overdue: %v", mine)
		}
		code, detail := call(t, h, learner, "GET", "/api/v1/courses/"+courseID, nil)
		mustStatus(t, code, 200, "detail")
		if detail["overdue"] != true {
			t.Fatalf("detail overdue: %v", detail["overdue"])
		}
		code, g := call(t, h, trainerA, "GET", "/api/v1/courses/"+courseID+"/gradebook", nil)
		mustStatus(t, code, 200, "gradebook")
		overdue := false
		for _, r := range arr(g, "rows") {
			if str(obj(r), "user_id") == learnerID {
				overdue = obj(r)["overdue"] == true
			}
		}
		if !overdue {
			t.Fatalf("gradebook overdue: %v", g)
		}
		code, e = call(t, h, trainerA, "PATCH", "/api/v1/enrolments/"+learnerEnrolment, map[string]any{"due_at": nil})
		mustStatus(t, code, 200, "clear due date")
		if e["due_at"] != nil || e["overdue"] != false {
			t.Fatalf("cleared: %v", e)
		}
	})
}
