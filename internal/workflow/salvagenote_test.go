package workflow

import (
	"strings"
	"testing"

	"github.com/johnpostlethwait/bluforge/internal/makemkv"
)

// A salvaged file carries damage nothing about the file itself explains. The
// job record is the only place that explanation can live.
func TestSalvageNoteNamesWhatWasLost(t *testing.T) {
	note := salvageNoteFor(&RecoveredDisc{Salvaged: true, Unrecovered: 168000})

	if !strings.Contains(note, "168.0 kB") {
		t.Errorf("note does not say how much was lost: %q", note)
	}
	if !strings.Contains(strings.ToLower(note), "salvaged") {
		t.Errorf("note does not say the disc was salvaged: %q", note)
	}
}

// A salvage that recovered everything is still worth recording: "this came off
// a damaged disc" is information about the file.
func TestSalvageNoteIsWrittenEvenWhenNothingWasLost(t *testing.T) {
	note := salvageNoteFor(&RecoveredDisc{Salvaged: true})
	if note == "" {
		t.Error("a clean salvage left no note at all")
	}
}

// An ordinary AACS recovery produces a faithful copy. A note there would be
// noise on a file with nothing wrong with it.
func TestAnOrdinaryRecoveryGetsNoNote(t *testing.T) {
	if note := salvageNoteFor(&RecoveredDisc{}); note != "" {
		t.Errorf("an ordinary recovery was annotated: %q", note)
	}
}

// The note has to reach the jobs, or it is a string nobody ever sees.
func TestJobsRippedFromASalvageCarryTheNote(t *testing.T) {
	orch, store, outputDir := setupOrchestratorWithScanner(t, &mockDriveExecutor{})
	orch.registerRecovered(0, &RecoveredDisc{
		Source:      makemkv.FileSource(outputDir),
		Dir:         outputDir,
		Ephemeral:   true,
		Salvaged:    true,
		Unrecovered: 168000,
	})

	orch.ManualRip(ManualRipParams{
		DriveIndex:      0,
		DiscName:        "RAMBO_DISC2",
		OutputDir:       outputDir,
		DuplicateAction: "overwrite",
		Titles:          []TitleSelection{{TitleIndex: 0, TitleName: "Feature", SourceFile: "00800.mpls"}},
	})

	jobs, err := store.ListAllJobs(10, 0)
	if err != nil {
		t.Fatalf("ListAllJobs: %v", err)
	}
	if len(jobs) == 0 {
		t.Fatal("no job was created")
	}
	if !strings.Contains(jobs[0].SalvageNote, "168.0 kB") {
		t.Errorf("SalvageNote = %q, want the loss recorded against the job", jobs[0].SalvageNote)
	}
}

// A rip of an undamaged disc must not be labelled as salvaged.
func TestAnOrdinaryRipCarriesNoNote(t *testing.T) {
	orch, store, outputDir := setupOrchestratorWithScanner(t, &mockDriveExecutor{})

	orch.ManualRip(ManualRipParams{
		DriveIndex:      0,
		DiscName:        "DEADPOOL_2",
		OutputDir:       outputDir,
		DuplicateAction: "overwrite",
		Titles:          []TitleSelection{{TitleIndex: 0, TitleName: "Feature", SourceFile: "00800.mpls"}},
	})

	jobs, err := store.ListAllJobs(10, 0)
	if err != nil {
		t.Fatalf("ListAllJobs: %v", err)
	}
	if len(jobs) == 0 {
		t.Fatal("no job was created")
	}
	if jobs[0].SalvageNote != "" {
		t.Errorf("an ordinary rip was marked salvaged: %q", jobs[0].SalvageNote)
	}
}
