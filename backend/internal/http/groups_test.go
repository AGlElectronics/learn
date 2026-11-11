package http_test

// Keycloak group sync tests against a fake Keycloak Admin API. Same
// DB-backed setup (and TEST_DATABASE_URL warning) as courses_test.go.
//
// The router runs in dev auth mode with the sync issuer set to "dev", so a
// synced Keycloak member "ann" is the same users row as the dev caller
// "ann": exactly how a synced OIDC user merges with their first login.

import (
	"context"
	"database/sql"
	"encoding/json"
	nethttp "net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pressly/goose/v3"
	"github.com/rs/zerolog"

	"github.com/AGlElectronics/learn/backend/internal/auth"
	"github.com/AGlElectronics/learn/backend/internal/config"
	"github.com/AGlElectronics/learn/backend/internal/db"
	httpx "github.com/AGlElectronics/learn/backend/internal/http"
	"github.com/AGlElectronics/learn/backend/internal/keycloakadmin"
	"github.com/AGlElectronics/learn/backend/internal/learn"
	"github.com/AGlElectronics/learn/backend/migrations"
)

// fakeRealm is a mutable Keycloak realm: group name -> member usernames.
// Member ids equal usernames to keep the test readable.
type fakeRealm struct {
	mu      sync.Mutex
	groups  map[string][]string
	failing map[string]bool
}

func (f *fakeRealm) set(group string, members ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.groups[group] = members
}

func (f *fakeRealm) fail(group string, on bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failing[group] = on
}

func (f *fakeRealm) handler() nethttp.Handler {
	writeJSON := func(w nethttp.ResponseWriter, r *nethttp.Request, v []map[string]any) {
		if first, _ := strconv.Atoi(r.URL.Query().Get("first")); first > 0 {
			v = []map[string]any{}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(v)
	}
	mux := nethttp.NewServeMux()
	mux.HandleFunc("POST /realms/conservices/protocol/openid-connect/token", func(w nethttp.ResponseWriter, r *nethttp.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"tok","expires_in":300}`))
	})
	mux.HandleFunc("GET /admin/realms/conservices/groups", func(w nethttp.ResponseWriter, r *nethttp.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		out := []map[string]any{}
		for name := range f.groups {
			out = append(out, map[string]any{"id": "id-" + name, "name": name, "path": "/" + name, "subGroupCount": 0})
		}
		writeJSON(w, r, out)
	})
	mux.HandleFunc("GET /admin/realms/conservices/groups/{id}/members", func(w nethttp.ResponseWriter, r *nethttp.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		name := r.PathValue("id")[len("id-"):]
		if f.failing[name] {
			w.WriteHeader(nethttp.StatusInternalServerError)
			return
		}
		out := []map[string]any{}
		for _, m := range f.groups[name] {
			out = append(out, map[string]any{"id": m, "username": m, "email": m + "@kc.test", "firstName": m, "lastName": "Synced", "enabled": true})
		}
		writeJSON(w, r, out)
	})
	return mux
}

// setupGroupRouter is setupRouter plus a Keycloak admin client pointing at
// the fake realm. It also returns the pool and client for worker checks.
func setupGroupRouter(t *testing.T, realm *fakeRealm) (nethttp.Handler, *pgxpool.Pool, *keycloakadmin.Client) {
	t.Helper()
	return setupGroupRouterWith(t, realm, nil)
}

// setupGroupRouterWith is setupGroupRouter with a config tweak (nil = none).
// Trainers may link every group unless the tweak says otherwise.
func setupGroupRouterWith(t *testing.T, realm *fakeRealm, tweak func(*config.Config)) (nethttp.Handler, *pgxpool.Pool, *keycloakadmin.Client) {
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
	if err := os.WriteFile(accessPath, []byte(accessConfigTOML), 0o600); err != nil {
		t.Fatalf("write access config: %v", err)
	}
	access, err := auth.LoadAccessConfig(accessPath)
	if err != nil {
		t.Fatalf("load access config: %v", err)
	}

	srv := httptest.NewServer(realm.handler())
	t.Cleanup(srv.Close)
	kc, err := keycloakadmin.New(keycloakadmin.Config{BaseURL: srv.URL, Realm: "conservices", ClientID: "learn-provisioner", ClientSecret: "s3cret"})
	if err != nil {
		t.Fatalf("keycloak client: %v", err)
	}

	cfg := &config.Config{
		AppEnv:         "development",
		HTTPAddr:       ":0",
		DatabaseURL:    dsn,
		AuthMode:       config.AuthModeDev,
		DevDefaultUser: "dev-admin",
		OIDCIssuerURL:  "dev",
		LinkableGroups: []string{"*"},
	}
	if tweak != nil {
		tweak(cfg)
	}
	return httpx.NewRouter(httpx.Deps{
		Config:   cfg,
		Pool:     pool,
		Logger:   zerolog.Nop(),
		Access:   access,
		Keycloak: kc,
	}, "test"), pool, kc
}

// enrolmentsByUser maps user id -> enrolment for a course.
func enrolmentsByUser(t *testing.T, h nethttp.Handler, courseID string) map[string]map[string]any {
	t.Helper()
	code, out := call(t, h, trainerA, "GET", "/api/v1/courses/"+courseID+"/enrolments", nil)
	mustStatus(t, code, 200, "list enrolments")
	m := map[string]map[string]any{}
	for _, e := range arr(out, "enrolments") {
		m[str(obj(e), "user_id")] = obj(e)
	}
	return m
}

func syncNow(t *testing.T, h nethttp.Handler, courseID string) map[string]any {
	t.Helper()
	code, res := call(t, h, trainerA, "POST", "/api/v1/courses/"+courseID+"/group-links/sync", nil)
	mustStatus(t, code, 200, "sync now")
	return res
}

func TestGroupSync(t *testing.T) {
	realm := &fakeRealm{
		groups:  map[string][]string{"cohort-a": {"ann", "bob"}, "cohort-b": {"cat"}},
		failing: map[string]bool{},
	}
	h, pool, kc := setupGroupRouter(t, realm)

	ann := identity{user: "ann", groups: "cohort-a"}
	cat := identity{user: "cat", groups: "cohort-b"}
	gus := identity{user: "gus", groups: ""}
	ids := signIn(t, h, trainerA, learner, cat)
	syncedID := func(subject string) func() string {
		return func() string {
			var id string
			if err := pool.QueryRow(context.Background(), `SELECT id::text FROM users WHERE issuer = 'dev' AND subject = $1`, subject).Scan(&id); err != nil {
				t.Fatalf("user %s: %v", subject, err)
			}
			return id
		}
	}
	annID, bobID := syncedID("ann"), syncedID("bob")

	code, course := call(t, h, trainerA, "POST", "/api/v1/courses", map[string]any{"title": "Stewarding 101"})
	mustStatus(t, code, 201, "create course")
	courseID := str(course, "id")
	code, section := call(t, h, trainerA, "POST", "/api/v1/courses/"+courseID+"/sections", map[string]any{"title": "Basics"})
	mustStatus(t, code, 201, "create section")
	code, page := call(t, h, trainerA, "POST", "/api/v1/sections/"+str(section, "id")+"/activities",
		map[string]any{"title": "Radio calls", "body": "Over."})
	mustStatus(t, code, 201, "create page")
	pageID := str(page, "id")
	code, _ = call(t, h, trainerA, "POST", "/api/v1/courses/"+courseID+"/publish", nil)
	mustStatus(t, code, 200, "publish")

	// cat is enrolled by hand before her group is linked.
	code, _ = call(t, h, trainerA, "POST", "/api/v1/courses/"+courseID+"/enrolments",
		map[string]any{"user_id": ids[cat.user], "role": "learner"})
	mustStatus(t, code, 201, "manual enrolment of cat")

	t.Run("group list is staff only", func(t *testing.T) {
		code, _ := call(t, h, learner, "GET", "/api/v1/groups", nil)
		mustStatus(t, code, 403, "learner lists groups")
		code, out := call(t, h, trainerA, "GET", "/api/v1/groups", nil)
		mustStatus(t, code, 200, "trainer lists groups")
		if len(arr(out, "groups")) != 2 {
			t.Fatalf("groups = %v", out)
		}
		code, _ = call(t, h, learner, "GET", "/api/v1/courses/"+courseID+"/group-links", nil)
		mustStatus(t, code, 403, "learner lists group links")
	})

	t.Run("linking creates users and enrols members", func(t *testing.T) {
		code, out := call(t, h, trainerA, "POST", "/api/v1/courses/"+courseID+"/group-links", map[string]any{"group_name": "cohort-a"})
		mustStatus(t, code, 201, "link cohort-a")
		res := obj(out["sync"])
		if num(res, "users_created") != 2 || num(res, "enrolled") != 2 {
			t.Fatalf("first sync = %v, want 2 users created and 2 enrolled", res)
		}
		code, _ = call(t, h, trainerA, "POST", "/api/v1/courses/"+courseID+"/group-links", map[string]any{"group_name": "cohort-a"})
		mustStatus(t, code, 409, "link cohort-a twice")
		code, _ = call(t, h, trainerA, "POST", "/api/v1/courses/"+courseID+"/group-links", map[string]any{"group_name": "nope"})
		mustStatus(t, code, 422, "link unknown group")
		code, out = call(t, h, trainerA, "POST", "/api/v1/courses/"+courseID+"/group-links", map[string]any{"group_name": "cohort-b"})
		mustStatus(t, code, 201, "link cohort-b")
		if num(obj(out["sync"]), "enrolled") != 0 {
			t.Fatalf("cohort-b sync = %v: cat is already enrolled by hand", out["sync"])
		}

		var issuer, subject string
		if err := pool.QueryRow(context.Background(), `SELECT issuer, subject FROM users WHERE email = 'bob@kc.test'`).
			Scan(&issuer, &subject); err != nil {
			t.Fatalf("synced user bob: %v", err)
		}
		if issuer != "dev" || subject != "bob" {
			t.Fatalf("bob stored as (%s, %s), want (dev, bob)", issuer, subject)
		}

		e := enrolmentsByUser(t, h, courseID)
		if str(e[annID()], "source") != "group" || str(e[annID()], "status") != "active" {
			t.Fatalf("ann enrolment = %v", e[annID()])
		}
		if str(e[ids[cat.user]], "source") != "manual" {
			t.Fatalf("cat enrolment = %v, want manual", e[ids[cat.user]])
		}
	})

	t.Run("synced member logs in as the same person", func(t *testing.T) {
		code, _ := call(t, h, ann, "GET", "/api/v1/me", nil)
		mustStatus(t, code, 200, "ann signs in")
		var n int
		if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM users WHERE subject = 'ann'`).Scan(&n); err != nil || n != 1 {
			t.Fatalf("users for ann = %d (%v), want 1", n, err)
		}
		code, _ = call(t, h, ann, "GET", "/api/v1/courses/"+courseID, nil)
		mustStatus(t, code, 200, "ann reads the course")
		code, _ = call(t, h, ann, "PUT", "/api/v1/activities/"+pageID+"/completion", map[string]any{"completed": true})
		mustStatus(t, code, 200, "ann completes the page")
	})

	t.Run("leaving the group makes the enrolment inactive; manual untouched", func(t *testing.T) {
		realm.set("cohort-a", "bob")
		realm.set("cohort-b")
		res := syncNow(t, h, courseID)
		if num(res, "deactivated") != 1 {
			t.Fatalf("sync = %v, want 1 deactivated", res)
		}
		e := enrolmentsByUser(t, h, courseID)
		if str(e[annID()], "status") != "inactive" || e[annID()]["left_group_at"] == nil {
			t.Fatalf("ann enrolment = %v, want inactive with left_group_at", e[annID()])
		}
		if str(e[ids[cat.user]], "status") != "active" || str(e[ids[cat.user]], "source") != "manual" {
			t.Fatalf("cat enrolment = %v, want manual and active", e[ids[cat.user]])
		}

		annNow := identity{user: "ann", groups: ""}
		code, _ := call(t, h, annNow, "GET", "/api/v1/courses/"+courseID, nil)
		mustStatus(t, code, 403, "inactive ann reads the course")
		code, mine := call(t, h, annNow, "GET", "/api/v1/courses/mine", nil)
		mustStatus(t, code, 200, "ann lists mine")
		if len(arr(mine, "courses")) != 0 {
			t.Fatalf("inactive ann still sees %d courses", len(arr(mine, "courses")))
		}
		code, _ = call(t, h, annNow, "PUT", "/api/v1/activities/"+pageID+"/completion", map[string]any{"completed": false})
		mustStatus(t, code, 403, "inactive ann changes progress")
		code, gb := call(t, h, trainerA, "GET", "/api/v1/courses/"+courseID+"/gradebook", nil)
		mustStatus(t, code, 200, "gradebook")
		for _, row := range arr(gb, "rows") {
			if str(obj(row), "user_id") == annID() {
				t.Fatal("inactive ann is still in the gradebook")
			}
		}
	})

	t.Run("rejoining reactivates with progress kept", func(t *testing.T) {
		realm.set("cohort-a", "bob", "ann")
		res := syncNow(t, h, courseID)
		if num(res, "reactivated") != 1 {
			t.Fatalf("sync = %v, want 1 reactivated", res)
		}
		code, detail := call(t, h, ann, "GET", "/api/v1/courses/"+courseID, nil)
		mustStatus(t, code, 200, "ann reads the course again")
		if p := obj(detail["progress"]); num(p, "completed") != 1 {
			t.Fatalf("ann progress = %v, want the page still done", p)
		}
	})

	t.Run("still in another linked group: stays active", func(t *testing.T) {
		realm.set("cohort-b", "bob")
		realm.set("cohort-a", "ann")
		res := syncNow(t, h, courseID)
		if num(res, "deactivated") != 0 {
			t.Fatalf("sync = %v, want nobody deactivated", res)
		}
		if e := enrolmentsByUser(t, h, courseID); str(e[bobID()], "status") != "active" {
			t.Fatalf("bob = %v, want active", e[bobID()])
		}
	})

	t.Run("a group that can't be read deactivates nobody", func(t *testing.T) {
		realm.set("cohort-a")
		realm.fail("cohort-b", true)
		res := syncNow(t, h, courseID)
		if num(res, "deactivated") != 0 || len(arr(res, "errors")) != 1 {
			t.Fatalf("sync = %v, want an error and nobody deactivated", res)
		}
		realm.fail("cohort-b", false)
		realm.set("cohort-a", "ann")
	})

	t.Run("worker sync runs under the advisory lock", func(t *testing.T) {
		gs := learn.NewGroupSync(learn.NewService(pool), kc, "dev", zerolog.Nop())
		ran, res, err := gs.SyncAll(context.Background())
		if err != nil || !ran || res.Courses != 1 {
			t.Fatalf("SyncAll = %v %+v %v", ran, res, err)
		}
		if learn.NewGroupSync(learn.NewService(pool), nil, "dev", zerolog.Nop()) != nil {
			t.Fatal("group sync without a Keycloak client should be disabled")
		}
	})

	t.Run("login refreshes groups and applies them at once", func(t *testing.T) {
		gusIn := identity{user: gus.user, groups: "cohort-a"}
		code, mine := call(t, h, gusIn, "GET", "/api/v1/courses/mine", nil)
		mustStatus(t, code, 200, "gus lists mine in cohort-a")
		if len(arr(mine, "courses")) != 1 {
			t.Fatalf("gus in cohort-a sees %d courses, want 1", len(arr(mine, "courses")))
		}
		code, _ = call(t, h, gus, "GET", "/api/v1/courses/"+courseID, nil)
		mustStatus(t, code, 403, "gus after leaving cohort-a")
		code, _ = call(t, h, gusIn, "GET", "/api/v1/courses/"+courseID, nil)
		mustStatus(t, code, 200, "gus back in cohort-a")
	})

	t.Run("unlinking deactivates; manual re-enrol reactivates as manual", func(t *testing.T) {
		code, out := call(t, h, trainerA, "DELETE", "/api/v1/courses/"+courseID+"/group-links?group=cohort-a", nil)
		mustStatus(t, code, 200, "unlink cohort-a")
		if num(out, "deactivated") < 2 { // ann and gus
			t.Fatalf("unlink = %v, want ann and gus deactivated", out)
		}
		e := enrolmentsByUser(t, h, courseID)
		if str(e[bobID()], "status") != "active" {
			t.Fatalf("bob (cohort-b) = %v, want active", e[bobID()])
		}
		annRow := e[annID()]
		if str(annRow, "status") != "inactive" {
			t.Fatalf("ann = %v, want inactive", annRow)
		}
		code, re := call(t, h, trainerA, "POST", "/api/v1/courses/"+courseID+"/enrolments",
			map[string]any{"user_id": str(annRow, "user_id"), "role": "learner"})
		mustStatus(t, code, 201, "manual re-enrol of ann")
		if str(re, "source") != "manual" || str(re, "status") != "active" {
			t.Fatalf("re-enrolment = %v", re)
		}
		code, _ = call(t, h, trainerA, "POST", "/api/v1/courses/"+courseID+"/enrolments",
			map[string]any{"user_id": str(annRow, "user_id"), "role": "learner"})
		mustStatus(t, code, 409, "enrol an active person twice")
	})
}
