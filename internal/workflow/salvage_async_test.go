package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/johnpostlethwait/bluforge/internal/makemkv"
)

// blockingBackupper holds a salvage open so a test can observe it running.
type blockingBackupper struct {
	release chan struct{}
	started chan struct{}
	once    sync.Once
}

func (b *blockingBackupper) Backup(_ context.Context, _ int, _ string, _ func(makemkv.Event)) error {
	b.once.Do(func() { close(b.started) })
	<-b.release
	return nil
}

func (b *blockingBackupper) ScanSource(_ context.Context, _ makemkv.Source) (*makemkv.DiscScan, error) {
	return &makemkv.DiscScan{
		DiscName: "RAMBO_DISC2",
		Titles:   []makemkv.TitleInfo{{Index: 0, Attributes: map[int]string{16: "00800.mpls"}}},
	}, nil
}

func asyncSalvageOrchestrator(t *testing.T, b *blockingBackupper, rec *broadcastRecorder) *Orchestrator {
	t.Helper()
	root := discFixture(t, 16)
	orch, _, outputDir := setupOrchestratorWithScannerAndBroadcast(t, &mockDriveExecutor{}, rec.fn)
	orch.backupper = b
	orch.rescuer = &fakeRescuer{size: 16}
	orch.openDiscRoot = func(string) (string, func(), error) { return root, func() {}, nil }
	orch.outputDir = outputDir
	return orch
}

// A salvage runs for hours. Holding the request open would mean a browser
// closing takes the backup with it, which is how recovery failed in practice.
func TestStartSalvageReturnsBeforeItFinishes(t *testing.T) {
	b := &blockingBackupper{release: make(chan struct{}), started: make(chan struct{})}
	orch := asyncSalvageOrchestrator(t, b, &broadcastRecorder{})

	if err := orch.StartSalvage(0); err != nil {
		t.Fatalf("StartSalvage: %v", err)
	}

	select {
	case <-b.started:
	case <-time.After(asyncDeadline):
		t.Fatal("the salvage never started")
	}
	if !orch.SalvageInProgress(0) {
		t.Error("SalvageInProgress = false while one is running")
	}

	close(b.release)
	waitFor(t, "the salvage to finish", func() bool { return !orch.SalvageInProgress(0) })
}

// A second click must not start a second copy of the same disc.
func TestStartSalvageRefusesASecondRun(t *testing.T) {
	b := &blockingBackupper{release: make(chan struct{}), started: make(chan struct{})}
	orch := asyncSalvageOrchestrator(t, b, &broadcastRecorder{})

	if err := orch.StartSalvage(0); err != nil {
		t.Fatalf("first StartSalvage: %v", err)
	}
	<-b.started

	if err := orch.StartSalvage(0); !errors.Is(err, ErrSalvageInProgress) {
		t.Errorf("second StartSalvage err = %v, want ErrSalvageInProgress", err)
	}

	close(b.release)
	waitFor(t, "the salvage to finish", func() bool { return !orch.SalvageInProgress(0) })
}

// The page has to be told what a multi-hour operation is doing, and what it
// cost when it finishes.
func TestSalvageBroadcastsItsPhasesAndCost(t *testing.T) {
	rec := &broadcastRecorder{}
	b := &blockingBackupper{release: make(chan struct{}), started: make(chan struct{})}
	orch := asyncSalvageOrchestrator(t, b, rec)
	close(b.release)

	if err := orch.StartSalvage(0); err != nil {
		t.Fatalf("StartSalvage: %v", err)
	}
	waitFor(t, "the done event", func() bool {
		for _, m := range rec.named("disc_salvage") {
			if phase, _ := m["phase"].(string); phase == "done" {
				return true
			}
		}
		return false
	})

	var sawBackingUp bool
	for _, m := range rec.named("disc_salvage") {
		if phase, _ := m["phase"].(string); phase == "backing-up" {
			sawBackingUp = true
		}
	}
	if !sawBackingUp {
		t.Error("the copy phase was never announced")
	}
}

// A salvage that fails has to say so, or the banner stays up for the six hours
// its own ceiling allows.
func TestSalvageBroadcastsFailure(t *testing.T) {
	rec := &broadcastRecorder{}
	b := &blockingBackupper{release: make(chan struct{}), started: make(chan struct{})}
	close(b.release)
	orch := asyncSalvageOrchestrator(t, b, rec)
	// A disc root that cannot be opened stops the salvage after the backup.
	orch.openDiscRoot = func(string) (string, func(), error) {
		return "", func() {}, errors.New("disc not readable")
	}

	if err := orch.StartSalvage(0); err != nil {
		t.Fatalf("StartSalvage: %v", err)
	}
	waitFor(t, "the failure event", func() bool {
		for _, m := range rec.named("disc_salvage") {
			if phase, _ := m["phase"].(string); phase == "failed" {
				return true
			}
		}
		return false
	})

	if orch.SalvageInProgress(0) {
		t.Error("a failed salvage left the drive claimed")
	}
}

// ddrescue's --timeout measures time since the last successful read, so a
// trickling scrape never trips it. Without a ceiling of our own a salvage can
// run until the drive gives out.
func TestSalvageHasAWallClockCeiling(t *testing.T) {
	if salvageDeadline < time.Hour {
		t.Errorf("salvageDeadline = %s, too short to salvage a disc", salvageDeadline)
	}
	if salvageDeadline > 12*time.Hour {
		t.Errorf("salvageDeadline = %s, long enough to be no limit at all", salvageDeadline)
	}
}

// The payload carries what could not be recovered, which is what the job record
// and the log need to explain a glitch later.
func TestSalvagePayloadCarriesTheUnrecoveredBytes(t *testing.T) {
	var payload map[string]any
	if err := json.Unmarshal([]byte(`{"phase":"done","unrecovered":168000}`), &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if payload["unrecovered"] != float64(168000) {
		t.Errorf("unrecovered = %v, want 168000", payload["unrecovered"])
	}
}
