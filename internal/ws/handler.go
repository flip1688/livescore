package ws

import (
	"net/http"
	"time"

	"github.com/gorilla/websocket"
)

// HandlerConfig holds everything the /ws upgrade endpoint needs to gate a
// connection before it hands off to the hub.
type HandlerConfig struct {
	// AllowedOrigins restricts WebSocket upgrades by Origin header (exact
	// match). Empty = allow all origins (dev only).
	AllowedOrigins []string

	// TokenSecret verifies the "token" query param as
	// "<exp>.<nonce>.<sig>" (see verifyToken). Empty disables token
	// checking entirely — every connection is accepted regardless of
	// whether a token query param is present. This is the deliberate
	// rollout-window behavior: flip WS_TOKEN_SECRET on once the frontend
	// is issuing tokens.
	TokenSecret string

	// MaxConnsPerIP caps concurrent live connections per client IP (see
	// extractClientIP for how the IP is resolved). <= 0 disables the cap.
	MaxConnsPerIP int
}

// Handler upgrades GET /ws requests to WebSocket connections and hands them
// to the hub, after checking (in order, before any upgrade attempt):
//  1. Origin, via the gorilla upgrader's CheckOrigin (cfg.AllowedOrigins).
//  2. Access token, if cfg.TokenSecret is set (401 on missing/malformed/
//     bad-signature/expired).
//  3. Per-IP connection cap, if cfg.MaxConnsPerIP > 0 (429 over limit).
func (h *Hub) Handler(cfg HandlerConfig) http.Handler {
	allowed := make(map[string]struct{}, len(cfg.AllowedOrigins))
	for _, o := range cfg.AllowedOrigins {
		allowed[o] = struct{}{}
	}

	upgrader := websocket.Upgrader{
		ReadBufferSize:  4096,
		WriteBufferSize: 4096,
		CheckOrigin: func(r *http.Request) bool {
			if len(allowed) == 0 {
				return true
			}
			_, ok := allowed[r.Header.Get("Origin")]
			return ok
		},
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if cfg.TokenSecret != "" {
			if err := verifyToken(cfg.TokenSecret, r.URL.Query().Get("token"), time.Now()); err != nil {
				h.log.Debug("ws: token rejected", "err", err)
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
		}

		// ip is only resolved/tracked when the cap is enabled: it's the
		// empty-string sentinel newClient/disconnect use to know there is
		// nothing to release later.
		var ip string
		if cfg.MaxConnsPerIP > 0 {
			ip = extractClientIP(r)
			if !h.tryReserveIP(ip, cfg.MaxConnsPerIP) {
				http.Error(w, "too many connections", http.StatusTooManyRequests)
				return
			}
		}

		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			// Upgrader already wrote the error response.
			h.log.Error("ws: upgrade failed", "err", err)
			if ip != "" {
				h.releaseIP(ip)
			}
			return
		}

		client := newClient(h, conn, ip)
		select {
		case h.registerCh <- client:
		case <-h.done:
			conn.Close()
			if ip != "" {
				h.releaseIP(ip)
			}
			return
		}

		go client.writePump()
		go client.readPump()
	})
}
