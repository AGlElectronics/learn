package http

import (
	"context"
	"crypto/subtle"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/AGlElectronics/learn/backend/internal/brand"
	"github.com/AGlElectronics/learn/backend/internal/clientip"
	"github.com/AGlElectronics/learn/backend/internal/config"
	"github.com/AGlElectronics/learn/backend/internal/denlink"
	"github.com/AGlElectronics/learn/backend/internal/httperror"
	"github.com/AGlElectronics/learn/backend/internal/learn"
)

// ----------------------------------------------------------------------------
// Public certificate verification
// ----------------------------------------------------------------------------

// rateLimiter allows `limit` requests per client IP per window. Counters
// live in memory per replica, which is enough to slow down code guessing.
type rateLimiter struct {
	limit  int
	window time.Duration

	mu      sync.Mutex
	started time.Time
	counts  map[string]int
}

func newRateLimiter(limit int, window time.Duration) *rateLimiter {
	return &rateLimiter{limit: limit, window: window, counts: map[string]int{}}
}

func (l *rateLimiter) allow(key string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if now.Sub(l.started) >= l.window {
		l.started = now
		clear(l.counts)
	}
	l.counts[key]++
	return l.counts[key] <= l.limit
}

// Allow implements Limiter.
func (l *rateLimiter) Allow(key string) bool { return l.allow(key, time.Now()) }

func (l *rateLimiter) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !l.allow(clientip.FromRequest(r), time.Now()) {
			w.Header().Set("Retry-After", "60")
			writeJSON(w, http.StatusTooManyRequests, map[string]any{
				"error": map[string]string{"code": "rate_limited", "message": "too many requests; try again in a minute"},
			})
			return
		}
		next.ServeHTTP(w, r)
	})
}

// verifyHandler serves GET /api/public/verify/{code} without auth: only
// the holder's display name, the qualification, dates and status.
func verifyHandler(svc *learn.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		v, err := svc.VerifyCertificate(r.Context(), chi.URLParam(r, "code"))
		if err == nil {
			err = svc.TranslateVerification(r.Context(), &v, requestLangs(r, svc))
		}
		if err != nil {
			httperror.Render(w, r, err)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, v)
	}
}

// ----------------------------------------------------------------------------
// Qualification catalog and holdings (for HR)
// ----------------------------------------------------------------------------

// qualificationCatalogHandler serves GET /api/qualification-catalog
// (public, read-only), the counterpart of the role catalog.
func qualificationCatalogHandler(svc *learn.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		list, err := svc.QualificationCatalog(r.Context())
		if err != nil {
			httperror.Render(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"module":         "learn",
			"display_name":   "Learn",
			"qualifications": list,
		})
	}
}

// HoldingsIssuer is the issuer whose subjects /api/internal/holdings
// answers for: the OIDC issuer, "dev" in dev auth mode, else the Keycloak
// realm the group sync uses.
func HoldingsIssuer(cfg *config.Config) string {
	switch {
	case cfg.OIDCIssuerURL != "":
		return cfg.OIDCIssuerURL
	case cfg.AuthMode == config.AuthModeDev:
		return "dev"
	default:
		return learn.GroupSyncIssuer(cfg)
	}
}

const maxHoldingsSubjects = 500

// holdingsHandler serves GET /api/internal/holdings?subjects=a,b for HR,
// authenticated by the shared LEARN_SERVICE_TOKEN bearer token. Without a
// configured token the route doesn't exist (404).
func holdingsHandler(cfg *config.Config, svc *learn.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := strings.TrimSpace(cfg.LearnServiceToken)
		if token == "" {
			httperror.Render(w, r, httperror.ErrNotFound)
			return
		}
		got, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || subtle.ConstantTimeCompare([]byte(strings.TrimSpace(got)), []byte(token)) != 1 {
			httperror.Render(w, r, httperror.ErrUnauthorized)
			return
		}
		subjects := []string{}
		seen := map[string]bool{}
		for _, s := range strings.Split(r.URL.Query().Get("subjects"), ",") {
			if s = strings.TrimSpace(s); s != "" && !seen[s] {
				seen[s] = true
				subjects = append(subjects, s)
			}
		}
		if len(subjects) > maxHoldingsSubjects {
			httperror.Render(w, r, httperror.BadRequest("at most 500 subjects per request"))
			return
		}
		out, err := svc.Holdings(r.Context(), HoldingsIssuer(cfg), subjects)
		if err != nil {
			httperror.Render(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"holdings": out})
	}
}

// ----------------------------------------------------------------------------
// Reminders
// ----------------------------------------------------------------------------

// OrgNameFunc returns the organisation name for notification texts: Den's
// when paired, else the local brand file's ("" when neither has one).
func OrgNameFunc(provider brand.Provider, den *denlink.Client) func(context.Context) string {
	return func(ctx context.Context) string {
		if den != nil {
			if name := den.OrganizationName(); name != "" {
				return name
			}
		}
		if provider != nil {
			if b, err := provider.Get(ctx); err == nil && b != nil {
				return b.OrganizationName
			}
		}
		return ""
	}
}

// runRemindersHandler serves POST /api/v1/reminders/run (settings.manage):
// one reminder run now, returning what it sent.
func runRemindersHandler(rem *learn.Reminders) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		res, err := rem.RunOnce(r.Context())
		if err != nil {
			httperror.Render(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, res)
	}
}
