package makemkv

import (
	"context"
	"strings"
	"testing"
)

// The job ID lines the saved file up with the activity page and the "rip:
// starting" log line, so it is used whenever the context carries it.
func TestFailureLogNameUsesTheJobIDWhenPresent(t *testing.T) {
	ctx := WithRipJobID(context.Background(), 1789)
	name := failureLogName(ctx, "disc:1", 5)
	if !strings.HasPrefix(name, "rip-job1789-") || !strings.HasSuffix(name, ".log") {
		t.Errorf("name = %q, want rip-job1789-<ts>.log", name)
	}
}

// Without a job ID the disc and title still have to identify the failure, and
// the disc's colon must not leak into the filename.
func TestFailureLogNameFallsBackToDiscAndTitle(t *testing.T) {
	name := failureLogName(context.Background(), "disc:1", 5)
	if strings.Contains(name, ":") {
		t.Errorf("name %q contains a colon; it must be filename-safe", name)
	}
	if !strings.HasPrefix(name, "rip-disc-1-t5-") {
		t.Errorf("name = %q, want rip-disc-1-t5-<ts>.log", name)
	}
}
