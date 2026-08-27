package workflow

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/johnpostlethwait/bluforge/internal/makemkv"
)

// playlistDir writes a BDMV/PLAYLIST tree, so a root can be fingerprinted.
func playlistDir(t *testing.T, playlists map[string]int) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "BDMV", "PLAYLIST")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	for name, size := range playlists {
		if err := os.WriteFile(filepath.Join(dir, name), make([]byte, size), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	return root
}

// bindCopy makes a drive read from a repaired copy sitting in dir.
func bindCopy(o *Orchestrator, driveIndex int, dir string, labels ...string) {
	o.recoveredMu.Lock()
	defer o.recoveredMu.Unlock()
	o.recovered[driveIndex] = &recoveredDisc{
		source:     makemkv.FileSource(dir),
		dir:        dir,
		discLabels: labels,
	}
}

func isRetired(o *Orchestrator, driveIndex int) bool {
	o.recoveredMu.Lock()
	defer o.recoveredMu.Unlock()
	rec, ok := o.recovered[driveIndex]
	return ok && rec.retired
}

// A repaired copy is bound to a drive index, and SetDriveDisc only unbinds it
// when the volume label differs. Both discs of a two-disc set answer to one
// label, so swapping them left the drive reading the first disc's copy — and
// ScanDisc consults the copy before the cache, so nothing downstream could
// catch it.
//
// The disc itself is readable: a copy exists because makemkvcon fails on the
// AACS directory, not because the filesystem is unreadable. Comparing the
// playlists on the disc against the playlists in the copy settles it.
func TestASameLabelSwapUnbindsTheRepairedCopy(t *testing.T) {
	copyOfMain := playlistDir(t, map[string]int{"00800.mpls": 512, "00801.mpls": 256})
	bonusInDrive := playlistDir(t, map[string]int{"00010.mpls": 300, "00011.mpls": 128})

	scanner := &swappableScanner{discs: []*makemkv.DiscScan{
		discNamed("PERFECT_BLUE", "00010.mpls", "00011.mpls"),
	}}
	orch, _, _ := setupOrchestratorWithScanner(t, scanner)
	orch.openDiscRoot = func(string) (string, func(), error) { return bonusInDrive, func() {}, nil }
	bindCopy(orch, 0, copyOfMain, "PERFECT_BLUE")
	orch.SetDriveDisc(0, "PERFECT_BLUE")

	if _, err := orch.RescanDisc(context.Background(), 0); err != nil {
		t.Fatalf("rescan: %v", err)
	}

	if !isRetired(orch, 0) {
		t.Error("the copy is still bound to the drive after a different disc was found in it")
	}
	if scanner.scans() != 1 {
		t.Errorf("the drive was read %d times, want 1 — the copy was served instead of the disc", scanner.scans())
	}
}

// The ordinary case must not regress: the disc the copy was made from is still
// read from the copy. Reading the drive means re-running a recovery that takes
// tens of minutes and ~100GB.
func TestTheDiscACopyWasMadeFromIsStillReadFromTheCopy(t *testing.T) {
	disc := map[string]int{"00800.mpls": 512, "00801.mpls": 256}
	copyOfDisc := playlistDir(t, disc)
	sameDiscInDrive := playlistDir(t, disc)

	scanner := &swappableScanner{discs: []*makemkv.DiscScan{discNamed("PERFECT_BLUE", "00800.mpls")}}
	orch, _, _ := setupOrchestratorWithScanner(t, scanner)
	orch.openDiscRoot = func(string) (string, func(), error) { return sameDiscInDrive, func() {}, nil }
	orch.InjectCachedScan(0, discNamed("PERFECT_BLUE", "00800.mpls", "00801.mpls"))
	bindCopy(orch, 0, copyOfDisc, "PERFECT_BLUE")
	orch.SetDriveDisc(0, "PERFECT_BLUE")

	if _, err := orch.ScanDisc(context.Background(), 0); err != nil {
		t.Fatalf("scan: %v", err)
	}

	if isRetired(orch, 0) {
		t.Error("the copy was unbound for the very disc it was made from")
	}
	if scanner.scans() != 0 {
		t.Errorf("the drive was read %d times, want 0 — that is what the copy exists to avoid", scanner.scans())
	}
}

// A disc that cannot be mounted says nothing about which disc it is. Unbinding
// on no evidence would send the drive back to reading a disc that needed
// repairing in the first place.
func TestAnUnreadableDiscLeavesTheCopyBound(t *testing.T) {
	copyOfDisc := playlistDir(t, map[string]int{"00800.mpls": 512})

	scanner := &swappableScanner{discs: []*makemkv.DiscScan{discNamed("PERFECT_BLUE", "00800.mpls")}}
	orch, _, _ := setupOrchestratorWithScanner(t, scanner)
	orch.openDiscRoot = func(string) (string, func(), error) { return "", func() {}, os.ErrNotExist }
	orch.InjectCachedScan(0, discNamed("PERFECT_BLUE", "00800.mpls"))
	bindCopy(orch, 0, copyOfDisc, "PERFECT_BLUE")
	orch.SetDriveDisc(0, "PERFECT_BLUE")

	if _, err := orch.ScanDisc(context.Background(), 0); err != nil {
		t.Fatalf("scan: %v", err)
	}

	if isRetired(orch, 0) {
		t.Error("the copy was unbound because the disc could not be mounted")
	}
}
