package web

import (
	"bufio"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
)

func startSSEServer(t *testing.T, hub *SSEHub) *http.Response {
	t.Helper()
	s := &Server{sseHub: hub}
	e := echo.New()
	e.GET("/events", s.handleSSE)
	srv := httptest.NewServer(e)
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/events")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

// The page counts pings to tell a live stream from a dead one, so they must
// arrive on schedule even when nothing else is happening.
func TestSSEStreamSendsPings(t *testing.T) {
	old := ssePingInterval
	ssePingInterval = 20 * time.Millisecond
	defer func() { ssePingInterval = old }()

	resp := startSSEServer(t, NewSSEHub())

	pings := make(chan struct{}, 8)
	go func() {
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			if strings.TrimSpace(sc.Text()) == "event: ping" {
				pings <- struct{}{}
			}
		}
	}()

	for i := 0; i < 2; i++ {
		select {
		case <-pings:
		case <-time.After(2 * time.Second):
			t.Fatalf("ping %d did not arrive", i+1)
		}
	}
}

// A stream the hub closed must end the response, so the browser reconnects.
func TestSSEStreamEndsWhenHubClosesIt(t *testing.T) {
	hub := NewSSEHub()
	resp := startSSEServer(t, hub)

	var ch chan SSEEvent
	deadline := time.Now().Add(2 * time.Second)
	for ch == nil && time.Now().Before(deadline) {
		hub.mu.Lock()
		for c := range hub.clients {
			ch = c
		}
		hub.mu.Unlock()
		time.Sleep(5 * time.Millisecond)
	}
	if ch == nil {
		t.Fatal("handler never subscribed")
	}
	hub.mu.Lock()
	hub.drop(ch, "test")
	hub.mu.Unlock()

	done := make(chan struct{})
	go func() {
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("stream stayed open after the hub closed it")
	}
}
