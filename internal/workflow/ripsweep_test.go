package workflow

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnpostlethwait/bluforge/internal/makemkv"
)

// completedRipExecutor writes a whole title, as a rip that succeeded does.
type completedRipExecutor struct{}

func (completedRipExecutor) StartRip(_ context.Context, _ makemkv.Source, _ int, _ string, outputDir string, onEvent func(makemkv.Event), _ *makemkv.SelectionOpts) error {
	if err := os.WriteFile(filepath.Join(outputDir, "title_t00.mkv"), []byte("a whole film"), 0o644); err != nil {
		return err
	}
	if onEvent != nil {
		onEvent(makemkv.Event{
			Type:     "PRGV",
			Progress: &makemkv.Progress{Current: 65536, Total: 65536, Max: 65536},
		})
	}
	return nil
}

// runUnmovableRip rips a title successfully into a destination that cannot be
// written, which is the failure this behaviour exists for: a full, read-only or
// unwritable output directory.
func runUnmovableRip(t *testing.T) (*Orchestrator, string, string) {
	t.Helper()
	orch, store, outputDir := setupOrchestratorWithRipExecutor(t, completedRipExecutor{})

	// A regular file where the destination directory needs to be, so MkdirAll
	// inside AtomicMove fails without needing a read-only filesystem.
	blocker := filepath.Join(outputDir, "RAMBO_DISC2")
	if err := os.WriteFile(blocker, []byte("not a directory"), 0o644); err != nil {
		t.Fatalf("write blocker: %v", err)
	}

	orch.ManualRip(ManualRipParams{
		DriveIndex:      0,
		DiscName:        "RAMBO_DISC2",
		OutputDir:       outputDir,
		DuplicateAction: "overwrite",
		Titles: []TitleSelection{{
			TitleIndex: 0, TitleName: "First Blood", SourceFile: "title00.mkv",
			SizeBytes: 1024, ContentType: "movie",
		}},
	})

	job := waitForFailedJob(t, store)
	return orch, outputDir, job.OutputPath
}

// A rip that finished but could not be moved must keep the file. It is complete
// and it is the only copy: deleting it would throw away the whole rip over a
// destination problem the user can fix.
func TestUnmovableRipKeepsTheFile(t *testing.T) {
	_, _, kept := runUnmovableRip(t)

	if kept == "" {
		t.Fatal("the failed job recorded no path, so the kept file cannot be found")
	}
	if !strings.Contains(kept, RipTempPrefix) {
		t.Errorf("recorded path %q is not inside a rip temp dir", kept)
	}
	data, err := os.ReadFile(kept)
	if err != nil {
		t.Fatalf("the ripped file was not kept: %v", err)
	}
	if string(data) != "a whole film" {
		t.Errorf("kept file = %q, want the complete rip", data)
	}
}

// The sweep must not delete the directory holding that file.
func TestSweepRipDirsKeepsPreservedRip(t *testing.T) {
	orch, outputDir, kept := runUnmovableRip(t)

	keep, err := orch.PreservedRipDirs()
	if err != nil {
		t.Fatalf("PreservedRipDirs: %v", err)
	}
	if len(keep) != 1 {
		t.Fatalf("expected 1 preserved rip dir, got %d: %v", len(keep), keep)
	}
	if err := SweepRipDirs(outputDir, keep); err != nil {
		t.Fatalf("SweepRipDirs: %v", err)
	}
	if _, err := os.Stat(kept); err != nil {
		t.Fatalf("the sweep deleted a rip that was preserved on purpose: %v", err)
	}
}

// Debris from a run that was killed mid-rip has no job pointing at it and must
// go, or one directory accumulates per interrupted rip.
func TestSweepRipDirsRemovesCrashDebris(t *testing.T) {
	outputDir := t.TempDir()

	debris := filepath.Join(outputDir, ".rip-123456", "t0-abcdef")
	if err := os.MkdirAll(debris, 0o775); err != nil {
		t.Fatalf("mkdir debris: %v", err)
	}
	if err := os.WriteFile(filepath.Join(debris, "partial.mkv"), []byte("half"), 0o644); err != nil {
		t.Fatalf("write partial: %v", err)
	}

	if err := SweepRipDirs(outputDir, nil); err != nil {
		t.Fatalf("SweepRipDirs: %v", err)
	}
	if _, err := os.Stat(filepath.Join(outputDir, ".rip-123456")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("crash debris survived the sweep: %v", err)
	}
}

// The sweep runs across the media library and must touch nothing else in it.
func TestSweepRipDirsLeavesEverythingElseAlone(t *testing.T) {
	outputDir := t.TempDir()

	media := filepath.Join(outputDir, "Rambo (1982)")
	scratch := filepath.Join(outputDir, ScratchDirName, "disc-1")
	for _, dir := range []string{media, scratch} {
		if err := os.MkdirAll(dir, 0o775); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	film := filepath.Join(media, "Rambo.mkv")
	if err := os.WriteFile(film, []byte("film"), 0o644); err != nil {
		t.Fatalf("write film: %v", err)
	}

	if err := SweepRipDirs(outputDir, nil); err != nil {
		t.Fatalf("SweepRipDirs: %v", err)
	}
	for _, path := range []string{film, scratch} {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("the sweep removed %s, which is not its to touch: %v", path, err)
		}
	}
}

func TestSweepRipDirsOnMissingOutputDir(t *testing.T) {
	if err := SweepRipDirs(filepath.Join(t.TempDir(), "gone"), nil); err != nil {
		t.Errorf("a missing output dir is not an error: %v", err)
	}
}

func TestRipDirOf(t *testing.T) {
	tests := []struct {
		name string
		path string
		want string
	}{
		{
			name: "inside a per-title subdir",
			path: "/output/.rip-123/t0-abc/title.mkv",
			want: "/output/.rip-123",
		},
		{
			name: "directly inside the batch dir",
			path: "/output/.rip-123/title.mkv",
			want: "/output/.rip-123",
		},
		{
			name: "organised media is not inside one",
			path: "/output/Rambo (1982)/Rambo.mkv",
			want: "",
		},
		{name: "empty", path: "", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ripDirOf(tt.path); got != tt.want {
				t.Errorf("ripDirOf(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}

// A record can outlive the file when the user moves it out by hand. The
// directory is then debris like any other.
func TestPreservedRipDirsIgnoresAVanishedFile(t *testing.T) {
	orch, _, kept := runUnmovableRip(t)

	if err := os.Remove(kept); err != nil {
		t.Fatalf("remove kept file: %v", err)
	}

	keep, err := orch.PreservedRipDirs()
	if err != nil {
		t.Fatalf("PreservedRipDirs: %v", err)
	}
	if len(keep) != 0 {
		t.Errorf("expected no preserved dirs once the file is gone, got %v", keep)
	}
}
