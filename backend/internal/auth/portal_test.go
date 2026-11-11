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

// access marks claims as an access token (Keycloak's typ claim).
func access(extra map[string]any) map[string]any {
	out := map[string]any{"typ": "Bearer"}
	for k, v := range extra {
		out[k] = v
	}
	return out
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
