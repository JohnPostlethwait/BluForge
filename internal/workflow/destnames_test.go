package workflow

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/johnpostlethwait/bluforge/internal/db"
)

// Police Story 2 offers the feature twice: as the playlist 00000.mpls and as
// the raw stream 00000.m2ts. The destination name is the source file with its
// extension stripped, so both resolved to "POLICE STORY 2 4K UHD - 00000.mkv"
// and ripping both would have silently overwritten 67GB with 67GB.
func TestTitlesThatWouldShareAFilenameAreDistinguished(t *testing.T) {
	orch, _, _ := setupOrchestrator(t)

	params := ManualRipParams{
		DiscName: "POLICE STORY 2 4K UHD",
		Titles: []TitleSelection{
			{TitleIndex: 4, SourceFile: "00000.mpls"},
			{TitleIndex: 6, SourceFile: "00000.m2ts"},
		},
	}

	paths := orch.buildDestPaths(params)

	if len(paths) != 2 {
		t.Fatalf("got %d paths, want 2", len(paths))
	}
	if paths[0] == paths[1] {
		t.Fatalf("both titles resolve to %q; one would overwrite the other", paths[0])
	}
	// The disambiguation has to say which is which, or the user is left with two
	// files and no way to tell the playlist from the stream.
	if !strings.Contains(paths[0], "mpls") || !strings.Contains(paths[1], "m2ts") {
		t.Errorf("paths do not identify their source: %q and %q", paths[0], paths[1])
	}
}

// A title with no competition keeps the name it has always had. Renaming every
// rip to avoid a collision that is not happening would be a worse bug.
func TestASingleTitleKeepsItsOrdinaryName(t *testing.T) {
	orch, _, _ := setupOrchestrator(t)

	paths := orch.buildDestPaths(ManualRipParams{
		DiscName: "POLICE STORY 2 4K UHD",
		Titles:   []TitleSelection{{TitleIndex: 4, SourceFile: "00000.mpls"}},
	})

	if len(paths) != 1 {
		t.Fatalf("got %d paths, want 1", len(paths))
	}
	const want = "POLICE STORY 2 4K UHD/POLICE STORY 2 4K UHD - 00000.mkv"
	if paths[0] != want {
		t.Errorf("path = %q, want %q", paths[0], want)
	}
}

// Titles that never collided must be untouched even when others in the same
// batch do collide.
func TestOnlyTheCollidingTitlesAreRenamed(t *testing.T) {
	orch, _, _ := setupOrchestrator(t)

	paths := orch.buildDestPaths(ManualRipParams{
		DiscName: "POLICE STORY 2 4K UHD",
		Titles: []TitleSelection{
			{TitleIndex: 4, SourceFile: "00000.mpls"},
			{TitleIndex: 6, SourceFile: "00000.m2ts"},
			{TitleIndex: 2, SourceFile: "00002.mpls"},
		},
	})

	const want = "POLICE STORY 2 4K UHD/POLICE STORY 2 4K UHD - 00002.mkv"
	if paths[2] != want {
		t.Errorf("uninvolved title was renamed to %q, want %q", paths[2], want)
	}
}

// Matched titles are named from the media title and episode, which is a
// different source of collisions and not one this change invents a fix for —
// but two titles matched to the same episode must still not overwrite.
func TestMatchedTitlesThatCollideAreAlsoDistinguished(t *testing.T) {
	orch, _, _ := setupOrchestrator(t)

	paths := orch.buildDestPaths(ManualRipParams{
		DiscName:   "SOME_SHOW",
		MediaTitle: "Some Show",
		Titles: []TitleSelection{
			{TitleIndex: 0, SourceFile: "00001.mpls", TitleName: "S01E01 - Pilot"},
			{TitleIndex: 1, SourceFile: "00002.mpls", TitleName: "S01E01 - Pilot"},
		},
	})

	if paths[0] == paths[1] {
		t.Errorf("both episodes resolve to %q; one would overwrite the other", paths[0])
	}
}

// buildDestPaths is only worth anything if ManualRip uses it. Computing the
// names correctly and then ignoring them would leave the overwrite in place
// with a green test suite over it.
func TestManualRipWritesCollidingTitlesToSeparateFiles(t *testing.T) {
	orch, store, outputDir := setupOrchestrator(t)

	orch.ManualRip(ManualRipParams{
		DriveIndex:      0,
		DiscName:        "POLICE STORY 2 4K UHD",
		OutputDir:       outputDir,
		DuplicateAction: "overwrite",
		Titles: []TitleSelection{
			{TitleIndex: 4, SourceFile: "00000.mpls"},
			{TitleIndex: 7, SourceFile: "00000.m2ts"},
		},
	})

	var jobs []db.RipJob
	deadline := time.Now().Add(asyncDeadline)
	for time.Now().Before(deadline) {
		found, err := store.ListJobsByStatus("completed")
		if err != nil {
			t.Fatalf("ListJobsByStatus: %v", err)
		}
		if len(found) == 2 {
			jobs = found
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(jobs) != 2 {
		t.Fatalf("got %d completed jobs, want 2", len(jobs))
	}

	if jobs[0].OutputPath == jobs[1].OutputPath {
		t.Fatalf("both titles were written to %q; one overwrote the other", jobs[0].OutputPath)
	}
	for _, j := range jobs {
		if _, err := os.Stat(j.OutputPath); err != nil {
			t.Errorf("output missing for job %d: %v", j.ID, err)
		}
	}
}
