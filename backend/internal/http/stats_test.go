package http_test

// Question statistics: attempts, % correct, option picks and hotspot hits
// from submitted attempts. Same DB-backed setup as courses_test.go.

import (
	"math"
	"testing"
)

func TestQuestionStats(t *testing.T) {
	store := newFakeStore()
	h := setupRouterWithFiles(t, store)
	ids := signIn(t, h, trainerA, trainerB, learner, kim)

	code, course := call(t, h, trainerA, "POST", "/api/v1/courses", map[string]any{"title": "Stats"})
	mustStatus(t, code, 201, "create course")
	courseID := str(course, "id")
	code, section := call(t, h, trainerA, "POST", "/api/v1/courses/"+courseID+"/sections", map[string]any{"title": "S"})
	mustStatus(t, code, 201, "section")
	code, quiz := call(t, h, trainerA, "POST", "/api/v1/sections/"+str(section, "id")+"/activities",
		map[string]any{"kind": "quiz", "title": "Q", "body": ""})
	mustStatus(t, code, 201, "quiz")
	quizID := str(quiz, "id")
	code, cat := call(t, h, trainerA, "POST", "/api/v1/courses/"+courseID+"/question-categories", map[string]any{"name": "C"})
	mustStatus(t, code, 201, "category")
	catID := str(cat, "id")

	single := createQuestion(t, h, catID, "single", "Exit sign colour?", opt("Green", true), opt("Red", false), opt("Blue", false))
	multi := createQuestion(t, h, catID, "multiple", "Extinguishers?", opt("CO2", true), opt("Foam", true), opt("Hose", false))
	unused := createQuestion(t, h, catID, "single", "Never asked?", opt("Yes", true), opt("No", false))
	img := uploadMedia(t, h, store, courseID, "map.png", "image/png", "Site map")
	code, hot := call(t, h, trainerA, "POST", "/api/v1/question-categories/"+catID+"/questions", map[string]any{
		"kind": "hotspot", "prompt": "Tap both", "media_id": str(img, "id"), "regions": []any{
			map[string]any{"shape": "rect", "coords": map[string]any{"x": 0.1, "y": 0.1, "w": 0.2, "h": 0.2}, "correct": true},
			map[string]any{"shape": "circle", "coords": map[string]any{"cx": 0.7, "cy": 0.7, "r": 0.1}, "correct": true},
			map[string]any{"shape": "rect", "coords": map[string]any{"x": 0.5, "y": 0.1, "w": 0.1, "h": 0.1}, "correct": false},
		},
	})
	mustStatus(t, code, 201, "hotspot question")
	singleID, multiID, hotID := str(single, "id"), str(multi, "id"), str(hot, "id")
	for _, qid := range []string{singleID, multiID, hotID} {
		code, _ := call(t, h, trainerA, "POST", "/api/v1/activities/"+quizID+"/slots", map[string]any{"question_id": qid})
		mustStatus(t, code, 201, "slot")
	}
	code, _ = call(t, h, trainerA, "POST", "/api/v1/courses/"+courseID+"/publish", nil)
	mustStatus(t, code, 200, "publish")
	for _, who := range []identity{learner, kim} {
		code, _ := call(t, h, trainerA, "POST", "/api/v1/courses/"+courseID+"/enrolments", map[string]any{"user_id": ids[who.user]})
		mustStatus(t, code, 201, "enrol "+who.user)
	}

	pt := func(x, y float64) map[string]any { return map[string]any{"x": x, "y": y} }
	take := func(who identity, singlePick []string, multiPick []string, taps []any) {
		t.Helper()
		code, attempt := call(t, h, who, "POST", "/api/v1/activities/"+quizID+"/attempts", nil)
		mustStatus(t, code, 201, "start attempt")
		sq := attemptQuestion(t, attempt, "Exit sign colour?")
		mq := attemptQuestion(t, attempt, "Extinguishers?")
		answers := map[string]any{}
		var s, m []string
		for _, l := range singlePick {
			s = append(s, optionByLabel(t, sq, l))
		}
		for _, l := range multiPick {
			m = append(m, optionByLabel(t, mq, l))
		}
		answers[singleID] = s
		answers[multiID] = m
		code, _ = call(t, h, who, "POST", "/api/v1/attempts/"+str(attempt, "id")+"/submit",
			map[string]any{"answers": answers, "taps": map[string]any{hotID: taps}})
		mustStatus(t, code, 200, "submit")
	}
	take(learner, []string{"Green"}, []string{"CO2"}, []any{pt(0.2, 0.2), pt(0.72, 0.68)})
	take(learner, []string{"Red"}, []string{"CO2", "Foam"}, []any{pt(0.2, 0.2), pt(0.55, 0.15)})
	take(kim, []string{"Green"}, []string{}, []any{pt(0.9, 0.1)})
	// An open attempt doesn't count.
	code, _ = call(t, h, kim, "POST", "/api/v1/activities/"+quizID+"/attempts", nil)
	mustStatus(t, code, 201, "open attempt")

	t.Run("staff only", func(t *testing.T) {
		code, _ := call(t, h, learner, "GET", "/api/v1/courses/"+courseID+"/question-stats", nil)
		mustStatus(t, code, 403, "learner reads stats")
		code, _ = call(t, h, trainerB, "GET", "/api/v1/courses/"+courseID+"/question-stats", nil)
		mustStatus(t, code, 403, "other trainer reads stats")
	})

	code, out := call(t, h, trainerA, "GET", "/api/v1/courses/"+courseID+"/question-stats", nil)
	mustStatus(t, code, 200, "stats")
	stats := map[string]map[string]any{}
	for _, s := range arr(out, "questions") {
		stats[str(obj(s), "question_id")] = obj(s)
	}
	if len(stats) != 4 {
		t.Fatalf("want 4 questions, got %d: %v", len(stats), out)
	}
	near := func(v any, want float64) bool {
		f, ok := v.(float64)
		return ok && math.Abs(f-want) < 0.01
	}
	picks := func(s map[string]any) map[string]float64 {
		m := map[string]float64{}
		for _, o := range arr(s, "options") {
			m[str(obj(o), "label")] = num(obj(o), "picks")
		}
		return m
	}

	t.Run("single choice", func(t *testing.T) {
		s := stats[singleID]
		if num(s, "attempts") != 3 || num(s, "correct") != 2 || !near(s["percent_correct"], 200.0/3) {
			t.Fatalf("single stats: %v", s)
		}
		if p := picks(s); p["Green"] != 2 || p["Red"] != 1 || p["Blue"] != 0 {
			t.Fatalf("single picks: %v", p)
		}
	})
	t.Run("multiple choice", func(t *testing.T) {
		s := stats[multiID]
		if num(s, "attempts") != 3 || num(s, "correct") != 1 || !near(s["percent_correct"], 100.0/3) {
			t.Fatalf("multiple stats: %v", s)
		}
		if p := picks(s); p["CO2"] != 2 || p["Foam"] != 1 || p["Hose"] != 0 {
			t.Fatalf("multiple picks: %v", p)
		}
	})
	t.Run("hotspot", func(t *testing.T) {
		s := stats[hotID]
		if num(s, "attempts") != 3 || num(s, "correct") != 1 || num(s, "taps") != 5 || num(s, "miss_taps") != 2 {
			t.Fatalf("hotspot stats: %v", s)
		}
		rs := arr(s, "regions")
		if len(rs) != 3 {
			t.Fatalf("regions: %v", rs)
		}
		wantHits := []float64{2, 1, 1}
		for i, r := range rs {
			if num(obj(r), "hits") != wantHits[i] || !near(obj(r)["hit_rate"], wantHits[i]*100/3) {
				t.Fatalf("region %d: %v", i, r)
			}
		}
	})
	t.Run("unanswered question", func(t *testing.T) {
		s := stats[str(unused, "id")]
		if num(s, "attempts") != 0 || s["percent_correct"] != nil {
			t.Fatalf("unused stats: %v", s)
		}
	})
}
