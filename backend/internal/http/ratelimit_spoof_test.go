package http

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/AGlElectronics/learn/backend/internal/authbase"
)

func defaultProxies(t *testing.T) authbase.TrustedProxies {
	t.Helper()
	trusted, err := authbase.ParseTrustedProxies(authbase.DefaultTrustedProxyCIDRs)
	if err != nil {
		t.Fatal(err)
	}
	return trusted
}

// A direct client rotating X-Forwarded-For values must still share one bucket.
func TestRateLimitIgnoresForgedForwardedFor(t *testing.T) {
	limiter := authbase.NewRateLimiter(authbase.RateLimitOptions{Rate: 1, Burst: 3})
	h := defaultProxies(t).Middleware(limiter.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})))

	var limited bool
	for i := range 10 {
		req := httptest.NewRequest(http.MethodGet, "/api/public/verify/x", nil)
		req.RemoteAddr = "203.0.113.7:5555"
		req.Header.Set("X-Forwarded-For", fmt.Sprintf("198.51.100.%d", i))
		req.Header.Set("True-Client-IP", fmt.Sprintf("198.51.100.%d", i))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code == http.StatusTooManyRequests {
			if body := rec.Body.String(); !strings.Contains(body, `"rate_limited"`) {
				t.Fatalf("429 body = %s, want code rate_limited", body)
			}
			if rec.Header().Get("Retry-After") == "" {
				t.Fatal("429 without Retry-After")
			}
			limited = true
			break
		}
	}
	if !limited {
		t.Fatal("forged X-Forwarded-For values bypassed the rate limit")
	}
}

// TestTrustedProxyClientAddress pins the TRUSTED_PROXY_CIDRS rules Learn
// had before it used authbase: headers only from a trusted peer,
// X-Forwarded-For read right to left, X-Real-IP never.
func TestTrustedProxyClientAddress(t *testing.T) {
	trusted := defaultProxies(t)
	cases := []struct {
		name    string
		remote  string
		headers map[string][]string
		want    string
	}{
		{"untrusted peer forges XFF", "203.0.113.7:5555", map[string][]string{"X-Forwarded-For": {"1.2.3.4"}}, "203.0.113.7"},
		{"untrusted peer forges private XFF", "203.0.113.7:5555", map[string][]string{"X-Forwarded-For": {"10.0.0.1"}}, "203.0.113.7"},
		{"trusted peer, no header", "10.0.0.5:1234", nil, "10.0.0.5"},
		{"single proxy", "10.0.0.5:1234", map[string][]string{"X-Forwarded-For": {"198.51.100.9"}}, "198.51.100.9"},
		{"Gateway then nginx", "10.0.1.20:40000", map[string][]string{"X-Forwarded-For": {"198.51.100.9, 10.0.2.3"}}, "198.51.100.9"},
		{"client prepends fake entries", "127.0.0.1:1", map[string][]string{"X-Forwarded-For": {"8.8.8.8,9.9.9.9 , 198.51.100.9,172.18.0.4"}}, "198.51.100.9"},
		{"multiple XFF headers merged", "10.0.1.20:40000", map[string][]string{"X-Forwarded-For": {"1.2.3.4", "198.51.100.9", "10.0.2.3"}}, "198.51.100.9"},
		{"IPv6 proxy chain", "[fd00::5]:8080", map[string][]string{"X-Forwarded-For": {"2001:db8::42, fd00::1"}}, "2001:db8::42"},
		{"garbage only", "10.0.0.5:1", map[string][]string{"X-Forwarded-For": {"not-an-ip"}}, "10.0.0.5"},
		{"proxy with X-Real-IP only is not believed", "10.0.0.5:1234", map[string][]string{"X-Real-IP": {"1.2.3.4"}, "True-Client-IP": {"1.2.3.4"}}, "10.0.0.5"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got string
			h := trusted.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				got, _, _ = net.SplitHostPort(r.RemoteAddr)
			}))
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.RemoteAddr = tc.remote
			for k, vs := range tc.headers {
				for _, v := range vs {
					req.Header.Add(k, v)
				}
			}
			h.ServeHTTP(httptest.NewRecorder(), req)
			if got != tc.want {
				t.Fatalf("client = %q, want %q", got, tc.want)
			}
		})
	}
}
