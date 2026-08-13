package workflow

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/johnpostlethwait/bluforge/internal/ddrescue"
	"github.com/johnpostlethwait/bluforge/internal/makemkv"
)

// salvageBackupper reproduces what makemkvcon's backup did to Rambo: it copies
// the healthy streams, omits the damaged one entirely, and reports an error
// while having produced a usable tree.
type salvageBackupper struct {
	// omit is the stream left out, relative to the disc root.
	omit string
	// backupErr is returned after the copy, as MakeMKV does when files fail
	// their hash check.
	backupErr error
	// titles is what a scan of the repaired tree finds.
	titles int
	// scanMu guards scanned: a salvage per drive means more than one writer.
	scanMu   sync.Mutex
	scanned  []string
	discRoot string
}

func (b *salvageBackupper) Backup(_ context.Context, _ int, destDir string, _ func(makemkv.Event)) error {
	entries, _ := os.ReadDir(filepath.Join(b.discRoot, streamDir))
	for _, e := range entries {
		name := filepath.Join(streamDir, e.Name())
		if name == b.omit {
			continue
		}
		src, err := os.ReadFile(filepath.Join(b.discRoot, name))
		if err != nil {
			continue
		}
		dst := filepath.Join(destDir, name)
		_ = os.MkdirAll(filepath.Dir(dst), 0o777)
		_ = os.WriteFile(dst, src, 0o644)
	}
	return b.backupErr
}

func (b *salvageBackupper) ScanSource(_ context.Context, src makemkv.Source) (*makemkv.DiscScan, error) {
	b.scanMu.Lock()
	b.scanned = append(b.scanned, src.Arg())
	b.scanMu.Unlock()
	scan := &makemkv.DiscScan{DiscName: "RAMBO_DISC2"}
	for i := 0; i < b.titles; i++ {
		scan.Titles = append(scan.Titles, makemkv.TitleInfo{
			Index: i, Attributes: map[int]string{2: "Feature", 9: "1:35:34", 16: "00800.mpls"},
		})
	}
	return scan, nil
}

// fakeRescuer writes the destination file at full length, as ddrescue does when
// it fills what it cannot read.
type fakeRescuer struct {
	// Two drives can be salvaged at once, so the record of what was rescued is
	// written from more than one goroutine.
	mu       sync.Mutex
	rescued  []string
	badBytes int64
	err      error
	size     int
}

func (f *fakeRescuer) Run(_ context.Context, args []string, onLine func(string)) error {
	if f.err != nil {
		return f.err
	}
	// The last three arguments are source, destination and map file.
	dest := args[len(args)-2]
	f.mu.Lock()
	f.rescued = append(f.rescued, dest)
	f.mu.Unlock()
	_ = os.MkdirAll(filepath.Dir(dest), 0o777)
	_ = os.WriteFile(dest, make([]byte, f.size), 0o644)
	if onLine != nil && f.badBytes > 0 {
		onLine("non-tried:       0 B,  bad-sector:   168000 B,    error rate:     146 B/s")
	}
	return nil
}

// discFixture builds a disc tree with one oversized stream standing in for the
// feature.
func discFixture(t *testing.T, featureSize int) string {
	t.Helper()
	root := t.TempDir()
	streams := filepath.Join(root, streamDir)
	if err := os.MkdirAll(streams, 0o777); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(streams, "00000.m2ts"), make([]byte, featureSize), 0o644); err != nil {
		t.Fatalf("write feature: %v", err)
	}
	if err := os.WriteFile(filepath.Join(streams, "00001.m2ts"), make([]byte, 16), 0o644); err != nil {
		t.Fatalf("write extra: %v", err)
	}
	return root
}

func salvageOrchestrator(t *testing.T, root string, b *salvageBackupper, r ddrescue.Runner) (*Orchestrator, string) {
	t.Helper()
	orch, _, outputDir := setupOrchestratorWithScanner(t, &mockDriveExecutor{})
	b.discRoot = root
	orch.backupper = b
	orch.rescuer = r
	orch.openDiscRoot = func(string) (string, func(), error) { return root, func() {}, nil }
	return orch, outputDir
}

// Rambo's backup finished, reported "1 files failed hash check", and simply left
// 00000.m2ts out of the tree. Treating that error as fatal would have thrown
// away a complete copy of everything else on the disc.
func TestSalvageContinuesWhenTheBackupReportsFailure(t *testing.T) {
	root := discFixture(t, 1024)
	b := &salvageBackupper{
		omit:      filepath.Join(streamDir, "00000.m2ts"),
		backupErr: errors.New("backup done but 1 files failed hash check"),
		titles:    1,
	}
	r := &fakeRescuer{size: 1024, badBytes: 168000}
	orch, outputDir := salvageOrchestrator(t, root, b, r)

	rec, err := orch.Salvage(context.Background(), SalvageRequest{
		DriveIndex: 0, DiscLabel: "RAMBO_DISC2", OutputDir: outputDir,
	})
	if err != nil {
		t.Fatalf("Salvage: %v", err)
	}
	if len(rec.Scan.Titles) != 1 {
		t.Errorf("got %d titles, want 1", len(rec.Scan.Titles))
	}
}

// The stream the backup omitted is the one that has to be rescued, and the
// healthy ones must be left alone: re-reading them costs an hour of drive time
// for bytes already in hand.
func TestSalvageRescuesOnlyTheMissingStream(t *testing.T) {
	root := discFixture(t, 1024)
	b := &salvageBackupper{omit: filepath.Join(streamDir, "00000.m2ts"), titles: 1}
	r := &fakeRescuer{size: 1024}
	orch, outputDir := salvageOrchestrator(t, root, b, r)

	if _, err := orch.Salvage(context.Background(), SalvageRequest{
		DriveIndex: 0, DiscLabel: "RAMBO_DISC2", OutputDir: outputDir,
	}); err != nil {
		t.Fatalf("Salvage: %v", err)
	}

	if len(r.rescued) != 1 {
		t.Fatalf("rescued %d files, want 1: %v", len(r.rescued), r.rescued)
	}
	if !strings.HasSuffix(r.rescued[0], "00000.m2ts") {
		t.Errorf("rescued %q, want the missing feature stream", r.rescued[0])
	}
}

// A backup that came back whole needs no rescue at all.
func TestSalvageSkipsRescueWhenTheBackupIsComplete(t *testing.T) {
	root := discFixture(t, 1024)
	b := &salvageBackupper{titles: 1}
	r := &fakeRescuer{size: 1024}
	orch, outputDir := salvageOrchestrator(t, root, b, r)

	if _, err := orch.Salvage(context.Background(), SalvageRequest{
		DriveIndex: 0, DiscLabel: "RAMBO_DISC2", OutputDir: outputDir,
	}); err != nil {
		t.Fatalf("Salvage: %v", err)
	}
	if len(r.rescued) != 0 {
		t.Errorf("rescued %v from a complete backup", r.rescued)
	}
}

// What could not be read is the honest measure of what the user is getting, and
// the only thing that explains a glitch to them a year from now.
func TestSalvageReportsWhatItCouldNotRecover(t *testing.T) {
	root := discFixture(t, 1024)
	b := &salvageBackupper{omit: filepath.Join(streamDir, "00000.m2ts"), titles: 1}
	r := &fakeRescuer{size: 1024, badBytes: 168000}
	orch, outputDir := salvageOrchestrator(t, root, b, r)

	rec, err := orch.Salvage(context.Background(), SalvageRequest{
		DriveIndex: 0, DiscLabel: "RAMBO_DISC2", OutputDir: outputDir,
	})
	if err != nil {
		t.Fatalf("Salvage: %v", err)
	}
	if rec.Unrecovered != 168000 {
		t.Errorf("Unrecovered = %d, want 168000", rec.Unrecovered)
	}
}

// The rip must read the repaired folder, never the drive that could not produce
// the title in the first place.
func TestSalvageReturnsTheRepairedFolderAsTheSource(t *testing.T) {
	root := discFixture(t, 1024)
	b := &salvageBackupper{omit: filepath.Join(streamDir, "00000.m2ts"), titles: 1}
	orch, outputDir := salvageOrchestrator(t, root, b, &fakeRescuer{size: 1024})

	rec, err := orch.Salvage(context.Background(), SalvageRequest{
		DriveIndex: 0, DiscLabel: "RAMBO_DISC2", OutputDir: outputDir,
	})
	if err != nil {
		t.Fatalf("Salvage: %v", err)
	}
	if rec.Source.Kind != makemkv.SourceFile {
		t.Errorf("source kind = %v, want a folder", rec.Source.Kind)
	}
	if !strings.HasPrefix(rec.Source.Path, outputDir) {
		t.Errorf("source %q is not inside the output directory", rec.Source.Path)
	}
}

// A repaired copy that still yields nothing is a failure, not a success with an
// empty title list — the user would otherwise be sent to pick from nothing.
func TestSalvageFailsWhenTheRepairedCopyHasNoTitles(t *testing.T) {
	root := discFixture(t, 1024)
	b := &salvageBackupper{omit: filepath.Join(streamDir, "00000.m2ts"), titles: 0}
	orch, outputDir := salvageOrchestrator(t, root, b, &fakeRescuer{size: 1024})

	_, err := orch.Salvage(context.Background(), SalvageRequest{
		DriveIndex: 0, DiscLabel: "RAMBO_DISC2", OutputDir: outputDir,
	})
	if err == nil || !strings.Contains(err.Error(), "no readable titles") {
		t.Errorf("err = %v, want a complaint that nothing is rippable", err)
	}
}

// A rescue that could not run at all stops the salvage: continuing would scan a
// tree still missing its feature and report a confusing emptiness instead.
func TestSalvageStopsWhenTheRescueCannotRun(t *testing.T) {
	root := discFixture(t, 1024)
	b := &salvageBackupper{omit: filepath.Join(streamDir, "00000.m2ts"), titles: 1}
	r := &fakeRescuer{err: errors.New("ddrescue: executable file not found")}
	orch, outputDir := salvageOrchestrator(t, root, b, r)

	_, err := orch.Salvage(context.Background(), SalvageRequest{
		DriveIndex: 0, DiscLabel: "RAMBO_DISC2", OutputDir: outputDir,
	})
	if err == nil {
		t.Fatal("salvage succeeded with no rescuer")
	}
}

// A stream the backup truncated rather than omitted resumes from what is there.
func TestSalvageResumesAPartiallyCopiedStream(t *testing.T) {
	root := discFixture(t, 4096)
	b := &salvageBackupper{titles: 1}
	_, outputDir := salvageOrchestrator(t, root, b, &fakeRescuer{size: 4096})

	// Stand in for a backup that stopped partway through the feature.
	dir := filepath.Join(outputDir, ScratchDirName, scratchSlug("RAMBO_DISC2", ""))
	if err := os.MkdirAll(filepath.Join(dir, streamDir), 0o777); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	short, err := incompleteStreams(root, dir)
	if err != nil {
		t.Fatalf("incompleteStreams: %v", err)
	}
	if len(short) != 2 {
		t.Fatalf("found %d short streams, want 2", len(short))
	}

	if err := os.WriteFile(filepath.Join(dir, streamDir, "00000.m2ts"), make([]byte, 1024), 0o644); err != nil {
		t.Fatalf("write partial: %v", err)
	}
	short, err = incompleteStreams(root, dir)
	if err != nil {
		t.Fatalf("incompleteStreams: %v", err)
	}
	for _, s := range short {
		if strings.HasSuffix(s.name, "00000.m2ts") && s.have != 1024 {
			t.Errorf("resume offset = %d, want 1024 — the rescue would re-read what is already copied", s.have)
		}
	}
}
