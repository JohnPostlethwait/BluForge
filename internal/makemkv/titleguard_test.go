package makemkv

import (
	"errors"
	"strings"
	"testing"
)

// added builds the enumeration line makemkvcon emits before it copies anything.
func added(source string, index int) Event {
	return Event{Type: "MSG", Message: &Message{
		Code:   3307,
		Text:   "File " + source + " was added as title #" + itoa(index),
		Format: "File %1 was added as title #%2",
		Params: []string{source, itoa(index)},
	}}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

func saving() Event {
	return Event{Type: "MSG", Message: &Message{
		Code: 5014, Text: "Saving 1 titles into directory /out",
	}}
}

// Police Story 2, 2026-08-12. The first rip failed on 00005.mpls; every rip
// after it skipped that title during enumeration, shifting all the numbers down
// by one. BluForge asked for index 4 believing it was the 67GB feature and got
// 00006.m2ts, a damaged 118MB title, filed under the feature's name. Three of
// four titles were wrong and nothing noticed.
func TestGuardCatchesATitleThatMovedIndex(t *testing.T) {
	g := newTitleGuard(4, "00000.mpls")

	for _, ev := range []Event{
		added("00003.mpls", 0),
		added("00002.mpls", 1),
		added("00001.mpls", 2),
		added("00000.mpls", 3), // the feature is at 3 now, not 4
		added("00006.m2ts", 4),
		added("00005.m2ts", 5),
	} {
		g.observe(ev)
	}

	err := g.verdict()
	if err == nil {
		t.Fatal("the guard allowed a rip of the wrong title")
	}

	var moved *TitleMovedError
	if !errors.As(err, &moved) {
		t.Fatalf("error is %T, want *TitleMovedError", err)
	}
	if moved.Found != "00006.m2ts" {
		t.Errorf("Found = %q, want 00006.m2ts", moved.Found)
	}
	if moved.CorrectIndex != 3 {
		t.Errorf("CorrectIndex = %d, want 3 (where the feature actually is)", moved.CorrectIndex)
	}
	if !strings.Contains(err.Error(), "00000.mpls") {
		t.Errorf("error does not name the title asked for: %v", err)
	}
}

// The ordinary case must not be disturbed: on a disc that enumerates the same
// way twice, every rip proceeds.
func TestGuardAllowsAStableEnumeration(t *testing.T) {
	g := newTitleGuard(1, "00003.mpls")
	g.observe(added("00005.mpls", 0))
	g.observe(added("00003.mpls", 1))
	g.observe(added("00002.mpls", 2))

	if err := g.verdict(); err != nil {
		t.Errorf("guard blocked a correct rip: %v", err)
	}
}

// A title that is not in this pass at all cannot be ripped by any index. Saying
// so beats ripping whatever happens to hold that number.
func TestGuardReportsATitleThatIsGoneEntirely(t *testing.T) {
	g := newTitleGuard(0, "00005.mpls")
	g.observe(added("00003.mpls", 0))
	g.observe(added("00002.mpls", 1))
	if err := g.verdict(); err != nil {
		t.Fatalf("gave up while the enumeration was still arriving: %v", err)
	}

	g.observe(saving())
	err := g.verdict()
	var moved *TitleMovedError
	if !errors.As(err, &moved) {
		t.Fatalf("error is %T, want *TitleMovedError", err)
	}
	if moved.CorrectIndex != -1 {
		t.Errorf("CorrectIndex = %d, want -1 for a title that is not present", moved.CorrectIndex)
	}
}

// A caller that does not know which title it expects gets the old behaviour
// rather than a refusal.
func TestGuardWithoutAnExpectationAllowsTheRip(t *testing.T) {
	g := newTitleGuard(2, "")
	g.observe(added("00003.mpls", 0))

	if err := g.verdict(); err != nil {
		t.Errorf("guard blocked a rip it had no expectation for: %v", err)
	}
}

// v0.4.5 failed a correct rip of the Police Story 2 feature within seconds of
// starting: progress was treated as the start of copying, but makemkvcon
// reports progress through its preliminary phases too, so the guard ruled on an
// empty enumeration and called the title absent before it had been announced.
func TestGuardDoesNotRuleOnAnEnumerationStillArriving(t *testing.T) {
	g := newTitleGuard(4, "00000.mpls")

	g.observe(Event{Type: "PRGV", Progress: &Progress{Current: 1, Total: 100, Max: 100}})
	if err := g.verdict(); err != nil {
		t.Fatalf("ruled before any title was announced: %v", err)
	}

	// Titles arrive one at a time over several minutes; none of these is proof.
	g.observe(added("00003.mpls", 0))
	g.observe(Event{Type: "PRGV", Progress: &Progress{Current: 50, Total: 100, Max: 100}})
	g.observe(added("00002.mpls", 1))
	if err := g.verdict(); err != nil {
		t.Fatalf("ruled on a partial enumeration: %v", err)
	}

	// The requested index turning up as another title is proof.
	g.observe(added("00001.mpls", 2))
	g.observe(added("00000.mpls", 3))
	if err := g.verdict(); err == nil {
		t.Error("did not object once the feature appeared at another index")
	}
}

// A title genuinely missing is only knowable once the enumeration is over,
// which is when copying starts.
func TestGuardCallsATitleAbsentOnlyOnceCopyingBegins(t *testing.T) {
	g := newTitleGuard(4, "00000.mpls")
	g.observe(added("00003.mpls", 0))
	if err := g.verdict(); err != nil {
		t.Fatalf("called the title absent while the enumeration ran: %v", err)
	}

	g.observe(saving())
	err := g.verdict()
	if err == nil {
		t.Fatal("did not object to an index that was never announced")
	}
	var moved *TitleMovedError
	if !errors.As(err, &moved) || moved.CorrectIndex != -1 {
		t.Errorf("got %v, want an absent title with CorrectIndex -1", err)
	}
}

// The whole run of a correct rip must pass without objection, progress and all.
func TestGuardStaysSilentThroughACorrectRip(t *testing.T) {
	g := newTitleGuard(3, "00000.mpls")
	for _, ev := range []Event{
		{Type: "PRGV", Progress: &Progress{Current: 1, Total: 100, Max: 100}},
		added("00003.mpls", 0),
		added("00002.mpls", 1),
		added("00001.mpls", 2),
		added("00000.mpls", 3),
		saving(),
		{Type: "PRGV", Progress: &Progress{Current: 100, Total: 100, Max: 100}},
	} {
		g.observe(ev)
		if err := g.verdict(); err != nil {
			t.Fatalf("objected to a correct rip: %v", err)
		}
	}
}

// The enumeration is read from the message parameters, not the English text.
func TestGuardReadsParametersNotProse(t *testing.T) {
	g := newTitleGuard(0, "00003.mpls")
	g.observe(Event{Type: "MSG", Message: &Message{
		Code:   3307,
		Text:   "Datei 00003.mpls wurde als Titel #0 hinzugefügt",
		Format: "File %1 was added as title #%2",
		Params: []string{"00003.mpls", "0"},
	}})

	if err := g.verdict(); err != nil {
		t.Errorf("localized enumeration was not understood: %v", err)
	}
}

// Attribute 16 is the playlist name on a UHD disc but can be a segment list
// like "1,2,3" on standard Blu-ray. That never appears in the enumeration, so
// enforcing it would fail every rip on those discs — the guard must recognise
// an expectation it cannot check and stand aside.
func TestGuardIgnoresAnExpectationItCannotCheck(t *testing.T) {
	for _, expect := range []string{"1,2,3", "1", "", "Title 1"} {
		g := newTitleGuard(0, expect)
		g.observe(added("00003.mpls", 0))
		if err := g.verdict(); err != nil {
			t.Errorf("guard blocked a rip on an uncheckable expectation %q: %v", expect, err)
		}
	}
}

// A real source file is still checked.
func TestGuardChecksAnythingThatNamesAFile(t *testing.T) {
	for _, expect := range []string{"00000.mpls", "00004.m2ts"} {
		g := newTitleGuard(0, expect)
		g.observe(added("00003.mpls", 0))
		g.observe(saving())
		if err := g.verdict(); err == nil {
			t.Errorf("guard allowed the wrong title for expectation %q", expect)
		}
	}
}

// Police Story 2, second attempt. The scan put the feature at index 3, but by
// rip time 00005.mpls had become readable again and pushed everything down one.
// Index 3 was announced as 00001.mpls -- proof the request was wrong -- while
// the feature was announced at 4 two lines later. Aborting on the first proof
// reported "not in this pass" and skipped the retry that would have ripped it.
func TestGuardWaitsToLearnWhereTheTitleWent(t *testing.T) {
	g := newTitleGuard(3, "00000.mpls")

	g.observe(added("00005.mpls", 0))
	g.observe(added("00003.mpls", 1))
	g.observe(added("00002.mpls", 2))
	g.observe(added("00001.mpls", 3)) // proof the requested index is wrong
	if err := g.verdict(); err != nil {
		t.Fatalf("gave up before learning where the feature went: %v", err)
	}

	g.observe(added("00000.mpls", 4))
	err := g.verdict()
	if err == nil {
		t.Fatal("did not object once the drift was fully known")
	}
	var moved *TitleMovedError
	if !errors.As(err, &moved) {
		t.Fatalf("error is %T, want *TitleMovedError", err)
	}
	if moved.CorrectIndex != 4 {
		t.Errorf("CorrectIndex = %d, want 4 — without it there is no retry", moved.CorrectIndex)
	}
	if moved.Found != "00001.mpls" {
		t.Errorf("Found = %q, want 00001.mpls", moved.Found)
	}
}
