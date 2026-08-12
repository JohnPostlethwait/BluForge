package web

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// countingExecutor blocks until released, so a test can hold one submission open
// while a second arrives.
type countingExecutor struct {
	mu      sync.Mutex
	calls   int
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (c *countingExecutor) Execute(context.Context, int64) (string, error) {
	c.mu.Lock()
	c.calls++
	c.mu.Unlock()
	c.once.Do(func() { close(c.started) })
	<-c.release
	return "https://github.com/TheDiscDB/thediscdb/pull/1", nil
}

func (c *countingExecutor) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

// The Alpine flag on the submit button lives in one page's state, so two tabs —
// or a back-navigation and a resubmit — each have their own and neither knows
// about the other. Both reach the server, and Execute picks between opening a
// new PR and pushing to the existing one by reading a status that is not written
// until the first submission finishes: so both open one. Two PRs upstream for
// the same disc, neither withdrawable cleanly.
func TestASecondSubmissionIsRefusedWhileTheFirstRuns(t *testing.T) {
	srv := &Server{}
	exec := &countingExecutor{started: make(chan struct{}), release: make(chan struct{})}

	go srv.submitContribution(context.Background(), exec, 7)
	<-exec.started

	_, err := srv.submitContribution(context.Background(), exec, 7)
	if !errors.Is(err, ErrSubmitInProgress) {
		t.Errorf("second submission err = %v, want ErrSubmitInProgress", err)
	}
	if n := exec.count(); n != 1 {
		t.Errorf("Execute ran %d times, want 1 — the second would open a second PR", n)
	}

	close(exec.release)
}

// A different contribution is not blocked by an unrelated one in flight.
func TestADifferentContributionIsNotBlocked(t *testing.T) {
	srv := &Server{}
	first := &countingExecutor{started: make(chan struct{}), release: make(chan struct{})}
	second := &countingExecutor{started: make(chan struct{}), release: make(chan struct{})}
	close(second.release)

	go srv.submitContribution(context.Background(), first, 7)
	<-first.started

	if _, err := srv.submitContribution(context.Background(), second, 8); err != nil {
		t.Errorf("an unrelated contribution was blocked: %v", err)
	}

	close(first.release)
}

// The claim has to be given back, or one submission locks that contribution out
// for the life of the process — including a retry after a genuine failure.
func TestTheClaimIsReleasedWhenTheSubmissionEnds(t *testing.T) {
	srv := &Server{}

	failing := &countingExecutor{started: make(chan struct{}), release: make(chan struct{})}
	close(failing.release)
	if _, err := srv.submitContribution(context.Background(), failing, 7); err != nil {
		t.Fatalf("first submission: %v", err)
	}

	retry := &countingExecutor{started: make(chan struct{}), release: make(chan struct{})}
	close(retry.release)

	done := make(chan error, 1)
	go func() {
		_, err := srv.submitContribution(context.Background(), retry, 7)
		done <- err
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("a retry after the first finished was refused: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the retry never ran; the claim was never released")
	}
}
