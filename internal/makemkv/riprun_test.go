package makemkv

import (
	"errors"
	"strings"
	"testing"
)

// The enumeration Police Story 2 produced on its second and later rips, where
// 00005.mpls had dropped out and everything below it shifted down one.
const driftedEnumeration = `MSG:3016,0,1,"Title #00005.mpls was skipped","%1","00005.mpls"
MSG:3307,0,2,"File 00003.mpls was added as title #0","File %1 was added as title #%2","00003.mpls","0"
MSG:3307,0,2,"File 00002.mpls was added as title #1","File %1 was added as title #%2","00002.mpls","1"
MSG:3307,0,2,"File 00001.mpls was added as title #2","File %1 was added as title #%2","00001.mpls","2"
MSG:3307,0,2,"File 00000.mpls was added as title #3","File %1 was added as title #%2","00000.mpls","3"
MSG:3307,0,2,"File 00006.m2ts was added as title #4","File %1 was added as title #%2","00006.m2ts","4"
MSG:5014,0,2,"Saving 1 titles into directory /out","%1","1"
PRGV:100,100,100
MSG:5036,0,1,"Copy complete. 1 titles saved.","%1","1"`

// Every guard test could pass while StartRip never consulted the guard, and the
// wrong title would still be ripped. This is the wiring that matters: the rip
// has to be stopped, and stopped before the copy begins.
func TestStreamRipStopsAMovedTitleBeforeCopying(t *testing.T) {
	killed := false
	kill := func() { killed = true }

	guardErr, _ := streamRip(strings.NewReader(driftedEnumeration), 4, "00000.mpls", kill, nil, "disc:0")

	if guardErr == nil {
		t.Fatal("the rip was allowed to proceed at an index holding another title")
	}
	if !killed {
		t.Error("makemkvcon was not killed; it would have written the wrong title")
	}

	var moved *TitleMovedError
	if !errors.As(guardErr, &moved) {
		t.Fatalf("error is %T, want *TitleMovedError", guardErr)
	}
	if moved.CorrectIndex != 3 {
		t.Errorf("CorrectIndex = %d, want 3", moved.CorrectIndex)
	}
}

// The kill has to land while the enumeration is still being read. Deciding at
// the end of the stream would be deciding after the file was written.
func TestStreamRipKillsBeforeTheCopyEvents(t *testing.T) {
	var seen []string
	kill := func() { seen = append(seen, "KILL") }
	onEvent := func(ev Event) {
		if ev.Type == "MSG" && ev.Message != nil && ev.Message.Code == 5014 {
			seen = append(seen, "SAVING")
		}
	}

	streamRip(strings.NewReader(driftedEnumeration), 4, "00000.mpls", kill, onEvent, "disc:0")

	if len(seen) < 2 || seen[0] != "KILL" {
		t.Errorf("event order was %v; the kill must precede the save", seen)
	}
}

// A correct rip must be left completely alone.
func TestStreamRipLeavesACorrectRipAlone(t *testing.T) {
	killed := false
	guardErr, copyFailed := streamRip(strings.NewReader(driftedEnumeration), 3, "00000.mpls",
		func() { killed = true }, nil, "disc:0")

	if guardErr != nil {
		t.Errorf("a correct rip was blocked: %v", guardErr)
	}
	if killed {
		t.Error("a correct rip was killed")
	}
	if copyFailed {
		t.Error("a successful copy was reported as saving nothing")
	}
}

// "Copy complete. 0 titles saved, 1 failed" with exit code zero is what made a
// 14-minute run that wrote nothing get logged as a success.
func TestStreamRipNoticesACopyThatSavedNothing(t *testing.T) {
	const out = `MSG:3307,0,2,"File 00005.mpls was added as title #0","File %1 was added as title #%2","00005.mpls","0"
MSG:5004,0,2,"0 titles saved, 1 failed","%1","0"
MSG:5037,0,2,"Copy complete. 0 titles saved, 1 failed.","%1","0"`

	guardErr, copyFailed := streamRip(strings.NewReader(out), 0, "00005.mpls", func() {}, nil, "disc:0")

	if guardErr != nil {
		t.Errorf("unexpected guard objection: %v", guardErr)
	}
	if !copyFailed {
		t.Error("a copy that saved nothing was not noticed")
	}
}

// The reason has to survive the symptom: killing the process makes it exit with
// "signal: killed", which says nothing about why.
func TestRipOutcomePrefersTheReasonOverTheSignal(t *testing.T) {
	moved := &TitleMovedError{Requested: 4, Expected: "00000.mpls", Found: "00006.m2ts", CorrectIndex: 3}

	err := ripOutcome(moved, errors.New("signal: killed"), false, "disc:0", 4)

	if !errors.Is(err, error(moved)) {
		t.Errorf("got %v, want the title-moved reason", err)
	}
	if strings.Contains(err.Error(), "signal: killed") {
		t.Errorf("the kernel's symptom leaked into the reason: %v", err)
	}
}

// A rip that saved nothing must say that, not merely fail to produce a file
// somewhere further down the pipeline.
func TestRipOutcomeReportsAnEmptyCopy(t *testing.T) {
	err := ripOutcome(nil, nil, true, "disc:0", 0)
	if err == nil {
		t.Fatal("a copy that saved no titles was reported as success")
	}
	if !strings.Contains(err.Error(), "saved no titles") {
		t.Errorf("error does not say what happened: %v", err)
	}

	// "saved no titles" is load-bearing beyond its reading: salvageable() in
	// internal/web keys the salvage button off this substring, so rewording it
	// away removes the button and nothing fails to say so.
	if !salvageable(err.Error()) {
		t.Errorf("the wording no longer marks this failure as salvageable: %v", err)
	}

	// MakeMKV reported that the copy failed and saved nothing. Why is not
	// something this knows: a rip saves nothing when the guard stops it, when
	// the selection matches no track, and when the disc really is unreadable.
	if strings.Contains(strings.ToLower(err.Error()), "drive") {
		t.Errorf("the error blames the drive for something it did not observe: %v", err)
	}
}

// salvageable mirrors the substring match in internal/web/handlers_activity.go.
// Duplicated rather than imported because that function is unexported and web
// imports makemkv, not the other way round; if the two drift, the test that
// notices is this one.
func salvageable(errMessage string) bool {
	msg := strings.ToLower(errMessage)
	for _, sign := range []string{"saved no titles", "could not read it", "no .mkv file found"} {
		if strings.Contains(msg, sign) {
			return true
		}
	}
	return false
}

// An ordinary failure is still reported as itself.
func TestRipOutcomePassesAnOrdinaryFailureThrough(t *testing.T) {
	err := ripOutcome(nil, errors.New("exit status 1"), false, "disc:0", 2)
	if err == nil || !strings.Contains(err.Error(), "exit status 1") {
		t.Errorf("got %v, want the underlying failure", err)
	}
}

// And a clean rip returns nothing at all.
func TestRipOutcomeIsSilentOnSuccess(t *testing.T) {
	if err := ripOutcome(nil, nil, false, "disc:0", 1); err != nil {
		t.Errorf("a clean rip reported %v", err)
	}
}
