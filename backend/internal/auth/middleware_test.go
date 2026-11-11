package auth

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/AGlElectronics/learn/backend/internal/config"
)

func TestBearerFromHeader(t *testing.T) {
	for in, want := range map[string]string{
		"":                 "",
		"Bearer abc":       "abc",
		"bearer  abc ":     "abc",
		"Basic dXNlcg==":   "",
		"Bearer":           "",
		"Token abc def":    "",
		"BEARER x.y.z":     "x.y.z",
		"Bearer a b":       "a b",
		"NotBearer abc":    "",
		"Bearer\tabc":      "",
		"Bearer abc\r\n":   "abc",
		"Bearer   padded ": "padded",
	} {
		if got := bearerFromHeader(in); got != want {
			t.Errorf("bearerFromHeader(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSplitGroups(t *testing.T) {
	got := splitGroups(" learn-admin, ,/staff ,")
	if !slices.Equal(got, []string{"learn-admin", "/staff"}) {
		t.Fatalf("splitGroups = %v", got)
	}
	if len(splitGroups("")) != 0 {
		t.Fatal("empty header should give no groups")
	}
}

func TestDisplayFromDevHeader(t *testing.T) {
	for in, want := range map[string]string{"dev-admin": "Dev Admin", "": "Dev User", "ann": "Ann", "ann-lee-ng": "Ann Lee Ng"} {
		if got := displayFromDevHeader(in); got != want {
			t.Errorf("displayFromDevHeader(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDisplayNameFromClaims(t *testing.T) {
	if got := displayNameFromClaims(&OIDCClaims{Subject: "s", Name: " Ann Lee ", PreferredUsername: "ann"}); got != "Ann Lee" {
		t.Fatalf("name = %q", got)
	}
	if got := displayNameFromClaims(&OIDCClaims{Subject: "s", Email: "a@x.test"}); got != "a@x.test" {
		t.Fatalf("email fallback = %q", got)
	}
	if got := displayNameFromClaims(&OIDCClaims{Subject: "sub-1"}); got != "sub-1" {
		t.Fatalf("subject fallback = %q", got)
	}
}

// TestMiddlewareRefusesWithoutIdentity covers the OIDC paths that need no
// database: no verifier, no bearer token, an unknown auth mode.
func TestMiddlewareRefusesWithoutIdentity(t *testing.T) {
	reached := false
	next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached = true })
	for name, tc := range map[string]struct {
		cfg      *config.Config
		verifier *OIDCVerifier
		header   string
		want     int
	}{
		"oidc without a verifier": {&config.Config{AuthMode: config.AuthModeOIDC}, nil, "Bearer x", http.StatusInternalServerError},
		"oidc without a bearer":   {&config.Config{AuthMode: config.AuthModeOIDC}, &OIDCVerifier{}, "", http.StatusUnauthorized},
		"oidc with basic auth":    {&config.Config{AuthMode: config.AuthModeOIDC}, &OIDCVerifier{}, "Basic eDp5", http.StatusUnauthorized},
		"unknown mode":            {&config.Config{AuthMode: "magic"}, nil, "", http.StatusInternalServerError},
	} {
		t.Run(name, func(t *testing.T) {
			reached = false
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
			if tc.header != "" {
				req.Header.Set("Authorization", tc.header)
			}
			req.Header.Set("X-Dev-User", "dev-admin")
			Middleware(tc.cfg, NewResolver(nil, DefaultAccessConfig()), tc.verifier)(next).ServeHTTP(rec, req)
			if rec.Code != tc.want || reached {
				t.Fatalf("status %d (handler reached: %v), want %d", rec.Code, reached, tc.want)
			}
		})
	}
}

func TestUserPermissions(t *testing.T) {
	var nilUser *User
	if nilUser.HasAny("x") || nilUser.HasInOrg("x", [16]byte{}) || nilUser.EffectivePermissions() != nil {
		t.Fatal("nil user holds nothing")
	}
}
