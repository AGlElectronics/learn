package http

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/AGlElectronics/learn/backend/internal/clientip"
)

// A direct client rotating X-Forwarded-For values must still share one counter.
func TestRateLimitIgnoresForgedForwardedFor(t *testing.T) {
	trusted, err := clientip.ParseCIDRs(clientip.DefaultTrustedProxies)
	if err != nil {
		t.Fatal(err)
	}
	h := clientip.Middleware(trusted)(newRateLimiter(3, time.Minute).middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})))

	var limited bool
	for i := range 10 {
		req := httptest.NewRequest(http.MethodGet, "/api/public/verify/x", nil)
		req.RemoteAddr = "203.0.113.7:5555"
		req.Header.Set("X-Forwarded-For", fmt.Sprintf("198.51.100.%d", i))
		req.Header.Set("True-Client-IP", fmt.Sprintf("198.51.100.%d", i))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code == http.StatusTooManyRequests {
			limited = true
			break
		}
	}
	if !limited {
		t.Fatal("forged X-Forwarded-For values bypassed the rate limit")
	}
}
