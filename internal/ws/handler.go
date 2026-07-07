package ws

import (
	"net/http"

	"github.com/gorilla/websocket"
)

// Handler upgrades GET /ws requests to WebSocket connections and hands
// them to the hub. If allowedOrigins is empty, every origin is allowed
// (dev); otherwise the request's Origin header must exactly match one of
// allowedOrigins (e.g. "https://example.com").
func (h *Hub) Handler(allowedOrigins []string) http.Handler {
	allowed := make(map[string]struct{}, len(allowedOrigins))
	for _, o := range allowedOrigins {
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
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			// Upgrader already wrote the error response.
			h.log.Error("ws: upgrade failed", "err", err)
			return
		}

		client := newClient(h, conn)
		select {
		case h.registerCh <- client:
		case <-h.done:
			conn.Close()
			return
		}

		go client.writePump()
		go client.readPump()
	})
}
