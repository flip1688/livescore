package ws

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http/httptest"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// testSnapshot returns one "snapshot" message per subscribed channel, so
// tests can use its arrival as a deterministic signal that a subscribe
// request has been fully processed (registered with the hub, then
// delivered) before doing anything that depends on that.
func testSnapshot(_ context.Context, channel string) ([]Message, error) {
	data, _ := json.Marshal(map[string]string{"seq": "snapshot"})
	return []Message{{Channel: channel, Type: "snapshot", Data: data}}, nil
}

// newTestHub starts a Hub and an httptest server serving it at /ws, both
// torn down on test cleanup. Returns the ws:// URL.
func newTestHub(t *testing.T, snap SnapshotFunc) (string, *Hub) {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := New(log, snap)

	ctx, cancel := context.WithCancel(context.Background())
	go h.Run(ctx)

	srv := httptest.NewServer(h.Handler(HandlerConfig{}))
	t.Cleanup(func() {
		srv.Close()
		cancel()
	})

	return "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws", h
}

func dial(t *testing.T, url string) *websocket.Conn {
	t.Helper()
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

// dialTinyRecvBuf dials with a tiny OS-level receive buffer so that, on
// loopback, a peer that keeps writing without this connection ever being
// read from hits real TCP backpressure after a small amount of unread
// data — instead of relying on default kernel buffers (often hundreds of
// KB to a few MB), which a short burst of small messages would never fill.
func dialTinyRecvBuf(t *testing.T, url string) *websocket.Conn {
	t.Helper()
	dialer := websocket.Dialer{
		NetDialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			d := net.Dialer{
				Control: func(_, _ string, c syscall.RawConn) error {
					var sockErr error
					if err := c.Control(func(fd uintptr) {
						sockErr = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_RCVBUF, 1024)
					}); err != nil {
						return err
					}
					return sockErr
				},
			}
			return d.DialContext(ctx, network, addr)
		},
	}
	conn, _, err := dialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

func subscribeAndAwaitSnapshot(t *testing.T, conn *websocket.Conn, channel string) Message {
	t.Helper()
	if err := conn.WriteJSON(map[string]string{"subscribe": channel}); err != nil {
		t.Fatalf("write subscribe: %v", err)
	}
	return readMessage(t, conn)
}

func unsubscribe(t *testing.T, conn *websocket.Conn, channel string) {
	t.Helper()
	if err := conn.WriteJSON(map[string]string{"unsubscribe": channel}); err != nil {
		t.Fatalf("write unsubscribe: %v", err)
	}
}

func readMessage(t *testing.T, conn *websocket.Conn) Message {
	t.Helper()
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	var m Message
	if err := conn.ReadJSON(&m); err != nil {
		t.Fatalf("read message: %v", err)
	}
	return m
}

// TestPublishDeliversToSubscriber covers: subscribe then Publish -> the
// subscriber receives the message with the right channel/type/data.
func TestPublishDeliversToSubscriber(t *testing.T) {
	url, h := newTestHub(t, testSnapshot)
	conn := dial(t, url)

	// The snapshot ack is also proof the subscribe has been fully
	// processed by the hub, so the Publish below is guaranteed to see
	// this client as a subscriber.
	subscribeAndAwaitSnapshot(t, conn, "live")

	h.Publish("live", "score", map[string]int{"home": 1, "away": 0})

	msg := readMessage(t, conn)
	if msg.Channel != "live" || msg.Type != "score" {
		t.Fatalf("got channel=%q type=%q, want channel=live type=score", msg.Channel, msg.Type)
	}
	var data map[string]int
	if err := json.Unmarshal(msg.Data, &data); err != nil {
		t.Fatalf("unmarshal data: %v", err)
	}
	if data["home"] != 1 || data["away"] != 0 {
		t.Fatalf("got data=%v, want home=1 away=0", data)
	}
}

// TestChannelIsolation covers: a client subscribed to match:1 does not
// receive publishes to match:2.
func TestChannelIsolation(t *testing.T) {
	url, h := newTestHub(t, testSnapshot)

	connA := dial(t, url) // subscribes match:1
	connB := dial(t, url) // subscribes match:2

	subscribeAndAwaitSnapshot(t, connA, MatchChannel("1"))
	subscribeAndAwaitSnapshot(t, connB, MatchChannel("2"))

	h.Publish(MatchChannel("1"), "score", map[string]int{"seq": 1})
	// Publish to B's channel too; whatever B reads next must be this one,
	// proving the match:1 publish never arrived on B's connection.
	h.Publish(MatchChannel("2"), "score", map[string]int{"seq": 2})

	gotA := readMessage(t, connA)
	if gotA.Channel != MatchChannel("1") {
		t.Fatalf("A got channel=%q, want %q", gotA.Channel, MatchChannel("1"))
	}

	gotB := readMessage(t, connB)
	if gotB.Channel != MatchChannel("2") {
		t.Fatalf("B got channel=%q, want %q (match:1 leaked into match:2 subscriber)", gotB.Channel, MatchChannel("2"))
	}
}

// TestSnapshotBeforeDelta covers: SnapshotFunc fires on subscribe and its
// message arrives before a subsequent Publish to the same channel.
func TestSnapshotBeforeDelta(t *testing.T) {
	url, h := newTestHub(t, testSnapshot)
	conn := dial(t, url)

	snap := subscribeAndAwaitSnapshot(t, conn, "live")
	if snap.Type != "snapshot" {
		t.Fatalf("got type=%q, want snapshot", snap.Type)
	}

	h.Publish("live", "score", map[string]int{"seq": 1})

	delta := readMessage(t, conn)
	if delta.Type != "score" {
		t.Fatalf("got type=%q, want score (delta arrived out of order vs snapshot)", delta.Type)
	}
}

// TestUnsubscribeStopsDelivery covers: after unsubscribing, further
// publishes to that channel are not delivered.
func TestUnsubscribeStopsDelivery(t *testing.T) {
	url, h := newTestHub(t, testSnapshot)
	conn := dial(t, url)

	subscribeAndAwaitSnapshot(t, conn, "live")

	h.Publish("live", "score", map[string]int{"seq": 1})
	got := readMessage(t, conn)
	if got.Channel != "live" {
		t.Fatalf("got channel=%q, want live", got.Channel)
	}

	unsubscribe(t, conn, "live")

	// Subscribing to a sentinel channel and waiting for its snapshot ack
	// proves the prior unsubscribe was fully processed: the client's read
	// pump handles one incoming frame at a time, and subscribe blocks on
	// the hub's ack before returning.
	subscribeAndAwaitSnapshot(t, conn, "sentinel")

	h.Publish("live", "score", map[string]int{"seq": 2})     // must NOT be delivered
	h.Publish("sentinel", "score", map[string]int{"seq": 3}) // must be delivered

	next := readMessage(t, conn)
	if next.Channel != "sentinel" {
		t.Fatalf("got channel=%q, want sentinel (unsubscribe did not stop delivery)", next.Channel)
	}
}

// TestSlowClientDropped covers: a client that doesn't drain its buffer
// gets disconnected once publishes exceed the buffer, and the hub keeps
// serving another, healthy client throughout.
func TestSlowClientDropped(t *testing.T) {
	url, h := newTestHub(t, testSnapshot)

	slow := dialTinyRecvBuf(t, url)
	healthy := dial(t, url)

	subscribeAndAwaitSnapshot(t, slow, "live")
	subscribeAndAwaitSnapshot(t, healthy, "live")

	const n = 500                        // well over the 64-message send buffer
	payload := strings.Repeat("x", 2048) // large enough to exhaust slow's tiny TCP window quickly

	// slow never reads, so once the OS-level window it advertised fills
	// (see dialTinyRecvBuf), the server's write pump for it blocks, its
	// 64-slot app buffer backs up behind that, and the hub drops it.
	// healthy is drained after every single publish, so it can never fall
	// behind far enough to overflow itself — proving the hub kept serving
	// it throughout, undisturbed by dropping slow.
	for i := 0; i < n; i++ {
		h.Publish("live", "score", map[string]any{"seq": i, "pad": payload})

		msg := readMessage(t, healthy)
		var data map[string]json.RawMessage
		if err := json.Unmarshal(msg.Data, &data); err != nil {
			t.Fatalf("healthy client unmarshal: %v", err)
		}
		var seq int
		if err := json.Unmarshal(data["seq"], &seq); err != nil {
			t.Fatalf("healthy client unmarshal seq: %v", err)
		}
		if seq != i {
			t.Fatalf("healthy client got seq=%d, want %d", seq, i)
		}
	}

	// The slow client should have been disconnected: draining whatever it
	// did manage to buffer must eventually end in a close error, not a
	// read timeout (a timeout would mean the connection is still open and
	// the hub simply never sent anything further).
	slow.SetReadDeadline(time.Now().Add(2 * time.Second))
	for {
		_, _, err := slow.ReadMessage()
		if err == nil {
			continue
		}
		var netErr net.Error
		if errors.As(err, &netErr) && netErr.Timeout() {
			t.Fatalf("slow client read timed out instead of seeing a disconnect: %v", err)
		}
		return // disconnected, as expected
	}
}
