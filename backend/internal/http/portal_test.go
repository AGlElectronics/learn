package http_test

// Portal to-dos (content, severity, order), Den-audience tokens accepted
// on that route only, notification channels, and reminder routing through
// the notifier. Same DB-backed setup as courses_test.go.

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/AGlElectronics/learn/backend/internal/auth"
	"github.com/AGlElectronics/learn/backend/internal/config"
	"github.com/AGlElectronics/learn/backend/internal/denlink"
	httpx "github.com/AGlElectronics/learn/backend/internal/http"
	"github.com/AGlElectronics/learn/backend/internal/notify"
)

func TestPortalTodos(t *testing.T) {
	h, pool := setupQualRouter(t, nil, nil)
	cred := pairPortal(t, pool)
	pia := identity{user: "pia-portal"}
	ids := signIn(t, h, admin, trainerA, pia)
	piaID := ids[pia.user]
	now := time.Now()

	course := func(title string, due time.Duration, kinds ...string) (string, []string) {
		t.Helper()
		courseID, sectionID := newCourse(t, h, title)
		acts, quizzes := []string{}, []string{}
		for i, k := range kinds {
			name := title + " " + k + " " + string(rune('A'+i))
			if k == "quiz" {
				// Empty quizzes block publishing; they turn into quizzes after.
				id := newActivity(t, h, sectionID, "page", name)
				acts, quizzes = append(acts, id), append(quizzes, id)
				continue
			}
			acts = append(acts, newActivity(t, h, sectionID, k, name))
		}
		publishAndEnrol(t, h, courseID, piaID)
		for _, id := range quizzes {
			exec(t, pool, `UPDATE activities SET kind = 'quiz' WHERE id = $1`, id)
		}
		exec(t, pool, `UPDATE enrolments SET due_at = $3 WHERE course_id = $1 AND user_id = $2`, courseID, piaID, now.Add(due))
		return courseID, acts
	}
	day := 24 * time.Hour
	soonID, _ := course("Due soon", 2*day, "page")
	lateID, _ := course("Overdue course", -day, "page")
	laterID, laterActs := course("Later course", 10*day, "page", "session", "quiz", "quiz", "quiz")
	course("Far away", 20*day, "page")
	_, doneActs := course("Already done", day, "page")
	code, _ := call(t, h, pia, "PUT", "/api/v1/activities/"+doneActs[0]+"/completion", map[string]any{"completed": true})
	mustStatus(t, code, 200, "finish the done course")

	code, slot := call(t, h, trainerA, "POST", "/api/v1/activities/"+laterActs[1]+"/session-slots",
		map[string]any{"starts_at": rfc(now.Add(3 * day)), "ends_at": rfc(now.Add(3*day + time.Hour)), "location": "Hall B", "capacity": 5})
	mustStatus(t, code, 201, "slot")
	code, _ = call(t, h, pia, "POST", "/api/v1/slots/"+str(slot, "id")+"/booking", nil)
	mustStatus(t, code, 201, "book")

	retry, spent, passed := laterActs[2], laterActs[3], laterActs[4]
	exec(t, pool, `UPDATE activities SET max_attempts = 3 WHERE id = $1`, retry)
	exec(t, pool, `UPDATE activities SET max_attempts = 1 WHERE id = $1`, spent)
	exec(t, pool, `
		INSERT INTO quiz_attempts (activity_id, user_id, cycle, submitted_at, score, max_score, passed)
		VALUES ($1, $4, 1, now(), 1, 4, false), ($2, $4, 1, now(), 1, 4, false),
		       ($3, $4, 1, now(), 1, 4, false), ($3, $4, 1, now(), 4, 4, true)`, retry, spent, passed, piaID)

	for _, q := range []struct{ key, course string }{{"q-soon", soonID}, {"q-late", lateID}} {
		code, qual := call(t, h, admin, "POST", "/api/v1/qualifications", map[string]any{"key": q.key, "name": "Qual " + q.key, "valid_months": 12})
		mustStatus(t, code, 201, "qualification "+q.key)
		setQualification(t, h, q.course, str(qual, "id"))
		expires := now.Add(9 * day)
		if q.key == "q-late" {
			expires = now.Add(-5 * day)
		}
		code2 := "DDDDDDDDDDD2"
		if q.key == "q-late" {
			code2 = "DDDDDDDDDDD3"
		}
		exec(t, pool, `
			INSERT INTO certificates (user_id, qualification_id, course_id, verify_code, expires_at)
			VALUES ($1, $2, $3, $4, $5)`, piaID, str(qual, "id"), q.course, code2, expires)
	}

	code, out := rawCall(t, h, "GET", "/api/portal/todos", portalHeaders(cred, pia.user))
	mustStatus(t, code, 200, "portal todos")
	items := arr(out, "items")
	type want struct{ prefix, severity, title string }
	wants := []want{
		{"learn:certificate:", "danger", "Renew Qual q-late"},
		{"learn:course:" + lateID, "danger", "Finish Overdue course"},
		{"learn:course:" + soonID, "warning", "Finish Due soon"},
		{"learn:session:" + str(slot, "id"), "info", "Later course session B"},
		{"learn:certificate:", "warning", "Qual q-soon expires soon"},
		{"learn:course:" + laterID, "info", "Finish Later course"},
		{"learn:quiz:" + retry, "info", "Retry Later course quiz C"},
	}
	if len(items) != len(wants) {
		raw, _ := json.MarshalIndent(items, "", "  ")
		t.Fatalf("got %d items, want %d:\n%s", len(items), len(wants), raw)
	}
	for i, w := range wants {
		it := obj(items[i])
		if !strings.HasPrefix(str(it, "id"), w.prefix) || str(it, "severity") != w.severity || str(it, "title") != w.title {
			t.Fatalf("item %d = %v, want %+v", i, it, w)
		}
		if !strings.HasPrefix(str(it, "url"), "https://learn.test/courses") {
			t.Fatalf("item %d url = %q", i, str(it, "url"))
		}
		if _, ok := it["due_at"]; !ok {
			t.Fatalf("item %d has no due_at key", i)
		}
	}
	if obj(items[6])["due_at"] != nil {
		t.Fatalf("quiz retry has no due date: %v", items[6])
	}
	if !strings.Contains(str(obj(items[3]), "detail"), "Hall B") || !strings.Contains(str(obj(items[6]), "detail"), "2 attempts left") {
		t.Fatalf("details: %v / %v", items[3], items[6])
	}

	t.Run("scoped to the caller", func(t *testing.T) {
		code, out := rawCall(t, h, "GET", "/api/portal/todos", portalHeaders(cred, trainerA.user))
		mustStatus(t, code, 200, "trainer todos")
		if len(arr(out, "items")) != 0 {
			t.Fatalf("trainer sees someone else's to-dos: %v", out)
		}
	})
}

// --- Portal auth: Den's backend, not a person's token ------------------------

const portalIssuer = "https://auth.example.test/realms/test"

// pairPortal stores a development pairing on pool and returns the credential
// Den would send. The router's den client reads the same row.
func pairPortal(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	const cred = "portal-cred"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/integrations/pair":
			_, _ = w.Write([]byte(`{"credential":"` + cred + `","module_id":"mod-learn"}`))
		case "/api/v1/integrations/bundle":
			_, _ = w.Write([]byte(`{"brand":{"revision":1,"organization_name":"Con Org"}}`))
		default:
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	c := denlink.New(pool, denlink.Config{AppEnv: "development", Slug: "learn", Version: "test"}, zerolog.Nop())
	if _, err := c.Pair(context.Background(), srv.URL+"/api/v1/integrations/pair?token=pair-me"); err != nil {
		t.Fatalf("pair: %v", err)
	}
	return cred
}

func portalHeaders(cred, subject string) map[string]string {
	return map[string]string{
		"Authorization":           "Bearer " + cred,
		denlink.HeaderUserIssuer:  "dev",
		denlink.HeaderUserSubject: subject,
	}
}

func signJWT(t *testing.T, key *rsa.PrivateKey, claims map[string]any) string {
	t.Helper()
	enc := func(v any) string {
		raw, _ := json.Marshal(v)
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

func TestPortalTodosFromDen(t *testing.T) {
	_, pool := setupQualRouter(t, nil, nil)
	cred := pairPortal(t, pool)
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("key: %v", err)
	}
	keys := &oidc.StaticKeySet{PublicKeys: []crypto.PublicKey{key.Public()}}
	accessPath := filepath.Join(t.TempDir(), "access-config.toml")
	if err := os.WriteFile(accessPath, []byte(accessConfigTOML), 0o600); err != nil {
		t.Fatalf("write access config: %v", err)
	}
	access, err := auth.LoadAccessConfig(accessPath)
	if err != nil {
		t.Fatalf("access config: %v", err)
	}
	cfg := &config.Config{
		AppEnv: "development", DatabaseURL: os.Getenv("TEST_DATABASE_URL"), AuthMode: config.AuthModeOIDC,
		OIDCIssuerURL: portalIssuer, OIDCClientID: "learn-spa",
		CORSAllowedOrigins: []string{"https://learn.test"}, PublicBaseURL: "https://learn.test",
	}
	h := httpx.NewRouter(httpx.Deps{
		Config: cfg, Pool: pool, Logger: zerolog.Nop(), Access: access,
		OIDCVerifier: auth.NewOIDCVerifierWithKeySet(portalIssuer, cfg.Audience(), keys, cfg.GroupMatch),
	}, "test")

	// typed signs a token of the given type (Keycloak's typ claim: Bearer
	// for access tokens, ID for ID tokens). Every route, including portal
	// to-dos, refuses anything but a learn-api access token — and the
	// portal route refuses that too. Den sends the pairing credential.
	typed := func(typ, aud string, groups []string) map[string]string {
		c := map[string]any{
			"iss": portalIssuer, "sub": "kc-user-1", "aud": aud, "name": "Olive Oidc", "typ": typ,
			"iat": time.Now().Unix(), "exp": time.Now().Add(time.Hour).Unix(),
		}
		if groups != nil {
			c["groups"] = groups
		}
		return map[string]string{"Authorization": "Bearer " + signJWT(t, key, c)}
	}
	accessToken := typed("Bearer", "learn-api", []string{"team-red"})

	code, _ := rawCall(t, h, "GET", "/api/v1/me", typed("ID", "learn-api", []string{"team-red"}))
	mustStatus(t, code, 401, "ID token for learn-api on /api/v1/me")
	code, _ = rawCall(t, h, "GET", "/api/v1/me", typed("Bearer", "learn-spa", []string{"team-red"}))
	mustStatus(t, code, 401, "access token for the client id instead of learn-api")
	code, _ = rawCall(t, h, "GET", "/api/v1/me", typed("Bearer", "den-spa", nil))
	mustStatus(t, code, 401, "den-spa token on /api/v1/me")

	code, _ = rawCall(t, h, "GET", "/api/v1/me", accessToken)
	mustStatus(t, code, 200, "learn-api access token on /api/v1/me")

	code, _ = rawCall(t, h, "GET", "/api/portal/todos", accessToken)
	mustStatus(t, code, 401, "a person's access token is not a pairing credential")
	code, _ = rawCall(t, h, "GET", "/api/portal/todos", map[string]string{"Authorization": "Bearer wrong"})
	mustStatus(t, code, 401, "wrong credential")
	code, _ = rawCall(t, h, "GET", "/api/portal/todos", nil)
	mustStatus(t, code, 401, "no credential")
	code, out := rawCall(t, h, "GET", "/api/portal/todos", map[string]string{
		"Authorization": "Bearer " + cred,
	})
	mustStatus(t, code, 400, "missing identity headers")
	if str(obj(out["error"]), "message") != "issuer and subject are required" {
		t.Fatalf("missing headers: %v", out)
	}

	code, out = rawCall(t, h, "GET", "/api/portal/todos", map[string]string{
		"Authorization":           "Bearer " + cred,
		denlink.HeaderUserIssuer:  portalIssuer,
		denlink.HeaderUserSubject: "kc-user-1",
	})
	mustStatus(t, code, 200, "den call for the signed-in person")
	if len(arr(out, "items")) != 0 {
		t.Fatalf("person with no open work: %v", out)
	}
	var users int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM users WHERE subject = 'kc-user-1'`).Scan(&users); err != nil || users != 1 {
		t.Fatalf("portal call must not create a second user (%d, %v)", users, err)
	}

	code, out = rawCall(t, h, "GET", "/api/portal/todos", map[string]string{
		"Authorization":           "Bearer " + cred,
		denlink.HeaderUserIssuer:  portalIssuer,
		denlink.HeaderUserSubject: "never-signed-in",
	})
	mustStatus(t, code, 200, "unknown person")
	if len(arr(out, "items")) != 0 {
		t.Fatalf("unknown person has to-dos: %v", out)
	}
}

// --- Notification channels ---------------------------------------------------

// fakeTelegram is a "telegram" channel that records what it was given.
type fakeTelegram struct {
	mu   sync.Mutex
	sent map[string][]string // address -> notice kinds
}

func (f *fakeTelegram) Name() string { return notify.ChannelTelegram }

func (f *fakeTelegram) Send(_ context.Context, r notify.Recipient, n notify.Notice) error {
	addr := r.Channels[notify.ChannelTelegram]
	if addr == "" {
		return notify.ErrNoAddress
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent[addr] = append(f.sent[addr], n.Kind)
	return nil
}

func TestChannelsAndReminderRouting(t *testing.T) {
	tg := &fakeTelegram{sent: map[string][]string{}}
	mailer := &fakeMailer{}
	h, pool := setupQualRouter(t, nil, notify.New(zerolog.Nop(), tg, notify.NewEmail(mailer)))
	tess := identity{user: "tess-telegram"}
	mo := identity{user: "mo-mailonly"}
	ids := signIn(t, h, admin, trainerA, tess, mo)

	t.Run("me/channels", func(t *testing.T) {
		code, ch := call(t, h, tess, "GET", "/api/v1/me/channels", nil)
		mustStatus(t, code, 200, "get channels")
		if ch["telegram_chat_id"] != "" || ch["telegram_enabled"] != true || ch["email_enabled"] != true ||
			str(ch, "email") != "tess-telegram@learn.local" {
			t.Fatalf("channels: %v", ch)
		}
		code, _ = call(t, h, tess, "PUT", "/api/v1/me/channels", map[string]any{"telegram_chat_id": "@tess"})
		mustStatus(t, code, 422, "invalid chat id")
		code, ch = call(t, h, tess, "PUT", "/api/v1/me/channels", map[string]any{"telegram_chat_id": " 123456789 "})
		mustStatus(t, code, 200, "save chat id")
		if ch["telegram_chat_id"] != "123456789" {
			t.Fatalf("saved: %v", ch)
		}
		code, _ = call(t, h, mo, "PUT", "/api/v1/me/channels", map[string]any{"telegram_chat_id": "42"})
		mustStatus(t, code, 200, "mo saves")
		code, ch = call(t, h, mo, "PUT", "/api/v1/me/channels", map[string]any{"telegram_chat_id": ""})
		mustStatus(t, code, 200, "mo clears")
		if ch["telegram_chat_id"] != "" {
			t.Fatalf("cleared: %v", ch)
		}
	})

	t.Run("reminders go through every channel with the stored address", func(t *testing.T) {
		courseID, sectionID := newCourse(t, h, "Routing course")
		newActivity(t, h, sectionID, "page", "Read me")
		publishAndEnrol(t, h, courseID, ids[tess.user], ids[mo.user])
		exec(t, pool, `UPDATE enrolments SET due_at = now() - interval '1 hour' WHERE course_id = $1`, courseID)
		code, res := call(t, h, admin, "POST", "/api/v1/reminders/run", nil)
		mustStatus(t, code, 200, "run reminders")
		if num(obj(res["sent"]), "overdue") != 2 {
			t.Fatalf("run: %v", res)
		}
		tg.mu.Lock()
		got := tg.sent
		tg.mu.Unlock()
		if len(got) != 1 || len(got["123456789"]) != 1 || got["123456789"][0] != "overdue" {
			t.Fatalf("telegram deliveries: %v", got)
		}
		if len(mailer.messages()) != 2 {
			t.Fatalf("emails: %d", len(mailer.messages()))
		}
	})
}
