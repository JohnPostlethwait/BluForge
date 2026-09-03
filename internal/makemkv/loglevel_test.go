package makemkv

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/johnpostlethwait/bluforge/testutil"
)

// recordCapture collects records with their levels intact.
//
// The existing captureLogs helper flattens everything to text at debug, which
// cannot answer the only question these tests ask: what level was this emitted
// at?
type recordCapture struct {
	mu      sync.Mutex
	records []slog.Record
	level   slog.Level
}

func (c *recordCapture) Enabled(_ context.Context, l slog.Level) bool { return l >= c.level }

func (c *recordCapture) Handle(_ context.Context, r slog.Record) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.records = append(c.records, r.Clone())
	return nil
}

func (c *recordCapture) WithAttrs([]slog.Attr) slog.Handler { return c }
func (c *recordCapture) WithGroup(string) slog.Handler      { return c }

// messagesAt returns the messages captured at exactly one level.
func (c *recordCapture) messagesAt(level slog.Level) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []string
	for _, r := range c.records {
		if r.Level == level {
			out = append(out, r.Message)
		}
	}
	return out
}

func (c *recordCapture) containsAt(level slog.Level, substr string) bool {
	for _, m := range c.messagesAt(level) {
		if strings.Contains(m, substr) {
			return true
		}
	}
	return false
}

// captureRecords runs fn with slog directed at a capture handler admitting
// everything from level upward.
func captureRecords(t *testing.T, level slog.Level, fn func()) *recordCapture {
	t.Helper()
	c := &recordCapture{level: level}
	prev := slog.Default()
	slog.SetDefault(slog.New(c))
	defer slog.SetDefault(prev)
	fn()
	return c
}

// runRealRunner invokes the real runner without running makemkvcon.
//
// An already-cancelled context makes exec.Cmd.Start return before it spawns
// anything, so this exercises the logging around the call without depending on
// a makemkvcon binary being installed — and without a stray `info disc:9999`
// spinning up the drives of whatever machine runs the tests.
func runRealRunner(args ...string) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := &realRunner{}
	_, _ = r.Run(ctx, args...)
}

// The drive poll runs makemkvcon every five seconds and the runner announced
// each one twice at INFO — about 17,000 lines a day reporting that nothing had
// happened. The runner is plumbing: it cannot tell a poll from a disc scan,
// because it serves both, so it is the wrong place to decide what is worth
// saying. It says nothing at INFO now.
func TestTheRunnerLogsItsInvocationsAtDebug(t *testing.T) {
	c := captureRecords(t, slog.LevelDebug, func() {
		runRealRunner("-r", "--cache=1", "info", "disc:9999")
	})

	if c.containsAt(slog.LevelInfo, "executing") {
		t.Errorf("the runner still narrates itself at INFO: %v", c.messagesAt(slog.LevelInfo))
	}
	if !c.containsAt(slog.LevelDebug, "executing") {
		t.Errorf("the runner's invocation is not recorded at DEBUG: %v", c.messagesAt(slog.LevelDebug))
	}
}

// The demotion must stop at the runner. A scan is something happening, and it
// announces itself — this guards the operations against being swept up by a
// later pass at the same noise.
func TestAScanStillAnnouncesItselfAtInfo(t *testing.T) {
	runner := &recordingRunner{output: testutil.SampleDiscInfoOutput}
	ex := NewExecutor(WithRunner(runner))

	c := captureRecords(t, slog.LevelInfo, func() {
		_, _ = ex.ScanDisc(context.Background(), 0)
	})

	if !c.containsAt(slog.LevelInfo, "starting scan") {
		t.Errorf("a scan no longer reports that it started: %v", c.messagesAt(slog.LevelInfo))
	}
	if !c.containsAt(slog.LevelInfo, "scan completed") {
		t.Errorf("a scan no longer reports that it finished: %v", c.messagesAt(slog.LevelInfo))
	}
}
