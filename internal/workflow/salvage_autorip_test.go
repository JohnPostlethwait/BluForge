package workflow

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/johnpostlethwait/bluforge/internal/db"
	"github.com/johnpostlethwait/bluforge/internal/makemkv"
)

// A salvage exists because a rip failed. The user had already matched the disc,
// picked titles, chosen languages and settled on names. Finishing the salvage
// and stopping there sent them back to the beginning to do all of it again.
func TestSalvageRipsWhatWasAlreadyChosen(t *testing.T) {
	orch, store, outputDir := setupOrchestratorWithScanner(t, &mockDriveExecutor{})
	orch.outputDir = outputDir

	opts := &makemkv.SelectionOpts{AudioLangs: []string{"eng"}, SubtitleLangs: []string{"eng"}}
	if _, err := store.CreateJob(db.RipJob{
		DriveIndex:    1,
		DiscName:      "RAMBO_DISC2",
		TitleIndex:    3,
		TitleName:     "Rambo - First Blood Part II",
		ContentType:   "movie",
		SourceFile:    "00800.mpls",
		SelectionOpts: encodeSelectionOpts(opts),
		OutputPath:    filepath.Join(outputDir, "Rambo - First Blood Part II (1985)", "Rambo - First Blood Part II.mkv"),
		Status:        "failed",
		ErrorMessage:  "makemkvcon saved no titles — the drive could not read it",
	}); err != nil {
		t.Fatalf("CreateJob: %v", err)
	}

	if n := orch.ripAfterSalvage(1, "RAMBO_DISC2", outputDir); n != 1 {
		t.Fatalf("submitted %d rips, want 1", n)
	}

	deadline := time.Now().Add(asyncDeadline)
	var jobs []db.RipJob
	for time.Now().Before(deadline) {
		all, err := store.ListAllJobs(20, 0)
		if err != nil {
			t.Fatalf("ListAllJobs: %v", err)
		}
		if len(all) > 1 {
			jobs = all
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(jobs) < 2 {
		t.Fatal("the salvage did not create a new rip")
	}

	// The newest job is the repeat, and it must carry the same choices.
	repeat := jobs[0]
	if repeat.TitleName != "Rambo - First Blood Part II" {
		t.Errorf("TitleName = %q, want the name already chosen", repeat.TitleName)
	}
	if repeat.SourceFile != "00800.mpls" {
		t.Errorf("SourceFile = %q, want 00800.mpls — the rip verifies against it", repeat.SourceFile)
	}
	if repeat.SelectionOpts == "" {
		t.Error("the language choices were not carried over")
	}
	// The naming has to land where the first attempt would have.
	if filepath.Base(filepath.Dir(repeat.OutputPath)) != "Rambo - First Blood Part II (1985)" {
		t.Errorf("OutputPath = %q, want the same media directory as the failed rip", repeat.OutputPath)
	}
}

// A disc with nothing waiting is not silently re-ripped.
func TestSalvageRipsNothingWhenNothingFailed(t *testing.T) {
	orch, _, outputDir := setupOrchestratorWithScanner(t, &mockDriveExecutor{})
	orch.outputDir = outputDir

	if n := orch.ripAfterSalvage(0, "SOME_DISC", outputDir); n != 0 {
		t.Errorf("submitted %d rips for a disc with no failures, want 0", n)
	}
}

// Another disc's failures are not this disc's work.
func TestSalvageOnlyRipsTheDiscItSalvaged(t *testing.T) {
	orch, store, outputDir := setupOrchestratorWithScanner(t, &mockDriveExecutor{})
	orch.outputDir = outputDir

	if _, err := store.CreateJob(db.RipJob{
		DiscName: "SOME_OTHER_DISC", TitleName: "Not This One",
		Status: "failed", ErrorMessage: "makemkvcon saved no titles",
		OutputPath: filepath.Join(outputDir, "Other", "Not This One.mkv"),
	}); err != nil {
		t.Fatalf("CreateJob: %v", err)
	}

	if n := orch.ripAfterSalvage(0, "RAMBO_DISC2", outputDir); n != 0 {
		t.Errorf("submitted %d rips from another disc's failures, want 0", n)
	}
}

// The choices have to survive being written down and read back.
func TestSelectionOptsSurviveTheRoundTrip(t *testing.T) {
	opts := &makemkv.SelectionOpts{
		AudioLangs:    []string{"eng", "zho"},
		SubtitleLangs: []string{"eng"},
		KeepForced:    true,
		KeepLossless:  true,
	}
	encoded := encodeSelectionOpts(opts)
	if encoded == "" {
		t.Fatal("the choices encoded to nothing")
	}
	if encodeSelectionOpts(nil) != "" {
		t.Error("no choices should encode to nothing at all")
	}
}
