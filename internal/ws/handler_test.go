package ws

import (
	"context"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// startHandlerTestServer starts a Hub + httptest server with the given
// HandlerConfig, both torn down on test cleanup. Returns the ws:// base URL
// (no query string) for /ws.
func startHandlerTestServer(t *testing.T, cfg HandlerConfig) string {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := New(log, testSnapshot)

	ctx, cancel := context.WithCancel(context.Background())
	go h.Run(ctx)

	srv := httptest.NewServer(h.Handler(cfg))
	t.Cleanup(func() {
		srv.Close()
		cancel()
	})

	return "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"
}

func TestHandlerTokenAuth(t *testing.T) {
	const secret = "handler-test-secret"
	now := time.Now()
	url := startHandlerTestServer(t, HandlerConfig{TokenSecret: secret})

	tests := []struct {
		name       string
		token      string
		omitToken  bool
		wantStatus int // 0 means "connection should succeed"
	}{
		{
			name:  "valid token connects",
			token: sign(secret, now.Add(time.Minute).Unix(), "0123456789abcdef"),
		},
		{
			name:       "missing token is rejected",
			omitToken:  true,
			wantStatus: 401,
		},
		{
			name:       "garbage token is rejected",
			token:      "garbage",
			wantStatus: 401,
		},
		{
			name:       "expired token is rejected",
			token:      sign(secret, now.Add(-time.Hour).Unix(), "0123456789abcdef"),
			wantStatus: 401,
		},
		{
			name:       "bad signature is rejected",
			token:      sign("wrong-secret", now.Add(time.Minute).Unix(), "0123456789abcdef"),
			wantStatus: 401,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dialURL := url
			if !tt.omitToken {
				dialURL += "?token=" + tt.token
			}
			conn, resp, err := websocket.DefaultDialer.Dial(dialURL, nil)
			if tt.wantStatus == 0 {
				if err != nil {
					t.Fatalf("expected connection to succeed, got err=%v", err)
				}
				conn.Close()
				return
			}
			if err == nil {
				conn.Close()
				t.Fatalf("expected connection to be rejected, but it succeeded")
			}
			if resp == nil {
				t.Fatalf("expected an HTTP response with the upgrade rejection, got none (err=%v)", err)
			}
			if resp.StatusCode != tt.wantStatus {
				t.Fatalf("got status %d, want %d", resp.StatusCode, tt.wantStatus)
			}
		})
	}
}

// TestHandlerEmptySecretAllowsAnyRequest covers the rollout-window
// passthrough: when TokenSecret is unset, connections succeed with no
// token, a garbage token, or anything else in the query string.
func TestHandlerEmptySecretAllowsAnyRequest(t *testing.T) {
	url := startHandlerTestServer(t, HandlerConfig{})

	for _, dialURL := range []string{url, url + "?token=garbage", url + "?token="} {
		conn, _, err := websocket.DefaultDialer.Dial(dialURL, nil)
		if err != nil {
			t.Fatalf("dial %q: expected success with token checking disabled, got %v", dialURL, err)
		}
		conn.Close()
	}
}

// TestHandlerPerIPCap covers: connections from the same IP beyond
// MaxConnsPerIP get 429, a different IP is unaffected, and closing a
// connection frees up a slot for that IP again.
func TestHandlerPerIPCap(t *testing.T) {
	url := startHandlerTestServer(t, HandlerConfig{MaxConnsPerIP: 2})

	dialAs := func(t *testing.T, ip string) (*websocket.Conn, int) {
		t.Helper()
		header := map[string][]string{"X-Forwarded-For": {ip}}
		conn, resp, err := websocket.DefaultDialer.Dial(url, header)
		if err != nil {
			if resp != nil {
				return nil, resp.StatusCode
			}
			t.Fatalf("dial as %s: %v", ip, err)
		}
		return conn, 0
	}

	conn1, status := dialAs(t, "203.0.113.1")
	if status != 0 {
		t.Fatalf("1st connection: got status %d, want success", status)
	}
	defer conn1.Close()

	conn2, status := dialAs(t, "203.0.113.1")
	if status != 0 {
		t.Fatalf("2nd connection (still under cap): got status %d, want success", status)
	}
	defer conn2.Close()

	_, status = dialAs(t, "203.0.113.1")
	if status != 429 {
		t.Fatalf("3rd connection (over cap): got status %d, want 429", status)
	}

	// A different IP is not affected by the first IP's cap.
	conn3, status := dialAs(t, "198.51.100.1")
	if status != 0 {
		t.Fatalf("connection from a different IP: got status %d, want success", status)
	}
	defer conn3.Close()

	// Closing one of the first IP's connections frees a slot.
	conn1.Close()
	// Give the hub goroutine a moment to process the disconnect and this
	// package's IP-release path (which runs synchronously in the client's
	// close path, but the peer-side close is still async over the network).
	deadline := time.Now().Add(2 * time.Second)
	var lastStatus int
	for time.Now().Before(deadline) {
		conn4, s := dialAs(t, "203.0.113.1")
		if s == 0 {
			conn4.Close()
			return
		}
		lastStatus = s
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("expected a slot to free up after closing a connection, last status = %d", lastStatus)
}
