package handler

import "net/http"

// corsMiddleware adds CORS headers so a browser frontend on an allowed
// origin can call this read-only API directly (it otherwise sits behind
// Cloudflare with no CORS support). No credentials are supported, so the
// allowed origin is simply echoed back rather than using
// Access-Control-Allow-Credentials.
//
// allowedOrigins empty ("*" is not in it and no explicit origins are
// configured) means CORS is off entirely — no headers are added, matching
// today's behavior for server-to-server / same-origin callers. "*" in
// allowedOrigins allows any origin.
func corsMiddleware(allowedOrigins []string, next http.Handler) http.Handler {
	if len(allowedOrigins) == 0 {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin == "" || !corsOriginAllowed(allowedOrigins, origin) {
			next.ServeHTTP(w, r)
			return
		}

		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Add("Vary", "Origin")

		if r.Method == http.MethodOptions {
			w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
			w.Header().Set("Access-Control-Max-Age", "86400")
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// corsOriginAllowed reports whether origin is allowed by the configured
// allowlist. "*" matches any origin.
func corsOriginAllowed(allowedOrigins []string, origin string) bool {
	for _, allowed := range allowedOrigins {
		if allowed == "*" || allowed == origin {
			return true
		}
	}
	return false
}
