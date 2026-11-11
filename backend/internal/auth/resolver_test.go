package auth

// Resolver tests against a real PostgreSQL. They run only when
// TEST_DATABASE_URL is set and MIGRATE THE DATABASE FROM SCRATCH (drop and
// recreate the public schema): point it at a disposable database.

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/AGlElectronics/learn/backend/internal/config"
	"github.com/AGlElectronics/learn/backend/internal/db"
	"github.com/AGlElectronics/learn/backend/internal/domain"
	"github.com/AGlElectronics/learn/backend/internal/groupmatch"
	"github.com/AGlElectronics/learn/backend/migrations"
)

const resolverAccessTOML = `
organization = "demo-org"

[[bindings]]
group = "learn-admin"
role = "organization_admin"

[[bindings]]
group = "learn-manager"
role = "training_manager"
`

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

func testResolver(t *testing.T) (*Resolver, *pgxpool.Pool) {
	t.Helper()
	pool := testPool(t)
	access, err := parseAccessConfig([]byte(resolverAccessTOML))
	if err != nil {
		t.Fatal(err)
	}
	return NewResolver(pool, access, groupmatch.Config{}), pool
}

func TestResolverOIDC(t *testing.T) {
	r, pool := testResolver(t)
	ctx := context.Background()
	const issuer = "https://idp.test/realms/conservices"

	var hooked []string
	r.SetGroupsHook(func(_ context.Context, _ uuid.UUID, groups []string) error {
		hooked = groups
		return nil
	})
	u, err := r.LoadOrProvisionOIDCUser(ctx, &OIDCClaims{Issuer: issuer, Subject: "sub-1", Email: "ann@x.test",
		PreferredUsername: "ann", Name: "Ann", Groups: []string{"/Learn-Admin"}})
	if err != nil {
		t.Fatal(err)
	}
	if !u.HasAny(domain.PermSettingsManage) || len(u.Memberships) != 1 || len(hooked) != 1 {
		t.Fatalf("user = %+v, hook saw %v", u, hooked)
	}
	again, err := r.LoadOrProvisionOIDCUser(ctx, &OIDCClaims{Issuer: issuer, Subject: "sub-1", Name: "Ann Lee"})
	if err != nil {
		t.Fatal(err)
	}
	if again.ID != u.ID || again.DisplayName != "Ann Lee" || again.Email != "" || again.HasAny(domain.PermSettingsManage) {
		t.Fatalf("second request = %+v: same row, refreshed name, roles only from this token", again)
	}
	var email, username string
	var orgs int
	if err := pool.QueryRow(ctx, `
		SELECT email, idp_username, (SELECT count(*) FROM user_organizations WHERE user_id = u.id)
		  FROM users u WHERE id = $1`, u.ID).Scan(&email, &username, &orgs); err != nil {
		t.Fatal(err)
	}
	if email != "ann@x.test" || username != "ann" || orgs != 1 {
		t.Fatalf("stored email %q, username %q, orgs %d", email, username, orgs)
	}

	if _, err := r.LoadOrProvisionOIDCUser(ctx, &OIDCClaims{Issuer: issuer}); err == nil {
		t.Fatal("claims without a subject accepted")
	}
}

// TestResolverAdoptsSyncedUser: the group sync stored a person under their
// directory id; their token's sub is different (a pairwise sub). The first
// sign-in takes over that row instead of creating a second person.
func TestResolverAdoptsSyncedUser(t *testing.T) {
	r, pool := testResolver(t)
	ctx := context.Background()
	const issuer = "https://idp.test/realms/conservices"
	var synced uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (issuer, subject, display_name, idp_user_id, idp_username)
		VALUES ($1, 'kc-123', 'Bob Synced', 'kc-123', 'bob') RETURNING id`, issuer).Scan(&synced); err != nil {
		t.Fatal(err)
	}
	u, err := r.LoadOrProvisionOIDCUser(ctx, &OIDCClaims{Issuer: issuer, Subject: "pairwise-bob", PreferredUsername: "Bob"})
	if err != nil {
		t.Fatal(err)
	}
	if u.ID != synced {
		t.Fatalf("signed in as %s, want the synced row %s", u.ID, synced)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM users WHERE issuer = $1`, issuer).Scan(&n); err != nil || n != 1 {
		t.Fatalf("users = %d (%v), want 1", n, err)
	}

	// A row that has signed in before is never taken over.
	other, err := r.LoadOrProvisionOIDCUser(ctx, &OIDCClaims{Issuer: issuer, Subject: "someone-else", PreferredUsername: "bob"})
	if err != nil {
		t.Fatal(err)
	}
	if other.ID == synced {
		t.Fatal("a second sub with the same username took over a person who already signed in")
	}
}

func TestDevMiddlewareProvisions(t *testing.T) {
	r, _ := testResolver(t)
	cfg := &config.Config{AppEnv: "development", AuthMode: config.AuthModeDev, DevDefaultUser: "dev-admin", DevDefaultGroups: []string{"learn-admin"}}
	var got *User
	h := Middleware(cfg, r, nil)(http.HandlerFunc(func(_ http.ResponseWriter, req *http.Request) {
		got, _ = FromContext(req.Context())
	}))

	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	if got == nil || got.Subject != "dev-admin" || got.Issuer != "dev" || !got.HasAny(domain.PermCourseManage) {
		t.Fatalf("default dev user = %+v", got)
	}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Dev-User", "tina")
	req.Header.Set("X-Dev-Groups", "learn-manager")
	h.ServeHTTP(httptest.NewRecorder(), req)
	if got.Subject != "tina" || got.DisplayName != "Tina" || !got.HasAny(domain.PermStaffManage) || got.HasAny(domain.PermCourseManage) {
		t.Fatalf("header dev user = %+v", got)
	}
	req = httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Dev-User", "tina")
	req.Header["X-Dev-Groups"] = []string{""}
	h.ServeHTTP(httptest.NewRecorder(), req)
	if len(got.EffectivePermissions()) != 0 {
		t.Fatalf("an empty X-Dev-Groups should mean no groups: %v", got.EffectivePermissions())
	}
}
