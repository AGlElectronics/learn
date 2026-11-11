package denlink

// Pair, Sync, Unpair and preferences against a fake Den and a real
// PostgreSQL. They run only when TEST_DATABASE_URL is set and MIGRATE THE
// DATABASE FROM SCRATCH (drop and recreate the public schema): point it at
// a disposable database.

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/rs/zerolog"

	"github.com/AGlElectronics/learn/backend/internal/db"
	"github.com/AGlElectronics/learn/backend/migrations"
)

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping DB-backed tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	t.Cleanup(cancel)
	sqlDB, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if _, err := sqlDB.ExecContext(ctx, `DROP SCHEMA public CASCADE; CREATE SCHEMA public;`); err != nil {
		t.Fatal(err)
	}
	goose.SetBaseFS(migrations.FS)
	goose.SetTableName("goose_db_version")
	goose.SetLogger(goose.NopLogger())
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatal(err)
	}
	if err := goose.UpContext(ctx, sqlDB, "."); err != nil {
		t.Fatal(err)
	}
	pool, err := db.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// fakeDen redeems one pairing token and serves the bundle, a logo and
// preferences to the credential it handed out, until revoked.
type fakeDen struct {
	mu       sync.Mutex
	revoked  bool
	theme    string
	redeemed map[string]string
}

func (f *fakeDen) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	if r.Method == http.MethodPost && r.URL.Path == "/api/v1/integrations/pair" {
		if r.URL.Query().Get("token") != "pair-me" {
			w.WriteHeader(http.StatusUnprocessableEntity)
			_, _ = w.Write([]byte(`{"error":{"message":"this pairing URL was already used"}}`))
			return
		}
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.redeemed = body
		_, _ = w.Write([]byte(`{"credential":"cred-1","module_id":"mod-learn"}`))
		return
	}
	if f.revoked || r.Header.Get("Authorization") != "Bearer cred-1" {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	switch r.URL.Path {
	case "/api/v1/integrations/bundle":
		_, _ = w.Write([]byte(`{"brand":{"revision":3,"organization_name":"Con Org","theme":{"primary":"#123"},
			"assets":{"logo":"logo-1"}},"settings":{"data":{"timezone":"Europe/Amsterdam","retention":{"inactive_user_months":24}}}}`))
	case "/api/v1/integrations/branding/assets/logo-1":
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte("\x89PNG"))
	case "/api/v1/integrations/user-preferences":
		if r.Method == http.MethodPut {
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			f.theme = body["theme"]
		}
		theme := f.theme
		if theme == "" {
			theme = "system"
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"theme": theme})
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func TestPairSyncUnpair(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	den := &fakeDen{}
	srv := httptest.NewServer(den)
	defer srv.Close()
	c := New(pool, Config{Slug: "learn", Version: "1.0.0", Mode: ModeRequired}, zerolog.Nop())

	if _, err := c.Pair(ctx, "not a url"); err == nil {
		t.Fatal("a malformed pairing URL was accepted")
	}
	if _, err := c.Pair(ctx, srv.URL+"/api/v1/integrations/pair?token=used"); err == nil || !strings.Contains(err.Error(), "already used") {
		t.Fatalf("refused pairing = %v", err)
	}
	if err := c.Sync(ctx); err != ErrNotPaired {
		t.Fatalf("sync before pairing = %v", err)
	}

	st, err := c.Pair(ctx, srv.URL+"/api/v1/integrations/pair?token=pair-me")
	if err != nil {
		t.Fatal(err)
	}
	if !st.Paired || st.ModuleID != "mod-learn" || st.DenBaseURL != srv.URL || den.redeemed["module_slug"] != "learn" {
		t.Fatalf("status = %+v, redeemed %v", st, den.redeemed)
	}
	if !c.Linked() || !c.Paired() || c.OrganizationName() != "Con Org" {
		t.Fatalf("after pairing: linked %v paired %v org %q", c.Linked(), c.Paired(), c.OrganizationName())
	}
	if tz, ok := c.Setting("timezone"); !ok || tz != "Europe/Amsterdam" {
		t.Fatalf("setting = %q %v", tz, ok)
	}
	if _, ok := c.Bundle().Settings.Data["retention"].(map[string]any); !ok {
		t.Fatalf("retention not in the bundle: %v", c.Bundle().Settings.Data)
	}
	if got := c.AssetURL("/api/v1/brand", AssetLogo); got != "/api/v1/brand/logo?v=logo-1" {
		t.Fatalf("asset URL = %q", got)
	}

	p, err := c.SetPreferences(ctx, "iss", "sub", "dark")
	if err != nil || p.Theme != "dark" || !p.Synced {
		t.Fatalf("set preferences = %+v %v", p, err)
	}
	if p, err := c.Preferences(ctx, "iss", "sub"); err != nil || p.Theme != "dark" {
		t.Fatalf("preferences = %+v %v", p, err)
	}

	// A restarted module restores the pairing and the bundle from the database.
	restarted := New(pool, Config{Slug: "learn", Mode: ModeRequired}, zerolog.Nop())
	if err := restarted.Restore(ctx); err != nil || !restarted.Linked() || restarted.OrganizationName() != "Con Org" {
		t.Fatalf("restore: %v linked %v org %q", err, restarted.Linked(), restarted.OrganizationName())
	}

	den.mu.Lock()
	den.revoked = true
	den.mu.Unlock()
	if err := c.Sync(ctx); err == nil || !strings.Contains(err.Error(), "rejected") {
		t.Fatalf("sync after revoke = %v", err)
	}
	if st, _ := c.Status(ctx); !strings.Contains(st.LastError, "rejected") {
		t.Fatalf("last error = %q", st.LastError)
	}
	if !c.Paired() {
		t.Fatal("a failed sync keeps the last good bundle")
	}

	if err := c.Unpair(ctx); err != nil {
		t.Fatal(err)
	}
	if c.Linked() || c.Paired() || c.Bundle() != nil {
		t.Fatal("still paired after unpairing")
	}
	if st, err := c.Status(ctx); err != nil || st.Paired {
		t.Fatalf("status after unpair = %+v %v", st, err)
	}
	if _, err := c.SetPreferences(ctx, "iss", "sub", "light"); err != ErrNotPaired {
		t.Fatalf("preferences when standalone = %v", err)
	}
}

func TestPairUsesInternalURL(t *testing.T) {
	pool := testPool(t)
	srv := httptest.NewServer(&fakeDen{})
	defer srv.Close()
	c := New(pool, Config{Slug: "learn", InternalURL: srv.URL}, zerolog.Nop())
	st, err := c.Pair(context.Background(), "https://den.example.invalid/api/v1/integrations/pair?token=pair-me")
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(srv.URL)
	if st.DenBaseURL != "http://"+u.Host {
		t.Fatalf("den base URL = %q, want the internal address", st.DenBaseURL)
	}
}

func TestPairingHandlers(t *testing.T) {
	pool := testPool(t)
	srv := httptest.NewServer(&fakeDen{})
	defer srv.Close()
	c := New(pool, Config{Slug: "learn"}, zerolog.Nop())

	do := func(h http.HandlerFunc, method, body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h(rec, httptest.NewRequest(method, "/api/v1/den/pair", strings.NewReader(body)))
		return rec
	}
	if rec := do(c.PairHandler, http.MethodPost, `{`); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad JSON = %d", rec.Code)
	}
	if rec := do(c.SyncHandler, http.MethodPost, ``); rec.Code != http.StatusConflict {
		t.Fatalf("sync unpaired = %d", rec.Code)
	}
	if rec := do(c.PairHandler, http.MethodPost, `{"pairing_url":"`+srv.URL+`/api/v1/integrations/pair?token=pair-me"}`); rec.Code != http.StatusOK {
		t.Fatalf("pair = %d %s", rec.Code, rec.Body.String())
	}
	if rec := do(c.StatusHandler, http.MethodGet, ``); !strings.Contains(rec.Body.String(), `"paired":true`) {
		t.Fatalf("status = %s", rec.Body.String())
	}
	if rec := do(c.UnpairHandler, http.MethodDelete, ``); rec.Code != http.StatusNoContent {
		t.Fatalf("unpair = %d", rec.Code)
	}
}
