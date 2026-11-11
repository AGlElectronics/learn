package http_test

// Quiz, question bank and gradebook tests. Same DB-backed setup (and the
// same TEST_DATABASE_URL warning) as courses_test.go.

import (
	nethttp "net/http"
	"testing"
)

var outsider = identity{user: "outsider-otto", groups: ""}

func arr(m map[string]any, key string) []any {
	a, _ := m[key].([]any)
	return a
}

func obj(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

func num(m map[string]any, key string) float64 {
	f, _ := m[key].(float64)
	return f
}

// optionByLabel finds an option id by label in a question or attempt question.
func optionByLabel(t *testing.T, q map[string]any, label string) string {
	t.Helper()
	for _, o := range arr(q, "options") {
		if str(obj(o), "label") == label {
			return str(obj(o), "id")
		}
	}
	t.Fatalf("option %q not found in %v", label, q)
	return ""
}

// attemptQuestion finds a question in an attempt by prompt.
func attemptQuestion(t *testing.T, attempt map[string]any, prompt string) map[string]any {
	t.Helper()
	for _, q := range arr(attempt, "questions") {
		if str(obj(q), "prompt") == prompt {
			return obj(q)
		}
	}
	t.Fatalf("question %q not in attempt", prompt)
	return nil
}

func createQuestion(t *testing.T, h nethttp.Handler, categoryID, kind, prompt string, options ...map[string]any) map[string]any {
	t.Helper()
	code, q := call(t, h, trainerA, "POST", "/api/v1/question-categories/"+categoryID+"/questions",
		map[string]any{"kind": kind, "prompt": prompt, "feedback": "Because " + prompt, "options": options})
	mustStatus(t, code, 201, "create question "+prompt)
	return q
}

func opt(label string, correct bool) map[string]any {
	return map[string]any{"label": label, "correct": correct}
}

// findActivity returns an activity from a course detail by id.
func findActivity(t *testing.T, detail map[string]any, id string) map[string]any {
	t.Helper()
	for _, s := range arr(detail, "sections") {
		for _, a := range arr(obj(s), "activities") {
			if str(obj(a), "id") == id {
				return obj(a)
			}
		}
	}
	t.Fatalf("activity %s not in course detail", id)
	return nil
}

func TestQuizzes(t *testing.T) {
	h := setupRouter(t)
	for _, id := range []identity{admin, trainerA, trainerB, learner, outsider} {
		code, _ := call(t, h, id, "GET", "/api/v1/me", nil)
		mustStatus(t, code, 200, "me for "+id.user)
	}

	code, course := call(t, h, trainerA, "POST", "/api/v1/courses", map[string]any{"title": "Fire safety"})
	mustStatus(t, code, 201, "create course")
	courseID := str(course, "id")
	code, section := call(t, h, trainerA, "POST", "/api/v1/courses/"+courseID+"/sections", map[string]any{"title": "Basics"})
	mustStatus(t, code, 201, "create section")
	sectionID := str(section, "id")

	code, quiz := call(t, h, trainerA, "POST", "/api/v1/sections/"+sectionID+"/activities", map[string]any{
		"kind": "quiz", "title": "Check", "body": "Two questions", "pass_percent": 100, "max_attempts": 2,
	})
	mustStatus(t, code, 201, "create quiz")
	quizID := str(quiz, "id")
	if str(quiz, "kind") != "quiz" || num(quiz, "pass_percent") != 100 || num(quiz, "max_attempts") != 2 {
		t.Fatalf("quiz settings not stored: %v", quiz)
	}

	code, cat := call(t, h, trainerA, "POST", "/api/v1/courses/"+courseID+"/question-categories", map[string]any{"name": "Safety"})
	mustStatus(t, code, 201, "create category")
	catID := str(cat, "id")

	t.Run("question validation", func(t *testing.T) {
		code, _ := call(t, h, trainerA, "POST", "/api/v1/question-categories/"+catID+"/questions",
			map[string]any{"kind": "single", "prompt": "Two right?", "options": []any{opt("a", true), opt("b", true)}})
		mustStatus(t, code, 422, "single with two correct")
		code, _ = call(t, h, trainerA, "POST", "/api/v1/question-categories/"+catID+"/questions",
			map[string]any{"kind": "multiple", "prompt": "None right?", "options": []any{opt("a", false), opt("b", false)}})
		mustStatus(t, code, 422, "multiple with none correct")
		code, _ = call(t, h, trainerA, "POST", "/api/v1/question-categories/"+catID+"/questions",
			map[string]any{"kind": "single", "prompt": "Lonely?", "options": []any{opt("a", true)}})
		mustStatus(t, code, 422, "one option")
		code, _ = call(t, h, trainerB, "POST", "/api/v1/question-categories/"+catID+"/questions",
			map[string]any{"kind": "single", "prompt": "Hijack", "options": []any{opt("a", true), opt("b", false)}})
		mustStatus(t, code, 404, "other trainer writes the draft course's bank")
	})

	q1 := createQuestion(t, h, catID, "single", "Fire exit sign colour?", opt("Green", true), opt("Red", false), opt("Blue", false))
	q2 := createQuestion(t, h, catID, "multiple", "Which are extinguishers?", opt("CO2", true), opt("Foam", true), opt("Hose", false))
	q1ID, q2ID := str(q1, "id"), str(q2, "id")

	t.Run("publish blocked by an empty quiz", func(t *testing.T) {
		code, body := call(t, h, trainerA, "POST", "/api/v1/courses/"+courseID+"/publish", nil)
		mustStatus(t, code, 422, "publish with empty quiz")
		if str(obj(body["error"]), "code") != "validation_failed" {
			t.Fatalf("publish error: %v", body)
		}
	})

	for _, qid := range []string{q1ID, q2ID} {
		code, _ := call(t, h, trainerA, "POST", "/api/v1/activities/"+quizID+"/slots", map[string]any{"question_id": qid})
		mustStatus(t, code, 201, "add fixed slot")
	}
	code, slots := call(t, h, trainerA, "GET", "/api/v1/activities/"+quizID+"/slots", nil)
	mustStatus(t, code, 200, "list slots")
	if len(arr(slots, "slots")) != 2 || len(arr(slots, "problems")) != 0 {
		t.Fatalf("slots: %v", slots)
	}

	t.Run("draft quiz is invisible to learners", func(t *testing.T) {
		code, _ := call(t, h, learner, "GET", "/api/v1/activities/"+quizID+"/quiz", nil)
		mustStatus(t, code, 404, "learner reads draft quiz")
		code, _ = call(t, h, learner, "POST", "/api/v1/activities/"+quizID+"/attempts", nil)
		mustStatus(t, code, 404, "learner starts draft quiz")
	})

	code, _ = call(t, h, trainerA, "POST", "/api/v1/courses/"+courseID+"/publish", nil)
	mustStatus(t, code, 200, "publish valid course")

	code, people := call(t, h, trainerA, "GET", "/api/v1/users?q=lou", nil)
	mustStatus(t, code, 200, "find learner")
	learnerID := str(obj(arr(people, "people")[0]), "id")
	code, _ = call(t, h, trainerA, "POST", "/api/v1/courses/"+courseID+"/enrolments",
		map[string]any{"user_id": learnerID, "role": "learner"})
	mustStatus(t, code, 201, "enrol learner")

	t.Run("unenrolled people can't take the quiz", func(t *testing.T) {
		code, _ := call(t, h, outsider, "GET", "/api/v1/activities/"+quizID+"/quiz", nil)
		mustStatus(t, code, 403, "outsider reads quiz")
		code, _ = call(t, h, outsider, "POST", "/api/v1/activities/"+quizID+"/attempts", nil)
		mustStatus(t, code, 403, "outsider starts attempt")
	})

	t.Run("manual completion is refused for quizzes", func(t *testing.T) {
		code, _ := call(t, h, learner, "PUT", "/api/v1/activities/"+quizID+"/completion", map[string]any{"completed": true})
		mustStatus(t, code, 400, "mark quiz done by hand")
	})

	// Attempt 1: start, check nothing leaks, resume, then fail it.
	code, a1 := call(t, h, learner, "POST", "/api/v1/activities/"+quizID+"/attempts", nil)
	mustStatus(t, code, 201, "start attempt 1")
	a1ID := str(a1, "id")

	t.Run("answers are hidden before submitting", func(t *testing.T) {
		qs := arr(a1, "questions")
		if len(qs) != 2 {
			t.Fatalf("attempt questions: got %d, want 2", len(qs))
		}
		for _, q := range qs {
			m := obj(q)
			for _, key := range []string{"correct", "correct_option_ids", "feedback", "points_awarded"} {
				if _, ok := m[key]; ok {
					t.Fatalf("open attempt leaks %q: %v", key, m)
				}
			}
			for _, o := range arr(m, "options") {
				if len(obj(o)) != 2 {
					t.Fatalf("option exposes more than id+label: %v", o)
				}
			}
		}
		code, again := call(t, h, learner, "GET", "/api/v1/attempts/"+a1ID, nil)
		mustStatus(t, code, 200, "read own open attempt")
		if _, ok := obj(arr(again, "questions")[0])["correct_option_ids"]; ok {
			t.Fatalf("GET open attempt leaks answers")
		}
	})

	t.Run("an open attempt is resumed", func(t *testing.T) {
		code, again := call(t, h, learner, "POST", "/api/v1/activities/"+quizID+"/attempts", nil)
		mustStatus(t, code, 200, "start again resumes")
		if str(again, "id") != a1ID {
			t.Fatalf("resume returned %s, want %s", str(again, "id"), a1ID)
		}
		code, _ = call(t, h, outsider, "GET", "/api/v1/attempts/"+a1ID, nil)
		mustStatus(t, code, 403, "outsider reads someone's attempt")
	})

	aq1 := attemptQuestion(t, a1, "Fire exit sign colour?")
	aq2 := attemptQuestion(t, a1, "Which are extinguishers?")
	green, red := optionByLabel(t, aq1, "Green"), optionByLabel(t, aq1, "Red")
	co2, foam, hose := optionByLabel(t, aq2, "CO2"), optionByLabel(t, aq2, "Foam"), optionByLabel(t, aq2, "Hose")

	t.Run("marking is all-or-nothing per question; failing writes no completion", func(t *testing.T) {
		code, res := call(t, h, learner, "POST", "/api/v1/attempts/"+a1ID+"/submit", map[string]any{
			"answers": map[string]any{q1ID: []string{green}, q2ID: []string{co2}},
		})
		mustStatus(t, code, 200, "submit attempt 1")
		if num(res, "score") != 1 || num(res, "max_score") != 2 || res["passed"] != false {
			t.Fatalf("attempt 1 result: score=%v max=%v passed=%v", res["score"], res["max_score"], res["passed"])
		}
		r1 := attemptQuestion(t, res, "Fire exit sign colour?")
		r2 := attemptQuestion(t, res, "Which are extinguishers?")
		if r1["correct"] != true || r2["correct"] != false || num(r2, "points_awarded") != 0 {
			t.Fatalf("per-question marks: %v / %v", r1, r2)
		}
		if len(arr(r2, "correct_option_ids")) != 2 || str(r2, "feedback") == "" {
			t.Fatalf("result must show correct options and feedback: %v", r2)
		}
		code, _ = call(t, h, learner, "POST", "/api/v1/attempts/"+a1ID+"/submit", map[string]any{"answers": map[string]any{}})
		mustStatus(t, code, 409, "submit twice")

		code, detail := call(t, h, learner, "GET", "/api/v1/courses/"+courseID, nil)
		mustStatus(t, code, 200, "detail after fail")
		a := findActivity(t, detail, quizID)
		st := obj(a["quiz"])
		if a["completed"] != false || num(st, "best_percent") != 50 || num(st, "attempts_used") != 1 || num(st, "attempts_left") != 1 {
			t.Fatalf("after fail: completed=%v standing=%v", a["completed"], st)
		}
	})

	t.Run("options lock once answered; prompt edits stay allowed", func(t *testing.T) {
		code, _ := call(t, h, trainerA, "PATCH", "/api/v1/options/"+red, map[string]any{"label": "Crimson"})
		mustStatus(t, code, 409, "edit locked option")
		code, _ = call(t, h, trainerA, "DELETE", "/api/v1/options/"+hose, nil)
		mustStatus(t, code, 409, "delete locked option")
		code, _ = call(t, h, trainerA, "POST", "/api/v1/questions/"+q1ID+"/options", opt("Yellow", false))
		mustStatus(t, code, 409, "add option to locked question")
		code, _ = call(t, h, trainerA, "PATCH", "/api/v1/questions/"+q2ID, map[string]any{"kind": "single"})
		mustStatus(t, code, 409, "change kind of locked question")
		code, q := call(t, h, trainerA, "PATCH", "/api/v1/questions/"+q1ID, map[string]any{"prompt": "Fire exit sign colour?"})
		mustStatus(t, code, 200, "edit prompt of locked question")
		if q["locked"] != true {
			t.Fatalf("question should report locked: %v", q)
		}
		code, _ = call(t, h, trainerA, "DELETE", "/api/v1/questions/"+q1ID, nil)
		mustStatus(t, code, 409, "delete question used in attempts")
	})

	t.Run("passing writes the completion", func(t *testing.T) {
		code, a2 := call(t, h, learner, "POST", "/api/v1/activities/"+quizID+"/attempts", nil)
		mustStatus(t, code, 201, "start attempt 2")
		code, res := call(t, h, learner, "POST", "/api/v1/attempts/"+str(a2, "id")+"/submit", map[string]any{
			"answers": map[string]any{q1ID: []string{green}, q2ID: []string{foam, co2}},
		})
		mustStatus(t, code, 200, "submit attempt 2")
		if res["passed"] != true || num(res, "percent") != 100 {
			t.Fatalf("attempt 2: %v", res)
		}
		code, detail := call(t, h, learner, "GET", "/api/v1/courses/"+courseID, nil)
		mustStatus(t, code, 200, "detail after pass")
		a := findActivity(t, detail, quizID)
		if a["completed"] != true || obj(a["quiz"])["passed"] != true {
			t.Fatalf("after pass: %v", a)
		}
	})

	t.Run("max attempts", func(t *testing.T) {
		code, _ := call(t, h, learner, "POST", "/api/v1/activities/"+quizID+"/attempts", nil)
		mustStatus(t, code, 409, "third attempt with max 2")
	})

	t.Run("gradebook is for staff", func(t *testing.T) {
		code, g := call(t, h, trainerA, "GET", "/api/v1/courses/"+courseID+"/gradebook", nil)
		mustStatus(t, code, 200, "trainer reads gradebook")
		rows := arr(g, "rows")
		if len(arr(g, "quizzes")) != 1 || len(rows) != 1 {
			t.Fatalf("gradebook shape: %v", g)
		}
		cell := obj(arr(obj(rows[0]), "cells")[0])
		if num(cell, "attempts") != 2 || num(cell, "best_percent") != 100 || cell["passed"] != true {
			t.Fatalf("gradebook cell: %v", cell)
		}
		code, _ = call(t, h, learner, "GET", "/api/v1/courses/"+courseID+"/gradebook", nil)
		mustStatus(t, code, 403, "learner reads gradebook")
		code, _ = call(t, h, trainerB, "GET", "/api/v1/courses/"+courseID+"/gradebook", nil)
		mustStatus(t, code, 403, "other trainer reads gradebook")
	})

	t.Run("reset deletes attempts and completions and unlocks options", func(t *testing.T) {
		reset := "/api/v1/activities/" + quizID + "/reset"
		code, _ := call(t, h, learner, "POST", reset, map[string]any{"confirm": "Check"})
		mustStatus(t, code, 403, "learner resets quiz")
		code, _ = call(t, h, trainerA, "POST", reset, map[string]any{"confirm": "Check"})
		mustStatus(t, code, 403, "trainer resets quiz (needs course.manage)")
		code, _ = call(t, h, admin, "POST", reset, map[string]any{"confirm": "check"})
		mustStatus(t, code, 422, "reset with the wrong confirmation")
		code, out := call(t, h, admin, "POST", reset, map[string]any{"confirm": "Check"})
		mustStatus(t, code, 200, "admin resets quiz")
		if num(out, "attempts") != 2 || num(out, "completions") != 1 {
			t.Fatalf("reset deleted %v, want 2 attempts and 1 completion", out)
		}
		code, _ = call(t, h, trainerA, "PATCH", "/api/v1/options/"+red, map[string]any{"label": "Crimson"})
		mustStatus(t, code, 200, "edit option after reset")
		code, detail := call(t, h, learner, "GET", "/api/v1/courses/"+courseID, nil)
		mustStatus(t, code, 200, "detail after reset")
		a := findActivity(t, detail, quizID)
		if a["completed"] != false || num(obj(a["quiz"]), "attempts_used") != 0 {
			t.Fatalf("after reset: %v", a)
		}
	})

	t.Run("single choice keeps exactly one correct option", func(t *testing.T) {
		code, q := call(t, h, trainerA, "PATCH", "/api/v1/options/"+red, map[string]any{"correct": true})
		mustStatus(t, code, 200, "move the correct answer")
		n := 0
		for _, o := range arr(q, "options") {
			if obj(o)["correct"] == true {
				n++
			}
		}
		if n != 1 {
			t.Fatalf("single choice has %d correct options", n)
		}
		code, _ = call(t, h, trainerA, "PATCH", "/api/v1/options/"+red, map[string]any{"correct": false})
		mustStatus(t, code, 422, "unmark the only correct option")
	})

	t.Run("random draw from a category, without duplicates; invalid draw blocks publish", func(t *testing.T) {
		code, pool := call(t, h, trainerA, "POST", "/api/v1/courses/"+courseID+"/question-categories", map[string]any{"name": "Pool"})
		mustStatus(t, code, 201, "create pool category")
		poolID := str(pool, "id")
		var fixedID string
		for i, p := range []string{"P1", "P2", "P3"} {
			q := createQuestion(t, h, poolID, "single", p, opt("yes", true), opt("no", false))
			if i == 0 {
				fixedID = str(q, "id")
			}
		}
		code, quiz2 := call(t, h, trainerA, "POST", "/api/v1/sections/"+sectionID+"/activities",
			map[string]any{"kind": "quiz", "title": "Random", "shuffle_options": false})
		mustStatus(t, code, 201, "create quiz 2")
		quiz2ID := str(quiz2, "id")
		code, _ = call(t, h, trainerA, "POST", "/api/v1/activities/"+quiz2ID+"/slots", map[string]any{"question_id": fixedID})
		mustStatus(t, code, 201, "fixed slot from pool")
		code, drawSlot := call(t, h, trainerA, "POST", "/api/v1/activities/"+quiz2ID+"/slots",
			map[string]any{"category_id": poolID, "draw_count": 3})
		mustStatus(t, code, 201, "draw slot")

		code, _ = call(t, h, trainerA, "POST", "/api/v1/courses/"+courseID+"/publish", nil)
		mustStatus(t, code, 422, "publish with a draw larger than the pool")

		code, _ = call(t, h, trainerA, "PATCH", "/api/v1/slots/"+str(drawSlot, "id"), map[string]any{"draw_count": 2})
		mustStatus(t, code, 200, "shrink draw")
		code, _ = call(t, h, trainerA, "POST", "/api/v1/courses/"+courseID+"/publish", nil)
		mustStatus(t, code, 200, "publish once the draw fits")

		code, att := call(t, h, learner, "POST", "/api/v1/activities/"+quiz2ID+"/attempts", nil)
		mustStatus(t, code, 201, "start random quiz")
		seen := map[string]bool{}
		for _, q := range arr(att, "questions") {
			seen[str(obj(q), "prompt")] = true
		}
		if len(arr(att, "questions")) != 3 || !seen["P1"] || !seen["P2"] || !seen["P3"] {
			t.Fatalf("random draw: got %v", seen)
		}
		code, _ = call(t, h, learner, "PUT", "/api/v1/activities/"+quiz2ID+"/completion", map[string]any{"completed": true})
		mustStatus(t, code, 400, "manual completion on random quiz")
	})
}
