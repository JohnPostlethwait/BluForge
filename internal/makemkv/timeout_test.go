package makemkv

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// A scan that hits the ceiling reported "signal: killed", which is what the
// kernel did rather than what happened. Twice in production that sent the
// investigation after cancellation bugs when the real answer was a timeout.
func TestScanSourceReportsTimeoutRatherThanTheSignal(t *testing.T) {
	runner := &recordingRunner{err: errors.New("signal: killed")}
	ex := NewExecutor(WithRunner(runner))

	// A context that is already past its deadline, as it is when the cap fires.
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()

	_, err := ex.ScanSource(ctx, DiscSource(0))
	if err == nil {
		t.Fatal("ScanSource succeeded, want an error")
	}

	msg := err.Error()
	if !strings.Contains(msg, "timed out") {
		t.Errorf("error %q does not say it timed out", msg)
	}
	if !strings.Contains(msg, scanTimeout.String()) {
		t.Errorf("error %q does not name the limit (%s)", msg, scanTimeout)
	}
}

// A scan that fails for an ordinary reason must not be described as a timeout.
func TestScanSourceOrdinaryFailureIsNotCalledATimeout(t *testing.T) {
	runner := &recordingRunner{err: errors.New("exit status 1")}
	ex := NewExecutor(WithRunner(runner))

	_, err := ex.ScanSource(context.Background(), DiscSource(0))
	if err == nil {
		t.Fatal("ScanSource succeeded, want an error")
	}
	if strings.Contains(err.Error(), "timed out") {
		t.Errorf("ordinary failure reported as a timeout: %v", err)
	}
}

// The ceiling exists to stop a wedged process holding the executor mutex
// forever, not to bound how long a damaged disc may take. A disc that retries
// every unreadable sector legitimately runs well past ten minutes — the value
// that killed a real scan at 10m07s.
func TestScanTimeoutAllowsForADamagedDisc(t *testing.T) {
	if scanTimeout < 30*time.Minute {
		t.Errorf("scanTimeout = %s, too tight for a disc that retries bad sectors", scanTimeout)
	}
}
