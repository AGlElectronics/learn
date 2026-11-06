package http_test

// Course copy (phase 9): deep copy with every reference remapped into the
// new course, no learner data, access rules, and shared-object-safe media
// delete. Same DB-backed setup as courses_test.go.

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func countQ(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		t.Fatalf("count %q: %v", sql, err)
	}
	return n
}

func TestCourseCopy(t *testing.T) {
	store := newFakeStore()
	h, pool := setupP8Router(t, store)
	ids := signIn(t, h, admin, trainerA, trainerB, learner)

	courseID, sectionID := newCourse(t, h, "Fire safety 2026")
	pageID := newActivity(t, h, sectionID, "page", "Read me")
	quizID := newActivity(t, h, sectionID, "quiz", "Check")
	sessionID := newActivity(t, h, sectionID, "session", "Drill")
	code, _ := call(t, h, trainerA, "POST", "/api/v1/activities/"+sessionID+"/session-slots", map[string]any{
		"starts_at": rfc(time.Now().Add(48 * time.Hour)), "ends_at": rfc(time.Now().Add(50 * time.Hour)), "location": "Yard", "capacity": 5,
	})
	mustStatus(t, code, 201, "session slot")
	code, _ = call(t, h, trainerA, "PUT", "/api/v1/activities/"+quizID+"/prerequisites", map[string]any{"requires": []string{pageID}})
	mustStatus(t, code, 200, "prerequisite")
	code, _ = call(t, h, trainerA, "PATCH", "/api/v1/courses/"+courseID, map[string]any{"due_days": 14})
	mustStatus(t, code, 200, "due days")

	sign := uploadMedia(t, h, store, courseID, "sign.png", "image/png", "Exit sign")
	spare := uploadMedia(t, h, store, courseID, "spare.png", "image/png", "Unused")
	code, _ = call(t, h, trainerA, "POST", "/api/v1/activities/"+pageID+"/media", map[string]any{"media_id": str(sign, "id"), "caption": "Look"})
	mustStatus(t, code, 201, "attach media")
	code, _ = call(t, h, trainerA, "POST", "/api/v1/activities/"+pageID+"/media", map[string]any{"external_url": "https://www.youtube.com/watch?v=dQw4w9WgXcQ"})
	mustStatus(t, code, 201, "attach embed")

	code, cat := call(t, h, trainerA, "POST", "/api/v1/courses/"+courseID+"/question-categories", map[string]any{"name": "Signs"})
	mustStatus(t, code, 201, "category")
	catID := str(cat, "id")
	code, single := call(t, h, trainerA, "POST", "/api/v1/question-categories/"+catID+"/questions", map[string]any{
		"kind": "single", "prompt": "Which is the exit?", "options": []any{
			map[string]any{"label": "", "media_id": str(sign, "id"), "correct": true}, opt("Door", false)},
	})
	mustStatus(t, code, 201, "single question")
	code, _ = call(t, h, trainerA, "POST", "/api/v1/question-categories/"+catID+"/questions", map[string]any{
		"kind": "hotspot", "prompt": "Tap the sign", "media_id": str(sign, "id"), "regions": []any{
			map[string]any{"shape": "rect", "coords": map[string]any{"x": 0.1, "y": 0.1, "w": 0.2, "h": 0.2}, "correct": true},
			map[string]any{"shape": "rect", "coords": map[string]any{"x": 0.6, "y": 0.6, "w": 0.1, "h": 0.1}, "correct": false},
		},
	})
	mustStatus(t, code, 201, "hotspot question")
	code, _ = call(t, h, trainerA, "POST", "/api/v1/activities/"+quizID+"/slots", map[string]any{"question_id": str(single, "id")})
	mustStatus(t, code, 201, "fixed slot")
	code, _ = call(t, h, trainerA, "POST", "/api/v1/activities/"+quizID+"/slots", map[string]any{"category_id": catID, "draw_count": 1})
	mustStatus(t, code, 201, "draw slot")
	exec(t, pool, `INSERT INTO course_group_links (course_id, group_name) VALUES ($1, 'crew')`, courseID)
	code, _ = call(t, h, trainerA, "PUT", "/api/v1/courses/"+courseID+"/team-board/settings",
		map[string]any{"enabled": true, "groups": []string{"crew", "night-shift"}})
	mustStatus(t, code, 200, "team board settings")
	code, _ = call(t, h, trainerA, "PATCH", "/api/v1/courses/"+courseID, map[string]any{"languages": []string{"en", "nl"}})
	mustStatus(t, code, 200, "course languages")
	translateAll(t, h, courseID, "nl")

	publishAndEnrol(t, h, courseID, ids["learner-lou"])
	code, _ = call(t, h, learner, "PUT", "/api/v1/activities/"+pageID+"/completion", map[string]any{"completed": true})
	mustStatus(t, code, 200, "learner completes page")

	var adminCopyID string
	t.Run("access", func(t *testing.T) {
		code, _ := call(t, h, trainerB, "POST", "/api/v1/courses/"+courseID+"/copy", map[string]any{"title": "Nope"})
		mustStatus(t, code, 403, "other trainer copies a published course")
		code, _ = call(t, h, learner, "POST", "/api/v1/courses/"+courseID+"/copy", map[string]any{"title": "Nope"})
		mustStatus(t, code, 403, "learner copies")
		code, _ = call(t, h, trainerA, "POST", "/api/v1/courses/"+courseID+"/copy", map[string]any{"title": "  "})
		mustStatus(t, code, 422, "empty title")
		code, out := call(t, h, admin, "POST", "/api/v1/courses/"+courseID+"/copy", map[string]any{"title": "Admin copy"})
		mustStatus(t, code, 201, "admin copies any course")
		adminCopyID = str(out, "id")
		if countQ(t, pool, `SELECT count(*) FROM enrolments WHERE course_id = $1 AND role = 'trainer' AND user_id = $2`, str(out, "id"), ids["dev-admin"]) != 1 {
			t.Fatal("copier should be trainer of the copy")
		}
	})

	code, cp := call(t, h, trainerA, "POST", "/api/v1/courses/"+courseID+"/copy", map[string]any{"title": "Fire safety 2027", "include_group_links": true})
	mustStatus(t, code, 201, "trainer copies own course")
	newID := str(cp, "id")
	if cp["published"] != false || str(cp, "title") != "Fire safety 2027" {
		t.Fatalf("copy: %v", cp)
	}

	t.Run("content is copied and remapped", func(t *testing.T) {
		checks := []struct {
			what string
			sql  string
			want int
		}{
			{"sections", `SELECT count(*) FROM sections WHERE course_id = $1`, 1},
			{"activities", `SELECT count(*) FROM activities a JOIN sections s ON s.id = a.section_id WHERE s.course_id = $1`, 3},
			{"session activity without slots", `SELECT count(*) FROM session_slots sl JOIN activities a ON a.id = sl.activity_id JOIN sections s ON s.id = a.section_id WHERE s.course_id = $1`, 0},
			{"prerequisites inside the copy", `SELECT count(*) FROM activity_prerequisites p
				JOIN activities a ON a.id = p.activity_id JOIN sections s ON s.id = a.section_id
				JOIN activities r ON r.id = p.requires_activity_id JOIN sections rs ON rs.id = r.section_id
				WHERE s.course_id = $1 AND rs.course_id = $1 AND a.kind = 'quiz' AND r.kind = 'page'`, 1},
			{"media rows", `SELECT count(*) FROM media WHERE course_id = $1`, 2},
			{"attachments with copied media", `SELECT count(*) FROM activity_media am
				JOIN activities a ON a.id = am.activity_id JOIN sections s ON s.id = a.section_id
				LEFT JOIN media m ON m.id = am.media_id
				WHERE s.course_id = $1 AND (am.media_id IS NULL OR m.course_id = $1)`, 2},
			{"categories", `SELECT count(*) FROM question_categories WHERE course_id = $1`, 1},
			{"questions with copied media", `SELECT count(*) FROM questions q JOIN question_categories c ON c.id = q.category_id
				JOIN media m ON m.id = q.media_id WHERE c.course_id = $1 AND m.course_id = $1`, 1},
			{"options", `SELECT count(*) FROM question_options o JOIN questions q ON q.id = o.question_id
				JOIN question_categories c ON c.id = q.category_id WHERE c.course_id = $1`, 2},
			{"option media remapped", `SELECT count(*) FROM question_options o JOIN media m ON m.id = o.media_id
				JOIN questions q ON q.id = o.question_id JOIN question_categories c ON c.id = q.category_id
				WHERE c.course_id = $1 AND m.course_id = $1`, 1},
			{"regions", `SELECT count(*) FROM question_regions r JOIN questions q ON q.id = r.question_id
				JOIN question_categories c ON c.id = q.category_id WHERE c.course_id = $1 AND r.correct`, 1},
			{"quiz slots point into the copy", `SELECT count(*) FROM quiz_slots sl
				JOIN activities a ON a.id = sl.activity_id JOIN sections s ON s.id = a.section_id
				LEFT JOIN questions q ON q.id = sl.question_id LEFT JOIN question_categories qc ON qc.id = q.category_id
				LEFT JOIN question_categories c ON c.id = sl.category_id
				WHERE s.course_id = $1 AND COALESCE(qc.course_id, c.course_id) = $1`, 2},
			{"group links", `SELECT count(*) FROM course_group_links WHERE course_id = $1`, 1},
			{"team board setting", `SELECT count(*) FROM courses WHERE id = $1 AND team_board_enabled`, 1},
			{"team board groups", `SELECT count(*) FROM team_board_groups WHERE course_id = $1`, 2},
			{"languages", `SELECT count(*) FROM courses WHERE id = $1 AND default_lang = 'en' AND languages = '{en,nl}'`, 1},
			{"due days and qualification fields", `SELECT count(*) FROM courses WHERE id = $1 AND due_days = 14 AND NOT published`, 1},
			{"no learner enrolments", `SELECT count(*) FROM enrolments WHERE course_id = $1 AND role = 'learner'`, 0},
			{"no completions", `SELECT count(*) FROM completions c JOIN activities a ON a.id = c.activity_id JOIN sections s ON s.id = a.section_id WHERE s.course_id = $1`, 0},
			{"no attempts", `SELECT count(*) FROM quiz_attempts qa JOIN activities a ON a.id = qa.activity_id JOIN sections s ON s.id = a.section_id WHERE s.course_id = $1`, 0},
			{"no certificates", `SELECT count(*) FROM certificates WHERE course_id = $1`, 0},
		}
		for _, c := range checks {
			if got := countQ(t, pool, c.sql, newID); got != c.want {
				t.Errorf("%s: got %d, want %d", c.what, got, c.want)
			}
		}
		// The source is untouched.
		if got := countQ(t, pool, `SELECT count(*) FROM quiz_slots sl JOIN activities a ON a.id = sl.activity_id WHERE a.id = $1`, quizID); got != 2 {
			t.Errorf("source slots: %d", got)
		}
		if got := countQ(t, pool, `SELECT count(DISTINCT a.id) FROM media a JOIN media b ON a.object_key = b.object_key AND b.course_id = $2 WHERE a.course_id = $1`, newID, courseID); got != 2 {
			t.Errorf("copied media should share object keys: %d", got)
		}
		code, detail := call(t, h, trainerA, "GET", "/api/v1/courses/"+newID, nil)
		mustStatus(t, code, 200, "copy detail")
		if len(arr(obj(arr(detail, "sections")[0]), "activities")) != 3 {
			t.Fatalf("copy detail: %v", detail)
		}
		code, _ = call(t, h, trainerA, "POST", "/api/v1/courses/"+newID+"/publish", nil)
		mustStatus(t, code, 200, "the copied quiz is valid and the copy publishes")
	})

	t.Run("translations follow the remapped rows", func(t *testing.T) {
		code, src := call(t, h, trainerA, "GET", "/api/v1/courses/"+courseID+"/translations?lang=nl", nil)
		mustStatus(t, code, 200, "source sheet")
		code, cp := call(t, h, trainerA, "GET", "/api/v1/courses/"+newID+"/translations?lang=nl", nil)
		mustStatus(t, code, 200, "copy sheet")
		if num(src, "missing_count") != 0 || len(arr(cp, "fields")) != len(arr(src, "fields")) {
			t.Fatalf("source %v / copy %v", src, cp)
		}
		kinds := map[string]bool{}
		for _, f := range arr(cp, "fields") {
			fo := obj(f)
			kinds[str(fo, "entity_type")] = true
			if str(fo, "entity_type") == "course" && str(fo, "field") == "title" {
				if fo["missing"] != true {
					t.Fatalf("the new title must not inherit the old title's translation: %v", fo)
				}
				continue
			}
			if fo["missing"] != false || str(fo, "text") != "NL "+str(fo, "default") {
				t.Fatalf("copied translation: %v", fo)
			}
		}
		for _, k := range []string{"course", "section", "activity", "question", "option", "media", "activity_media"} {
			if !kinds[k] {
				t.Errorf("copy sheet has no %s fields", k)
			}
		}
		if num(cp, "missing_count") != 1 {
			t.Fatalf("copy should miss only its title: %v", cp["missing_count"])
		}
		if got := countQ(t, pool, `SELECT count(*) FROM translations t JOIN sections s ON s.id = t.entity_id WHERE s.course_id = $1`, courseID); got != 1 {
			t.Errorf("source section translations: %d", got)
		}
	})

	t.Run("media delete keeps shared objects", func(t *testing.T) {
		copied := func(courseID string) string {
			var id string
			if err := pool.QueryRow(context.Background(), `
				SELECT c.id FROM media c JOIN media s ON s.object_key = c.object_key
				 WHERE s.id = $1 AND c.course_id = $2`, str(spare, "id"), courseID).Scan(&id); err != nil {
				t.Fatalf("find copied spare: %v", err)
			}
			return id
		}
		trainerSpare, adminSpare := copied(newID), copied(adminCopyID)
		before := len(store.removed)
		code, _ := call(t, h, trainerA, "DELETE", "/api/v1/media/"+trainerSpare, nil)
		mustStatus(t, code, 204, "delete copied media")
		code, _ = call(t, h, trainerA, "GET", "/api/v1/media/"+str(spare, "id")+"/url", nil)
		mustStatus(t, code, 200, "the source's file still works")
		code, _ = call(t, h, trainerA, "DELETE", "/api/v1/media/"+str(spare, "id"), nil)
		mustStatus(t, code, 204, "delete the source's row")
		if len(store.removed) != before {
			t.Fatal("deleting a row while another still uses the object must keep it")
		}
		code, _ = call(t, h, admin, "DELETE", "/api/v1/media/"+adminSpare, nil)
		mustStatus(t, code, 204, "delete the last row")
		if len(store.removed) != before+1 {
			t.Fatal("deleting the last row must remove the object")
		}
	})
}
