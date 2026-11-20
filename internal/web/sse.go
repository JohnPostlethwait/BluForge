package web

import (
	"log/slog"
	"sync"
)

// SSEEvent represents a single Server-Sent Event with a named event type and
// a JSON-encoded data payload.
type SSEEvent struct {
	Event string // SSE event name
	Data  string // JSON payload
}

// SSEClientBuffer is how many events a client may fall behind by before the
// hub closes its stream.
const SSEClientBuffer = 32

// SSEHub manages a set of subscriber channels and broadcasts SSEEvents to all
// of them. It is safe for concurrent use.
type SSEHub struct {
	mu      sync.Mutex
	clients map[chan SSEEvent]struct{}
}

// NewSSEHub returns an initialised, ready-to-use SSEHub.
func NewSSEHub() *SSEHub {
	return &SSEHub{
		clients: make(map[chan SSEEvent]struct{}),
	}
}

// Subscribe creates a buffered channel, registers it with the hub, and returns
// it. The caller should pass the channel to Unsubscribe when done to release
// resources. The hub closes the channel itself if the client falls behind.
func (h *SSEHub) Subscribe() chan SSEEvent {
	ch := make(chan SSEEvent, SSEClientBuffer)
	h.mu.Lock()
	h.clients[ch] = struct{}{}
	h.mu.Unlock()
	return ch
}

// Unsubscribe removes ch from the hub and closes it. Calling Unsubscribe on a
// channel that is no longer subscribed is a no-op.
func (h *SSEHub) Unsubscribe(ch chan SSEEvent) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.clients[ch]; ok {
		delete(h.clients, ch)
		close(ch)
	}
}

// Broadcast sends ev to every subscribed client.
//
// A client whose buffer is full is closed rather than skipped. A skipped event
// was invisible to the page: a scan's "done" went missing and the page showed
// it running for minutes after it finished. Closing ends the stream, the
// browser reconnects, and the page resyncs from the server's own state.
func (h *SSEHub) Broadcast(ev SSEEvent) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.clients {
		select {
		case ch <- ev:
		default:
			h.drop(ch, ev.Event)
		}
	}
}

// drop closes a client that could not take an event. The caller holds h.mu.
func (h *SSEHub) drop(ch chan SSEEvent, event string) {
	delete(h.clients, ch)
	close(ch)
	slog.Warn("sse: client fell behind; closing its stream so it reconnects",
		"event", event, "buffer", SSEClientBuffer)
}
