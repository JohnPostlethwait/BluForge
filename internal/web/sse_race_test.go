package web_test

import (
	"fmt"
	"sync"
	"testing"

	"github.com/johnpostlethwait/bluforge/internal/web"
)

func TestSSEHub_ConcurrentBroadcast(t *testing.T) {
	hub := web.NewSSEHub()

	// Subscribe 5 clients.
	clients := make([]chan web.SSEEvent, 5)
	for i := range clients {
		clients[i] = hub.Subscribe()
	}

	var wg sync.WaitGroup
	const goroutines = 10
	const iterations = 100

	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				hub.Broadcast(web.SSEEvent{
					Event: fmt.Sprintf("event-%d", id),
					Data:  fmt.Sprintf(`{"i":%d}`, i),
				})
			}
		}(g)
	}

	wg.Wait()

	// Drain and unsubscribe all clients.
	for _, ch := range clients {
		hub.Unsubscribe(ch)
	}
}

func TestSSEHub_ConcurrentSubscribeUnsubscribe(t *testing.T) {
	hub := web.NewSSEHub()

	var wg sync.WaitGroup
	const goroutines = 20

	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			ch := hub.Subscribe()
			hub.Broadcast(web.SSEEvent{
				Event: fmt.Sprintf("sub-%d", id),
				Data:  `{}`,
			})
			hub.Unsubscribe(ch)
		}(g)
	}

	wg.Wait()
}
