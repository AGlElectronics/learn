package http_test

// Reports (phase 8): completion and qualification reports, their filters,
// statuses, access (report.view, coordinator, trainers on own courses) and
// CSV output. Same DB-backed setup (and TEST_DATABASE_URL warning) as
// courses_test.go.

import (
	"context"
	"database/sql"
	"encoding/csv"
	nethttp "net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pressly/goose/v3"
	"github.com/rs/zerolog"

	"github.com/AGlElectronics/learn/backend/internal/auth"
	"github.com/AGlElectronics/learn/backend/internal/config"
	"github.com/AGlElectronics/learn/backend/internal/db"
	httpx "github.com/AGlElectronics/learn/backend/internal/http"
	"github.com/AGlElectronics/learn/backend/internal/learn"
	"github.com/AGlElectronics/learn/backend/migrations"
)

const reportsAccessTOML = `
organization = "demo-org"

[roles.organization_admin]
description = "Admin"
permissions = ["course.manage", "enrol.manage", "staff.manage", "qualification.manage", "settings.manage", "report.view"]

[roles.trainer]
description = "Trainer"
permissions = ["course.teach", "enrol.manage"]

[roles.coordinator]
description = "Coordinator"
permissions = ["report.view"]

[[bindings]]
group = "learn-admin"
role = "organization_admin"

[[bindings]]
group = "learn-trainer"
role = "trainer"

[[bindings]]
group = "learn-coordinator"
role = "coordinator"
`

var coordinator = identity{user: "cora-coord", groups: "learn-coordinator"}

// setupP8Router is setupRouter with the phase 8 access config (report.view,
// coordinator) and an optional object store; it also returns the pool.
func setupP8Router(t *testing.T, store learn.ObjectStore) (nethttp.Handler, *pgxpool.Pool) {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping DB-backed tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	t.Cleanup(cancel)
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
	if err := os.WriteFile(accessPath, []byte(reportsAccessTOML), 0o600); err != nil {
		t.Fatalf("write access config: %v", err)
	}
	access, err := auth.LoadAccessConfig(accessPath)
	if err != nil {
		t.Fatalf("load access config: %v", err)
	}
	cfg := &config.Config{
		AppEnv: "development", HTTPAddr: ":0", DatabaseURL: dsn,
		AuthMode: config.AuthModeDev, DevDefaultUser: "dev-admin", PublicBaseURL: "https://learn.test",
	}
	return httpx.NewRouter(httpx.Deps{Config: cfg, Pool: pool, Logger: zerolog.Nop(), Access: access, Files: store}, "test"), pool
}

// rawGet performs an authenticated GET and returns the recorder (for CSV).
func rawGet(t *testing.T, h nethttp.Handler, id identity, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("GET", path, nil)
	req.Header.Set("X-Dev-User", id.user)
	req.Header.Set("X-Dev-Groups", id.groups)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func reportRows(t *testing.T, h nethttp.Handler, id identity, path string) []map[string]any {
	t.Helper()
	code, out := call(t, h, id, "GET", path, nil)
	mustStatus(t, code, 200, path)
	rows := []map[string]any{}
	for _, r := range arr(out, "rows") {
		rows = append(rows, obj(r))
	}
	return rows
}

func statusByPerson(rows []map[string]any) map[string]string {
	out := map[string]string{}
	for _, r := range rows {
		out[str(r, "display_name")] = str(r, "status")
	}
	return out
}

func TestReports(t *testing.T) {
	h, pool := setupP8Router(t, nil)
	ann := identity{user: "ann-done"}
	bob := identity{user: "bob-progress"}
	cat := identity{user: "cat-new"}
	dan := identity{user: "dan-late"}
	eve := identity{user: "eve-left"}
	ids := signIn(t, h, admin, trainerA, trainerB, coordinator, learner, ann, bob, cat, dan, eve)

	courseA, secA := newCourse(t, h, "Forklift")
	a1 := newActivity(t, h, secA, "page", "Intro")
	newActivity(t, h, secA, "page", "Driving")
	publishAndEnrol(t, h, courseA, ids["ann-done"], ids["bob-progress"], ids["cat-new"], ids["dan-late"], ids["eve-left"])

	code, courseB := call(t, h, trainerB, "POST", "/api/v1/courses", map[string]any{"title": "Bar work"})
	mustStatus(t, code, 201, "trainer B course")
	courseBID := str(courseB, "id")
	code, secB := call(t, h, trainerB, "POST", "/api/v1/courses/"+courseBID+"/sections", map[string]any{"title": "S"})
	mustStatus(t, code, 201, "section B")
	code, _ = call(t, h, trainerB, "POST", "/api/v1/sections/"+str(secB, "id")+"/activities", map[string]any{"title": "Taps"})
	mustStatus(t, code, 201, "activity B")
	code, _ = call(t, h, trainerB, "POST", "/api/v1/courses/"+courseBID+"/publish", nil)
	mustStatus(t, code, 200, "publish B")
	code, _ = call(t, h, trainerB, "POST", "/api/v1/courses/"+courseBID+"/enrolments", map[string]any{"user_id": ids["ann-done"]})
	mustStatus(t, code, 201, "enrol ann on B")

	// Ann finishes A, Bob is halfway, Dan is overdue, Eve left the group.
	detail := func(id identity) {
		code, d := call(t, h, id, "GET", "/api/v1/courses/"+courseA, nil)
		mustStatus(t, code, 200, "detail")
		for _, s := range arr(d, "sections") {
			for _, a := range arr(obj(s), "activities") {
				if id == ann || str(obj(a), "id") == a1 {
					code, _ := call(t, h, id, "PUT", "/api/v1/activities/"+str(obj(a), "id")+"/completion", map[string]any{"completed": true})
					mustStatus(t, code, 200, "complete")
				}
			}
		}
	}
	detail(ann)
	detail(bob)
	exec(t, pool, `UPDATE enrolments SET due_at = now() - interval '1 day' WHERE course_id = $1 AND user_id = $2`, courseA, ids["dan-late"])
	exec(t, pool, `UPDATE enrolments SET status = 'inactive', source = 'group' WHERE course_id = $1 AND user_id = $2`, courseA, ids["eve-left"])
	exec(t, pool, `INSERT INTO user_groups (user_id, group_name) VALUES ($1, 'team-red'), ($2, 'team-red')`, ids["ann-done"], ids["bob-progress"])

	t.Run("completion statuses and filters", func(t *testing.T) {
		rows := reportRows(t, h, admin, "/api/v1/reports/completion?course="+courseA)
		got := statusByPerson(rows)
		want := map[string]string{"Ann Done": "done", "Bob Progress": "in_progress", "Cat New": "not_started", "Dan Late": "overdue"}
		if len(got) != len(want) {
			t.Fatalf("completion rows: got %v, want %v (inactive enrolments excluded)", got, want)
		}
		for k, v := range want {
			if got[k] != v {
				t.Fatalf("status of %s: got %q, want %q (%v)", k, got[k], v, got)
			}
		}
		if rows := reportRows(t, h, admin, "/api/v1/reports/completion?course="+courseA+"&status=done"); len(rows) != 1 || str(rows[0], "display_name") != "Ann Done" {
			t.Fatalf("status=done: %v", rows)
		}
		if rows := reportRows(t, h, admin, "/api/v1/reports/completion?status=overdue"); len(rows) != 1 {
			t.Fatalf("status=overdue: %v", rows)
		}
		if rows := reportRows(t, h, admin, "/api/v1/reports/completion?group=team-red"); len(rows) != 3 { // ann A+B, bob A
			t.Fatalf("group=team-red: %v", rows)
		}
		code, _ := call(t, h, admin, "GET", "/api/v1/reports/completion?status=bogus", nil)
		mustStatus(t, code, 422, "bad status")
	})

	t.Run("access", func(t *testing.T) {
		if rows := reportRows(t, h, coordinator, "/api/v1/reports/completion"); len(rows) != 5 {
			t.Fatalf("coordinator sees every course: %d rows", len(rows))
		}
		rows := reportRows(t, h, trainerA, "/api/v1/reports/completion")
		for _, r := range rows {
			if str(r, "course_id") != courseA {
				t.Fatalf("trainer A sees another course: %v", r)
			}
		}
		if len(rows) != 4 {
			t.Fatalf("trainer A rows: %d", len(rows))
		}
		code, _ := call(t, h, trainerA, "GET", "/api/v1/reports/completion?course="+courseBID, nil)
		mustStatus(t, code, 403, "trainer A filters on trainer B's course")
		code, _ = call(t, h, trainerA, "GET", "/api/v1/reports/qualifications", nil)
		mustStatus(t, code, 403, "trainer without report.view on qualifications")
		code, _ = call(t, h, learner, "GET", "/api/v1/reports/completion", nil)
		mustStatus(t, code, 403, "learner on completion report")
		code, scope := call(t, h, trainerA, "GET", "/api/v1/reports/scope", nil)
		mustStatus(t, code, 200, "scope")
		if scope["all"] != false || len(arr(scope, "courses")) != 1 {
			t.Fatalf("trainer scope: %v", scope)
		}
		code, scope = call(t, h, coordinator, "GET", "/api/v1/reports/scope", nil)
		mustStatus(t, code, 200, "coordinator scope")
		if scope["all"] != true || len(arr(scope, "courses")) != 2 {
			t.Fatalf("coordinator scope: %v", scope)
		}
	})

	t.Run("csv escaping and disposition", func(t *testing.T) {
		exec(t, pool, `UPDATE users SET display_name = $2 WHERE id = $1`, ids["cat-new"], `=Cat, "the" New`)
		rec := rawGet(t, h, admin, "/api/v1/reports/completion?course="+courseA+"&format=csv")
		if rec.Code != 200 || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/csv") {
			t.Fatalf("csv: %d %s", rec.Code, rec.Header().Get("Content-Type"))
		}
		if cd := rec.Header().Get("Content-Disposition"); !strings.HasPrefix(cd, `attachment; filename="completion-report-`) {
			t.Fatalf("content-disposition: %q", cd)
		}
		if !strings.Contains(rec.Body.String(), `"'=Cat, ""the"" New"`) {
			t.Fatalf("csv not escaped: %s", rec.Body.String())
		}
		records, err := csv.NewReader(strings.NewReader(rec.Body.String())).ReadAll()
		if err != nil || len(records) != 5 || records[0][0] != "course" {
			t.Fatalf("csv parse: %v %v", err, records)
		}
	})

	t.Run("qualification report", func(t *testing.T) {
		code, q := call(t, h, admin, "POST", "/api/v1/qualifications", map[string]any{"key": "forklift", "name": "Forklift licence", "valid_months": 12})
		mustStatus(t, code, 201, "create qualification")
		qID := str(q, "id")
		setQualification(t, h, courseA, qID) // awards Ann retroactively
		exec(t, pool, `INSERT INTO course_group_links (course_id, group_name) VALUES ($1, 'team-red')`, courseA)

		base := "/api/v1/reports/qualifications?qualification=" + qID
		got := statusByPerson(reportRows(t, h, coordinator, base))
		if got["Ann Done"] != "valid" || got["Bob Progress"] != "missing" || len(got) != 2 {
			t.Fatalf("qualification report: %v", got)
		}
		if rows := reportRows(t, h, admin, base+"&status=missing"); len(rows) != 1 || str(rows[0], "display_name") != "Bob Progress" || rows[0]["required"] != true {
			t.Fatalf("status=missing: %v", rows)
		}
		exec(t, pool, `UPDATE certificates SET expires_at = now() + interval '10 days' WHERE user_id = $1`, ids["ann-done"])
		if got := statusByPerson(reportRows(t, h, admin, base)); got["Ann Done"] != "expiring" {
			t.Fatalf("expiring: %v", got)
		}
		exec(t, pool, `UPDATE certificates SET expires_at = now() - interval '1 day' WHERE user_id = $1`, ids["ann-done"])
		if rows := reportRows(t, h, admin, base+"&status=expired"); len(rows) != 1 {
			t.Fatalf("expired: %v", rows)
		}
		if rows := reportRows(t, h, admin, base+"&group=nobody"); len(rows) != 0 {
			t.Fatalf("group filter: %v", rows)
		}
		rec := rawGet(t, h, admin, base+"&format=csv")
		if rec.Code != 200 || !strings.Contains(rec.Body.String(), "forklift,Forklift licence") {
			t.Fatalf("qualification csv: %d %s", rec.Code, rec.Body.String())
		}
	})
}
