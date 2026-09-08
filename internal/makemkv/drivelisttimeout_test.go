package makemkv

import (
	"context"
	"errors"
	"testing"
	"time"
)

// A drive listing that the timeout interrupts is not a command failure — the
// listing never finished, which is what a drive that stopped responding looks
// like. It must be a distinct signal, so the poller can say what happened
// rather than surface the raw "signal: interrupt" the interrupt leaves behind.
func TestDriveListTimeoutIsADistinctSignal(t *testing.T) {
	// An already-expired context makes the inner driveListTimeout trip at once,
	// so the interrupt path runs without a 30s wait.
	expired, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	ex := NewExecutor(WithRunner(&mockCmdRunner{err: errors.New("signal: interrupt")}))

	_, err := ex.ListDrives(expired)
	if err == nil {
		t.Fatal("a timed-out drive listing returned no error")
	}
	if !errors.Is(err, ErrDriveListTimeout) {
		t.Fatalf("a drive-list timeout was not distinguishable from a command failure: %v", err)
	}
}
