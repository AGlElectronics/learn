package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/AGlElectronics/learn/backend/internal/groupmatch"
)

// Resolver turns an authenticated external identity (OIDC claims, or a dev
// bypass header) into a *User backed by Learn's users table.
//
// Identity invariants (architecture.md §4.4):
//
//   - The (issuer, subject) pair is the stable identity key. Email and
//     display name are refreshed every request; never used as join keys.
//   - A user existing with the same email as an incoming OIDC login is
//     NEVER automatically linked: a never-seen-before (issuer, subject)
//     pair always creates a new local user.
//
// Authority is never persisted: the token's IdP groups map to roles through
// the access config bindings on every request, so removing someone from a
// group revokes the role on their next request.
type Resolver struct {
	pool   *pgxpool.Pool
	access *AccessConfig
	groups groupmatch.Config
	orgIDs sync.Map // organization slug -> uuid.UUID
	// orgsSeen remembers user/organization sets already recorded by this
	// process, so a signed-in request costs no extra write.
	orgsSeen sync.Map
	// subjectsSeen remembers (issuer, subject) pairs adoptSyncedUser has
	// already handled.
	subjectsSeen sync.Map
	// groupsHook receives the caller's IdP groups on every request (see
	// SetGroupsHook); nil = off.
	groupsHook GroupsHook
}

// NewResolver constructs a Resolver backed by the given pool and access
// config catalog; groups are matched to bindings with gm.
func NewResolver(pool *pgxpool.Pool, access *AccessConfig, gm groupmatch.Config) *Resolver {
	return &Resolver{pool: pool, access: access, groups: gm}
}

// LoadOrProvisionDevUser is used by the dev bypass middleware. The user
// header becomes the OIDC subject under the reserved "dev" issuer, and the
// groups go through exactly the same access-config binding lookup as OIDC
// token groups, so dev mode exercises the real authorization path.
func (r *Resolver) LoadOrProvisionDevUser(ctx context.Context, name string, groups []string) (*User, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "dev-admin"
	}

	display := displayFromDevHeader(name)
	email := name + "@learn.local"

	userID, err := r.upsertUser(ctx, "dev", name, display, &email, "")
	if err != nil {
		return nil, fmt.Errorf("provision dev user: %w", err)
	}

	r.applyGroups(ctx, userID, groups)
	memberships, err := r.groupMemberships(ctx, groups)
	if err != nil {
		return nil, fmt.Errorf("resolve dev group bindings: %w", err)
	}
	if err := r.recordOrganizations(ctx, userID, memberships); err != nil {
		return nil, fmt.Errorf("record organizations: %w", err)
	}
	return r.buildUser(userID, "dev", name, display, email, memberships), nil
}

// LoadOrProvisionOIDCUser is called by the OIDC middleware after a
// successful JWT verification. It upserts the user row, refreshes the
// metadata (display name, email, last login), then returns the request
// principal populated from the access config catalog.
func (r *Resolver) LoadOrProvisionOIDCUser(ctx context.Context, claims *OIDCClaims) (*User, error) {
	if claims == nil || claims.Subject == "" || claims.Issuer == "" {
		return nil, errors.New("oidc claims missing issuer/subject")
	}
	display := displayNameFromClaims(claims)
	var emailPtr *string
	if e := strings.TrimSpace(claims.Email); e != "" {
		emailPtr = &e
	}
	username := strings.TrimSpace(claims.PreferredUsername)
	if err := r.adoptSyncedUser(ctx, claims.Issuer, claims.Subject, username); err != nil {
		return nil, fmt.Errorf("adopt synced user: %w", err)
	}
	userID, err := r.upsertUser(ctx, claims.Issuer, claims.Subject, display, emailPtr, username)
	if err != nil {
		return nil, fmt.Errorf("upsert oidc user: %w", err)
	}

	r.applyGroups(ctx, userID, claims.Groups)
	memberships, err := r.groupMemberships(ctx, claims.Groups)
	if err != nil {
		return nil, fmt.Errorf("resolve group bindings: %w", err)
	}
	if err := r.recordOrganizations(ctx, userID, memberships); err != nil {
		return nil, fmt.Errorf("record organizations: %w", err)
	}

	email := ""
	if emailPtr != nil {
		email = *emailPtr
	}
	return r.buildUser(userID, claims.Issuer, claims.Subject, display, email, memberships), nil
}

// adoptSyncedUser handles an IdP whose token `sub` is not the directory's
// user id. The group sync stores people it has not seen sign in under
// subject = directory id; on their first sign-in, a row of the same issuer
// that has never signed in, has a directory id and the token's
// preferred_username takes the token's subject instead of a second row
// being created. Runs once per subject per process.
func (r *Resolver) adoptSyncedUser(ctx context.Context, issuer, subject, username string) error {
	if username == "" {
		return nil
	}
	key := issuer + "\x00" + subject
	if _, done := r.subjectsSeen.Load(key); done {
		return nil
	}
	if _, err := r.pool.Exec(ctx, `
		UPDATE users SET subject = $2, updated_at = now()
		 WHERE id = (SELECT id FROM users
		              WHERE issuer = $1 AND subject <> $2 AND last_login_at IS NULL
		                AND idp_user_id IS NOT NULL AND erased_at IS NULL
		                AND lower(idp_username) = lower($3)
		              LIMIT 1)
		   AND NOT EXISTS (SELECT 1 FROM users WHERE issuer = $1 AND subject = $2)`,
		issuer, subject, username); err != nil {
		return err
	}
	r.subjectsSeen.Store(key, struct{}{})
	return nil
}

// upsertUser looks up the local user by (issuer, subject). If absent, a
// fresh users row is created. Metadata (display_name, email, IdP username,
// last_login_at) is refreshed on every call; an empty username keeps the
// stored one. The username lets the group sync match the person later.
func (r *Resolver) upsertUser(
	ctx context.Context,
	issuer, subject, display string,
	email *string,
	username string,
) (uuid.UUID, error) {
	var userID uuid.UUID
	err := r.pool.QueryRow(ctx,
		`INSERT INTO users (issuer, subject, display_name, email, idp_username, last_login_at)
		 VALUES ($1, $2, $3, $4, NULLIF($5, ''), now())
		 ON CONFLICT (issuer, subject) DO UPDATE
		    SET display_name  = EXCLUDED.display_name,
		        email         = COALESCE(EXCLUDED.email, users.email),
		        idp_username  = COALESCE(EXCLUDED.idp_username, users.idp_username),
		        last_login_at = now(),
		        updated_at    = now()
		 RETURNING id`,
		issuer, subject, display, email, username,
	).Scan(&userID)
	if err != nil {
		return uuid.Nil, err
	}
	return userID, nil
}

// buildUser assembles the request principal from the identity row and
// group-derived memberships, projecting role → permission grants from the
// access config catalog.
func (r *Resolver) buildUser(
	userID uuid.UUID,
	issuer, subject, display, email string,
	memberships []Membership,
) *User {
	global := map[string]struct{}{}
	orgPerms := map[uuid.UUID]map[string]struct{}{}

	for _, m := range memberships {
		set := r.access.PermissionsForRole(m.Role)
		if set == nil {
			continue
		}
		orgSet, ok := orgPerms[m.OrganizationID]
		if !ok {
			orgSet = map[string]struct{}{}
			orgPerms[m.OrganizationID] = orgSet
		}
		for p := range set {
			global[p] = struct{}{}
			orgSet[p] = struct{}{}
		}
	}

	return &User{
		ID:             userID,
		Issuer:         issuer,
		Subject:        subject,
		Email:          email,
		DisplayName:    display,
		Memberships:    memberships,
		Permissions:    global,
		OrgPermissions: orgPerms,
	}
}

func displayFromDevHeader(h string) string {
	h = strings.TrimSpace(h)
	if h == "" {
		return "Dev User"
	}
	// "dev-admin" → "Dev Admin". Small cosmetic nicety; the identity key
	// stays the raw header value.
	parts := strings.Split(h, "-")
	for i, p := range parts {
		if p == "" {
			continue
		}
		parts[i] = strings.ToUpper(p[:1]) + p[1:]
	}
	return strings.Join(parts, " ")
}

func displayNameFromClaims(c *OIDCClaims) string {
	if c == nil {
		return ""
	}
	for _, s := range []string{c.Nickname, c.Name, c.PreferredUsername, c.Email} {
		if t := strings.TrimSpace(s); t != "" {
			return t
		}
	}
	// Fallback to the subject — never pretty, but always non-empty.
	return c.Subject
}

// groupMemberships turns the token's IdP groups into organization-wide
// memberships via the access config bindings. Nothing is persisted, so
// removing someone from a group revokes the role on their next request.
// Bindings naming an organization slug that does not exist grant nothing.
func (r *Resolver) groupMemberships(ctx context.Context, groups []string) ([]Membership, error) {
	var out []Membership
	for _, b := range r.access.BindingsFor(r.groups, groups) {
		orgID, ok, err := r.orgIDBySlug(ctx, b.Organization)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, Membership{OrganizationID: orgID, Role: b.Role})
		}
	}
	return out, nil
}

// recordOrganizations notes that the user belongs to the access config's
// home organization and to every organization a binding gave them a role
// in. The people picker searches only these.
func (r *Resolver) recordOrganizations(ctx context.Context, userID uuid.UUID, memberships []Membership) error {
	ids := make([]uuid.UUID, 0, len(memberships)+1)
	if slug := r.access.HomeOrganization(); slug != "" {
		id, ok, err := r.orgIDBySlug(ctx, slug)
		if err != nil {
			return err
		}
		if ok {
			ids = append(ids, id)
		}
	}
	for _, m := range memberships {
		ids = append(ids, m.OrganizationID)
	}
	if len(ids) == 0 {
		return nil
	}
	key := userID.String()
	for _, id := range ids {
		key += ":" + id.String()
	}
	if _, done := r.orgsSeen.Load(key); done {
		return nil
	}
	if _, err := r.pool.Exec(ctx, `
		INSERT INTO user_organizations (user_id, organization_id)
		SELECT $1, o FROM unnest($2::uuid[]) AS o
		ON CONFLICT DO NOTHING`, userID, ids); err != nil {
		return err
	}
	r.orgsSeen.Store(key, struct{}{})
	return nil
}

func (r *Resolver) orgIDBySlug(ctx context.Context, slug string) (uuid.UUID, bool, error) {
	if id, ok := r.orgIDs.Load(slug); ok {
		return id.(uuid.UUID), true, nil
	}
	var id uuid.UUID
	err := r.pool.QueryRow(ctx, `SELECT id FROM organizations WHERE slug = $1`, slug).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, false, nil
	}
	if err != nil {
		return uuid.Nil, false, err
	}
	r.orgIDs.Store(slug, id)
	return id, true, nil
}
