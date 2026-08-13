package workflow

import (
	"context"
	"testing"
	"time"

	"github.com/johnpostlethwait/bluforge/internal/makemkv"
)

// pausableBackupper blocks until the context is cancelled, so a test can pause a
// salvage the way the button does.
type pausableBackupper struct {
	started chan struct{}
}

func (b *pausableBackupper) Backup(ctx context.Context, _ int, _ string, _ func(makemkv.Event)) error {
	close(b.started)
	<-ctx.Done()
	return ctx.Err()
}

func (b *pausableBackupper) ScanSource(context.Context, makemkv.Source) (*makemkv.DiscScan, error) {
	return &makemkv.DiscScan{DiscName: "RAMBO_DISC2"}, nil
}

// Pausing reported "failed" with the raw context error, and the panel — which
// only rendered while active or done — vanished a few seconds later, leaving no
// way back to the work still sitting on disk.
func TestPausingIsReportedAsPausedNotFailed(t *testing.T) {
	rec := &broadcastRecorder{}
	root := discFixture(t, 16)
	orch, _, outputDir := setupOrchestratorWithScannerAndBroadcast(t, &mockDriveExecutor{}, rec.fn)
	b := &pausableBackupper{started: make(chan struct{})}
	orch.backupper = b
	orch.rescuer = &fakeRescuer{size: 16}
	orch.openDiscRoot = func(string) (string, func(), error) { return root, func() {}, nil }
	orch.outputDir = outputDir

	if err := orch.StartSalvage(0); err != nil {
		t.Fatalf("StartSalvage: %v", err)
	}
	select {
	case <-b.started:
	case <-time.After(asyncDeadline):
		t.Fatal("the salvage never started")
	}

	if !orch.CancelSalvage(0) {
		t.Fatal("CancelSalvage reported nothing to stop")
	}

	waitFor(t, "the paused event", func() bool {
		for _, m := range rec.named("disc_salvage") {
			if phase, _ := m["phase"].(string); phase == "paused" {
				return true
			}
		}
		return false
	})

	// A pause must never be reported as a failure: the user is told their
	// salvage broke when they stopped it themselves.
	for _, m := range rec.named("disc_salvage") {
		if phase, _ := m["phase"].(string); phase == "failed" {
			t.Errorf("pausing was reported as a failure: %v", m["message"])
		}
	}
}

// The page decides whether to offer a resume from this flag, so it has to be in
// the payload rather than computed once when the page loaded.
func TestSalvageEventsCarryWhetherThereIsSomethingToResume(t *testing.T) {
	rec := &broadcastRecorder{}
	root := discFixture(t, 16)
	orch, _, outputDir := setupOrchestratorWithScannerAndBroadcast(t, &mockDriveExecutor{}, rec.fn)
	b := &blockingBackupper{release: make(chan struct{}), started: make(chan struct{})}
	close(b.release)
	orch.backupper = b
	orch.rescuer = &fakeRescuer{size: 16}
	orch.openDiscRoot = func(string) (string, func(), error) { return root, func() {}, nil }
	orch.outputDir = outputDir

	if err := orch.StartSalvage(0); err != nil {
		t.Fatalf("StartSalvage: %v", err)
	}
	waitFor(t, "any salvage event", func() bool { return len(rec.named("disc_salvage")) > 0 })

	for _, m := range rec.named("disc_salvage") {
		if _, ok := m["resumable"]; !ok {
			t.Fatalf("event has no resumable flag: %v", m)
		}
	}
}
