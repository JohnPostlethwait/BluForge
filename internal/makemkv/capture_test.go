package makemkv

import (
	"strings"
	"testing"
)

func msg(code int, text string) Event {
	return Event{Type: "MSG", Message: &Message{Code: code, Text: text}}
}

// A rip that fails is a rip whose messages nobody kept. They were parsed,
// logged, and dropped; when the rip of Kiki's Delivery Service failed, the
// enumeration that explained it survived only because it happened to be in the
// container's log.
func TestCaptureKeepsTheMessagesOfAnOperation(t *testing.T) {
	c := NewMessageCapture(100)
	c.Observe(msg(3307, "File 00303.mpls was added as title #0"))
	c.Observe(msg(3308, "File 00200.mpls (angle 1) was added as title #3"))

	got := c.Result()
	if len(got) != 2 {
		t.Fatalf("captured %d messages, want 2: %+v", len(got), got)
	}
	if got[0].Text != "File 00303.mpls was added as title #0" {
		t.Errorf("first message is %q", got[0].Text)
	}
	if got[0].Code != 3307 {
		t.Errorf("first code is %d, want 3307", got[0].Code)
	}
}

// Order is the point. The enumeration tells you which title landed at which
// index, which is only readable in the order MakeMKV announced them.
func TestCapturePreservesTheOrderMessagesArrived(t *testing.T) {
	c := NewMessageCapture(100)
	for _, text := range []string{"first", "second", "third"} {
		c.Observe(msg(3307, text))
	}

	got := c.Result()
	for i, want := range []string{"first", "second", "third"} {
		if got[i].Text != want {
			t.Errorf("position %d is %q, want %q", i, got[i].Text, want)
		}
	}
}

// A disc with damaged sectors emits the same read error once per retry, for
// thousands of retries. Keeping each one would bury the handful of lines that
// say what happened and would grow without limit during a rip that runs for
// half an hour. Collapsing to a count is what ScanOutput already does with a
// scan's messages.
func TestCaptureCollapsesRepeatsIntoACount(t *testing.T) {
	c := NewMessageCapture(100)
	c.Observe(msg(3307, "File 00303.mpls was added as title #0"))
	for range 500 {
		c.Observe(msg(2003, "Error 'Scsi error' occurred while reading '/BDMV/STREAM/00018.m2ts'"))
	}

	got := c.Result()
	if len(got) != 2 {
		t.Fatalf("captured %d distinct messages, want 2: %+v", len(got), got)
	}
	if got[1].Count != 500 {
		t.Errorf("the repeated error was counted %d times, want 500", got[1].Count)
	}
}

// Progress is the volume — thousands of events carrying nothing that explains a
// failure, and already reported by the decile logging.
func TestCaptureIgnoresProgressEvents(t *testing.T) {
	c := NewMessageCapture(100)
	c.Observe(Event{Type: "PRGV", Progress: &Progress{Current: 1, Total: 50, Max: 100}})
	c.Observe(msg(3307, "File 00303.mpls was added as title #0"))

	if got := c.Result(); len(got) != 1 {
		t.Errorf("captured %d messages, want only the MSG: %+v", len(got), got)
	}
}

// The cap is a safety valve against a disc nobody has met yet, so what it keeps
// matters: the enumeration arrives first and is what diagnoses a title that
// moved. Dropping the oldest to make room would discard exactly that.
func TestCaptureKeepsTheEarliestMessagesWhenItFillsUp(t *testing.T) {
	c := NewMessageCapture(3)
	for _, text := range []string{"first", "second", "third", "fourth", "fifth"} {
		c.Observe(msg(3307, text))
	}

	got := c.Result()
	if len(got) != 3 {
		t.Fatalf("captured %d messages, want the cap of 3: %+v", len(got), got)
	}
	if got[0].Text != "first" {
		t.Errorf("the earliest message was dropped: first entry is %q", got[0].Text)
	}
	if c.Dropped() != 2 {
		t.Errorf("Dropped() = %d, want 2", c.Dropped())
	}
}

// A count that is already being tracked must keep counting after the cap is
// reached: the cap limits distinct messages, not the tally of a known one.
func TestCaptureKeepsCountingKnownMessagesAfterTheCap(t *testing.T) {
	c := NewMessageCapture(1)
	c.Observe(msg(2003, "read error"))
	c.Observe(msg(3307, "something else"))
	c.Observe(msg(2003, "read error"))

	got := c.Result()
	if len(got) != 1 {
		t.Fatalf("captured %d messages, want 1: %+v", len(got), got)
	}
	if got[0].Count != 2 {
		t.Errorf("Count = %d, want 2", got[0].Count)
	}
	if c.Dropped() != 1 {
		t.Errorf("Dropped() = %d, want 1 for the distinct message turned away", c.Dropped())
	}
}

// Toy Story 4's failure capture was ~80 "title too short, skipped" lines and
// nothing else — the disclosure meant to explain a failure was pure noise. The
// per-title skip notices are dropped from the capture; the enumeration and any
// real error, which are the signal, are kept.
func TestCaptureDropsPerTitleSkipNoise(t *testing.T) {
	c := NewMessageCapture(200)
	c.Observe(msg(3025, "Title #00019.m2ts has length of 8 seconds ... was therefore skipped"))
	c.Observe(msg(3309, "Title 00004.mpls is equal to title 00800.mpls and was skipped"))
	c.Observe(msg(3016, "Title #00005.mpls was skipped"))
	c.Observe(msg(3307, "File 00800.mpls was added as title #3"))
	c.Observe(msg(2003, "Error 'Scsi error' occurred while reading '/BDMV/STREAM/00800.m2ts'"))

	got := c.Result()
	if len(got) != 2 {
		t.Fatalf("captured %d messages, want 2 (enumeration + error): %+v", len(got), got)
	}
	texts := got[0].Text + "\n" + got[1].Text
	if !strings.Contains(texts, "added as title") {
		t.Errorf("the enumeration was dropped: %+v", got)
	}
	if !strings.Contains(texts, "Scsi error") {
		t.Errorf("the read error was dropped: %+v", got)
	}
	for _, g := range got {
		if strings.Contains(g.Text, "skipped") {
			t.Errorf("a per-title skip notice was kept: %q", g.Text)
		}
	}
}
