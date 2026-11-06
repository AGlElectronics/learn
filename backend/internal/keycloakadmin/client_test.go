package keycloakadmin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/AGlElectronics/learn/backend/internal/config"
)

// fakeKeycloak serves a tiny realm: /learn-trainer, /learn-admin with a
// child /learn-admin/sub, 5 trainer members (paged), user u1 in two groups.
type fakeKeycloak struct {
	tokens       atomic.Int32
	rejectFirstN atomic.Int32 // admin calls answered 401 before succeeding
}

func (f *fakeKeycloak) handler(t *testing.T) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /realms/conservices/protocol/openid-connect/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.Form.Get("grant_type") != "client_credentials" || r.Form.Get("client_id") != "learn-provisioner" || r.Form.Get("client_secret") != "s3cret" {
			http.Error(w, `{"error":"unauthorized_client"}`, http.StatusUnauthorized)
			return
		}
		n := f.tokens.Add(1)
		writeJSON(w, map[string]any{"access_token": "tok" + strconv.Itoa(int(n)), "expires_in": 300})
	})
	auth := func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if f.rejectFirstN.Load() > 0 {
				f.rejectFirstN.Add(-1)
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			if r.Header.Get("Authorization") == "" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			next(w, r)
		}
	}
	zero, one := 0, 1
	mux.HandleFunc("GET /admin/realms/conservices/groups", auth(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, page(r, []kcGroup{
			{ID: "g1", Name: "learn-trainer", Path: "/learn-trainer", SubGroupCount: &zero},
			{ID: "g2", Name: "learn-admin", Path: "/learn-admin", SubGroupCount: &one},
		}))
	}))
	mux.HandleFunc("GET /admin/realms/conservices/groups/g2/children", auth(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, page(r, []kcGroup{{ID: "g3", Name: "sub", Path: "/learn-admin/sub"}}))
	}))
	mux.HandleFunc("GET /admin/realms/conservices/groups/g3/children", auth(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, []kcGroup{})
	}))
	mux.HandleFunc("GET /admin/realms/conservices/groups/g1/members", auth(func(w http.ResponseWriter, r *http.Request) {
		var all []kcUser
		for i := 1; i <= 5; i++ {
			all = append(all, kcUser{ID: fmt.Sprintf("u%d", i), Username: fmt.Sprintf("user%d", i), Email: fmt.Sprintf("u%d@x.test", i), Enabled: i != 3})
		}
		writeJSON(w, page(r, all))
	}))
	mux.HandleFunc("GET /admin/realms/conservices/users/u1/groups", auth(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, page(r, []kcGroup{{ID: "g1", Name: "learn-trainer", Path: "/learn-trainer"}, {ID: "g3", Name: "sub", Path: "/learn-admin/sub"}}))
	}))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request %s %s", r.Method, r.URL)
		w.WriteHeader(http.StatusNotFound)
	})
	return mux
}

func page[T any](r *http.Request, all []T) []T {
	first, _ := strconv.Atoi(r.URL.Query().Get("first"))
	max, _ := strconv.Atoi(r.URL.Query().Get("max"))
	if max == 0 {
		max = 100
	}
	if first >= len(all) {
		return []T{}
	}
	end := first + max
	if end > len(all) {
		end = len(all)
	}
	return all[first:end]
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func newTestClient(t *testing.T, f *fakeKeycloak) *Client {
	t.Helper()
	srv := httptest.NewServer(f.handler(t))
	t.Cleanup(srv.Close)
	c, err := New(Config{BaseURL: srv.URL + "/", Realm: "conservices", ClientID: "learn-provisioner", ClientSecret: "s3cret", PageSize: 2})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestListGroupsFlattensAndCachesToken(t *testing.T) {
	f := &fakeKeycloak{}
	c := newTestClient(t, f)
	gs, err := c.ListGroups(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []Group{{"g1", "learn-trainer", "/learn-trainer"}, {"g2", "learn-admin", "/learn-admin"}, {"g3", "sub", "/learn-admin/sub"}}
	if fmt.Sprint(gs) != fmt.Sprint(want) {
		t.Fatalf("got %v want %v", gs, want)
	}
	if _, err := c.ListGroups(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := f.tokens.Load(); n != 1 {
		t.Fatalf("token fetched %d times, want 1", n)
	}
}

func TestGroupMembersPaging(t *testing.T) {
	c := newTestClient(t, &fakeKeycloak{})
	ms, err := c.GroupMembers(context.Background(), "learn-trainer")
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) != 5 || ms[4].ID != "u5" || ms[2].Enabled || ms[0].Email != "u1@x.test" {
		t.Fatalf("members = %+v", ms)
	}
	if _, err := c.GroupMembers(context.Background(), "/learn-trainer"); err != nil {
		t.Fatalf("by path: %v", err)
	}
	if _, err := c.GroupMembers(context.Background(), "nope"); !errors.Is(err, ErrGroupNotFound) {
		t.Fatalf("err = %v", err)
	}
}

func TestUserGroups(t *testing.T) {
	c := newTestClient(t, &fakeKeycloak{})
	gs, err := c.UserGroups(context.Background(), "u1")
	if err != nil {
		t.Fatal(err)
	}
	if len(gs) != 2 || gs[1].Path != "/learn-admin/sub" {
		t.Fatalf("groups = %+v", gs)
	}
}

func TestRetriesOnceWithFreshToken(t *testing.T) {
	f := &fakeKeycloak{}
	c := newTestClient(t, f)
	f.rejectFirstN.Store(1)
	if _, err := c.UserGroups(context.Background(), "u1"); err != nil {
		t.Fatal(err)
	}
	if n := f.tokens.Load(); n != 2 {
		t.Fatalf("token fetched %d times, want 2", n)
	}
}

func TestBadCredentials(t *testing.T) {
	srv := httptest.NewServer((&fakeKeycloak{}).handler(t))
	defer srv.Close()
	c, _ := New(Config{BaseURL: srv.URL, Realm: "conservices", ClientID: "learn-provisioner", ClientSecret: "wrong"})
	if _, err := c.ListGroups(context.Background()); err == nil {
		t.Fatal("want token error")
	}
}

func TestNewFromConfig(t *testing.T) {
	c, err := NewFromConfig(&config.Config{KeycloakBaseURL: "http://kc"})
	if c != nil || err != nil {
		t.Fatalf("partial config: %v %v", c, err)
	}
	c, err = NewFromConfig(&config.Config{KeycloakBaseURL: "http://kc", KeycloakRealm: "r", KeycloakClientID: "id", KeycloakClientSecret: "s"})
	if c == nil || err != nil {
		t.Fatalf("full config: %v %v", c, err)
	}
}
