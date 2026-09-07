package makemkv

import (
	"errors"
	"os/exec"
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

	guardErr, _, _ := streamRip(strings.NewReader(driftedEnumeration), 4, "00000.mpls", kill, nil, "disc:0")

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

// makemkvcon returns a nonzero exit only on a fatal error, and it prints that
// error as a plain line — not robot format — so ParseLine rejects it. Dropping
// it is how a fatal rip came to "report no reason" in the log. The reason has to
// reach onEvent (and thus the failure capture) instead of the floor.
func TestStreamRipKeepsAnUnparseableFatalLine(t *testing.T) {
	input := "MSG:3307,0,2,\"File 00001.mpls was added as title #5\",\"File %1 was added as title #%2\",\"00001.mpls\",\"5\"\n" +
		"Fatal error occurred, program will now exit.\n"

	var texts []string
	onEvent := func(ev Event) {
		if ev.Message != nil {
			texts = append(texts, ev.Message.Text)
		}
	}

	streamRip(strings.NewReader(input), 5, "00001.mpls", func() {}, onEvent, "disc:1")

	found := false
	for _, tx := range texts {
		if strings.Contains(tx, "Fatal error occurred") {
			found = true
		}
	}
	if !found {
		t.Errorf("the fatal reason was dropped; onEvent saw %v", texts)
	}
}

// makemkvcon announces where it wrote its debug log, and in -r mode that
// announcement arrives as a parsed MSG:1004 line, not as plaintext. streamRip
// has to take the log path from the parsed message's text; if it only watches
// for a plaintext line (which -r never emits) debugLogPath stays empty and a
// fatal exit is left with "no reason" — the exact hole that hid why Monty Python
// exited 12.
func TestStreamRipTakesDebugLogPathFromParsedAnnounce(t *testing.T) {
	const announce = `MSG:1004,0,1,"Debug logging enabled, log will be saved as file:///tmp/bf-home/MakeMKV_log.txt","%1"`

	_, _, debugLogPath := streamRip(strings.NewReader(announce), 0, "00001.mpls", func() {}, nil, "disc:1")

	if debugLogPath != "/tmp/bf-home/MakeMKV_log.txt" {
		t.Errorf("debugLogPath = %q, want the path parsed from the MSG:1004 announce", debugLogPath)
	}
}

// The obfuscated "DEBUG: Code N at <hash>" markers and the announce line arrive
// as parsed MSG lines in -r mode (code 1003 and 1004). They mean nothing to a
// person, so they must not reach onEvent — which feeds the failure capture shown
// on the activity page — any more than their plaintext forms do.
func TestStreamRipDropsParsedDebugNoise(t *testing.T) {
	input := `MSG:1003,0,1,"DEBUG: Code 0 at abc123","%1"` + "\n" +
		`MSG:1004,0,1,"Debug logging enabled, log will be saved as file:///tmp/bf/MakeMKV_log.txt","%1"` + "\n" +
		`MSG:5011,0,1,"Operation successfully completed","%1"` + "\n"

	var texts []string
	onEvent := func(ev Event) {
		if ev.Message != nil {
			texts = append(texts, ev.Message.Text)
		}
	}

	streamRip(strings.NewReader(input), 0, "00001.mpls", func() {}, onEvent, "disc:1")

	for _, tx := range texts {
		if strings.HasPrefix(tx, "DEBUG:") || strings.HasPrefix(tx, debugAnnouncePrefix) {
			t.Errorf("debug noise reached the failure capture: %q", tx)
		}
	}
	// A real message alongside the noise still has to get through.
	found := false
	for _, tx := range texts {
		if tx == "Operation successfully completed" {
			found = true
		}
	}
	if !found {
		t.Errorf("a real message was dropped along with the noise; onEvent saw %v", texts)
	}
}

// A correct rip must be left completely alone.
func TestStreamRipLeavesACorrectRipAlone(t *testing.T) {
	killed := false
	guardErr, copyFailed, _ := streamRip(strings.NewReader(driftedEnumeration), 3, "00000.mpls",
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

	guardErr, copyFailed, _ := streamRip(strings.NewReader(out), 0, "00005.mpls", func() {}, nil, "disc:0")

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

// makemkvcon exited 12 on Toy Story 4 and BluForge reported the bare "exit
// status 12" — a number that says nothing. When makemkvcon quits with a code
// and no message of its own, the error says so in words and names the code,
// without inventing a specific meaning for it.
func TestRipOutcomeExplainsANonzeroExit(t *testing.T) {
	waitErr := exec.Command("sh", "-c", "exit 12").Run() // a real *exec.ExitError, code 12
	if waitErr == nil {
		t.Fatal("expected a nonzero exit to produce an error")
	}

	err := ripOutcome(nil, waitErr, false, "disc:1", 3)
	if err == nil {
		t.Fatal("a nonzero exit was reported as success")
	}
	msg := err.Error()
	if !strings.Contains(msg, "12") {
		t.Errorf("the error does not name the exit code: %q", msg)
	}
	if !strings.Contains(strings.ToLower(msg), "exited") {
		t.Errorf("the error does not say makemkvcon exited: %q", msg)
	}
	// makemkvcon exits nonzero only on a fatal error, and it prints the reason.
	// The message must say "fatal" and point at the captured output — not claim
	// there was no reason, which was false and sent us looking in the wrong
	// place.
	if !strings.Contains(strings.ToLower(msg), "fatal") {
		t.Errorf("a nonzero exit is a fatal error and should say so: %q", msg)
	}
	if strings.Contains(strings.ToLower(msg), "no reason") {
		t.Errorf("the message still claims makemkvcon gave no reason: %q", msg)
	}
}

// A process stopped by a signal — a cancelled context, a timeout — is not the
// same as makemkvcon choosing to exit, and must stay recognisable as a stop
// rather than be dressed up as an exit code.
func TestRipOutcomeLeavesASignalStopRecognisable(t *testing.T) {
	cmd := exec.Command("sh", "-c", "sleep 10")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	_ = cmd.Process.Kill()
	waitErr := cmd.Wait() // "signal: killed"

	err := ripOutcome(nil, waitErr, false, "disc:1", 3)
	if err == nil || !strings.Contains(err.Error(), "signal") {
		t.Errorf("a signal stop was not left recognisable: %v", err)
	}
}
