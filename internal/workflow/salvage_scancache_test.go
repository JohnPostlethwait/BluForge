package workflow

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/johnpostlethwait/bluforge/internal/makemkv"
)

// folderNamedBackupper reproduces what a real folder scan does: it reports the
// name the copied BDMV calls itself, which is not the disc's volume label.
type folderNamedBackupper struct {
	discRoot string
	done     chan struct{}
	once     sync.Once
}

func (b *folderNamedBackupper) Backup(ctx context.Context, driveIndex int, destDir string, ev func(makemkv.Event)) error {
	inner := &salvageBackupper{discRoot: b.discRoot}
	return inner.Backup(ctx, driveIndex, destDir, ev)
}

func (b *folderNamedBackupper) ScanSource(_ context.Context, _ makemkv.Source) (*makemkv.DiscScan, error) {
	b.once.Do(func() { close(b.done) })
	return &makemkv.DiscScan{
		// Not the disc's label: this is what the copied folder says about itself.
		DiscName: "BDMV",
		Titles: []makemkv.TitleInfo{
			{Index: 0, Attributes: map[int]string{2: "Feature", 9: "1:35:34", 16: "00800.mpls"}},
		},
	}, nil
}

// A salvage ends by caching what it recovered, and the page then asks for it by
// drive. Filing it under the name the copied folder reports leaves it
// unreachable to everyone asking about the disc — which is everyone — so the
// page shows no titles after hours of recovery.
//
// This drives the real completion path rather than the caching helper: the
// helper is easy to call correctly in a test and was being called wrongly in
// the code.
func TestASalvagesTitlesAreReachableAfterwards(t *testing.T) {
	root := discFixture(t, 16)
	b := &folderNamedBackupper{discRoot: root, done: make(chan struct{})}

	orch, _, outputDir := setupOrchestratorWithScanner(t, &mockDriveExecutor{})
	orch.backupper = b
	orch.rescuer = &fakeRescuer{size: 16}
	orch.openDiscRoot = func(string) (string, func(), error) { return root, func() {}, nil }
	orch.outputDir = outputDir

	// The disc is known before a salvage can be asked for: the drive reported it
	// on insert, and the page that carries the button reports it on every render.
	orch.SetDriveDisc(0, "DEADPOOL_2")

	if err := orch.StartSalvage(0); err != nil {
		t.Fatalf("StartSalvage: %v", err)
	}

	select {
	case <-b.done:
	case <-time.After(asyncDeadline):
		t.Fatal("the repaired copy was never scanned")
	}

	// The drive still holds the disc it always held, and the page asks by index.
	deadline := time.Now().Add(asyncDeadline)
	for time.Now().Before(deadline) {
		orch.SetDriveDisc(0, "DEADPOOL_2")
		if scan := orch.GetCachedScanByDrive(0); scan != nil {
			if len(scan.Titles) != 1 {
				t.Fatalf("got %d titles, want the 1 the salvage recovered", len(scan.Titles))
			}
			// The copy the salvage just made must still be the drive's source.
			// Labelling it after the copied folder made the drive look like it
			// held a different disc, and the salvage disowned its own output.
			if orch.RecoveredDir(0) == "" {
				t.Error("the salvage disowned the copy it had just made")
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the salvaged titles cannot be found for the disc they came from")
}
