package http_test

// Media library, question media, hotspot questions and page attachments.
// Same DB-backed setup (and TEST_DATABASE_URL warning) as courses_test.go.
// Object storage is a fake: "uploading" means putting the key in a map.

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	nethttp "net/http"
	"net/http/httptest"

	"github.com/AGlElectronics/learn/backend/internal/files"
)

type fakeStore struct {
	mu       sync.Mutex
	objects  map[string]files.ObjectInfo
	contents map[string][]byte
	removed  []string
}

func newFakeStore() *fakeStore {
	return &fakeStore{objects: map[string]files.ObjectInfo{}, contents: map[string][]byte{}}
}

func (f *fakeStore) PresignedPost(_ context.Context, key, contentType string, maxBytes int64, _ time.Duration) (string, map[string]string, error) {
	return "https://s3.test/learn-data-dev", map[string]string{"key": key, "Content-Type": contentType}, nil
}

func (f *fakeStore) PresignedGet(_ context.Context, key string, _ time.Duration) (string, error) {
	return "https://s3.test/learn-data-dev/" + key + "?X-Amz-Signature=fake", nil
}

func (f *fakeStore) Stat(_ context.Context, key string) (files.ObjectInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	info, ok := f.objects[key]
	if !ok {
		return files.ObjectInfo{}, errors.New("no such key")
	}
	return info, nil
}

// ReadHead returns the start of a real file of the stored type, unless
// putBytes stored other contents.
func (f *fakeStore) ReadHead(_ context.Context, key string, n int64) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	info, ok := f.objects[key]
	if !ok {
		return nil, errors.New("no such key")
	}
	data, ok := f.contents[key]
	if !ok {
		data = []byte(magicFor[info.ContentType])
	}
	if int64(len(data)) > n {
		data = data[:n]
	}
	return data, nil
}

// magicFor is the start of a real file of each type the tests upload.
var magicFor = map[string]string{
	"image/png":  "\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR",
	"image/jpeg": "\xff\xd8\xff\xe0\x00\x10JFIF",
	"audio/mpeg": "ID3\x04\x00\x00\x00\x00\x00\x00",
	"video/mp4":  "\x00\x00\x00\x18ftypmp42",
	"video/webm": "\x1a\x45\xdf\xa3\x9f\x42\x86\x81",
}

// putBytes uploads an object with the given contents.
func (f *fakeStore) putBytes(key, contentType string, data []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.objects[key] = files.ObjectInfo{Size: int64(len(data)), ContentType: contentType}
	f.contents[key] = data
}

func (f *fakeStore) Remove(_ context.Context, key string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.objects, key)
	f.removed = append(f.removed, key)
	return nil
}

func (f *fakeStore) put(key, contentType string, size int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.objects[key] = files.ObjectInfo{Size: size, ContentType: contentType}
}

func testDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("pgx", os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func errCode(m map[string]any) string {
	return str(obj(m["error"]), "code")
}

// uploadMedia runs the ticket → (fake) upload → complete flow.
func uploadMedia(t *testing.T, h nethttp.Handler, store *fakeStore, courseID, name, ct, alt string) map[string]any {
	t.Helper()
	code, ticket := call(t, h, trainerA, "POST", "/api/v1/courses/"+courseID+"/media/uploads",
		map[string]any{"filename": name, "content_type": ct, "size_bytes": 1234, "alt_text": alt})
	mustStatus(t, code, 201, "upload ticket "+name)
	media := obj(ticket["media"])
	if str(media, "status") != "pending" {
		t.Fatalf("new upload should be pending: %v", media)
	}
	key := str(obj(ticket["fields"]), "key")
	if key == "" || strings.Contains(key, name) {
		t.Fatalf("object key must be random and not contain the file name: %q", key)
	}
	store.put(key, ct, 1200)
	code, done := call(t, h, trainerA, "POST", "/api/v1/media/"+str(media, "id")+"/complete", nil)
	mustStatus(t, code, 200, "complete "+name)
	if str(done, "status") != "ready" || num(done, "size_bytes") != 1200 {
		t.Fatalf("completed upload: %v", done)
	}
	return done
}

func TestMediaWithoutStorage(t *testing.T) {
	h := setupRouter(t)
	for _, id := range []identity{trainerA, learner} {
		code, _ := call(t, h, id, "GET", "/api/v1/me", nil)
		mustStatus(t, code, 200, "me")
	}
	code, course := call(t, h, trainerA, "POST", "/api/v1/courses", map[string]any{"title": "No storage"})
	mustStatus(t, code, 201, "create course")
	courseID := str(course, "id")

	code, out := call(t, h, trainerA, "POST", "/api/v1/courses/"+courseID+"/media/uploads",
		map[string]any{"filename": "a.png", "content_type": "image/png", "size_bytes": 10})
	mustStatus(t, code, 503, "upload without storage")
	if errCode(out) != "unavailable" {
		t.Fatalf("error code = %q, want unavailable", errCode(out))
	}
	code, _ = call(t, h, learner, "POST", "/api/v1/courses/"+courseID+"/media/uploads",
		map[string]any{"filename": "a.png", "content_type": "image/png", "size_bytes": 10})
	mustStatus(t, code, 404, "non-staff upload on a draft (authz before 503)")

	// A row left over from when storage was on: listing still works, the
	// view link and delete answer 503 after the access checks pass.
	var mediaID string
	if err := testDB(t).QueryRow(`
		INSERT INTO media (course_id, kind, content_type, size_bytes, object_key, status, alt_text)
		VALUES ($1, 'image', 'image/png', 10, 'k1', 'ready', 'alt') RETURNING id`, courseID).Scan(&mediaID); err != nil {
		t.Fatalf("insert media: %v", err)
	}
	code, lib := call(t, h, trainerA, "GET", "/api/v1/courses/"+courseID+"/media", nil)
	mustStatus(t, code, 200, "library without storage")
	if len(arr(lib, "media")) != 1 {
		t.Fatalf("library: %v", lib)
	}
	code, _ = call(t, h, trainerA, "GET", "/api/v1/media/"+mediaID+"/url", nil)
	mustStatus(t, code, 503, "view link without storage")
	code, _ = call(t, h, learner, "GET", "/api/v1/media/"+mediaID+"/url", nil)
	mustStatus(t, code, 404, "outsider view link on a draft")
	code, _ = call(t, h, trainerA, "DELETE", "/api/v1/media/"+mediaID, nil)
	mustStatus(t, code, 503, "delete without storage")
}

func TestMediaAndHotspots(t *testing.T) {
	store := newFakeStore()
	h := setupRouterWithFiles(t, store)
	for _, id := range []identity{admin, trainerA, trainerB, learner, outsider} {
		code, _ := call(t, h, id, "GET", "/api/v1/me", nil)
		mustStatus(t, code, 200, "me for "+id.user)
	}
	code, course := call(t, h, trainerA, "POST", "/api/v1/courses", map[string]any{"title": "Spot the hazard"})
	mustStatus(t, code, 201, "create course")
	courseID := str(course, "id")
	code, section := call(t, h, trainerA, "POST", "/api/v1/courses/"+courseID+"/sections", map[string]any{"title": "S"})
	mustStatus(t, code, 201, "create section")
	sectionID := str(section, "id")
	code, page := call(t, h, trainerA, "POST", "/api/v1/sections/"+sectionID+"/activities", map[string]any{"title": "Read me", "body": "Look"})
	mustStatus(t, code, 201, "create page")
	pageID := str(page, "id")
	code, quiz := call(t, h, trainerA, "POST", "/api/v1/sections/"+sectionID+"/activities",
		map[string]any{"kind": "quiz", "title": "Hazards", "body": "", "pass_percent": 50})
	mustStatus(t, code, 201, "create quiz")
	quizID := str(quiz, "id")
	code, cat := call(t, h, trainerA, "POST", "/api/v1/courses/"+courseID+"/question-categories", map[string]any{"name": "Pictures"})
	mustStatus(t, code, 201, "create category")
	catID := str(cat, "id")

	t.Run("upload rules", func(t *testing.T) {
		code, _ := call(t, h, trainerB, "POST", "/api/v1/courses/"+courseID+"/media/uploads",
			map[string]any{"filename": "a.png", "content_type": "image/png", "size_bytes": 10})
		mustStatus(t, code, 404, "other trainer uploads to a draft")
		code, _ = call(t, h, trainerA, "POST", "/api/v1/courses/"+courseID+"/media/uploads",
			map[string]any{"filename": "a.pdf", "content_type": "application/pdf", "size_bytes": 10})
		mustStatus(t, code, 422, "pdf refused")
		code, _ = call(t, h, trainerA, "POST", "/api/v1/courses/"+courseID+"/media/uploads",
			map[string]any{"filename": "big.png", "content_type": "image/png", "size_bytes": 11 << 20})
		mustStatus(t, code, 422, "image over 10 MB refused")
		code, _ = call(t, h, trainerA, "POST", "/api/v1/courses/"+courseID+"/media/uploads",
			map[string]any{"filename": "clip.mp4", "content_type": "video/mp4", "size_bytes": 150 << 20})
		mustStatus(t, code, 201, "150 MB video accepted")
	})

	sign := uploadMedia(t, h, store, courseID, "exit-sign.png", "image/png", "A green running-man sign")
	signID := str(sign, "id")
	noAlt := uploadMedia(t, h, store, courseID, "noalt.jpg", "image/jpeg", "")
	noAltID := str(noAlt, "id")

	t.Run("complete checks the stored object", func(t *testing.T) {
		code, ticket := call(t, h, trainerA, "POST", "/api/v1/courses/"+courseID+"/media/uploads",
			map[string]any{"filename": "x.png", "content_type": "image/png", "size_bytes": 100})
		mustStatus(t, code, 201, "ticket")
		id := str(obj(ticket["media"]), "id")
		code, _ = call(t, h, trainerA, "POST", "/api/v1/media/"+id+"/complete", nil)
		mustStatus(t, code, 409, "complete before the object arrived")
		key := str(obj(ticket["fields"]), "key")
		store.put(key, "image/gif", 100)
		code, _ = call(t, h, trainerA, "POST", "/api/v1/media/"+id+"/complete", nil)
		mustStatus(t, code, 422, "stored type differs from the announced one")
		code, _ = call(t, h, trainerA, "GET", "/api/v1/media/"+id+"/url", nil)
		mustStatus(t, code, 404, "a rejected upload is deleted")
		if _, err := store.Stat(context.Background(), key); err == nil {
			t.Fatal("the rejected object should be removed from storage")
		}

		code, ticket = call(t, h, trainerA, "POST", "/api/v1/courses/"+courseID+"/media/uploads",
			map[string]any{"filename": "fake.png", "content_type": "image/png", "size_bytes": 100})
		mustStatus(t, code, 201, "ticket for a disguised file")
		id, key = str(obj(ticket["media"]), "id"), str(obj(ticket["fields"]), "key")
		store.putBytes(key, "image/png", []byte("<html><script>alert(1)</script></html>"))
		code, out := call(t, h, trainerA, "POST", "/api/v1/media/"+id+"/complete", nil)
		mustStatus(t, code, 422, "contents are not a PNG")
		if !strings.Contains(str(obj(out["error"]), "message"), "not a image/png file") {
			t.Fatalf("magic-byte rejection: %v", out)
		}
		if _, err := store.Stat(context.Background(), key); err == nil {
			t.Fatal("the disguised object should be removed from storage")
		}
	})

	t.Run("images need alt text before attaching", func(t *testing.T) {
		code, _ := call(t, h, trainerA, "POST", "/api/v1/question-categories/"+catID+"/questions", map[string]any{
			"kind": "single", "prompt": "Which?", "media_id": noAltID,
			"options": []any{opt("a", true), opt("b", false)},
		})
		mustStatus(t, code, 422, "image without alt text")
		code, _ = call(t, h, trainerA, "PATCH", "/api/v1/media/"+noAltID, map[string]any{"alt_text": "A blocked corridor"})
		mustStatus(t, code, 200, "add alt text")
	})

	code, picQ := call(t, h, trainerA, "POST", "/api/v1/question-categories/"+catID+"/questions", map[string]any{
		"kind": "single", "prompt": "Which picture shows the fire exit sign?", "feedback_media_id": noAltID,
		"options": []any{
			map[string]any{"label": "", "media_id": signID, "correct": true},
			map[string]any{"label": "", "media_id": noAltID, "correct": false},
		},
	})
	mustStatus(t, code, 201, "picture options without labels")
	if obj(arr(picQ, "options")[0])["media"] == nil {
		t.Fatalf("option media not returned: %v", picQ)
	}

	t.Run("hotspot validation", func(t *testing.T) {
		code, _ := call(t, h, trainerA, "POST", "/api/v1/question-categories/"+catID+"/questions", map[string]any{
			"kind": "hotspot", "prompt": "Tap", "regions": []any{
				map[string]any{"shape": "rect", "coords": map[string]any{"x": 0.1, "y": 0.1, "w": 0.1, "h": 0.1}, "correct": true}},
		})
		mustStatus(t, code, 422, "hotspot without image")
		code, _ = call(t, h, trainerA, "POST", "/api/v1/question-categories/"+catID+"/questions", map[string]any{
			"kind": "hotspot", "prompt": "Tap", "media_id": signID, "regions": []any{
				map[string]any{"shape": "rect", "coords": map[string]any{"x": 0.1, "y": 0.1, "w": 0.1, "h": 0.1}, "correct": false}},
		})
		mustStatus(t, code, 422, "hotspot without a correct region")
		code, _ = call(t, h, trainerA, "POST", "/api/v1/question-categories/"+catID+"/questions", map[string]any{
			"kind": "hotspot", "prompt": "Tap", "media_id": signID, "options": []any{opt("a", true), opt("b", false)},
			"regions": []any{map[string]any{"shape": "rect", "coords": map[string]any{"x": 0.1, "y": 0.1, "w": 0.1, "h": 0.1}, "correct": true}},
		})
		mustStatus(t, code, 422, "hotspot with options")
	})

	code, hot := call(t, h, trainerA, "POST", "/api/v1/question-categories/"+catID+"/questions", map[string]any{
		"kind": "hotspot", "prompt": "Tap both extinguishers", "media_id": signID, "regions": []any{
			map[string]any{"shape": "rect", "coords": map[string]any{"x": 0.1, "y": 0.1, "w": 0.2, "h": 0.2}, "correct": true},
			map[string]any{"shape": "circle", "coords": map[string]any{"cx": 0.7, "cy": 0.7, "r": 0.1}, "correct": true},
			map[string]any{"shape": "rect", "coords": map[string]any{"x": 0.5, "y": 0.1, "w": 0.1, "h": 0.1}, "correct": false},
		},
	})
	mustStatus(t, code, 201, "create hotspot question")
	hotID := str(hot, "id")
	if len(arr(hot, "regions")) != 3 {
		t.Fatalf("regions not stored: %v", hot)
	}

	t.Run("library and manual delete", func(t *testing.T) {
		code, lib := call(t, h, trainerA, "GET", "/api/v1/courses/"+courseID+"/media?filter=unused", nil)
		mustStatus(t, code, 200, "unused library")
		for _, m := range arr(lib, "media") {
			if id := str(obj(m), "id"); id == signID || id == noAltID {
				t.Fatalf("media in use listed as unused: %v", m)
			}
		}
		if len(arr(lib, "media")) != 1 { // the 150 MB video ticket, pending; rejected uploads are gone
			t.Fatalf("unused library: got %d, want 1: %v", len(arr(lib, "media")), lib)
		}
		pending := str(obj(arr(lib, "media")[0]), "id")
		if obj(arr(lib, "media")[0])["upload_unfinished"] != false {
			t.Fatalf("fresh pending upload labelled unfinished: %v", arr(lib, "media")[0])
		}
		if _, err := testDB(t).Exec(`UPDATE media SET created_at = now() - interval '25 hours' WHERE id = $1`, pending); err != nil {
			t.Fatalf("backdate: %v", err)
		}
		code, lib = call(t, h, trainerA, "GET", "/api/v1/courses/"+courseID+"/media?filter=unused", nil)
		mustStatus(t, code, 200, "unused library again")
		found := false
		for _, m := range arr(lib, "media") {
			if str(obj(m), "id") == pending {
				found = obj(m)["upload_unfinished"] == true
			}
		}
		if !found {
			t.Fatalf("pending upload older than 24 h should be labelled unfinished: %v", lib)
		}

		code, out := call(t, h, trainerA, "DELETE", "/api/v1/media/"+signID, nil)
		mustStatus(t, code, 409, "delete media in use")
		if errCode(out) != "conflict" {
			t.Fatalf("delete in use: %v", out)
		}
		code, _ = call(t, h, learner, "DELETE", "/api/v1/media/"+pending, nil)
		mustStatus(t, code, 404, "learner deletes media of a draft")
		removedBefore := len(store.removed)
		code, _ = call(t, h, trainerA, "DELETE", "/api/v1/media/"+pending, nil)
		mustStatus(t, code, 204, "delete an unused pending upload")
		if len(store.removed) != removedBefore+1 {
			t.Fatal("delete should remove the stored object")
		}
		code, _ = call(t, h, trainerA, "GET", "/api/v1/media/"+pending+"/url", nil)
		mustStatus(t, code, 404, "deleted media is gone")
	})

	spare := uploadMedia(t, h, store, courseID, "spare.png", "image/png", "Unused picture")
	spareID := str(spare, "id")
	clip := uploadMedia(t, h, store, courseID, "alarm.mp3", "audio/mpeg", "")
	clipID := str(clip, "id")

	t.Run("page attachments", func(t *testing.T) {
		code, am := call(t, h, trainerA, "POST", "/api/v1/activities/"+pageID+"/media",
			map[string]any{"media_id": clipID, "caption": "The fire alarm"})
		mustStatus(t, code, 201, "attach audio")
		if str(obj(am["media"]), "kind") != "audio" {
			t.Fatalf("attachment media: %v", am)
		}
		code, yt := call(t, h, trainerA, "POST", "/api/v1/activities/"+pageID+"/media",
			map[string]any{"external_url": "https://www.youtube.com/watch?v=dQw4w9WgXcQ"})
		mustStatus(t, code, 201, "attach youtube")
		if str(yt, "embed_url") != "https://www.youtube-nocookie.com/embed/dQw4w9WgXcQ" {
			t.Fatalf("youtube embed: %v", yt)
		}
		code, _ = call(t, h, trainerA, "POST", "/api/v1/activities/"+pageID+"/media",
			map[string]any{"external_url": "https://evil.example/video.mp4"})
		mustStatus(t, code, 422, "other hosts refused")
		code, _ = call(t, h, trainerA, "POST", "/api/v1/activities/"+pageID+"/media",
			map[string]any{"media_id": clipID, "external_url": "https://youtu.be/dQw4w9WgXcQ"})
		mustStatus(t, code, 422, "media and url together")
		code, _ = call(t, h, trainerA, "POST", "/api/v1/activities/"+quizID+"/media", map[string]any{"media_id": clipID})
		mustStatus(t, code, 422, "quizzes have no attachments")
		code, _ = call(t, h, trainerA, "PATCH", "/api/v1/activity-media/"+str(yt, "id"), map[string]any{"caption": "Watch first"})
		mustStatus(t, code, 200, "caption")
		code, list := call(t, h, trainerA, "GET", "/api/v1/activities/"+pageID+"/media", nil)
		mustStatus(t, code, 200, "list attachments")
		if len(arr(list, "attachments")) != 2 {
			t.Fatalf("attachments: %v", list)
		}
	})

	// Quiz: the picture question and the hotspot question; publish; enrol.
	for _, qid := range []string{str(picQ, "id"), hotID} {
		code, _ := call(t, h, trainerA, "POST", "/api/v1/activities/"+quizID+"/slots", map[string]any{"question_id": qid})
		mustStatus(t, code, 201, "add slot")
	}
	code, _ = call(t, h, trainerA, "POST", "/api/v1/courses/"+courseID+"/publish", nil)
	mustStatus(t, code, 200, "publish")
	code, people := call(t, h, trainerA, "GET", "/api/v1/users?q=lou", nil)
	mustStatus(t, code, 200, "people")
	learnerID := str(obj(arr(people, "people")[0]), "id")
	code, _ = call(t, h, trainerA, "POST", "/api/v1/courses/"+courseID+"/enrolments", map[string]any{"user_id": learnerID})
	mustStatus(t, code, 201, "enrol learner")

	t.Run("viewing access", func(t *testing.T) {
		code, out := call(t, h, learner, "GET", "/api/v1/media/"+signID+"/url", nil)
		mustStatus(t, code, 200, "learner views question media")
		if !strings.HasPrefix(str(out, "url"), "https://s3.test/") || str(out, "expires_at") == "" {
			t.Fatalf("view link: %v", out)
		}
		code, _ = call(t, h, learner, "GET", "/api/v1/media/"+clipID+"/url", nil)
		mustStatus(t, code, 200, "learner views page attachment")
		code, _ = call(t, h, learner, "GET", "/api/v1/media/"+spareID+"/url", nil)
		mustStatus(t, code, 404, "learner can't fetch unused library media")
		code, _ = call(t, h, outsider, "GET", "/api/v1/media/"+signID+"/url", nil)
		mustStatus(t, code, 404, "not enrolled")
		code, _ = call(t, h, trainerA, "GET", "/api/v1/media/"+spareID+"/url", nil)
		mustStatus(t, code, 200, "staff view anything in their library")
		code, _ = call(t, h, admin, "GET", "/api/v1/media/"+spareID+"/url", nil)
		mustStatus(t, code, 200, "admin views")

		req := httptest.NewRequest("GET", "/api/v1/media/"+signID, nil)
		req.Header.Set("X-Dev-User", learner.user)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != 302 || !strings.HasPrefix(rec.Header().Get("Location"), "https://s3.test/") {
			t.Fatalf("redirect: %d %s", rec.Code, rec.Header().Get("Location"))
		}
	})

	t.Run("hotspot attempt", func(t *testing.T) {
		code, attempt := call(t, h, learner, "POST", "/api/v1/activities/"+quizID+"/attempts", nil)
		mustStatus(t, code, 201, "start attempt")
		hq := attemptQuestion(t, attempt, "Tap both extinguishers")
		if _, leaked := hq["regions"]; leaked {
			t.Fatalf("regions sent before submitting: %v", hq)
		}
		if num(hq, "max_taps") != 2 || obj(hq["media"])["alt_text"] != "A green running-man sign" {
			t.Fatalf("hotspot question payload: %v", hq)
		}
		pq := attemptQuestion(t, attempt, "Which picture shows the fire exit sign?")
		if _, leaked := pq["feedback_media"]; leaked {
			t.Fatalf("feedback media sent before submitting: %v", pq)
		}
		var signOption string
		for _, o := range arr(pq, "options") {
			if str(obj(obj(o)["media"]), "id") == signID {
				signOption = str(obj(o), "id")
			}
		}
		attemptID := str(attempt, "id")
		code, _ = call(t, h, learner, "POST", "/api/v1/attempts/"+attemptID+"/submit", map[string]any{
			"answers": map[string]any{},
			"taps":    map[string]any{hotID: []any{map[string]any{"x": 0.1, "y": 0.1}, map[string]any{"x": 0.2, "y": 0.2}, map[string]any{"x": 0.3, "y": 0.3}}},
		})
		mustStatus(t, code, 422, "more taps than allowed")
		code, _ = call(t, h, learner, "POST", "/api/v1/attempts/"+attemptID+"/submit", map[string]any{
			"taps": map[string]any{str(picQ, "id"): []any{map[string]any{"x": 0.1, "y": 0.1}}},
		})
		mustStatus(t, code, 422, "taps on a non-hotspot question")
		code, result := call(t, h, learner, "POST", "/api/v1/attempts/"+attemptID+"/submit", map[string]any{
			"answers": map[string]any{str(picQ, "id"): []any{signOption}},
			"taps":    map[string]any{hotID: []any{map[string]any{"x": 0.2, "y": 0.2}, map[string]any{"x": 0.72, "y": 0.68}}},
		})
		mustStatus(t, code, 200, "submit")
		hq = attemptQuestion(t, result, "Tap both extinguishers")
		if hq["correct"] != true || len(arr(hq, "regions")) != 3 || len(arr(hq, "taps")) != 2 {
			t.Fatalf("hotspot result: %v", hq)
		}
		if result["passed"] != true || num(result, "score") != 2 {
			t.Fatalf("result: %v", result)
		}
		if attemptQuestion(t, result, "Which picture shows the fire exit sign?")["feedback_media"] == nil {
			t.Fatal("feedback media should come with the result")
		}

		code, attempt = call(t, h, learner, "POST", "/api/v1/activities/"+quizID+"/attempts", nil)
		mustStatus(t, code, 201, "second attempt")
		code, result = call(t, h, learner, "POST", "/api/v1/attempts/"+str(attempt, "id")+"/submit", map[string]any{
			"taps": map[string]any{hotID: []any{map[string]any{"x": 0.2, "y": 0.2}, map[string]any{"x": 0.55, "y": 0.15}}},
		})
		mustStatus(t, code, 200, "submit wrong taps")
		if attemptQuestion(t, result, "Tap both extinguishers")["correct"] != false {
			t.Fatal("a tap in a distractor region instead of a correct one must be wrong")
		}

		code, _ = call(t, h, trainerA, "PUT", "/api/v1/questions/"+hotID+"/regions", map[string]any{"regions": []any{
			map[string]any{"shape": "rect", "coords": map[string]any{"x": 0, "y": 0, "w": 1, "h": 1}, "correct": true}}})
		mustStatus(t, code, 409, "regions lock once answered")
		code, _ = call(t, h, trainerA, "PATCH", "/api/v1/questions/"+hotID, map[string]any{"media_id": spareID})
		mustStatus(t, code, 409, "a locked hotspot's image can't change")
		code, _ = call(t, h, trainerA, "PATCH", "/api/v1/questions/"+hotID, map[string]any{"prompt": "Tap both fire extinguishers"})
		mustStatus(t, code, 200, "prompt typos stay editable")
	})
}
