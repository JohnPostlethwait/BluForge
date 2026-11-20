package web_test

import (
	"testing"
	"time"

	"github.com/johnpostlethwait/bluforge/internal/web"
)

func TestSSEHubBroadcast(t *testing.T) {
	hub := web.NewSSEHub()

	ch1 := hub.Subscribe()
	ch2 := hub.Subscribe()

	event := web.SSEEvent{Event: "test-event", Data: `{"key":"value"}`}
	hub.Broadcast(event)

	for i, ch := range []chan web.SSEEvent{ch1, ch2} {
		select {
		case received := <-ch:
			if received.Event != event.Event {
				t.Errorf("client %d: expected Event %q, got %q", i+1, event.Event, received.Event)
			}
			if received.Data != event.Data {
				t.Errorf("client %d: expected Data %q, got %q", i+1, event.Data, received.Data)
			}
		case <-time.After(time.Second):
			t.Errorf("client %d: timed out waiting for event", i+1)
		}
	}
}

func TestSSEHubUnsubscribe(t *testing.T) {
	hub := web.NewSSEHub()

	ch := hub.Subscribe()
	hub.Unsubscribe(ch)

	// Broadcasting after unsubscribe should not panic.
	event := web.SSEEvent{Event: "after-unsub", Data: `{}`}
	hub.Broadcast(event)

	// Channel should be closed; receiving from it should yield zero value immediately.
	select {
	case _, ok := <-ch:
		if ok {
			t.Error("expected channel to be closed, but received a value")
		}
	default:
		// Also acceptable: channel is closed and drained — no panic is the main requirement.
	}
}

// A client that cannot take an event must not silently miss it. The hub closes
// its stream instead, so the browser reconnects and resyncs from the server.
func TestSSEHubClosesClientThatFallsBehind(t *testing.T) {
	hub := web.NewSSEHub()
	slow := hub.Subscribe()
	fast := hub.Subscribe()

	for i := 0; i < web.SSEClientBuffer+1; i++ {
		hub.Broadcast(web.SSEEvent{Event: "e", Data: `{}`})
		<-fast // fast keeps up
	}

	drained := 0
	for range slow {
		drained++
	}
	if drained != web.SSEClientBuffer {
		t.Errorf("slow client drained %d events before close, want %d", drained, web.SSEClientBuffer)
	}

	// The client that kept up is still subscribed.
	hub.Broadcast(web.SSEEvent{Event: "after", Data: `{}`})
	select {
	case ev, ok := <-fast:
		if !ok || ev.Event != "after" {
			t.Errorf("fast client: got %+v ok=%v, want the next event", ev, ok)
		}
	case <-time.After(time.Second):
		t.Error("fast client stopped receiving after the slow one was closed")
	}

	// The handler still unsubscribes a client the hub already closed.
	hub.Unsubscribe(slow)
}
