package http_test

// Question import: preview (nothing saved) and commit into a category, for
// GIFT, CSV and Moodle XML with an embedded image (with and without object
// storage). Same DB-backed setup as courses_test.go.

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	nethttp "net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AGlElectronics/learn/backend/internal/files"
)

// PutObject lets the fake store take embedded images from an import.
func (f *fakeStore) PutObject(_ context.Context, key string, r io.Reader, size int64, contentType string) error {
	n, err := io.Copy(io.Discard, r)
	if err != nil {
		return err
	}
	if size >= 0 && n != size {
		return io.ErrUnexpectedEOF
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.objects[key] = files.ObjectInfo{Size: n, ContentType: contentType}
	return nil
}

// callImport posts a multipart import request.
func callImport(t *testing.T, h nethttp.Handler, id identity, courseID, step, filename string, content []byte, fields map[string]string) (int, map[string]any) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for k, v := range fields {
		_ = mw.WriteField(k, v)
	}
	fw, err := mw.CreateFormFile("file", filename)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = fw.Write(content)
	_ = mw.Close()
	req := httptest.NewRequest("POST", "/api/v1/courses/"+courseID+"/question-import?step="+step, &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("X-Dev-User", id.user)
	req.Header.Set("X-Dev-Groups", id.groups)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	out := map[string]any{}
	if rec.Body.Len() > 0 {
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("import %s: invalid JSON (%d): %s", step, rec.Code, rec.Body.String())
		}
	}
	return rec.Code, out
}

func readTestdata(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "importer", "testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func importCourse(t *testing.T, h nethttp.Handler, title string) (courseID, categoryID string) {
	t.Helper()
	code, course := call(t, h, trainerA, "POST", "/api/v1/courses", map[string]any{"title": title})
	mustStatus(t, code, 201, "create course")
	code, cat := call(t, h, trainerA, "POST", "/api/v1/courses/"+str(course, "id")+"/question-categories", map[string]any{"name": "Imported"})
	mustStatus(t, code, 201, "category")
	return str(course, "id"), str(cat, "id")
}

func questionsIn(t *testing.T, h nethttp.Handler, categoryID string) []any {
	t.Helper()
	code, out := call(t, h, trainerA, "GET", "/api/v1/question-categories/"+categoryID+"/questions", nil)
	mustStatus(t, code, 200, "list questions")
	return arr(out, "questions")
}

func hasWarning(out map[string]any, substr string) bool {
	for _, w := range arr(out, "warnings") {
		if strings.Contains(str(obj(w), "message"), substr) {
			return true
		}
	}
	return false
}

func TestQuestionImport(t *testing.T) {
	store := newFakeStore()
	h := setupRouterWithFiles(t, store)
	signIn(t, h, trainerA, trainerB, learner)
	courseID, catID := importCourse(t, h, "Import target")
	gift := readTestdata(t, "sample.gift")

	t.Run("staff only", func(t *testing.T) {
		code, _ := callImport(t, h, trainerB, courseID, "preview", "bank.gift", gift, nil)
		mustStatus(t, code, 404, "other trainer imports into a draft")
		code, _ = callImport(t, h, learner, courseID, "preview", "bank.gift", gift, nil)
		mustStatus(t, code, 404, "learner imports into a draft")
	})

	t.Run("bad requests", func(t *testing.T) {
		code, _ := callImport(t, h, trainerA, courseID, "preview", "bank.gift", gift, map[string]string{"format": "docx"})
		mustStatus(t, code, 422, "unknown format")
		code, _ = callImport(t, h, trainerA, courseID, "preview", "notes.bin", []byte("hello"), nil)
		mustStatus(t, code, 422, "undetectable format")
		code, _ = callImport(t, h, trainerA, courseID, "launch", "bank.gift", gift, nil)
		mustStatus(t, code, 400, "unknown step")
		code, _ = callImport(t, h, trainerA, courseID, "commit", "bank.gift", gift, nil)
		mustStatus(t, code, 422, "commit without category")
		otherCourse, otherCat := importCourse(t, h, "Other")
		_ = otherCourse
		code, _ = callImport(t, h, trainerA, courseID, "commit", "bank.gift", gift, map[string]string{"category_id": otherCat})
		mustStatus(t, code, 422, "category of another course")
	})

	t.Run("GIFT preview then commit", func(t *testing.T) {
		code, prev := callImport(t, h, trainerA, courseID, "preview", "bank.gift", gift, nil)
		mustStatus(t, code, 200, "preview gift")
		qs := arr(prev, "questions")
		if str(prev, "format") != "gift" || len(qs) == 0 || len(arr(prev, "warnings")) == 0 {
			t.Fatalf("gift preview: %v", prev)
		}
		if len(questionsIn(t, h, catID)) != 0 {
			t.Fatal("preview must not save questions")
		}
		code, res := callImport(t, h, trainerA, courseID, "commit", "bank.gift", gift, map[string]string{"category_id": catID})
		mustStatus(t, code, 200, "commit gift")
		if int(num(res, "imported")) != len(qs) || num(res, "skipped") != 0 {
			t.Fatalf("gift commit: %v (preview had %d)", res, len(qs))
		}
		saved := questionsIn(t, h, catID)
		if len(saved) != len(qs) {
			t.Fatalf("saved %d questions, want %d", len(saved), len(qs))
		}
		first := obj(saved[0])
		if str(first, "prompt") != "Which extinguisher is for electrical fires?" || str(first, "kind") != "single" || len(arr(first, "options")) != 3 {
			t.Fatalf("first imported question: %v", first)
		}
	})

	t.Run("CSV template round trip", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/v1/question-import/csv-template", nil)
		req.Header.Set("X-Dev-User", trainerA.user)
		req.Header.Set("X-Dev-Groups", trainerA.groups)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != 200 || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/csv") ||
			!strings.HasPrefix(rec.Body.String(), "prompt,kind,option1") {
			t.Fatalf("template: %d %s %q", rec.Code, rec.Header().Get("Content-Type"), rec.Body.String())
		}
		csvCourse, csvCat := importCourse(t, h, "CSV")
		code, prev := callImport(t, h, trainerA, csvCourse, "preview", "template.csv", rec.Body.Bytes(), nil)
		mustStatus(t, code, 200, "preview csv")
		if str(prev, "format") != "csv" || len(arr(prev, "questions")) != 2 {
			t.Fatalf("csv preview: %v", prev)
		}
		code, res := callImport(t, h, trainerA, csvCourse, "commit", "template.csv", rec.Body.Bytes(),
			map[string]string{"category_id": csvCat, "format": "csv"})
		mustStatus(t, code, 200, "commit csv")
		if num(res, "imported") != 2 {
			t.Fatalf("csv commit: %v", res)
		}
		saved := questionsIn(t, h, csvCat)
		if len(saved) != 2 || str(obj(saved[1]), "kind") != "multiple" || num(obj(saved[1]), "points") != 2 {
			t.Fatalf("csv questions: %v", saved)
		}
	})

	moodle := readTestdata(t, "moodle.xml")

	t.Run("Moodle XML image goes to the media library", func(t *testing.T) {
		mCourse, mCat := importCourse(t, h, "Moodle")
		code, prev := callImport(t, h, trainerA, mCourse, "preview", "export.xml", moodle, nil)
		mustStatus(t, code, 200, "preview moodle")
		qs := arr(prev, "questions")
		if len(qs) != 3 || len(arr(obj(qs[0]), "images")) != 1 || len(arr(prev, "warnings")) != 3 {
			t.Fatalf("moodle preview: %v", prev)
		}
		code, res := callImport(t, h, trainerA, mCourse, "commit", "export.xml", moodle, map[string]string{"category_id": mCat})
		mustStatus(t, code, 200, "commit moodle")
		if num(res, "imported") != 3 || num(res, "images") != 1 {
			t.Fatalf("moodle commit: %v", res)
		}
		saved := questionsIn(t, h, mCat)
		media := obj(obj(saved[0])["media"])
		if str(media, "kind") != "image" || str(media, "alt_text") != "sign.png" {
			t.Fatalf("imported question media: %v", saved[0])
		}
		code, m := call(t, h, trainerA, "GET", "/api/v1/media/"+str(media, "id")+"/url", nil)
		mustStatus(t, code, 200, "view imported image")
		if !strings.HasPrefix(str(m, "url"), "https://s3.test/") {
			t.Fatalf("view link: %v", m)
		}
	})
}

func TestQuestionImportWithoutStorage(t *testing.T) {
	h := setupRouter(t)
	signIn(t, h, trainerA)
	courseID, catID := importCourse(t, h, "No storage import")
	code, res := callImport(t, h, trainerA, courseID, "commit", "export.xml", readTestdata(t, "moodle.xml"),
		map[string]string{"category_id": catID, "format": "moodle_xml"})
	mustStatus(t, code, 200, "commit moodle without storage")
	if num(res, "imported") != 3 || num(res, "images") != 0 || !hasWarning(res, "object storage is not configured") {
		t.Fatalf("commit without storage: %v", res)
	}
	saved := questionsIn(t, h, catID)
	if len(saved) != 3 || obj(saved[0])["media_id"] != nil {
		t.Fatalf("questions without storage: %v", saved)
	}
	var n int
	if err := testDB(t).QueryRow(`SELECT count(*) FROM media WHERE course_id = $1`, courseID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("no media rows expected, got %d", n)
	}
}
