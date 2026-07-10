package ws

import (
	"net"
	"net/http"
	"strings"
)

// extractClientIP resolves the real client IP for r. We sit behind
// Cloudflare, so CF-Connecting-IP is authoritative when present; otherwise
// fall back to the first hop of X-Forwarded-For (set by any other
// reverse proxy in front of us), and finally the direct TCP peer address.
func extractClientIP(r *http.Request) string {
	if ip := strings.TrimSpace(r.Header.Get("CF-Connecting-IP")); ip != "" {
		return ip
	}

	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		first, _, _ := strings.Cut(xff, ",")
		if ip := strings.TrimSpace(first); ip != "" {
			return ip
		}
	}

	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}
