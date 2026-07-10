package ws

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	// writeWait bounds a single write (data or ping) to the connection.
	writeWait = 10 * time.Second
	// pongWait bounds how long we wait for a pong (or any read activity)
	// before considering the connection dead; must be > pingPeriod.
	pongWait = 60 * time.Second
	// pingPeriod is how often the write pump pings an idle connection.
	pingPeriod = 54 * time.Second
	// maxMessageSize caps incoming client frames (subscribe/unsubscribe
	// requests are tiny; this just bounds abuse).
	maxMessageSize = 4096
	// sendBuffer is the per-client outbound queue depth; a client that
	// falls this far behind is dropped rather than blocking the hub.
	sendBuffer = 64
)

// clientMessage is the JSON frame a client sends to (un)subscribe. Exactly
// one of Subscribe/Unsubscribe is expected per message.
type clientMessage struct {
	Subscribe   string `json:"subscribe"`
	Unsubscribe string `json:"unsubscribe"`
}

// Client is one upgraded WebSocket connection. channels is read and
// written only by the Hub's Run goroutine (via subscribe/unsubscribe
// requests); everything else on Client may be used from the connection's
// own read/write pump goroutines.
type Client struct {
	hub  *Hub
	conn *websocket.Conn
	send chan Message

	channels map[string]struct{}

	// ip is the client's address as resolved by extractClientIP, set only
	// when the handler had a per-IP cap enabled for this connection (empty
	// otherwise). disconnect uses it to release the reservation made by
	// Hub.tryReserveIP; empty means there is nothing to release.
	ip string

	ctx    context.Context
	cancel context.CancelFunc

	closeOnce sync.Once
}

func newClient(h *Hub, conn *websocket.Conn, ip string) *Client {
	ctx, cancel := context.WithCancel(context.Background())
	return &Client{
		hub:      h,
		conn:     conn,
		send:     make(chan Message, sendBuffer),
		channels: make(map[string]struct{}),
		ip:       ip,
		ctx:      ctx,
		cancel:   cancel,
	}
}

// trySend enqueues msg for delivery without blocking. It reports whether
// the client's send buffer had room.
func (c *Client) trySend(msg Message) bool {
	select {
	case c.send <- msg:
		return true
	default:
		return false
	}
}

// disconnect unregisters the client from the hub and closes its
// connection. Safe to call multiple times (from the read pump, the write
// pump, and a slow-snapshot delivery) — only the first call does anything.
func (c *Client) disconnect() {
	c.closeOnce.Do(func() {
		c.cancel()
		select {
		case c.hub.unregisterCh <- c:
		case <-c.hub.done:
		}
		c.conn.Close()
		if c.ip != "" {
			c.hub.releaseIP(c.ip)
		}
	})
}

// readPump reads (un)subscribe requests from the connection until it
// errors or closes, then unregisters the client. Must run in its own
// goroutine; there is at most one reader per connection (gorilla
// requirement).
func (c *Client) readPump() {
	defer c.disconnect()

	c.conn.SetReadLimit(maxMessageSize)
	c.conn.SetReadDeadline(time.Now().Add(pongWait))
	c.conn.SetPongHandler(func(string) error {
		c.conn.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})

	for {
		_, data, err := c.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				c.hub.log.Debug("ws: read error", "err", err)
			}
			return
		}

		var msg clientMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			c.hub.log.Debug("ws: malformed client message", "err", err)
			continue
		}

		switch {
		case msg.Subscribe != "":
			c.subscribe(msg.Subscribe)
		case msg.Unsubscribe != "":
			c.unsubscribe(msg.Unsubscribe)
		default:
			c.hub.log.Debug("ws: client message missing subscribe/unsubscribe")
		}
	}
}

// subscribe registers interest in channel with the hub, then — if the hub
// has a SnapshotFunc — fetches and delivers the current snapshot to this
// client only, before any subsequent live deltas. A snapshot fetch error
// is logged and otherwise ignored: the connection stays open and simply
// starts from the next delta.
func (c *Client) subscribe(channel string) {
	done := make(chan struct{})
	select {
	case c.hub.subscribeCh <- subMsg{client: c, channel: channel, done: done}:
	case <-c.hub.done:
		return
	}
	select {
	case <-done:
	case <-c.hub.done:
		return
	}

	if c.hub.snapshotFn == nil {
		return
	}
	msgs, err := c.hub.snapshotFn(c.ctx, channel)
	if err != nil {
		c.hub.log.Error("ws: snapshot fetch failed", "channel", channel, "err", err)
		return
	}
	for _, m := range msgs {
		if !c.trySend(m) {
			c.hub.log.Warn("ws: client too slow for snapshot delivery, disconnecting", "channel", channel)
			c.disconnect()
			return
		}
	}
}

// unsubscribe removes interest in channel. A no-op if the client wasn't
// subscribed.
func (c *Client) unsubscribe(channel string) {
	done := make(chan struct{})
	select {
	case c.hub.unsubscribeCh <- subMsg{client: c, channel: channel, done: done}:
	case <-c.hub.done:
		return
	}
	select {
	case <-done:
	case <-c.hub.done:
	}
}

// writePump delivers queued messages and periodic pings to the connection
// until send is closed or a write fails. Must run in its own goroutine;
// there is at most one writer per connection (gorilla requirement).
func (c *Client) writePump() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		c.conn.Close()
	}()

	for {
		select {
		case msg, ok := <-c.send:
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			b, err := json.Marshal(msg)
			if err != nil {
				c.hub.log.Error("ws: marshal outgoing message", "err", err)
				continue
			}
			if err := c.conn.WriteMessage(websocket.TextMessage, b); err != nil {
				return
			}

		case <-ticker.C:
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}
