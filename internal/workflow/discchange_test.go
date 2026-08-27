package workflow

import (
	"context"
	"sync"
	"testing"

	"github.com/johnpostlethwait/bluforge/internal/makemkv"
)

// swappableScanner hands out a different disc on each scan, standing in for a
// drive whose disc was changed between reads.
type swappableScanner struct {
	mu    sync.Mutex
	discs []*makemkv.DiscScan
	n     int
}

func (s *swappableScanner) ScanDisc(_ context.Context, driveIndex int) (*makemkv.DiscScan, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d := s.discs[min(s.n, len(s.discs)-1)]
	s.n++
	out := *d
	out.DriveIndex = driveIndex
	return &out, nil
}

func (s *swappableScanner) scans() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.n
}

func discNamed(name string, sourceFiles ...string) *makemkv.DiscScan {
	s := &makemkv.DiscScan{DiscName: name, TitleCount: len(sourceFiles)}
	for i, sf := range sourceFiles {
		s.Titles = append(s.Titles, makemkv.TitleInfo{
			Index:      i,
			Attributes: map[int]string{9: "1:20:15", 11: "24696061952", 16: sf},
		})
	}
	return s
}

// perfectBlue is the two-disc set that started this: a main disc and a bonus
// disc that report the same volume label and share nothing else.
func perfectBlue() *swappableScanner {
	return &swappableScanner{discs: []*makemkv.DiscScan{
		discNamed("PERFECT_BLUE", "00800.mpls", "00801.mpls"),
		discNamed("PERFECT_BLUE", "00010.mpls", "00011.mpls", "00012.mpls"),
	}}
}

// The bug. Pressing Scan with the bonus disc in the drive answered from the
// main disc's cache, because the cache is keyed on drive index plus disc name
// and both discs answer to the same name.
func TestRescanReadsTheDiscRatherThanTheCache(t *testing.T) {
	scanner := perfectBlue()
	orch, _, _ := setupOrchestratorWithScanner(t, scanner)

	if _, err := orch.ScanDisc(context.Background(), 0); err != nil {
		t.Fatalf("first scan: %v", err)
	}
	orch.SetDriveDisc(0, "PERFECT_BLUE")

	got, err := orch.RescanDisc(context.Background(), 0)
	if err != nil {
		t.Fatalf("rescan: %v", err)
	}

	if len(got.Titles) != 3 {
		t.Errorf("rescan returned %d titles, want the bonus disc's 3", len(got.Titles))
	}
	if scanner.scans() != 2 {
		t.Errorf("the disc was read %d times, want 2 — the rescan was served from cache", scanner.scans())
	}
}

// And the cache must now hold the disc that is actually in the drive, or the
// next page load shows the main disc again.
func TestRescanReplacesTheCachedScan(t *testing.T) {
	scanner := perfectBlue()
	orch, _, _ := setupOrchestratorWithScanner(t, scanner)

	if _, err := orch.ScanDisc(context.Background(), 0); err != nil {
		t.Fatalf("first scan: %v", err)
	}
	orch.SetDriveDisc(0, "PERFECT_BLUE")
	if _, err := orch.RescanDisc(context.Background(), 0); err != nil {
		t.Fatalf("rescan: %v", err)
	}

	cached := orch.GetCachedScanByDrive(0)
	if cached == nil {
		t.Fatal("no cached scan after a rescan")
	}
	if len(cached.Titles) != 3 {
		t.Errorf("cache holds %d titles, want the bonus disc's 3", len(cached.Titles))
	}
}

// A disc change is not only a stale title list: the release the user picked for
// the main disc, and the mapping saved against it, belong to that disc. Whoever
// holds that state has to be told.
func TestADiscChangeUnderTheSameNameIsReported(t *testing.T) {
	scanner := perfectBlue()
	orch, _, _ := setupOrchestratorWithScanner(t, scanner)

	var mu sync.Mutex
	var changed []int
	orch.onDiscChanged = func(driveIndex int) {
		mu.Lock()
		defer mu.Unlock()
		changed = append(changed, driveIndex)
	}

	if _, err := orch.ScanDisc(context.Background(), 0); err != nil {
		t.Fatalf("first scan: %v", err)
	}
	orch.SetDriveDisc(0, "PERFECT_BLUE")
	if _, err := orch.RescanDisc(context.Background(), 0); err != nil {
		t.Fatalf("rescan: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(changed) != 1 || changed[0] != 0 {
		t.Errorf("disc change reported as %v, want exactly [0]", changed)
	}
}

// Rescanning the disc that is already there is the common case — the user
// presses Scan again. Reporting that as a disc change would throw away the
// release they just selected.
func TestRescanningTheSameDiscIsNotADiscChange(t *testing.T) {
	same := discNamed("PERFECT_BLUE", "00800.mpls", "00801.mpls")
	scanner := &swappableScanner{discs: []*makemkv.DiscScan{same, same}}
	orch, _, _ := setupOrchestratorWithScanner(t, scanner)

	var mu sync.Mutex
	changed := 0
	orch.onDiscChanged = func(int) {
		mu.Lock()
		defer mu.Unlock()
		changed++
	}

	if _, err := orch.ScanDisc(context.Background(), 0); err != nil {
		t.Fatalf("first scan: %v", err)
	}
	orch.SetDriveDisc(0, "PERFECT_BLUE")
	if _, err := orch.RescanDisc(context.Background(), 0); err != nil {
		t.Fatalf("rescan: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if changed != 0 {
		t.Errorf("rescanning the same disc reported %d disc changes, want 0", changed)
	}
}

// The very first scan of a drive has nothing to compare against. An empty
// cache is not evidence that the disc changed.
func TestAFirstScanIsNotADiscChange(t *testing.T) {
	scanner := perfectBlue()
	orch, _, _ := setupOrchestratorWithScanner(t, scanner)

	var mu sync.Mutex
	changed := 0
	orch.onDiscChanged = func(int) {
		mu.Lock()
		defer mu.Unlock()
		changed++
	}

	if _, err := orch.ScanDisc(context.Background(), 0); err != nil {
		t.Fatalf("first scan: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if changed != 0 {
		t.Errorf("the first scan of a drive reported %d disc changes, want 0", changed)
	}
}

// The page has to be able to say where a title list came from, so a cached one
// is never mistaken for a fresh read of the disc in the drive.
func TestCachedScanInfoDistinguishesCacheFromAFreshRead(t *testing.T) {
	scanner := perfectBlue()
	orch, _, _ := setupOrchestratorWithScanner(t, scanner)

	if info := orch.CachedScanInfo(0); info != nil {
		t.Fatalf("CachedScanInfo before any scan = %+v, want nil", info)
	}

	if _, err := orch.ScanDisc(context.Background(), 0); err != nil {
		t.Fatalf("first scan: %v", err)
	}
	orch.SetDriveDisc(0, "PERFECT_BLUE")

	info := orch.CachedScanInfo(0)
	if info == nil {
		t.Fatal("CachedScanInfo after a scan = nil, want the cached scan")
	}
	if info.Scan == nil || len(info.Scan.Titles) != 2 {
		t.Errorf("CachedScanInfo returned %v, want the main disc's 2-title scan", info.Scan)
	}
	if info.Fingerprint == "" {
		t.Error("cached scan carries no fingerprint, so nothing can verify it")
	}
	if info.CachedAt.IsZero() {
		t.Error("cached scan carries no timestamp, so the page cannot say how old it is")
	}
}
