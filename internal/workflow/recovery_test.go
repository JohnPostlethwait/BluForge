package workflow

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/johnpostlethwait/bluforge/internal/db"
	"github.com/johnpostlethwait/bluforge/internal/makemkv"
	"github.com/johnpostlethwait/bluforge/internal/organizer"
	"github.com/johnpostlethwait/bluforge/internal/ripper"
)

// --- fakes ------------------------------------------------------------------

// fakeBackupper stands in for makemkvcon. Backup copies the fake disc tree so
// that AACS removal, re-verification and the folder rescan all operate on real
// files rather than on mocked-out behaviour.
type fakeBackupper struct {
	mu sync.Mutex

	discRoot    string // tree copied into destDir on Backup
	backupCalls int
	backupErr   error

	scanCalls  []makemkv.Source
	scanResult *makemkv.DiscScan
	scanErr    error
}

func (f *fakeBackupper) Backup(_ context.Context, _ int, destDir string, onEvent func(makemkv.Event)) error {
	f.mu.Lock()
	f.backupCalls++
	err := f.backupErr
	root := f.discRoot
	f.mu.Unlock()

	if err != nil {
		return err
	}
	if onEvent != nil {
		onEvent(makemkv.Event{Type: "PRGV", Progress: &makemkv.Progress{Total: 100, Max: 100}})
	}
	return copyTree(root, destDir)
}

func (f *fakeBackupper) ScanSource(_ context.Context, src makemkv.Source) (*makemkv.DiscScan, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.scanCalls = append(f.scanCalls, src)
	if f.scanErr != nil {
		return nil, f.scanErr
	}
	if f.scanResult != nil {
		return f.scanResult, nil
	}
	return &makemkv.DiscScan{
		DiscName:   "RECOVERED_DISC",
		TitleCount: 1,
		Titles: []makemkv.TitleInfo{
			{Index: 0, Attributes: map[int]string{2: "Feature", 11: "1024", 16: "00800.mpls"}},
		},
	}, nil
}

func (f *fakeBackupper) calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.backupCalls
}

func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o777)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o666)
	})
}

// --- fake disc trees --------------------------------------------------------

// The aacs package's own tests cover packet classification exhaustively. Here we
// need only trees that produce each verdict.

func writeFakeDisc(t *testing.T, scrambled bool) string {
	t.Helper()
	root := t.TempDir()
	streamDir := filepath.Join(root, "BDMV", "STREAM")
	if err := os.MkdirAll(streamDir, 0o777); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "AACS"), 0o777); err != nil {
		t.Fatalf("mkdir aacs: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "AACS", "Unit_Key_RO.inf"), []byte("key"), 0o666); err != nil {
		t.Fatalf("write aacs: %v", err)
	}
	var tsc byte
	if scrambled {
		tsc = 0x03
	}
	if err := os.WriteFile(filepath.Join(streamDir, "00800.m2ts"), fakeM2TS(9000, tsc), 0o666); err != nil {
		t.Fatalf("write m2ts: %v", err)
	}
	return root
}

// fakeM2TS mirrors the BDAV layout: 4-byte TP_extra_header then a 188-byte TS
// packet, so the sync byte lands at offset 4 of each 192-byte unit.
func fakeM2TS(packets int, tsc byte) []byte {
	out := make([]byte, 0, packets*192)
	var seed uint32 = 1
	next := func() byte {
		seed = seed*1664525 + 1013904223
		b := byte(seed >> 24)
		if b == 0x47 {
			b = 0x46
		}
		return b
	}
	for i := 0; i < packets; i++ {
		for j := 0; j < 4; j++ {
			out = append(out, next())
		}
		out = append(out, 0x47, 0x01, 0x00, tsc<<6|0x10)
		for j := 0; j < 184; j++ {
			out = append(out, next())
		}
	}
	return out
}

// --- harness ----------------------------------------------------------------

type recoveryFixture struct {
	orch     *Orchestrator
	store    *db.Store
	output   string
	backup   *fakeBackupper
	discRoot string
}

func setupRecovery(t *testing.T, scrambled bool) *recoveryFixture {
	t.Helper()

	tmp := t.TempDir()
	store, err := db.Open(filepath.Join(tmp, "test.db"))
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	output := filepath.Join(tmp, "output")
	if err := os.MkdirAll(output, 0o777); err != nil {
		t.Fatalf("mkdir output: %v", err)
	}

	discRoot := writeFakeDisc(t, scrambled)
	backupper := &fakeBackupper{discRoot: discRoot}

	orch := NewOrchestrator(OrchestratorDeps{
		Store:       store,
		Engine:      ripper.NewEngine(&mockRipExecutor{}),
		Organizer:   organizer.New(),
		OnBroadcast: func(string, string) {},
		Backupper:   backupper,
		OpenDiscRoot: func(string) (string, func(), error) {
			return discRoot, func() {}, nil
		},
	})

	return &recoveryFixture{orch: orch, store: store, output: output, backup: backupper, discRoot: discRoot}
}

// --- tests ------------------------------------------------------------------

// The happy path: an AACS directory over an unencrypted payload. The backup is
// taken, AACS is stripped from the copy, and the rip source becomes the folder.
func TestRecoverSpuriousAACSHappyPath(t *testing.T) {
	f := setupRecovery(t, false)

	rec, err := f.orch.RecoverSpuriousAACS(context.Background(), RecoveryRequest{
		DriveIndex: 0,
		DevicePath: "/dev/sr0",
		DiscLabel:  "SOME_DISC",
		OutputDir:  f.output,
	})
	if err != nil {
		t.Fatalf("RecoverSpuriousAACS: %v", err)
	}

	if f.backup.calls() != 1 {
		t.Errorf("backup ran %d times, want 1", f.backup.calls())
	}
	if rec.Source.IsDisc() {
		t.Errorf("rip source = %v, want a folder source", rec.Source)
	}
	if _, err := os.Stat(filepath.Join(rec.Dir, "AACS")); !os.IsNotExist(err) {
		t.Error("AACS directory still present in the backup")
	}
	if _, err := os.Stat(filepath.Join(rec.Dir, "BDMV", "STREAM", "00800.m2ts")); err != nil {
		t.Errorf("backup is missing its stream content: %v", err)
	}
	if rec.Scan == nil || len(rec.Scan.Titles) == 0 {
		t.Error("recovery did not return a rescan with titles")
	}
	// The scratch directory lives under the output dir, hidden from media scanners.
	if !strings.HasPrefix(rec.Dir, filepath.Join(f.output, ScratchDirName)) {
		t.Errorf("backup dir %q is not under the scratch root", rec.Dir)
	}
}

// A genuinely encrypted disc must not be backed up: the workaround cannot help,
// and the backup would cost ~100GB and 40 minutes to prove nothing.
func TestRecoverRefusesWhenPayloadIsScrambled(t *testing.T) {
	f := setupRecovery(t, true)

	_, err := f.orch.RecoverSpuriousAACS(context.Background(), RecoveryRequest{
		DriveIndex: 0,
		DevicePath: "/dev/sr0",
		DiscLabel:  "ENCRYPTED_DISC",
		OutputDir:  f.output,
	})
	if err == nil {
		t.Fatal("recovery succeeded on a scrambled disc, want an error")
	}
	if !errors.Is(err, ErrGenuinelyEncrypted) {
		t.Errorf("error = %v, want it to wrap ErrGenuinelyEncrypted", err)
	}
	if f.backup.calls() != 0 {
		t.Errorf("backup ran %d times on a scrambled disc, want 0", f.backup.calls())
	}

	rows, err := f.store.ListDiscDiagnosticsByLabel("ENCRYPTED_DISC", 10)
	if err != nil {
		t.Fatalf("ListDiscDiagnosticsByLabel: %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("no diagnostic row recorded for a blocked disc")
	}
	if rows[0].RipPath != "blocked" {
		t.Errorf("rip_path = %q, want blocked", rows[0].RipPath)
	}
	if rows[0].ScrambleVerdict != "scrambled" {
		t.Errorf("scramble_verdict = %q, want scrambled", rows[0].ScrambleVerdict)
	}
}

// Failing early beats dying halfway through a 100GB copy.
func TestRecoverFailsEarlyOnInsufficientSpace(t *testing.T) {
	f := setupRecovery(t, false)
	f.orch.checkSpace = func(string, int64) error { return errors.New("insufficient disk space") }

	_, err := f.orch.RecoverSpuriousAACS(context.Background(), RecoveryRequest{
		DriveIndex: 0,
		DevicePath: "/dev/sr0",
		DiscLabel:  "BIG_DISC",
		OutputDir:  f.output,
	})
	if err == nil {
		t.Fatal("recovery succeeded with no space, want an error")
	}
	if f.backup.calls() != 0 {
		t.Errorf("backup ran %d times despite the space check failing, want 0", f.backup.calls())
	}
}

// A failed backup leaves the scratch copy in place for inspection, and says where.
func TestRecoverRetainsScratchOnBackupFailure(t *testing.T) {
	f := setupRecovery(t, false)
	f.backup.backupErr = errors.New("makemkvcon exploded")

	_, err := f.orch.RecoverSpuriousAACS(context.Background(), RecoveryRequest{
		DriveIndex: 0,
		DevicePath: "/dev/sr0",
		DiscLabel:  "FAILING_DISC",
		OutputDir:  f.output,
	})
	if err == nil {
		t.Fatal("recovery succeeded despite a failing backup")
	}

	scratchRoot := filepath.Join(f.output, ScratchDirName)
	entries, readErr := os.ReadDir(scratchRoot)
	if readErr != nil {
		t.Fatalf("scratch root unreadable: %v", readErr)
	}
	if len(entries) == 0 {
		t.Error("scratch directory was removed after a failed backup; it should be retained for inspection")
	}
	if !strings.Contains(err.Error(), scratchRoot) {
		t.Errorf("error %q does not say where the retained backup is", err)
	}
}

// The one destructive step in the whole feature. It must refuse any path that
// does not resolve inside the scratch root.
func TestRemoveAACSRefusesPathOutsideScratchRoot(t *testing.T) {
	scratchRoot := t.TempDir()
	outside := t.TempDir()
	if err := os.MkdirAll(filepath.Join(outside, "AACS"), 0o777); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	if err := removeAACSDir(scratchRoot, outside); err == nil {
		t.Fatal("removeAACSDir accepted a directory outside the scratch root")
	}
	if _, err := os.Stat(filepath.Join(outside, "AACS")); err != nil {
		t.Errorf("removeAACSDir deleted a directory outside the scratch root: %v", err)
	}
}

func TestRemoveAACSRemovesInsideScratchRoot(t *testing.T) {
	scratchRoot := t.TempDir()
	backupDir := filepath.Join(scratchRoot, "disc-abc")
	if err := os.MkdirAll(filepath.Join(backupDir, "AACS"), 0o777); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	if err := removeAACSDir(scratchRoot, backupDir); err != nil {
		t.Fatalf("removeAACSDir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(backupDir, "AACS")); !os.IsNotExist(err) {
		t.Error("AACS directory was not removed")
	}
}

// Leftovers from a crash must not accumulate — each is up to ~100GB.
func TestSweepScratchRemovesLeftovers(t *testing.T) {
	output := t.TempDir()
	stale := filepath.Join(output, ScratchDirName, "disc-stale")
	if err := os.MkdirAll(stale, 0o777); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	if err := SweepScratch(output); err != nil {
		t.Fatalf("SweepScratch: %v", err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Error("stale scratch backup was not swept")
	}
}

// A disc that scans cleanly is recorded too: knowing the other 22 discs in a box
// set took the direct path is part of what makes the odd 3 diagnosable.
func TestDirectScanRecordsDiagnostic(t *testing.T) {
	f := setupRecovery(t, false)

	f.orch.RecordDirectScan(&makemkv.DiscScan{
		DriveIndex: 1,
		DiscName:   "CLEAN_DISC",
		TitleCount: 3,
		Titles: []makemkv.TitleInfo{
			{Index: 0, Attributes: map[int]string{16: "00800.mpls"}},
		},
	})

	rows, err := f.store.ListDiscDiagnosticsByLabel("CLEAN_DISC", 10)
	if err != nil {
		t.Fatalf("ListDiscDiagnosticsByLabel: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("recorded %d rows, want 1", len(rows))
	}
	if rows[0].RipPath != "direct" {
		t.Errorf("rip_path = %q, want direct", rows[0].RipPath)
	}
	if rows[0].DiscKey == "" {
		t.Error("disc_key was not recorded for a successful scan")
	}
}
