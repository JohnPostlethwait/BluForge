package workflow

import (
	"testing"

	"github.com/johnpostlethwait/bluforge/internal/organizer"
)

// The naming function is the single authority for what a title's file is
// called. The review page shows what it returns and the rip writes what it
// returns; there is no second implementation to disagree with it.

// Kiki's Delivery Service announces both angles of its feature as 00200.mpls.
// Two titles, one source name — and the output must still be two distinct
// files, because a name BluForge generates itself may never collide with
// another name BluForge generates itself.
func TestOutputPathsGivesTwoAnglesDistinctNames(t *testing.T) {
	titles := []TitleSelection{
		{TitleIndex: 3, SourceFile: "00200.mpls"},
		{TitleIndex: 4, SourceFile: "00200.mpls"},
	}

	got := OutputPaths(organizer.New(), "KIKIS_DELIVERY_SERVICE_BD", "", titles)

	if len(got) != 2 {
		t.Fatalf("got %d paths, want 2", len(got))
	}
	if got[3] == got[4] {
		t.Fatalf("both angles resolved to the same file: %q", got[3])
	}
}

// Same input, same names, every time. The review page shows what this returns
// and that same string is carried into the rip and written verbatim, so the two
// can never disagree — but only if the function itself is deterministic.
func TestOutputPathsAreDeterministic(t *testing.T) {
	titles := []TitleSelection{
		{TitleIndex: 4, SourceFile: "00200.mpls"},
		{TitleIndex: 3, SourceFile: "00200.mpls"},
		{TitleIndex: 1, SourceFile: "00300.mpls"},
	}

	first := OutputPaths(organizer.New(), "KIKIS_DELIVERY_SERVICE_BD", "", titles)
	second := OutputPaths(organizer.New(), "KIKIS_DELIVERY_SERVICE_BD", "", titles)

	for idx, p := range first {
		if second[idx] != p {
			t.Errorf("title %d named %q then %q on identical input", idx, p, second[idx])
		}
	}
}

// The review form sends title_name = outputName || sourceFile (drive_detail.templ),
// so an unmatched title reaches naming with TitleName == SourceFile, both set.
// This is what the real submission looks like — and it is where the collision
// actually lived: the disambiguator modified TitleName while the unmatched name
// is built from SourceFile, so the suffix did nothing and both angles kept the
// same name. The earlier test missed it by passing an empty TitleName no real
// request has.
func TestTwoAnglesCollideEvenWhenTitleNameEqualsSourceFile(t *testing.T) {
	titles := []TitleSelection{
		{TitleIndex: 3, SourceFile: "00200.mpls", TitleName: "00200.mpls"},
		{TitleIndex: 4, SourceFile: "00200.mpls", TitleName: "00200.mpls"},
	}

	got := OutputPaths(organizer.New(), "KIKIS_DELIVERY_SERVICE_BD", "", titles)

	if got[3] == got[4] {
		t.Fatalf("both angles resolved to the same file: %q", got[3])
	}
}

// An ordinary disc, every source unique, gets plain names with no suffixes.
func TestOutputPathsLeaveUniqueTitlesPlain(t *testing.T) {
	titles := []TitleSelection{
		{TitleIndex: 1, SourceFile: "00300.mpls"},
		{TitleIndex: 3, SourceFile: "00200.mpls"},
	}

	got := OutputPaths(organizer.New(), "KIKIS_DELIVERY_SERVICE_BD", "", titles)

	if want := "KIKIS_DELIVERY_SERVICE_BD/KIKIS_DELIVERY_SERVICE_BD - 00300.mkv"; got[1] != want {
		t.Errorf("got[1] = %q, want %q", got[1], want)
	}
	if want := "KIKIS_DELIVERY_SERVICE_BD/KIKIS_DELIVERY_SERVICE_BD - 00200.mkv"; got[3] != want {
		t.Errorf("got[3] = %q, want %q", got[3], want)
	}
}
