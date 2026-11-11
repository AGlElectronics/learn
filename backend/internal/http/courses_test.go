package http_test

// Authorization and progress tests against a real PostgreSQL.
//
// They run only when TEST_DATABASE_URL is set and MIGRATE THE DATABASE FROM
// SCRATCH (drop + recreate the public schema), so point it at a disposable
// database, never a real one:
//
//	TEST_DATABASE_URL='postgres://learn:learn@localhost:5432/learn_test?sslmode=disable' go test ./...
//
// The suite runs through the real router in dev auth mode. Dev auth feeds
// X-Dev-Groups through the same access-config binding path as OIDC token
// groups, so these tests exercise the production authorization code.

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	nethttp "net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/rs/zerolog"

	"github.com/AGlElectronics/learn/backend/internal/auth"
	"github.com/AGlElectronics/learn/backend/internal/config"
	"github.com/AGlElectronics/learn/backend/internal/db"
	httpx "github.com/AGlElectronics/learn/backend/internal/http"
	"github.com/AGlElectronics/learn/backend/internal/learn"
	"github.com/AGlElectronics/learn/backend/migrations"
)

const accessConfigTOML = `
organization = "demo-org"

[roles.organization_admin]
description = "Admin"
permissions = ["course.manage", "enrol.manage", "staff.manage", "qualification.manage", "settings.manage"]

[roles.trainer]
description = "Trainer"
permissions = ["course.teach", "enrol.manage"]

[roles.training_manager]
description = "Training manager"
permissions = ["staff.manage"]

[roles.coordinator]
description = "Training office"
permissions = ["report.view", "qualification.manage"]

[[bindings]]
group = "learn-admin"
role = "organization_admin"

[[bindings]]
group = "learn-trainer"
role = "trainer"

[[bindings]]
group = "learn-manager"
role = "training_manager"

[[bindings]]
group = "learn-coordinator"
role = "coordinator"
`

// identity is a dev-mode caller: a name plus simulated IdP groups.
type identity struct {
	user   string
	groups string
}

var (
	admin    = identity{user: "dev-admin", groups: "learn-admin"}
	trainerA = identity{user: "trainer-a", groups: "learn-trainer"}
	trainerB = identity{user: "trainer-b", groups: "learn-trainer"}
	learner  = identity{user: "learner-lou", groups: ""}
)

func setupRouter(t *testing.T) nethttp.Handler {
	t.Helper()
	return setupRouterWithFiles(t, nil)
}

// setupRouterWithFiles is setupRouter with an object store (nil = off).
func setupRouterWithFiles(t *testing.T, store learn.ObjectStore) nethttp.Handler {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping DB-backed tests")
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	t.Cleanup(cancel)

	// Fresh schema + migrations on every run.
	sqlDB, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if _, err := sqlDB.ExecContext(ctx, `DROP SCHEMA public CASCADE; CREATE SCHEMA public;`); err != nil {
		t.Fatalf("reset schema: %v", err)
	}
	goose.SetBaseFS(migrations.FS)
	goose.SetTableName("goose_db_version")
	goose.SetLogger(goose.NopLogger())
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatalf("goose dialect: %v", err)
	}
	if err := goose.UpContext(ctx, sqlDB, "."); err != nil {
		t.Fatalf("goose up: %v", err)
	}

	pool, err := db.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	t.Cleanup(pool.Close)

	accessPath := filepath.Join(t.TempDir(), "access-config.toml")
	if err := os.WriteFile(accessPath, []byte(accessConfigTOML), 0o600); err != nil {
		t.Fatalf("write access config: %v", err)
	}
	access, err := auth.LoadAccessConfig(accessPath)
	if err != nil {
		t.Fatalf("load access config: %v", err)
	}

	cfg := &config.Config{
		AppEnv:         "test",
		HTTPAddr:       ":0",
		DatabaseURL:    dsn,
		AuthMode:       config.AuthModeDev,
		DevDefaultUser: "dev-admin",
	}
	return httpx.NewRouter(httpx.Deps{
		Config: cfg,
		Pool:   pool,
		Logger: zerolog.Nop(),
		Access: access,
		Files:  store,
	}, "test")
}

// call performs one request as the given identity and decodes the JSON body.
func call(t *testing.T, h nethttp.Handler, id identity, method, path string, body any) (int, map[string]any) {
	t.Helper()
	var rd *bytes.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
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
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	out := map[string]any{}
	if rec.Body.Len() > 0 {
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("%s %s: invalid JSON response (%d): %s", method, path, rec.Code, rec.Body.String())
		}
	}
	return rec.Code, out
}

func mustStatus(t *testing.T, got int, want int, what string) {
	t.Helper()
	if got != want {
		t.Fatalf("%s: got status %d, want %d", what, got, want)
	}
}

func str(m map[string]any, key string) string {
	s, _ := m[key].(string)
	return s
}

// TestCourseLifecycleAndAuthorization walks the whole slice: create → content
// → publish → enrol → complete, with the negative authorization checks the
// plan calls out.
func TestCourseLifecycleAndAuthorization(t *testing.T) {
	h := setupRouter(t)

	// Everyone signs in once so their users rows exist (enrolment needs it).
	for _, id := range []identity{admin, trainerA, trainerB, learner} {
		code, _ := call(t, h, id, "GET", "/api/v1/me", nil)
		mustStatus(t, code, 200, "me for "+id.user)
	}

	// Trainer A builds an unpublished course with one page.
	code, course := call(t, h, trainerA, "POST", "/api/v1/courses",
		map[string]any{"title": "Radio basics", "summary": "How to talk on air"})
	mustStatus(t, code, 201, "trainer A creates course")
	courseID := str(course, "id")

	code, section := call(t, h, trainerA, "POST", "/api/v1/courses/"+courseID+"/sections",
		map[string]any{"title": "Week 1"})
	mustStatus(t, code, 201, "trainer A creates section")
	sectionID := str(section, "id")

	code, page := call(t, h, trainerA, "POST", "/api/v1/sections/"+sectionID+"/activities",
		map[string]any{"title": "Welcome", "body": "Hello!"})
	mustStatus(t, code, 201, "trainer A creates page")
	pageID := str(page, "id")

	t.Run("unpublished course is invisible to non-staff", func(t *testing.T) {
		code, _ := call(t, h, learner, "GET", "/api/v1/courses/"+courseID, nil)
		mustStatus(t, code, 404, "learner reads unpublished course")
		// Another trainer is no better off — and gets a 404, not a 403
		// that would confirm the course exists.
		code, _ = call(t, h, trainerB, "GET", "/api/v1/courses/"+courseID, nil)
		mustStatus(t, code, 404, "trainer B reads unpublished course")
		code, _ = call(t, h, trainerB, "PATCH", "/api/v1/courses/"+courseID,
			map[string]any{"title": "Hijacked"})
		mustStatus(t, code, 404, "trainer B edits unpublished course")
	})

	t.Run("staff and admin see the draft", func(t *testing.T) {
		code, _ := call(t, h, trainerA, "GET", "/api/v1/courses/"+courseID, nil)
		mustStatus(t, code, 200, "trainer A reads own draft")
		code, _ = call(t, h, admin, "GET", "/api/v1/courses/"+courseID, nil)
		mustStatus(t, code, 200, "admin reads any draft")
	})

	// Publish.
	code, _ = call(t, h, trainerA, "POST", "/api/v1/courses/"+courseID+"/publish", nil)
	mustStatus(t, code, 200, "trainer A publishes")

	t.Run("published but not enrolled: readable to nobody but staff", func(t *testing.T) {
		code, _ := call(t, h, learner, "GET", "/api/v1/courses/"+courseID, nil)
		mustStatus(t, code, 403, "learner reads published course without enrolment")
	})

	t.Run("self-enrol denied while the course does not allow it", func(t *testing.T) {
		code, _ := call(t, h, learner, "POST", "/api/v1/courses/"+courseID+"/enrol", nil)
		mustStatus(t, code, 403, "self-enrol on a closed course")
	})

	t.Run("trainer cannot edit another trainer's published course", func(t *testing.T) {
		code, _ := call(t, h, trainerB, "PATCH", "/api/v1/courses/"+courseID,
			map[string]any{"title": "Hijacked"})
		mustStatus(t, code, 403, "trainer B edits trainer A's course")
		code, _ = call(t, h, trainerB, "POST", "/api/v1/courses/"+courseID+"/sections",
			map[string]any{"title": "Bogus"})
		mustStatus(t, code, 403, "trainer B adds section to trainer A's course")
		code, _ = call(t, h, trainerB, "GET", "/api/v1/courses/"+courseID+"/enrolments", nil)
		mustStatus(t, code, 403, "trainer B lists enrolments of trainer A's course")
	})

	// Find the learner's user id through the people picker (staff only).
	t.Run("people picker is closed to learners", func(t *testing.T) {
		code, _ := call(t, h, learner, "GET", "/api/v1/users?q=lou", nil)
		mustStatus(t, code, 403, "learner searches people")
	})
	code, people := call(t, h, trainerA, "GET", "/api/v1/users?q=lou", nil)
	mustStatus(t, code, 200, "trainer A searches people")
	list, _ := people["people"].([]any)
	if len(list) != 1 {
		t.Fatalf("people search: got %d results, want 1", len(list))
	}
	learnerID := str(list[0].(map[string]any), "id")

	// Staff enrols the learner.
	code, _ = call(t, h, trainerA, "POST", "/api/v1/courses/"+courseID+"/enrolments",
		map[string]any{"user_id": learnerID, "role": "learner"})
	mustStatus(t, code, 201, "trainer A enrols learner")

	t.Run("learner cannot edit the course they are enrolled in", func(t *testing.T) {
		code, _ := call(t, h, learner, "PATCH", "/api/v1/courses/"+courseID,
			map[string]any{"title": "Hijacked"})
		mustStatus(t, code, 403, "learner edits course")
		code, _ = call(t, h, learner, "DELETE", "/api/v1/activities/"+pageID, nil)
		mustStatus(t, code, 403, "learner deletes page")
	})

	t.Run("enrolled learner reads the course and completes the page", func(t *testing.T) {
		code, detail := call(t, h, learner, "GET", "/api/v1/courses/"+courseID, nil)
		mustStatus(t, code, 200, "learner reads published course")
		progress, _ := detail["progress"].(map[string]any)
		if progress["total"].(float64) != 1 || progress["completed"].(float64) != 0 {
			t.Fatalf("fresh progress: got %v, want 0/1", progress)
		}

		code, _ = call(t, h, learner, "PUT", "/api/v1/activities/"+pageID+"/completion",
			map[string]any{"completed": true})
		mustStatus(t, code, 200, "learner marks page done")

		code, detail = call(t, h, learner, "GET", "/api/v1/courses/"+courseID, nil)
		mustStatus(t, code, 200, "learner re-reads course")
		progress, _ = detail["progress"].(map[string]any)
		if progress["completed"].(float64) != 1 {
			t.Fatalf("progress after completion: got %v, want 1/1", progress)
		}

		// And back: unmarking works too.
		code, _ = call(t, h, learner, "PUT", "/api/v1/activities/"+pageID+"/completion",
			map[string]any{"completed": false})
		mustStatus(t, code, 200, "learner unmarks page")
		code, detail = call(t, h, learner, "GET", "/api/v1/courses/"+courseID, nil)
		mustStatus(t, code, 200, "learner re-reads course again")
		progress, _ = detail["progress"].(map[string]any)
		if progress["completed"].(float64) != 0 {
			t.Fatalf("progress after unmark: got %v, want 0/1", progress)
		}
	})

	t.Run("admin manages any course", func(t *testing.T) {
		code, _ := call(t, h, admin, "PATCH", "/api/v1/courses/"+courseID,
			map[string]any{"summary": "Now admin-approved"})
		mustStatus(t, code, 200, "admin edits course")
		code, enr := call(t, h, admin, "GET", "/api/v1/courses/"+courseID+"/enrolments", nil)
		mustStatus(t, code, 200, "admin lists enrolments")
		items, _ := enr["enrolments"].([]any)
		if len(items) != 2 { // trainer A (staff) + learner
			t.Fatalf("enrolments: got %d, want 2", len(items))
		}
	})

	t.Run("self-enrol works once the course allows it", func(t *testing.T) {
		code, _ := call(t, h, trainerA, "PATCH", "/api/v1/courses/"+courseID,
			map[string]any{"self_enrol": true})
		mustStatus(t, code, 200, "trainer A opens self-enrolment")

		code, catalog := call(t, h, trainerB, "GET", "/api/v1/courses/catalog", nil)
		mustStatus(t, code, 200, "catalog lists the course")
		items, _ := catalog["courses"].([]any)
		if len(items) != 1 {
			t.Fatalf("catalog: got %d courses, want 1", len(items))
		}

		code, _ = call(t, h, trainerB, "POST", "/api/v1/courses/"+courseID+"/enrol", nil)
		mustStatus(t, code, 201, "trainer B self-enrols as a learner")
		// Self-enrolment grants learner access, never staff authority.
		code, _ = call(t, h, trainerB, "PATCH", "/api/v1/courses/"+courseID,
			map[string]any{"title": "Still hijacked?"})
		mustStatus(t, code, 403, "trainer B still cannot edit after self-enrol")
	})

	t.Run("mine lists the enrolled course with progress", func(t *testing.T) {
		code, mine := call(t, h, learner, "GET", "/api/v1/courses/mine", nil)
		mustStatus(t, code, 200, "learner lists mine")
		items, _ := mine["courses"].([]any)
		if len(items) != 1 {
			t.Fatalf("mine: got %d courses, want 1", len(items))
		}
		first := items[0].(map[string]any)
		if str(first, "enrolment_role") != "learner" {
			t.Fatalf("mine: enrolment_role = %q, want learner", str(first, "enrolment_role"))
		}
	})
}

// TestRoleCatalogIsPublic pins the contract HR consumes.
func TestRoleCatalogIsPublic(t *testing.T) {
	h := setupRouter(t)

	req := httptest.NewRequest("GET", "/api/role-catalog", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req) // no auth headers at all
	if rec.Code != 200 {
		t.Fatalf("role catalog: got status %d, want 200", rec.Code)
	}
	var out struct {
		Module string `json:"module"`
		Roles  []struct {
			Key        string `json:"key"`
			Group      string `json:"group"`
			Assignable bool   `json:"assignable"`
		} `json:"roles"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("role catalog: invalid JSON: %v", err)
	}
	if out.Module != "learn" {
		t.Fatalf("role catalog module = %q, want learn", out.Module)
	}
	want := map[string]string{
		"organization_admin": "learn-admin", "trainer": "learn-trainer",
		"training_manager": "learn-manager", "coordinator": "learn-coordinator",
	}
	for _, r := range out.Roles {
		group, ok := want[r.Key]
		if !ok || r.Group != group || !r.Assignable {
			t.Fatalf("role catalog entry %+v unexpected (want %v)", r, want)
		}
		delete(want, r.Key)
	}
	if len(want) != 0 {
		t.Fatalf("role catalog missing roles: %v", want)
	}
}
