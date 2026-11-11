package auth

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"

	"github.com/AGlElectronics/learn/backend/internal/config"
	"github.com/AGlElectronics/learn/backend/internal/groupmatch"
)

const testIssuer = "https://auth.example.test/realms/test"

func signTestToken(t *testing.T, key *rsa.PrivateKey, claims map[string]any) string {
	t.Helper()
	enc := func(v any) string {
		raw, err := json.Marshal(v)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		return base64.RawURLEncoding.EncodeToString(raw)
	}
	signing := enc(map[string]string{"alg": "RS256", "typ": "JWT"}) + "." + enc(claims)
	sum := sha256.Sum256([]byte(signing))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return signing + "." + base64.RawURLEncoding.EncodeToString(sig)
}

func testClaims(aud any, extra map[string]any) map[string]any {
	c := map[string]any{
		"iss": testIssuer, "sub": "user-1", "aud": aud,
		"iat": time.Now().Unix(), "exp": time.Now().Add(time.Hour).Unix(),
		"name": "Una User", "email": "una@example.test",
	}
	for k, v := range extra {
		c[k] = v
	}
	return c
}

func TestAudienceAllowed(t *testing.T) {
	allowed := []string{"learn-spa", "den-spa"}
	cases := []struct {
		aud  []string
		want bool
	}{
		{[]string{"learn-spa"}, true},
		{[]string{"den-spa"}, true},
		{[]string{"account", "den-spa"}, true},
		{[]string{"hr-spa"}, false},
		{[]string{""}, false},
		{nil, false},
	}
	for _, c := range cases {
		if got := AudienceAllowed(c.aud, allowed); got != c.want {
			t.Errorf("AudienceAllowed(%v) = %v, want %v", c.aud, got, c.want)
		}
	}
}

func TestPortalAudiences(t *testing.T) {
	cfg := &config.Config{OIDCClientID: "learn-spa", OIDCExtraAudiences: []string{"den-spa", " learn-api ", ""}}
	got := PortalAudiences(cfg)
	if len(got) != 2 || got[0] != "learn-api" || got[1] != "den-spa" {
		t.Fatalf("PortalAudiences = %v (default audience learn-api first)", got)
	}
	cfg.OIDCAudience = "learn-test"
	if got := PortalAudiences(cfg); got[0] != "learn-test" {
		t.Fatalf("explicit audience first: %v", got)
	}
}

// access marks claims as an access token (Keycloak's typ claim).
func access(extra map[string]any) map[string]any {
	out := map[string]any{"typ": "Bearer"}
	for k, v := range extra {
		out[k] = v
	}
	return out
}

// The extra audience is accepted only by the portal verifier; the regular
// verifier (every /api/v1 route) keeps rejecting it.
func TestPortalVerifierAcceptsExtraAudienceOnly(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("key: %v", err)
	}
	keys := &oidc.StaticKeySet{PublicKeys: []crypto.PublicKey{key.Public()}}
	regular := NewOIDCVerifierWithKeySet(testIssuer, "learn-api", keys, groupmatch.Config{})
	portal := NewPortalVerifierWithKeySet(testIssuer, keys, []string{"learn-api", "den-spa"}, groupmatch.Config{})
	ctx := context.Background()

	den := signTestToken(t, key, testClaims("den-spa", map[string]any{"typ": "ID"}))
	if _, err := regular.Verify(ctx, den); err == nil {
		t.Fatal("regular verifier accepted a den-spa token")
	}
	c, groupsPresent, err := portal.Verify(ctx, den)
	if err != nil {
		t.Fatalf("portal verifier rejected den-spa token: %v", err)
	}
	if c.Subject != "user-1" || c.Issuer != testIssuer || groupsPresent {
		t.Fatalf("claims = %+v, groupsPresent = %v", c, groupsPresent)
	}

	learn := signTestToken(t, key, testClaims([]string{"learn-api"}, access(map[string]any{"groups": []string{"team-red"}})))
	if _, err := regular.Verify(ctx, learn); err != nil {
		t.Fatalf("regular verifier rejected learn access token: %v", err)
	}
	c, groupsPresent, err = portal.Verify(ctx, learn)
	if err != nil || !groupsPresent || len(c.Groups) != 1 {
		t.Fatalf("portal learn token: %+v %v %v", c, groupsPresent, err)
	}

	for name, tok := range map[string]string{
		"other audience": signTestToken(t, key, testClaims("hr-spa", nil)),
		"other issuer":   signTestToken(t, key, testClaims("den-spa", map[string]any{"iss": "https://evil.test"})),
		"expired":        signTestToken(t, key, testClaims("den-spa", map[string]any{"exp": time.Now().Add(-time.Hour).Unix()})),
		"learn ID token": signTestToken(t, key, testClaims("learn-api", map[string]any{"typ": "ID"})),
	} {
		if _, _, err := portal.Verify(ctx, tok); err == nil {
			t.Errorf("portal verifier accepted %s", name)
		}
	}
}

// The regular verifier wants an access token for learn-api; groups come
// only from OIDC_GROUPS_CLAIM, never from roles.
func TestOIDCVerifierAccessTokensAndGroupsClaim(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("key: %v", err)
	}
	keys := &oidc.StaticKeySet{PublicKeys: []crypto.PublicKey{key.Public()}}
	ctx := context.Background()
	v := NewOIDCVerifierWithKeySet(testIssuer, "learn-api", keys, groupmatch.Config{})

	for name, extra := range map[string]map[string]any{
		"ID token":      {"typ": "ID"},
		"no typ":        {},
		"refresh token": {"typ": "Refresh"},
	} {
		if _, err := v.Verify(ctx, signTestToken(t, key, testClaims("learn-api", extra))); err == nil {
			t.Errorf("accepted %s", name)
		}
	}
	if _, err := v.Verify(ctx, signTestToken(t, key, testClaims("learn-spa", access(nil)))); err == nil {
		t.Error("accepted an access token for the client id, not learn-api")
	}

	tok := signTestToken(t, key, testClaims("learn-api", access(map[string]any{
		"groups": []string{"/learn/cohort-a"}, "roles": []string{"learn-admin"},
	})))
	c, err := v.Verify(ctx, tok)
	if err != nil || len(c.Groups) != 1 || c.Groups[0] != "/learn/cohort-a" {
		t.Fatalf("groups = %v, %v (roles must not be merged)", c, err)
	}

	custom := NewOIDCVerifierWithKeySet(testIssuer, "learn-api", keys, groupmatch.Config{Claim: "realm_access.groups"})
	tok = signTestToken(t, key, testClaims("learn-api", access(map[string]any{
		"groups": []string{"ignored"}, "realm_access": map[string]any{"groups": []string{"/staff"}},
	})))
	if c, err := custom.Verify(ctx, tok); err != nil || len(c.Groups) != 1 || c.Groups[0] != "/staff" {
		t.Fatalf("OIDC_GROUPS_CLAIM path: %v, %v", c, err)
	}
}
