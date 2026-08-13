package workflow

import (
	"context"
	"testing"
	"time"

	"github.com/johnpostlethwait/bluforge/internal/makemkv"
)

// slowStartBackupper stands in for the setup before any long work begins.
type slowStartBackupper struct {
	entered chan struct{}
	ctxErr  chan error
}

func (b *slowStartBackupper) Backup(ctx context.Context, _ int, _ string, _ func(makemkv.Event)) error {
	close(b.entered)
	<-ctx.Done()
	b.ctxErr <- ctx.Err()
	return ctx.Err()
}

func (b *slowStartBackupper) ScanSource(context.Context, makemkv.Source) (*makemkv.DiscScan, error) {
	return &makemkv.DiscScan{DiscName: "RAMBO_DISC2"}, nil
}

// The Pause button appears the instant a salvage starts, so pausing has to work
// from that instant. The claim used to publish a no-op cancel and swap in the
// real one from the goroutine: a pause landing in that window called the no-op,
// reported success, and left the salvage running.
func TestPauseWorksFromTheMomentASalvageStarts(t *testing.T) {
	root := discFixture(t, 16)
	orch, _, outputDir := setupOrchestratorWithScanner(t, &mockDriveExecutor{})
	b := &slowStartBackupper{entered: make(chan struct{}), ctxErr: make(chan error, 1)}
	orch.backupper = b
	orch.rescuer = &fakeRescuer{size: 16}
	orch.openDiscRoot = func(string) (string, func(), error) { return root, func() {}, nil }
	orch.outputDir = outputDir

	if err := orch.StartSalvage(0); err != nil {
		t.Fatalf("StartSalvage: %v", err)
	}

	// Cancel immediately, without waiting for the goroutine to get going: this
	// is the window the button is live for and the claim used to be a no-op in.
	if !orch.CancelSalvage(0) {
		t.Fatal("CancelSalvage found nothing to stop right after starting")
	}

	select {
	case <-b.entered:
	case <-time.After(asyncDeadline):
		t.Fatal("the backup never started")
	}

	select {
	case err := <-b.ctxErr:
		if err != context.Canceled {
			t.Errorf("backup context ended with %v, want Canceled", err)
		}
	case <-time.After(asyncDeadline):
		t.Fatal("the pause never reached the running work")
	}
}

// Cancelling a drive with no salvage must say so rather than claim success.
func TestPauseOnADriveWithNoSalvageReportsNothingToStop(t *testing.T) {
	orch, _, _ := setupOrchestratorWithScanner(t, &mockDriveExecutor{})
	if orch.CancelSalvage(3) {
		t.Error("cancelling an idle drive claimed to have stopped something")
	}
}
