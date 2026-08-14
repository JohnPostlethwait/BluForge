package workflow

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/johnpostlethwait/bluforge/internal/makemkv"
)

// lockOrderBackupper records when the drive is locked and released against when
// the folder scan runs, and deadlocks the way the real executor does if the
// scan is asked for while the lock is held.
type lockOrderBackupper struct {
	mu     sync.Mutex
	events []string
	held   bool
	omit   string
	root   string
}

func (b *lockOrderBackupper) LockDrive() {
	b.mu.Lock()
	b.held = true
	b.events = append(b.events, "lock")
	b.mu.Unlock()
}

func (b *lockOrderBackupper) UnlockDrive() {
	b.mu.Lock()
	b.held = false
	b.events = append(b.events, "unlock")
	b.mu.Unlock()
}

func (b *lockOrderBackupper) Backup(_ context.Context, _ int, destDir string, _ func(makemkv.Event)) error {
	entries, _ := os.ReadDir(filepath.Join(b.root, streamDir))
	for _, e := range entries {
		name := filepath.Join(streamDir, e.Name())
		if name == b.omit {
			continue
		}
		src, err := os.ReadFile(filepath.Join(b.root, name))
		if err != nil {
			continue
		}
		dst := filepath.Join(destDir, name)
		_ = os.MkdirAll(filepath.Dir(dst), 0o777)
		_ = os.WriteFile(dst, src, 0o644)
	}
	return nil
}

func (b *lockOrderBackupper) ScanSource(context.Context, makemkv.Source) (*makemkv.DiscScan, error) {
	b.mu.Lock()
	held := b.held
	b.events = append(b.events, "scan")
	b.mu.Unlock()

	if held {
		// The real executor takes the same mutex here and blocks forever.
		return nil, errDeadlock
	}
	return &makemkv.DiscScan{
		DiscName: "RAMBO_DISC2",
		Titles:   []makemkv.TitleInfo{{Index: 0, Attributes: map[int]string{16: "00800.mpls"}}},
	}, nil
}

var errDeadlock = errDeadlockType{}

type errDeadlockType struct{}

func (errDeadlockType) Error() string {
	return "scan requested while the drive lock was held: the real executor would deadlock here"
}

// A salvage held the drive across the folder scan that follows the rescue. The
// scan takes the same executor mutex, Go mutexes do not nest, and a real
// salvage sat with its rescue finished and nothing running for forty minutes
// before anyone noticed.
func TestTheDriveIsReleasedBeforeTheFolderScan(t *testing.T) {
	root := discFixture(t, 1024)
	orch, _, outputDir := setupOrchestratorWithScanner(t, &mockDriveExecutor{})
	b := &lockOrderBackupper{root: root, omit: filepath.Join(streamDir, "00000.m2ts")}
	orch.backupper = b
	orch.rescuer = &fakeRescuer{size: 1024}
	orch.openDiscRoot = func(string) (string, func(), error) { return root, func() {}, nil }
	orch.outputDir = outputDir

	if _, err := orch.Salvage(context.Background(), SalvageRequest{
		DriveIndex: 0, DiscLabel: "RAMBO_DISC2", OutputDir: outputDir,
	}); err != nil {
		t.Fatalf("Salvage: %v", err)
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	var lockIdx, unlockIdx, scanIdx = -1, -1, -1
	for i, e := range b.events {
		switch e {
		case "lock":
			lockIdx = i
		case "unlock":
			unlockIdx = i
		case "scan":
			scanIdx = i
		}
	}
	if lockIdx < 0 || unlockIdx < 0 || scanIdx < 0 {
		t.Fatalf("expected a lock, an unlock and a scan: %v", b.events)
	}
	if unlockIdx > scanIdx {
		t.Errorf("the drive was still held when the scan ran: %v", b.events)
	}
}

// A rescue that fails must still hand the drive back, or every later operation
// on it blocks.
func TestTheDriveIsReleasedWhenARescueFails(t *testing.T) {
	root := discFixture(t, 1024)
	orch, _, outputDir := setupOrchestratorWithScanner(t, &mockDriveExecutor{})
	b := &lockOrderBackupper{root: root, omit: filepath.Join(streamDir, "00000.m2ts")}
	orch.backupper = b
	orch.rescuer = &fakeRescuer{err: errDeadlock}
	orch.openDiscRoot = func(string) (string, func(), error) { return root, func() {}, nil }
	orch.outputDir = outputDir

	if _, err := orch.Salvage(context.Background(), SalvageRequest{
		DriveIndex: 0, DiscLabel: "RAMBO_DISC2", OutputDir: outputDir,
	}); err == nil {
		t.Fatal("a failed rescue reported success")
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	if b.held {
		t.Error("the drive was left locked after a failed rescue")
	}
}
