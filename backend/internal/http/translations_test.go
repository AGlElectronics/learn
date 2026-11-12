package http_test

// Several languages (phase 11): language resolution per request, fallback
// to the default text, translated learner payloads (course, lists, quiz,
// attempts, portal, verify), the staff translation editor and its access
// rules, and the saved language preference. Same DB-backed setup (and
// TEST_DATABASE_URL warning) as courses_test.go.

import (
	"bytes"
	"encoding/json"
	nethttp "net/http"
	"net/http/httptest"
	"testing"
)

// callLang is call with an Accept-Language header.
func callLang(t *testing.T, h nethttp.Handler, id identity, method, path, acceptLanguage string, body any) (int, map[string]any) {
	t.Helper()
	var rd *bytes.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		rd = bytes.NewReader(raw)
	} else {
		rd = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, rd)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("X-Dev-User", id.user)
	req.Header.Set("X-Dev-Groups", id.groups)
	if acceptLanguage != "" {
		req.Header.Set("Accept-Language", acceptLanguage)
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

// translateAll gives every field of a course's sheet the text "NL <default>"
// (whatever lang is) as trainer A.
func translateAll(t *testing.T, h nethttp.Handler, courseID, lang string) map[string]any {
	t.Helper()
	path := "/api/v1/courses/" + courseID + "/translations?lang=" + lang
	code, sheet := call(t, h, trainerA, "GET", path, nil)
	mustStatus(t, code, 200, "translation sheet")
	items := []map[string]any{}
	for _, f := range arr(sheet, "fields") {
		fo := obj(f)
		items = append(items, map[string]any{
			"entity_type": fo["entity_type"], "entity_id": fo["entity_id"], "field": fo["field"],
			"text": "NL " + str(fo, "default"),
		})
	}
	code, sheet = call(t, h, trainerA, "PUT", path, map[string]any{"translations": items})
	mustStatus(t, code, 200, "save translations")
	if num(sheet, "missing_count") != 0 {
		t.Fatalf("after translating everything: %v", sheet)
	}
	return sheet
}

// sheetField finds one field of a translation sheet.
func sheetField(t *testing.T, sheet map[string]any, entityType, field, def string) map[string]any {
	t.Helper()
	for _, f := range arr(sheet, "fields") {
		fo := obj(f)
		if str(fo, "entity_type") == entityType && str(fo, "field") == field && str(fo, "default") == def {
			return fo
		}
	}
	t.Fatalf("no %s.%s %q in sheet %v", entityType, field, def, sheet)
	return nil
}

func TestTranslations(t *testing.T) {
	h, pool := setupQualRouter(t, nil, nil)
	cred := pairPortal(t, pool)
	ids := signIn(t, h, admin, trainerA, trainerB, learner)

	code, course := call(t, h, trainerA, "POST", "/api/v1/courses", map[string]any{"title": "Fire safety", "summary": "Stay safe"})
	mustStatus(t, code, 201, "course")
	courseID := str(course, "id")
	if str(course, "default_lang") != "en" || len(arr(course, "languages")) != 1 {
		t.Fatalf("new course languages: %v", course)
	}
	code, section := call(t, h, trainerA, "POST", "/api/v1/courses/"+courseID+"/sections", map[string]any{"title": "Week 1"})
	mustStatus(t, code, 201, "section")
	sectionID := str(section, "id")
	code, page := call(t, h, trainerA, "POST", "/api/v1/sections/"+sectionID+"/activities",
		map[string]any{"title": "Exits", "body": "Find the exit"})
	mustStatus(t, code, 201, "page")
	pageID := str(page, "id")
	code, wrap := call(t, h, trainerA, "POST", "/api/v1/sections/"+sectionID+"/activities",
		map[string]any{"title": "Wrap up", "body": "Well done"})
	mustStatus(t, code, 201, "second page")
	code, _ = call(t, h, trainerA, "PUT", "/api/v1/activities/"+str(wrap, "id")+"/prerequisites", map[string]any{"requires": []string{pageID}})
	mustStatus(t, code, 200, "prerequisite")
	code, quiz := call(t, h, trainerA, "POST", "/api/v1/sections/"+sectionID+"/activities",
		map[string]any{"kind": "quiz", "title": "Check", "body": "Three questions", "shuffle_options": false})
	mustStatus(t, code, 201, "quiz")
	quizID := str(quiz, "id")
	code, cat := call(t, h, trainerA, "POST", "/api/v1/courses/"+courseID+"/question-categories", map[string]any{"name": "Doors"})
	mustStatus(t, code, 201, "category")
	code, question := call(t, h, trainerA, "POST", "/api/v1/question-categories/"+str(cat, "id")+"/questions", map[string]any{
		"kind": "single", "prompt": "Which door?", "feedback": "Green means go",
		"options": []any{opt("Green door", true), opt("Red door", false)},
	})
	mustStatus(t, code, 201, "question")
	code, _ = call(t, h, trainerA, "POST", "/api/v1/activities/"+quizID+"/slots", map[string]any{"question_id": str(question, "id")})
	mustStatus(t, code, 201, "slot")

	sheetPath := "/api/v1/courses/" + courseID + "/translations"

	t.Run("course languages", func(t *testing.T) {
		code, _ := call(t, h, trainerA, "PATCH", "/api/v1/courses/"+courseID, map[string]any{"languages": []string{"en", "not a code"}})
		mustStatus(t, code, 422, "bad language")
		code, _ = call(t, h, trainerA, "PATCH", "/api/v1/courses/"+courseID, map[string]any{"default_lang": "x"})
		mustStatus(t, code, 422, "bad default")
		code, out := call(t, h, trainerA, "PATCH", "/api/v1/courses/"+courseID, map[string]any{"languages": []string{"NL-be", "nl"}})
		mustStatus(t, code, 200, "languages")
		// The default is always offered.
		if langs := arr(out, "languages"); len(langs) != 2 || langs[0] != "en" || langs[1] != "nl" {
			t.Fatalf("languages: %v", out)
		}
		code, _ = call(t, h, trainerB, "PATCH", "/api/v1/courses/"+courseID, map[string]any{"languages": []string{"en"}})
		mustStatus(t, code, 404, "other trainer on a draft")
	})

	t.Run("editor access", func(t *testing.T) {
		code, _ := call(t, h, learner, "GET", sheetPath+"?lang=nl", nil)
		mustStatus(t, code, 404, "learner on a draft")
		code, _ = call(t, h, trainerB, "GET", sheetPath+"?lang=nl", nil)
		mustStatus(t, code, 404, "other trainer on a draft")
		code, _ = call(t, h, trainerB, "PUT", sheetPath+"?lang=nl", map[string]any{"translations": []any{}})
		mustStatus(t, code, 404, "other trainer saves on a draft")
		code, _ = call(t, h, admin, "GET", sheetPath+"?lang=nl", nil)
		mustStatus(t, code, 200, "admin")
	})

	t.Run("sheet lists every field with missing flags", func(t *testing.T) {
		code, sheet := call(t, h, trainerA, "GET", sheetPath, nil)
		mustStatus(t, code, 200, "sheet without lang picks the first other language")
		if str(sheet, "lang") != "nl" || str(sheet, "default_lang") != "en" {
			t.Fatalf("sheet: %v", sheet)
		}
		// course title+summary, section, 3 activity titles + 3 bodies,
		// question prompt + feedback, 2 options, category-less extras none.
		if n := len(arr(sheet, "fields")); n != 13 || num(sheet, "missing_count") != 13 {
			t.Fatalf("fields %d, missing %v: %v", n, sheet["missing_count"], sheet)
		}
		f := sheetField(t, sheet, "activity", "body", "Find the exit")
		if f["missing"] != true || str(f, "text") != "" || str(f, "context") != "Week 1 › Exits" {
			t.Fatalf("field: %v", f)
		}
		code, _ = call(t, h, trainerA, "GET", sheetPath+"?lang=en", nil)
		mustStatus(t, code, 422, "the default language is not a translation")
		code, _ = call(t, h, trainerA, "GET", sheetPath+"?lang=fr", nil)
		mustStatus(t, code, 422, "a language the course does not offer")
	})

	t.Run("save rejects fields of another course", func(t *testing.T) {
		_, otherSection := newCourse(t, h, "Other course")
		code, _ := call(t, h, trainerA, "PUT", sheetPath+"?lang=nl", map[string]any{"translations": []any{
			map[string]any{"entity_type": "section", "entity_id": otherSection, "field": "title", "text": "Nope"},
		}})
		mustStatus(t, code, 422, "foreign section")
		code, _ = call(t, h, trainerA, "PUT", sheetPath+"?lang=nl", map[string]any{"translations": []any{
			map[string]any{"entity_type": "section", "entity_id": sectionID, "field": "body", "text": "Nope"},
		}})
		mustStatus(t, code, 422, "unknown field")
	})

	translateAll(t, h, courseID, "nl")
	// Remove one translation again: that field falls back to the default.
	code, sheet := call(t, h, trainerA, "PUT", sheetPath+"?lang=nl", map[string]any{"translations": []any{
		map[string]any{"entity_type": "activity", "entity_id": pageID, "field": "body", "text": "  "},
	}})
	mustStatus(t, code, 200, "remove a translation")
	if num(sheet, "missing_count") != 1 || sheetField(t, sheet, "activity", "body", "Find the exit")["missing"] != true {
		t.Fatalf("after removing: %v", sheet)
	}

	publishAndEnrol(t, h, courseID, ids[learner.user])
	coursePath := "/api/v1/courses/" + courseID

	t.Run("editor access once published", func(t *testing.T) {
		code, _ := call(t, h, learner, "GET", sheetPath+"?lang=nl", nil)
		mustStatus(t, code, 403, "learner")
		code, _ = call(t, h, trainerB, "PUT", sheetPath+"?lang=nl", map[string]any{"translations": []any{}})
		mustStatus(t, code, 403, "other trainer")
	})

	title := func(t *testing.T, path, acceptLanguage string) (string, string) {
		t.Helper()
		code, out := callLang(t, h, learner, "GET", path, acceptLanguage, nil)
		mustStatus(t, code, 200, path)
		return str(out, "title"), str(out, "lang")
	}

	t.Run("resolution order", func(t *testing.T) {
		if got, lang := title(t, coursePath, ""); got != "Fire safety" || lang != "en" {
			t.Fatalf("no wishes: %q %q", got, lang)
		}
		if got, lang := title(t, coursePath, "nl-NL,en;q=0.5"); got != "NL Fire safety" || lang != "nl" {
			t.Fatalf("Accept-Language nl: %q %q", got, lang)
		}
		if got, _ := title(t, coursePath, "de, fr;q=0.8"); got != "Fire safety" {
			t.Fatalf("nothing offered falls back to the default: %q", got)
		}

		code, prefs := call(t, h, learner, "PUT", "/api/v1/me/preferences", map[string]any{"language": "en"})
		mustStatus(t, code, 200, "save language")
		if str(prefs, "language") != "en" || str(prefs, "theme") != "system" {
			t.Fatalf("prefs: %v", prefs)
		}
		if got, _ := title(t, coursePath, "nl"); got != "Fire safety" {
			t.Fatalf("the preference beats Accept-Language: %q", got)
		}
		if got, _ := title(t, coursePath+"?lang=nl", "en"); got != "NL Fire safety" {
			t.Fatalf("?lang= beats the preference: %q", got)
		}
		if got, _ := title(t, coursePath+"?lang=de", "nl"); got != "Fire safety" {
			t.Fatalf("an unsupported ?lang= falls through to the preference: %q", got)
		}

		code, _ = call(t, h, learner, "PUT", "/api/v1/me/preferences", map[string]any{"language": "not a code"})
		mustStatus(t, code, 422, "bad language")
		code, _ = call(t, h, learner, "PUT", "/api/v1/me/preferences", map[string]any{"language": "nl"})
		mustStatus(t, code, 200, "switch to Dutch")
		code, prefs = call(t, h, learner, "GET", "/api/v1/me/preferences", nil)
		mustStatus(t, code, 200, "read prefs")
		if str(prefs, "language") != "nl" {
			t.Fatalf("prefs: %v", prefs)
		}
		if got, _ := title(t, coursePath, ""); got != "NL Fire safety" {
			t.Fatalf("saved Dutch: %q", got)
		}
		code, _ = call(t, h, learner, "PUT", "/api/v1/me/preferences", map[string]any{"theme": "dark"})
		mustStatus(t, code, 200, "theme only keeps the language")
		if got, _ := title(t, coursePath, ""); got != "NL Fire safety" {
			t.Fatalf("after a theme change: %q", got)
		}
	})

	t.Run("translated course detail with fallback", func(t *testing.T) {
		code, d := call(t, h, learner, "GET", coursePath, nil)
		mustStatus(t, code, 200, "detail")
		sec := obj(arr(d, "sections")[0])
		acts := arr(sec, "activities")
		if str(d, "summary") != "NL Stay safe" || str(sec, "title") != "NL Week 1" {
			t.Fatalf("detail: %v", d)
		}
		exits, wrapUp := obj(acts[0]), obj(acts[1])
		if str(exits, "title") != "NL Exits" || str(exits, "body") != "Find the exit" {
			t.Fatalf("missing body falls back to the default: %v", exits)
		}
		if str(wrapUp, "body") != "" || wrapUp["locked"] != true {
			t.Fatalf("locked page: %v", wrapUp)
		}
		if needs := arr(wrapUp, "needs"); len(needs) != 1 || needs[0] != "NL Exits" {
			t.Fatalf("needs follow the translated titles: %v", wrapUp)
		}

		code, staff := call(t, h, trainerA, "GET", coursePath, nil)
		mustStatus(t, code, 200, "staff detail")
		if str(staff, "title") != "Fire safety" || str(obj(arr(staff, "sections")[0]), "title") != "Week 1" {
			t.Fatalf("staff edit the default text: %v", staff)
		}
		code, staff = call(t, h, trainerA, "GET", coursePath+"?lang=nl", nil)
		mustStatus(t, code, 200, "staff preview")
		if str(staff, "title") != "NL Fire safety" {
			t.Fatalf("staff preview in Dutch: %v", staff)
		}
	})

	t.Run("translated lists", func(t *testing.T) {
		code, out := call(t, h, learner, "GET", "/api/v1/courses/mine", nil)
		mustStatus(t, code, 200, "mine")
		c := obj(arr(out, "courses")[0])
		if str(c, "title") != "NL Fire safety" || str(c, "lang") != "nl" {
			t.Fatalf("mine: %v", c)
		}
		code, out = call(t, h, trainerA, "GET", "/api/v1/courses/teach", nil)
		mustStatus(t, code, 200, "teach")
		for _, raw := range arr(out, "courses") {
			if str(obj(raw), "title") == "NL Fire safety" {
				t.Fatalf("the teach list shows the default text: %v", out)
			}
		}
	})

	t.Run("translated quiz, attempt and results; marking by id", func(t *testing.T) {
		code, info := call(t, h, learner, "GET", "/api/v1/activities/"+quizID+"/quiz", nil)
		mustStatus(t, code, 200, "quiz info")
		if str(info, "title") != "NL Check" || str(info, "body") != "NL Three questions" {
			t.Fatalf("quiz info: %v", info)
		}
		code, attempt := call(t, h, learner, "POST", "/api/v1/activities/"+quizID+"/attempts", nil)
		mustStatus(t, code, 201, "start")
		q := obj(arr(attempt, "questions")[0])
		opts := arr(q, "options")
		if str(q, "prompt") != "NL Which door?" || str(obj(opts[0]), "label") != "NL Green door" || q["feedback"] != nil {
			t.Fatalf("attempt question: %v", q)
		}
		code, again := call(t, h, learner, "GET", "/api/v1/attempts/"+str(attempt, "id")+"?lang=en", nil)
		mustStatus(t, code, 200, "reload in English")
		if str(obj(arr(again, "questions")[0]), "prompt") != "Which door?" {
			t.Fatalf("?lang=en: %v", again)
		}
		code, result := call(t, h, learner, "POST", "/api/v1/attempts/"+str(attempt, "id")+"/submit", map[string]any{
			"answers": map[string]any{str(q, "question_id"): []string{str(obj(opts[0]), "id")}},
		})
		mustStatus(t, code, 200, "submit")
		rq := obj(arr(result, "questions")[0])
		if result["passed"] != true || str(rq, "feedback") != "NL Green means go" || rq["correct"] != true {
			t.Fatalf("result: %v", result)
		}
	})

	t.Run("portal to-dos and public verify", func(t *testing.T) {
		exec(t, pool, `UPDATE enrolments SET due_at = now() + interval '2 days' WHERE course_id = $1 AND user_id = $2`,
			courseID, ids[learner.user])
		code, out := rawCall(t, h, "GET", "/api/portal/todos", portalHeaders(cred, learner.user))
		mustStatus(t, code, 200, "todos")
		items := arr(out, "items")
		if len(items) != 1 || str(obj(items[0]), "title") != "Rond NL Fire safety af" {
			t.Fatalf("Dutch to-dos: %v", out)
		}
		code, out = rawCall(t, h, "GET", "/api/portal/todos?lang=en", portalHeaders(cred, learner.user))
		mustStatus(t, code, 200, "todos in English")
		if str(obj(arr(out, "items")[0]), "title") != "Finish Fire safety" {
			t.Fatalf("English to-dos: %v", out)
		}

		code, qual := call(t, h, admin, "POST", "/api/v1/qualifications", map[string]any{"key": "fire", "name": "Fire warden"})
		mustStatus(t, code, 201, "qualification")
		qPath := "/api/v1/qualifications/" + str(qual, "id") + "/translations"
		code, _ = call(t, h, trainerA, "GET", qPath+"?lang=nl", nil)
		mustStatus(t, code, 403, "trainer edits qualification translations")
		code, qs := call(t, h, admin, "PUT", qPath+"?lang=nl", map[string]any{"translations": []any{
			map[string]any{"entity_type": "qualification", "entity_id": str(qual, "id"), "field": "name", "text": "Brandwacht"},
		}})
		mustStatus(t, code, 200, "qualification translation")
		if num(qs, "missing_count") != 0 || len(arr(qs, "fields")) != 1 {
			t.Fatalf("qualification sheet (empty description is not listed): %v", qs)
		}
		exec(t, pool, `INSERT INTO certificates (user_id, qualification_id, course_id, verify_code)
			VALUES ($1, $2, $3, 'TRANSLATE234')`, ids[learner.user], str(qual, "id"), courseID)
		code, v := rawCall(t, h, "GET", "/api/public/verify/TRANSLATE234", map[string]string{"Accept-Language": "nl-BE, en;q=0.5"})
		mustStatus(t, code, 200, "verify in Dutch")
		if str(v, "qualification") != "Brandwacht" {
			t.Fatalf("verify: %v", v)
		}
		code, v = rawCall(t, h, "GET", "/api/public/verify/TRANSLATE234", map[string]string{"Accept-Language": "en-GB"})
		mustStatus(t, code, 200, "verify in English")
		if str(v, "qualification") != "Fire warden" {
			t.Fatalf("verify: %v", v)
		}
		code, mine := call(t, h, learner, "GET", "/api/v1/me/qualifications", nil)
		mustStatus(t, code, 200, "my qualifications")
		mq := obj(arr(mine, "qualifications")[0])
		if str(mq, "qualification_name") != "Brandwacht" || str(mq, "course_title") != "NL Fire safety" {
			t.Fatalf("my qualifications: %v", mq)
		}
	})

	t.Run("deleting content removes its translations", func(t *testing.T) {
		before := countQ(t, pool, `SELECT count(*) FROM translations WHERE entity_id = $1`, pageID)
		if before == 0 {
			t.Fatal("the page should have a title translation")
		}
		code, _ := call(t, h, trainerA, "DELETE", "/api/v1/sections/"+sectionID, nil)
		mustStatus(t, code, 204, "delete section")
		if n := countQ(t, pool, `SELECT count(*) FROM translations WHERE entity_id::text = ANY($1)`,
			[]string{pageID, sectionID, quizID}); n != 0 {
			t.Fatalf("orphan translations: %d", n)
		}
	})
}
