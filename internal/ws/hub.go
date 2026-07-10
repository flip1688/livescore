// Package ws is the in-process WebSocket hub for realtime fan-out (see
// docs/architecture.md §6). A single Hub owns the client registry and
// channel subscriptions; the sync worker (or anything else in-process)
// calls Publish, and everything else — registration, subscription
// tracking, slow-client eviction — happens on the Hub's own goroutine.
package ws

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
)

// Message is the JSON envelope sent to subscribed clients.
// type is one of: score | status | card | event | snapshot (see
// docs/architecture.md §6); the hub does not interpret it.
type Message struct {
	Channel string          `json:"channel"`
	Type    string          `json:"type"`
	Data    json.RawMessage `json:"data"`
}

// SnapshotFunc returns the current state for channel when a client
// subscribes, so the client sees consistent state before any live deltas.
// A nil SnapshotFunc means no snapshot is sent on subscribe.
type SnapshotFunc func(ctx context.Context, channel string) ([]Message, error)

// subMsg is a subscribe/unsubscribe request for the Run goroutine. done is
// closed once the registry mutation is applied, so the requester can rely
// on the change being visible (e.g. before fetching a snapshot).
type subMsg struct {
	client  *Client
	channel string
	done    chan struct{}
}

// broadcastMsg is a fan-out request for the Run goroutine.
type broadcastMsg struct {
	channel string
	msg     Message
}

// Hub fans out published messages to clients subscribed to the same
// channel. All registry state (clients, subscriptions) is owned
// exclusively by the goroutine running Run — every other method only ever
// talks to it through channels.
type Hub struct {
	log        *slog.Logger
	snapshotFn SnapshotFunc

	clients map[*Client]struct{}
	subs    map[string]map[*Client]struct{}

	registerCh    chan *Client
	unregisterCh  chan *Client
	subscribeCh   chan subMsg
	unsubscribeCh chan subMsg
	broadcastCh   chan broadcastMsg

	// done is closed when Run returns, so callers on other goroutines never
	// block forever trying to hand work to a stopped hub.
	done chan struct{}

	// ipConns tracks live connection counts per client IP so the HTTP
	// handler can enforce a per-IP cap *before* upgrading (i.e. before a
	// Client even exists to go through the registerCh/unregisterCh flow
	// above). It's guarded by its own mutex rather than routed through
	// Run's channels because the handler needs a synchronous
	// check-and-increment from whichever goroutine is serving that
	// request, and many requests can be in flight concurrently.
	ipMu    sync.Mutex
	ipConns map[string]int
}

// New creates a Hub. Call Run in its own goroutine before serving any
// connections.
func New(log *slog.Logger, snapshotFn SnapshotFunc) *Hub {
	return &Hub{
		log:        log,
		snapshotFn: snapshotFn,

		clients: make(map[*Client]struct{}),
		subs:    make(map[string]map[*Client]struct{}),

		registerCh:    make(chan *Client, 32),
		unregisterCh:  make(chan *Client, 32),
		subscribeCh:   make(chan subMsg, 32),
		unsubscribeCh: make(chan subMsg, 32),
		broadcastCh:   make(chan broadcastMsg, 32),

		done: make(chan struct{}),

		ipConns: make(map[string]int),
	}
}

// tryReserveIP atomically checks and increments the live connection count
// for ip, reporting whether ip is under max. max <= 0 disables the cap
// (always reports true and does not track). Every successful reservation
// must be paired with exactly one releaseIP call, whether or not the
// connection ever completes its upgrade.
func (h *Hub) tryReserveIP(ip string, max int) bool {
	if max <= 0 {
		return true
	}
	h.ipMu.Lock()
	defer h.ipMu.Unlock()
	if h.ipConns[ip] >= max {
		return false
	}
	h.ipConns[ip]++
	return true
}

// releaseIP undoes a prior successful tryReserveIP for ip. Safe to call
// even if ip was never reserved (a no-op).
func (h *Hub) releaseIP(ip string) {
	h.ipMu.Lock()
	defer h.ipMu.Unlock()
	if h.ipConns[ip] <= 1 {
		delete(h.ipConns, ip)
		return
	}
	h.ipConns[ip]--
}

// Run owns the client registry until ctx is done, at which point it closes
// every client connection and returns. Run must be started exactly once,
// before Handler serves any connection.
func (h *Hub) Run(ctx context.Context) {
	defer close(h.done)
	for {
		select {
		case c := <-h.registerCh:
			h.clients[c] = struct{}{}

		case c := <-h.unregisterCh:
			h.removeClient(c)

		case s := <-h.subscribeCh:
			if h.subs[s.channel] == nil {
				h.subs[s.channel] = make(map[*Client]struct{})
			}
			h.subs[s.channel][s.client] = struct{}{}
			s.client.channels[s.channel] = struct{}{}
			close(s.done)

		case s := <-h.unsubscribeCh:
			delete(h.subs[s.channel], s.client)
			delete(s.client.channels, s.channel)
			close(s.done)

		case b := <-h.broadcastCh:
			for c := range h.subs[b.channel] {
				if !c.trySend(b.msg) {
					h.removeClient(c)
				}
			}

		case <-ctx.Done():
			for c := range h.clients {
				h.removeClient(c)
			}
			return
		}
	}
}

// removeClient drops c from the registry and every channel it was
// subscribed to, closes its send channel (which stops its write pump), and
// closes its connection. Safe to call more than once for the same client.
// Must only be called from the Run goroutine.
func (h *Hub) removeClient(c *Client) {
	if _, ok := h.clients[c]; !ok {
		return
	}
	delete(h.clients, c)
	for ch := range c.channels {
		delete(h.subs[ch], c)
	}
	close(c.send)
	c.conn.Close()
}

// Publish marshals data once and fans it out to every current subscriber
// of channel. It does not block on slow clients: hub-side delivery uses
// each client's buffered send channel, and a client that can't keep up is
// dropped (it can simply reconnect).
func (h *Hub) Publish(channel, msgType string, data any) {
	raw, err := json.Marshal(data)
	if err != nil {
		h.log.Error("ws: marshal publish data", "channel", channel, "type", msgType, "err", err)
		return
	}
	msg := Message{Channel: channel, Type: msgType, Data: raw}
	select {
	case h.broadcastCh <- broadcastMsg{channel: channel, msg: msg}:
	case <-h.done:
	}
}

// MatchChannel is the per-match live channel name.
func MatchChannel(matchID string) string {
	return "match:" + matchID
}

// MatchlistChannel is the per-day match list channel name.
func MatchlistChannel(date string) string {
	return "matchlist:" + date
}
