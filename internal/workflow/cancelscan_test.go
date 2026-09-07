package workflow

import (
	"context"
	"testing"
	"time"
)

// A physical drive change must be able to stop an in-flight scan. The scan is
// deliberately detached from the caller's context so a browser disconnect does
// not kill it (see TestScanSurvivesCallerCancellation), so cancellation needs
// its own handle: CancelScan.
func TestCancelScanStopsRunningScan(t *testing.T) {
	scanner := newSlowScanner()
	orch := NewOrchestrator(OrchestratorDeps{Scanner: scanner})
	defer close(scanner.release)

	go func() { _, _ = orch.ScanDisc(context.Background(), 0) }()

	select {
	case <-scanner.started:
	case <-time.After(asyncDeadline):
		t.Fatal("scan never started")
	}

	orch.CancelScan(0)

	scanCtx := scanner.context()
	if scanCtx == nil {
		t.Fatal("no context captured")
	}
	select {
	case <-scanCtx.Done():
		// Cancelled, as intended.
	case <-time.After(asyncDeadline):
		t.Fatal("CancelScan did not cancel the running scan")
	}
}

// CancelScan on a drive with no scan running is a harmless no-op.
func TestCancelScanNoScanIsNoop(t *testing.T) {
	orch := NewOrchestrator(OrchestratorDeps{Scanner: newSlowScanner()})
	orch.CancelScan(7) // must not panic
}
