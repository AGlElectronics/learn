package http

import (
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/AGlElectronics/learn/backend/internal/auth"
	"github.com/AGlElectronics/learn/backend/internal/authbase"
	"github.com/AGlElectronics/learn/backend/internal/httperror"
)

// Limiter decides whether one more request for key is allowed now, and
// how long to wait when not. *authbase.RateLimiter implements it.
type Limiter interface {
	Allow(key string) (bool, time.Duration)
}

// LimitName names one rate-limited route group (Deps.Limiters keys).
type LimitName string

// Rate-limited routes.
const (
	// POST /courses/{course}/enrol (self-enrolment).
	LimitSelfEnrol LimitName = "self_enrol"
	// POST /slots/{slot}/checkin (QR check-in).
	LimitCheckIn LimitName = "check_in"
	// POST /courses/{course}/feedback.
	LimitFeedback LimitName = "feedback"
	// GET /users (people picker).
	LimitPeopleSearch LimitName = "people_search"
)

// defaultLimits are per signed-in person per minute (and the burst).
var defaultLimits = map[LimitName]int{
	LimitSelfEnrol:    20,
	LimitCheckIn:      20,
	LimitFeedback:     10,
	LimitPeopleSearch: 60,
}

// limitSet holds the limiter of every rate-limited route group.
type limitSet map[LimitName]Limiter

// newLimitSet uses the given limiters and the defaults for the rest.
func newLimitSet(override map[LimitName]Limiter) limitSet {
	s := limitSet{}
	for name, perMinute := range defaultLimits {
		s[name] = authbase.NewRateLimiter(authbase.RateLimitOptions{Rate: float64(perMinute) / 60, Burst: perMinute})
	}
	for name, l := range override {
		s[name] = l
	}
	return s
}

// hook rate-limits a route per signed-in person (per client IP for
// anonymous requests), answering 429 rate_limited with Retry-After.
func (s limitSet) hook(name LimitName) func(http.Handler) http.Handler {
	l := s[name]
	return func(next http.Handler) http.Handler {
		if l == nil {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := authbase.ClientIP(r)
			if u, ok := auth.FromContext(r.Context()); ok {
				key = "user:" + u.ID.String()
			}
			if ok, wait := l.Allow(string(name) + "|" + key); !ok {
				secs := max(int(math.Ceil(wait.Seconds())), 1)
				w.Header().Set("Retry-After", strconv.Itoa(secs))
				httperror.Render(w, r, httperror.New(httperror.CodeRateLimited,
					"too many requests; try again in "+strconv.Itoa(secs)+" seconds"))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
