package http_test

// Course and session feedback: off by default, eligibility (finished
// course / attended session), one per person per target, anonymity and
// the staff summary. Same DB-backed setup as courses_test.go.

import (
	"testing"
	"time"
)

func TestFeedback(t *testing.T) {
	h := setupRouter(t)
	ids := signIn(t, h, trainerA, trainerB, learner, kim)

	code, course := call(t, h, trainerA, "POST", "/api/v1/courses", map[string]any{"title": "Feedback course"})
	mustStatus(t, code, 201, "create course")
	courseID := str(course, "id")
	code, section := call(t, h, trainerA, "POST", "/api/v1/courses/"+courseID+"/sections", map[string]any{"title": "S"})
	mustStatus(t, code, 201, "section")
	code, page := call(t, h, trainerA, "POST", "/api/v1/sections/"+str(section, "id")+"/activities",
		map[string]any{"title": "Read me", "body": "Text"})
	mustStatus(t, code, 201, "page")
	pageID := str(page, "id")
	code, session := call(t, h, trainerA, "POST", "/api/v1/sections/"+str(section, "id")+"/activities",
		map[string]any{"kind": "session", "title": "Walkthrough", "body": ""})
	mustStatus(t, code, 201, "session")
	sessionID := str(session, "id")
	now := time.Now()
	code, slot := call(t, h, trainerA, "POST", "/api/v1/activities/"+sessionID+"/session-slots",
		map[string]any{"starts_at": rfc(now.Add(2 * time.Hour)), "ends_at": rfc(now.Add(3 * time.Hour)), "capacity": 5})
	mustStatus(t, code, 201, "slot")
	slotID := str(slot, "id")
	code, _ = call(t, h, trainerA, "POST", "/api/v1/courses/"+courseID+"/publish", nil)
	mustStatus(t, code, 200, "publish")
	for _, who := range []identity{learner, kim} {
		code, _ := call(t, h, trainerA, "POST", "/api/v1/courses/"+courseID+"/enrolments", map[string]any{"user_id": ids[who.user]})
		mustStatus(t, code, 201, "enrol "+who.user)
	}
	attend := func(who identity) {
		t.Helper()
		code, b := call(t, h, who, "POST", "/api/v1/slots/"+slotID+"/booking", nil)
		mustStatus(t, code, 201, "book "+who.user)
		code, _ = call(t, h, trainerA, "PUT", "/api/v1/signups/"+str(b, "id"), map[string]any{"status": "attended"})
		mustStatus(t, code, 200, "mark attended "+who.user)
	}
	rate := func(who identity, body map[string]any, want int, what string) {
		t.Helper()
		code, _ := call(t, h, who, "POST", "/api/v1/courses/"+courseID+"/feedback", body)
		mustStatus(t, code, want, what)
	}
	courseRating := map[string]any{"rating": 5, "comment": "Great course"}

	t.Run("off by default", func(t *testing.T) {
		code, mine := call(t, h, learner, "GET", "/api/v1/courses/"+courseID+"/feedback/mine", nil)
		mustStatus(t, code, 200, "mine")
		if mine["enabled"] != false {
			t.Fatalf("feedback should be off: %v", mine)
		}
		rate(learner, courseRating, 403, "rate while disabled")
	})

	t.Run("settings are for staff", func(t *testing.T) {
		code, _ := call(t, h, learner, "PUT", "/api/v1/courses/"+courseID+"/feedback-settings", map[string]any{"enabled": true})
		mustStatus(t, code, 403, "learner changes settings")
		code, _ = call(t, h, trainerB, "PUT", "/api/v1/courses/"+courseID+"/feedback-settings", map[string]any{"enabled": true})
		mustStatus(t, code, 403, "other trainer changes settings")
		code, fs := call(t, h, trainerA, "PUT", "/api/v1/courses/"+courseID+"/feedback-settings", map[string]any{"enabled": true})
		mustStatus(t, code, 200, "enable feedback")
		if fs["enabled"] != true || fs["anonymous"] != false {
			t.Fatalf("settings: %v", fs)
		}
	})

	t.Run("eligibility", func(t *testing.T) {
		rate(learner, courseRating, 403, "rate an unfinished course")
		rate(learner, map[string]any{"activity_id": sessionID, "rating": 4}, 403, "rate a session not attended")
		rate(learner, map[string]any{"activity_id": pageID, "rating": 4}, 422, "rate a page")
		attend(learner)
		rate(learner, courseRating, 403, "course still has an unread page")
		code, _ := call(t, h, learner, "PUT", "/api/v1/activities/"+pageID+"/completion", map[string]any{"completed": true})
		mustStatus(t, code, 200, "complete page")
		code, mine := call(t, h, learner, "GET", "/api/v1/courses/"+courseID+"/feedback/mine", nil)
		mustStatus(t, code, 200, "mine")
		c, s := obj(mine["course"]), obj(arr(mine, "sessions")[0])
		if c["eligible"] != true || c["submitted"] != false || s["eligible"] != true || s["submitted"] != false {
			t.Fatalf("mine before rating: %v", mine)
		}
	})

	t.Run("one rating per target", func(t *testing.T) {
		rate(learner, map[string]any{"rating": 6}, 422, "rating out of range")
		rate(learner, courseRating, 201, "rate the course")
		rate(learner, map[string]any{"rating": 1}, 409, "rate the course again")
		rate(learner, map[string]any{"activity_id": sessionID, "rating": 4}, 201, "rate the session")
		rate(learner, map[string]any{"activity_id": sessionID, "rating": 4}, 409, "rate the session again")
		code, mine := call(t, h, learner, "GET", "/api/v1/courses/"+courseID+"/feedback/mine", nil)
		mustStatus(t, code, 200, "mine")
		if obj(mine["course"])["submitted"] != true || obj(arr(mine, "sessions")[0])["submitted"] != true {
			t.Fatalf("mine after rating: %v", mine)
		}
	})

	t.Run("anonymous feedback stores no user id", func(t *testing.T) {
		code, _ := call(t, h, trainerA, "PUT", "/api/v1/courses/"+courseID+"/feedback-settings", map[string]any{"anonymous": true})
		mustStatus(t, code, 200, "make anonymous")
		attend(kim)
		rate(kim, map[string]any{"activity_id": sessionID, "rating": 2, "comment": "Too long"}, 201, "kim rates anonymously")
		rate(kim, map[string]any{"activity_id": sessionID, "rating": 5}, 409, "kim rates again")
		code, mine := call(t, h, kim, "GET", "/api/v1/courses/"+courseID+"/feedback/mine", nil)
		mustStatus(t, code, 200, "kim mine")
		if obj(arr(mine, "sessions")[0])["submitted"] != true || obj(mine["course"])["eligible"] != false {
			t.Fatalf("kim mine: %v", mine)
		}
		var withUser, anonymous int
		if err := testDB(t).QueryRow(`
			SELECT count(*) FILTER (WHERE user_id IS NOT NULL), count(*) FILTER (WHERE user_id IS NULL)
			  FROM feedback WHERE course_id = $1`, courseID).Scan(&withUser, &anonymous); err != nil {
			t.Fatalf("count feedback: %v", err)
		}
		if withUser != 2 || anonymous != 1 {
			t.Fatalf("stored identities: %d with user, %d anonymous", withUser, anonymous)
		}
		var kimRows int
		if err := testDB(t).QueryRow(`SELECT count(*) FROM feedback WHERE user_id = $1`, ids[kim.user]).Scan(&kimRows); err != nil {
			t.Fatalf("count kim: %v", err)
		}
		if kimRows != 0 {
			t.Fatal("anonymous feedback must not store the user id")
		}
	})

	t.Run("staff summary", func(t *testing.T) {
		code, _ := call(t, h, learner, "GET", "/api/v1/courses/"+courseID+"/feedback", nil)
		mustStatus(t, code, 403, "learner reads summary")
		code, sum := call(t, h, trainerA, "GET", "/api/v1/courses/"+courseID+"/feedback", nil)
		mustStatus(t, code, 200, "summary")
		if sum["enabled"] != true || sum["anonymous"] != true {
			t.Fatalf("summary settings: %v", sum)
		}
		c := obj(sum["course"])
		if num(c, "count") != 1 || num(c, "average") != 5 || len(arr(c, "comments")) != 1 {
			t.Fatalf("course summary: %v", c)
		}
		if cm := obj(arr(c, "comments")[0]); str(cm, "comment") != "Great course" || str(cm, "display_name") == "" {
			t.Fatalf("named comment: %v", cm)
		}
		s := obj(arr(sum, "sessions")[0])
		if str(s, "activity_id") != sessionID || num(s, "count") != 2 || num(s, "average") != 3 {
			t.Fatalf("session summary: %v", s)
		}
		comments := arr(s, "comments")
		if len(comments) != 1 || str(obj(comments[0]), "comment") != "Too long" || str(obj(comments[0]), "display_name") != "" {
			t.Fatalf("anonymous comment: %v", comments)
		}
	})
}
