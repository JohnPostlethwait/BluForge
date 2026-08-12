package web

import (
	"context"
	"testing"
	"time"
)

// recordingExecutor captures the context its submission was given, so a test
// can ask whether the request's cancellation reached it.
type recordingExecutor struct {
	started chan struct{}
	release chan struct{}
	ctx     context.Context
}

func (r *recordingExecutor) Execute(ctx context.Context, _ int64) (string, error) {
	r.ctx = ctx
	close(r.started)
	<-r.release
	return "https://github.com/TheDiscDB/thediscdb/pull/1", nil
}

// Submitting opens a PR: a TMDB fetch, a poster download, a fork, a branch, a
// commit and a pull request. Run on the request's context, a browser that gave
// up in the middle could leave the PR opened upstream and unrecorded here —
// and the next submission would open a second one for the same disc.
func TestSubmissionOutlivesTheRequestThatStartedIt(t *testing.T) {
	srv := &Server{}
	exec := &recordingExecutor{started: make(chan struct{}), release: make(chan struct{})}

	parent, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := srv.submitContribution(parent, exec, 1)
		done <- err
	}()

	<-exec.started
	cancel() // the browser goes away mid-submission

	select {
	case <-exec.ctx.Done():
		t.Fatal("the request's cancellation reached the submission")
	case <-time.After(50 * time.Millisecond):
	}

	close(exec.release)
	if err := <-done; err != nil {
		t.Errorf("submitContribution: %v", err)
	}
}

// Detaching must not mean running forever: a wedged GitHub call would pin the
// goroutine for the life of the process.
func TestSubmissionIsBounded(t *testing.T) {
	srv := &Server{}
	exec := &recordingExecutor{started: make(chan struct{}), release: make(chan struct{})}

	go srv.submitContribution(context.Background(), exec, 1)
	<-exec.started
	defer close(exec.release)

	deadline, ok := exec.ctx.Deadline()
	if !ok {
		t.Fatal("the submission context has no deadline")
	}
	if remaining := time.Until(deadline); remaining <= 0 || remaining > contributionSubmitTimeout {
		t.Errorf("deadline is %s away, want within %s", remaining, contributionSubmitTimeout)
	}
}
