package web

import "sync"

// SSEEvent represents a single Server-Sent Event with a named event type and
// a JSON-encoded data payload.
type SSEEvent struct {
	Event string // SSE event name
	Data  string // JSON payload
}

// SSEHub manages a set of subscriber channels and broadcasts SSEEvents to all
// of them. It is safe for concurrent use.
type SSEHub struct {
	mu      sync.RWMutex
	clients map[chan SSEEvent]struct{}
}

// NewSSEHub returns an initialised, ready-to-use SSEHub.
func NewSSEHub() *SSEHub {
	return &SSEHub{
		clients: make(map[chan SSEEvent]struct{}),
	}
}

// Subscribe creates a buffered channel (capacity 32), registers it with the
// hub, and returns it. The caller should pass the channel to Unsubscribe when
// done to release resources.
func (h *SSEHub) Subscribe() chan SSEEvent {
	ch := make(chan SSEEvent, 32)
	h.mu.Lock()
	h.clients[ch] = struct{}{}
	h.mu.Unlock()
	return ch
}

// Unsubscribe removes ch from the hub and closes it. Calling Unsubscribe on a
// channel that was not returned by Subscribe is a no-op.
func (h *SSEHub) Unsubscribe(ch chan SSEEvent) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.clients[ch]; ok {
		delete(h.clients, ch)
		close(ch)
	}
}

// Broadcast sends ev to every subscribed client. If a client's channel is
// full the event is dropped for that client rather than blocking.
func (h *SSEHub) Broadcast(ev SSEEvent) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for ch := range h.clients {
		select {
		case ch <- ev:
		default:
			// Slow client — drop the event to avoid blocking the broadcaster.
		}
	}
}
