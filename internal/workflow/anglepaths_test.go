package workflow

import (
	"testing"

	"github.com/johnpostlethwait/bluforge/internal/organizer"
)

// Kiki's Delivery Service announces both angles of its feature as 00200.mpls,
// so two selected titles carry the same source file and differ only by index.
// Both resolved to KIKIS_DELIVERY_SERVICE_BD - 00200.mkv, and both jobs were
// filed at that one path.
//
// A name BluForge generates itself must never collide with another name
// BluForge generates itself. Whatever else is wrong, that is ours to get right.
func TestTwoAnglesOfOnePlaylistGetDistinctPaths(t *testing.T) {
	o := &Orchestrator{organizer: organizer.New()}
	params := ManualRipParams{
		DiscName:  "KIKIS_DELIVERY_SERVICE_BD",
		OutputDir: "/output",
		Titles: []TitleSelection{
			{TitleIndex: 3, SourceFile: "00200.mpls"},
			{TitleIndex: 4, SourceFile: "00200.mpls"},
		},
	}

	paths := o.buildDestPaths(params)

	if len(paths) != 2 {
		t.Fatalf("got %d paths, want 2", len(paths))
	}
	if paths[0] == paths[1] {
		t.Fatalf("both angles resolved to the same file: %q", paths[0])
	}
}

// The ordinary case must not start growing suffixes because a disc elsewhere
// in the list has angles.
func TestTitlesThatDoNotCollideKeepTheirPlainNames(t *testing.T) {
	o := &Orchestrator{organizer: organizer.New()}
	params := ManualRipParams{
		DiscName:  "KIKIS_DELIVERY_SERVICE_BD",
		OutputDir: "/output",
		Titles: []TitleSelection{
			{TitleIndex: 1, SourceFile: "00300.mpls"},
			{TitleIndex: 3, SourceFile: "00200.mpls"},
			{TitleIndex: 4, SourceFile: "00200.mpls"},
		},
	}

	paths := o.buildDestPaths(params)

	seen := map[string]bool{}
	for i, p := range paths {
		if seen[p] {
			t.Errorf("path %d duplicates an earlier one: %q", i, p)
		}
		seen[p] = true
	}
	if want := "KIKIS_DELIVERY_SERVICE_BD/KIKIS_DELIVERY_SERVICE_BD - 00300.mkv"; paths[0] != want {
		t.Errorf("the uncontested title was renamed:\n got %q\nwant %q", paths[0], want)
	}
}
