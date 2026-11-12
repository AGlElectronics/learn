package denlink_test

// Learn's own test of the shared denlink copy: Pair, Sync, Unpair,
// preferences and the revoked state against a fake Den and Learn's real
// migrations (so a missing den_link column fails here). They run only when
// TEST_DATABASE_URL is set. The package migrates that database once and
// each test empties the tables: point it at a disposable database.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/AGlElectronics/learn/backend/internal/denlink"
	"github.com/AGlElectronics/learn/backend/internal/testdb"
)

func learnPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	return testdb.Open(t)
}

// learnDen redeems one pairing token and serves the bundle, a logo and
// preferences to the credential it handed out, until revoked.
type learnDen struct {
	mu       sync.Mutex
	revoked  bool
	theme    string
	redeemed map[string]string
}

func (f *learnDen) ServeHTTP(w http.ResponseWriter, r *http.Request) {
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
			"assets":{"logo":"logo-1"}},"settings":{"data":{"timezone":"Europe/Amsterdam"}},
			"module_settings":{"data":{"retention":{"inactive_users_days":365},"learn":{"audit_log_days":90}}}}`))
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
	pool := learnPool(t)
	ctx := context.Background()
	den := &learnDen{}
	srv := httptest.NewServer(den)
	defer srv.Close()
	c := denlink.New(pool, denlink.Config{AppEnv: "development", Slug: "learn", Version: "1.0.0", Mode: denlink.ModeRequired}, zerolog.Nop())

	if _, err := c.Pair(ctx, "not a url"); err == nil {
		t.Fatal("a malformed pairing URL was accepted")
	}
	if _, err := c.Pair(ctx, srv.URL+"/api/v1/integrations/pair?token=used"); err == nil || !strings.Contains(err.Error(), "already used") {
		t.Fatalf("refused pairing = %v", err)
	}
	if err := c.Sync(ctx); err != denlink.ErrNotPaired {
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
	if d := c.ModuleSettings().Retention().InactiveUsersDays; d == nil || *d != 365 {
		t.Fatalf("retention.inactive_users_days = %v", d)
	}
	if d, ok := c.ModuleSettings().Days("learn.audit_log_days"); !ok || d != 90 {
		t.Fatalf("learn.audit_log_days = %d %v", d, ok)
	}
	if got := c.AssetURL("/api/v1/brand", denlink.AssetLogo); got != "/api/v1/brand/logo?v=logo-1" {
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
	restarted := denlink.New(pool, denlink.Config{AppEnv: "development", Slug: "learn", Mode: denlink.ModeRequired}, zerolog.Nop())
	if err := restarted.Restore(ctx); err != nil || !restarted.Linked() || restarted.OrganizationName() != "Con Org" {
		t.Fatalf("restore: %v linked %v org %q", err, restarted.Linked(), restarted.OrganizationName())
	}

	den.mu.Lock()
	den.revoked = true
	den.mu.Unlock()
	if err := c.Sync(ctx); !errors.Is(err, denlink.ErrRevoked) {
		t.Fatalf("sync after revoke = %v", err)
	}
	if st, _ := c.Status(ctx); !st.Revoked || st.RevokedAt == nil {
		t.Fatalf("status after revoke = %+v", st)
	}
	if !c.Revoked() || !c.Paired() {
		t.Fatalf("revoked %v paired %v: a rejected sync keeps the last good bundle", c.Revoked(), c.Paired())
	}
	// Restarting keeps the revoked state (den_link.revoked_at).
	again := denlink.New(pool, denlink.Config{AppEnv: "development", Slug: "learn"}, zerolog.Nop())
	if err := again.Restore(ctx); err != nil || !again.Revoked() {
		t.Fatalf("restore after revoke: %v revoked %v", err, again.Revoked())
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
	if _, err := c.SetPreferences(ctx, "iss", "sub", "light"); err != denlink.ErrNotPaired {
		t.Fatalf("preferences when standalone = %v", err)
	}
}

func TestPairUsesInternalURL(t *testing.T) {
	pool := learnPool(t)
	srv := httptest.NewServer(&learnDen{})
	defer srv.Close()
	c := denlink.New(pool, denlink.Config{AppEnv: "development", Slug: "learn", InternalURL: srv.URL}, zerolog.Nop())
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
	pool := learnPool(t)
	srv := httptest.NewServer(&learnDen{})
	defer srv.Close()
	c := denlink.New(pool, denlink.Config{AppEnv: "development", Slug: "learn"}, zerolog.Nop())

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
