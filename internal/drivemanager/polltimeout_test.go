package drivemanager

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/johnpostlethwait/bluforge/internal/makemkv"
)

// A poll that timed out is not the same as a poll that failed. Logging it as an
// opaque error — the raw "signal: interrupt" the interrupt leaves — is what made
// the wedge illegible. The poller says what actually happened: the listing did
// not complete because the drive is not responding.
func TestPollTimeoutIsLoggedAsATimeout(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	defer slog.SetDefault(prev)

	exec := &failingExecutor{
		err: fmt.Errorf("%w after 30s (the drive is not responding)", makemkv.ErrDriveListTimeout),
	}
	mgr := NewManager(exec, func(DriveEvent) {})

	mgr.PollOnce(context.Background())

	out := buf.String()
	if !strings.Contains(out, "drive poll timed out") {
		t.Fatalf("a poll timeout was not logged as a timeout; got:\n%s", out)
	}
	if strings.Contains(out, "signal: interrupt") {
		t.Errorf("the opaque interrupt error leaked into the log; got:\n%s", out)
	}
}
