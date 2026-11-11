package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/rs/zerolog/log"

	"github.com/AGlElectronics/learn/backend/internal/config"
	"github.com/AGlElectronics/learn/backend/internal/groupmatch"
	"github.com/AGlElectronics/learn/backend/internal/httperror"
)

// PortalVerifier verifies bearer tokens on the suite-wide portal routes
// (GET /api/portal/todos). Unlike OIDCVerifier it also accepts the
// audiences in OIDC_EXTRA_AUDIENCES (Den's SPA), with the same issuer and
// signature checks. Only mount it on read-only, caller-scoped routes.
//
// A token for Learn's own audience must be an access token, as everywhere
// else. A token accepted only for an extra audience may still be an ID
// token: Den's portal sends its ID token until Den fetches to-dos server
// to server (token contract, point 3).
type PortalVerifier struct {
	verifier  *oidc.IDTokenVerifier
	issuer    string
	primary   string
	audiences []string
	groups    groupmatch.Config
}

// PortalAudiences is Learn's own audience followed by the extra ones.
func PortalAudiences(cfg *config.Config) []string {
	out := []string{}
	for _, a := range append([]string{cfg.Audience()}, cfg.OIDCExtraAudiences...) {
		if a = strings.TrimSpace(a); a != "" && !slices.Contains(out, a) {
			out = append(out, a)
		}
	}
	return out
}

// NewPortalVerifier runs OIDC discovery for the portal verifier.
func NewPortalVerifier(ctx context.Context, cfg *config.Config) (*PortalVerifier, error) {
	if cfg.OIDCIssuerURL == "" || cfg.OIDCClientID == "" {
		return nil, errors.New("portal verifier requires OIDC_ISSUER_URL and OIDC_CLIENT_ID")
	}
	provider, err := oidc.NewProvider(ctx, cfg.OIDCIssuerURL)
	if err != nil {
		return nil, fmt.Errorf("oidc discovery: %w", err)
	}
	return NewPortalVerifierWithKeySet(cfg.OIDCIssuerURL, nil, PortalAudiences(cfg), cfg.GroupMatch).
		withVerifier(provider.Verifier(&oidc.Config{SkipClientIDCheck: true})), nil
}

func (v *PortalVerifier) withVerifier(iv *oidc.IDTokenVerifier) *PortalVerifier {
	v.verifier = iv
	return v
}

// NewPortalVerifierWithKeySet builds a portal verifier without discovery
// (tests, or a fixed JWKS). audiences[0] is Learn's own audience.
func NewPortalVerifierWithKeySet(issuer string, keySet oidc.KeySet, audiences []string, gm groupmatch.Config) *PortalVerifier {
	v := &PortalVerifier{issuer: issuer, audiences: audiences, groups: gm}
	if len(audiences) > 0 {
		v.primary = audiences[0]
	}
	if keySet != nil {
		// The audience is checked against the allow-list in Verify.
		v.verifier = oidc.NewVerifier(issuer, keySet, &oidc.Config{SkipClientIDCheck: true})
	}
	return v
}

// NewOIDCVerifierWithKeySet builds the regular verifier without discovery
// (tests, or a fixed JWKS).
func NewOIDCVerifierWithKeySet(issuer, audience string, keySet oidc.KeySet, gm groupmatch.Config) *OIDCVerifier {
	return &OIDCVerifier{verifier: oidc.NewVerifier(issuer, keySet, &oidc.Config{ClientID: audience}), issuer: issuer, groups: gm}
}

// AudienceAllowed reports whether any of the token's audiences is allowed.
func AudienceAllowed(tokenAudiences, allowed []string) bool {
	for _, a := range tokenAudiences {
		if a != "" && slices.Contains(allowed, a) {
			return true
		}
	}
	return false
}

// Verify validates the token and its audience. groupsPresent is false when
// the token carries no groups claim (OIDC_GROUPS_CLAIM) at all, e.g. Den's
// ID token, so the caller's cached groups must be left alone.
func (v *PortalVerifier) Verify(ctx context.Context, bearer string) (claims *OIDCClaims, groupsPresent bool, err error) {
	bearer = strings.TrimSpace(bearer)
	if bearer == "" {
		return nil, false, errors.New("missing bearer token")
	}
	token, err := v.verifier.Verify(ctx, bearer)
	if err != nil {
		return nil, false, fmt.Errorf("verify token: %w", err)
	}
	if !AudienceAllowed(token.Audience, v.audiences) {
		return nil, false, fmt.Errorf("token audience %v not accepted", token.Audience)
	}
	c, err := parseClaims(token, v.groups)
	if err != nil {
		return nil, false, err
	}
	if v.primary != "" && slices.Contains(token.Audience, v.primary) && !isAccessToken(c) {
		return nil, false, errIDToken
	}
	var raw map[string]json.RawMessage
	if err := token.Claims(&raw); err != nil {
		return nil, false, fmt.Errorf("parse claims: %w", err)
	}
	c.Issuer = v.issuer
	return c, hasClaim(raw, v.groups.ClaimName()), nil
}

// hasClaim reports whether claims carry name, also as a dotted path into
// nested objects ("realm_access.roles").
func hasClaim(claims map[string]json.RawMessage, name string) bool {
	if _, ok := claims[name]; ok {
		return true
	}
	parts := strings.Split(name, ".")
	cur, ok := claims[parts[0]]
	for _, p := range parts[1:] {
		var obj map[string]json.RawMessage
		if !ok || json.Unmarshal(cur, &obj) != nil {
			return false
		}
		cur, ok = obj[p]
	}
	return ok && len(parts) > 1
}

// LoadOrProvisionPortalUser is LoadOrProvisionOIDCUser for portal tokens.
// Without a groups claim the user row is still upserted by issuer+subject,
// but the group cache is not touched and no group roles are granted.
func (r *Resolver) LoadOrProvisionPortalUser(ctx context.Context, claims *OIDCClaims, groupsPresent bool) (*User, error) {
	if groupsPresent {
		return r.LoadOrProvisionOIDCUser(ctx, claims)
	}
	if claims == nil || claims.Subject == "" || claims.Issuer == "" {
		return nil, errors.New("oidc claims missing issuer/subject")
	}
	display := displayNameFromClaims(claims)
	var emailPtr *string
	email := strings.TrimSpace(claims.Email)
	if email != "" {
		emailPtr = &email
	}
	username := strings.TrimSpace(claims.PreferredUsername)
	if err := r.adoptSyncedUser(ctx, claims.Issuer, claims.Subject, username); err != nil {
		return nil, fmt.Errorf("adopt synced user: %w", err)
	}
	userID, err := r.upsertUser(ctx, claims.Issuer, claims.Subject, display, emailPtr, username)
	if err != nil {
		return nil, fmt.Errorf("upsert oidc user: %w", err)
	}
	return r.buildUser(userID, claims.Issuer, claims.Subject, display, email, nil), nil
}

// PortalMiddleware authenticates the portal routes. Dev mode behaves like
// Middleware. In OIDC mode the token is checked by the portal verifier
// (Learn's audience or an extra one); without one it falls back to the
// regular verifier, i.e. Learn's audience only.
func PortalMiddleware(cfg *config.Config, resolver *Resolver, portal *PortalVerifier, fallback *OIDCVerifier) func(http.Handler) http.Handler {
	regular := Middleware(cfg, resolver, fallback)
	return func(next http.Handler) http.Handler {
		if cfg.AuthMode != config.AuthModeOIDC || portal == nil {
			return regular(next)
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()
			bearer := bearerFromHeader(r.Header.Get("Authorization"))
			if bearer == "" {
				httperror.Render(w, r, httperror.New(httperror.CodeUnauthorized, "missing bearer token"))
				return
			}
			claims, groupsPresent, err := portal.Verify(ctx, bearer)
			if err != nil {
				log.Ctx(ctx).Debug().Err(err).Msg("portal token verify failed")
				httperror.Render(w, r, httperror.New(httperror.CodeUnauthorized, "invalid token"))
				return
			}
			user, err := resolver.LoadOrProvisionPortalUser(ctx, claims, groupsPresent)
			if err != nil {
				log.Ctx(ctx).Error().Err(err).Msg("portal resolver failed")
				httperror.Render(w, r, httperror.Internal(err))
				return
			}
			next.ServeHTTP(w, r.WithContext(WithUser(ctx, user)))
		})
	}
}
