package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// corsOriginAllowed is the pure decision logic behind the CORS middleware:
// an explicit "*" entry allows any origin, otherwise the request Origin must
// exactly match one of the configured allowlist entries.
func TestCORSOriginAllowed(t *testing.T) {
	cases := []struct {
		name    string
		allowed []string
		origin  string
		wantOK  bool
	}{
		{"empty allowlist", nil, "https://lsm-allsports.info", false},
		{"exact match", []string{"https://lsm-allsports.info"}, "https://lsm-allsports.info", true},
		{"no match", []string{"https://lsm-allsports.info"}, "https://evil.example", false},
		{"wildcard allows any origin", []string{"*"}, "https://anything.example", true},
		{"multiple entries, second matches", []string{"https://a.example", "https://b.example"}, "https://b.example", true},
		{"scheme mismatch is not a match", []string{"https://lsm-allsports.info"}, "http://lsm-allsports.info", false},
		{"trailing slash mismatch is not a match", []string{"https://lsm-allsports.info"}, "https://lsm-allsports.info/", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := corsOriginAllowed(tc.allowed, tc.origin); got != tc.wantOK {
				t.Errorf("corsOriginAllowed(%v, %q) = %v, want %v", tc.allowed, tc.origin, got, tc.wantOK)
			}
		})
	}
}

// corsMiddleware must add no headers at all when CORS is off (empty
// allowlist) — same-origin/server-to-server callers behind Cloudflare see
// exactly today's behavior.
func TestCORSMiddlewareDisabledWhenNoAllowlist(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	mw := corsMiddleware(nil, next)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/matches", nil)
	req.Header.Set("Origin", "https://lsm-allsports.info")
	mw.ServeHTTP(rr, req)

	if got := rr.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("Access-Control-Allow-Origin = %q, want empty (CORS disabled)", got)
	}
	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusOK)
	}
}

// A simple GET from an allowed origin gets the allow-origin + Vary headers
// and still reaches the wrapped handler.
func TestCORSMiddlewareAllowedOriginGet(t *testing.T) {
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	mw := corsMiddleware([]string{"https://lsm-allsports.info"}, next)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/matches", nil)
	req.Header.Set("Origin", "https://lsm-allsports.info")
	mw.ServeHTTP(rr, req)

	if !called {
		t.Error("wrapped handler was not called for a plain GET")
	}
	if got := rr.Header().Get("Access-Control-Allow-Origin"); got != "https://lsm-allsports.info" {
		t.Errorf("Access-Control-Allow-Origin = %q, want %q", got, "https://lsm-allsports.info")
	}
	if got := rr.Header().Get("Vary"); got != "Origin" {
		t.Errorf("Vary = %q, want %q", got, "Origin")
	}
}

// A GET from a disallowed origin gets no CORS headers but still reaches the
// wrapped handler (CORS is enforced by the browser, not the server; the
// response body is just not readable cross-origin).
func TestCORSMiddlewareDisallowedOriginGet(t *testing.T) {
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	mw := corsMiddleware([]string{"https://lsm-allsports.info"}, next)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/matches", nil)
	req.Header.Set("Origin", "https://evil.example")
	mw.ServeHTTP(rr, req)

	if !called {
		t.Error("wrapped handler was not called")
	}
	if got := rr.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("Access-Control-Allow-Origin = %q, want empty", got)
	}
}

// An OPTIONS preflight from an allowed origin is answered directly by the
// middleware: 204, the CORS headers, and it must NOT reach the wrapped
// handler (the mux has no OPTIONS route registered).
func TestCORSMiddlewarePreflight(t *testing.T) {
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true })
	mw := corsMiddleware([]string{"https://lsm-allsports.info"}, next)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodOptions, "/v1/matches", nil)
	req.Header.Set("Origin", "https://lsm-allsports.info")
	req.Header.Set("Access-Control-Request-Method", "GET")
	mw.ServeHTTP(rr, req)

	if called {
		t.Error("preflight must be answered by the middleware, not the wrapped handler")
	}
	if rr.Code != http.StatusNoContent {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusNoContent)
	}
	if got := rr.Header().Get("Access-Control-Allow-Origin"); got != "https://lsm-allsports.info" {
		t.Errorf("Access-Control-Allow-Origin = %q, want %q", got, "https://lsm-allsports.info")
	}
	if got := rr.Header().Get("Access-Control-Allow-Methods"); got != "GET, OPTIONS" {
		t.Errorf("Access-Control-Allow-Methods = %q, want %q", got, "GET, OPTIONS")
	}
	if got := rr.Header().Get("Access-Control-Allow-Headers"); got != "Content-Type" {
		t.Errorf("Access-Control-Allow-Headers = %q, want %q", got, "Content-Type")
	}
	if got := rr.Header().Get("Access-Control-Max-Age"); got != "86400" {
		t.Errorf("Access-Control-Max-Age = %q, want %q", got, "86400")
	}
}

// An OPTIONS preflight from a disallowed origin gets no CORS headers and is
// passed through to the wrapped handler (which has no OPTIONS route, so a
// real mux would 404/405 it — the middleware itself must not fake a 204 for
// an origin it didn't approve).
func TestCORSMiddlewarePreflightDisallowedOrigin(t *testing.T) {
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusNotFound)
	})
	mw := corsMiddleware([]string{"https://lsm-allsports.info"}, next)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodOptions, "/v1/matches", nil)
	req.Header.Set("Origin", "https://evil.example")
	mw.ServeHTTP(rr, req)

	if !called {
		t.Error("disallowed-origin preflight should fall through to the wrapped handler")
	}
	if got := rr.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("Access-Control-Allow-Origin = %q, want empty", got)
	}
}
