package workflow

import (
	"context"
	"testing"
	"time"

	"github.com/johnpostlethwait/bluforge/internal/makemkv"
)

// reportingBackupper keeps reporting progress after its context is cancelled,
// which is what a dying makemkvcon does while it is being killed.
type reportingBackupper struct {
	started chan struct{}
}

func (b *reportingBackupper) Backup(ctx context.Context, _ int, _ string, onEvent func(makemkv.Event)) error {
	close(b.started)
	<-ctx.Done()
	// The process is dying, and its last progress events are still arriving.
	for i := 0; i < 5; i++ {
		onEvent(makemkv.Event{Type: "PRGV", Progress: &makemkv.Progress{Total: i + 1, Max: 100}})
	}
	return ctx.Err()
}

func (b *reportingBackupper) ScanSource(context.Context, makemkv.Source) (*makemkv.DiscScan, error) {
	return &makemkv.DiscScan{DiscName: "RAMBO_DISC2"}, nil
}

// Pausing showed "paused", then the spinner returned seconds later: the run
// being killed kept reporting progress, and those events landed on the state
// the user had just set. Nothing distinguished one run's events from another's.
func TestACancelledRunStopsReportingProgress(t *testing.T) {
	rec := &broadcastRecorder{}
	root := discFixture(t, 16)
	orch, _, outputDir := setupOrchestratorWithScannerAndBroadcast(t, &mockDriveExecutor{}, rec.fn)
	b := &reportingBackupper{started: make(chan struct{})}
	orch.backupper = b
	orch.rescuer = &fakeRescuer{size: 16}
	orch.openDiscRoot = func(string) (string, func(), error) { return root, func() {}, nil }
	orch.outputDir = outputDir

	if err := orch.StartSalvage(0); err != nil {
		t.Fatalf("StartSalvage: %v", err)
	}
	<-b.started
	orch.CancelSalvage(0)

	waitFor(t, "the paused event", func() bool {
		for _, m := range rec.named("disc_salvage") {
			if phase, _ := m["phase"].(string); phase == "paused" {
				return true
			}
		}
		return false
	})
	time.Sleep(50 * time.Millisecond)

	// Nothing may claim the salvage is running after it was paused.
	events := rec.named("disc_salvage")
	var sawPaused bool
	for _, m := range events {
		phase, _ := m["phase"].(string)
		if phase == "paused" {
			sawPaused = true
			continue
		}
		if sawPaused && phase == "backing-up" {
			t.Errorf("a cancelled run reported progress after pausing: %v", m)
		}
	}
}

// Each run is numbered so the page can ignore a dead run's trailing events.
func TestSalvageEventsCarryARunNumber(t *testing.T) {
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
		run, ok := m["run"].(float64)
		if !ok || run <= 0 {
			t.Fatalf("event has no usable run number: %v", m)
		}
	}
}

// A second salvage must be numbered above the first, or the page cannot tell
// which events are current.
func TestEachSalvageRunGetsAHigherNumber(t *testing.T) {
	rec := &broadcastRecorder{}
	root := discFixture(t, 16)
	orch, _, outputDir := setupOrchestratorWithScannerAndBroadcast(t, &mockDriveExecutor{}, rec.fn)
	orch.rescuer = &fakeRescuer{size: 16}
	orch.openDiscRoot = func(string) (string, func(), error) { return root, func() {}, nil }
	orch.outputDir = outputDir

	first := &blockingBackupper{release: make(chan struct{}), started: make(chan struct{})}
	close(first.release)
	orch.backupper = first
	if err := orch.StartSalvage(0); err != nil {
		t.Fatalf("first StartSalvage: %v", err)
	}
	waitFor(t, "the first run to finish", func() bool { return !orch.SalvageInProgress(0) })
	firstRun := lastRun(rec)

	second := &blockingBackupper{release: make(chan struct{}), started: make(chan struct{})}
	close(second.release)
	orch.backupper = second
	if err := orch.StartSalvage(0); err != nil {
		t.Fatalf("second StartSalvage: %v", err)
	}
	waitFor(t, "the second run to finish", func() bool { return !orch.SalvageInProgress(0) })

	if lastRun(rec) <= firstRun {
		t.Errorf("second run numbered %v, not above the first %v", lastRun(rec), firstRun)
	}
}

func lastRun(rec *broadcastRecorder) float64 {
	var last float64
	for _, m := range rec.named("disc_salvage") {
		if run, ok := m["run"].(float64); ok {
			last = run
		}
	}
	return last
}
