package ripper

import (
	"sync"
	"testing"
	"time"
)

// A job's title index is decided when the job is created and never moves.
//
// This file used to test that rewriting the index mid-rip did not race with the
// pages reading it — the write happened on the rip goroutine while the activity
// and dashboard pages read the same field. The race was real and was fixed with
// a setter behind the job's lock.
//
// The write itself was the defect. A corrected index is a guess derived from a
// filename match, and on Kiki's Delivery Service it was wrong: the job for
// title 3 was re-pointed at title 1 and delivered a different cut of the film
// under the right name. There is no setter for the index any longer, so the
// field is written once, at construction.
//
// The concurrent reads stay. If a write is ever reintroduced, the race detector
// finds it here.
func TestAJobsTitleIndexIsFixedForItsLifetime(t *testing.T) {
	exec := &engineMovingExecutor{elsewhere: 9}
	engine := NewEngine(exec)

	job := NewJob(0, 4, "DISC", "/output")
	job.SourceFile = "00200.mpls"

	stop := make(chan struct{})
	seen := make(chan int, 1)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				if got := job.Snapshot().TitleIndex; got != 4 {
					select {
					case seen <- got:
					default:
					}
					return
				}
			}
		}
	}()

	if err := engine.Submit(job); err != nil {
		t.Fatalf("submit failed: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && job.Snapshot().Status != StatusFailed {
		time.Sleep(5 * time.Millisecond)
	}
	close(stop)
	wg.Wait()

	select {
	case got := <-seen:
		t.Errorf("the title index changed to %d while the rip ran", got)
	default:
	}
	if got := job.Snapshot().TitleIndex; got != 4 {
		t.Errorf("title index = %d after the rip, want 4", got)
	}
}
