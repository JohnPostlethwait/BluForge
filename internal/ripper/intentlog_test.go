package ripper

import (
	"log/slog"
	"strings"
	"testing"
	"time"
)

// Diagnosing the Kiki's Delivery Service rip meant noticing that a log line
// saying title=1 sat three lines away from a directory named t4-, and that the
// two disagreed. Nothing stated what the rip was *for*, so a rip doing the
// wrong thing looked exactly like a rip doing the right thing.
//
// A rip says what it intends before it starts: which job, which title index,
// which playlist, and how big the scan said that title was. Any of those
// disagreeing with what follows is then visible in one line rather than
// inferred from two.
func TestARipStatesItsIntentBeforeItStarts(t *testing.T) {
	cap := &levelCapture{}
	prev := slog.Default()
	slog.SetDefault(slog.New(cap))
	defer slog.SetDefault(prev)

	engine := NewEngine(&failingRipExecutor{})
	job := NewJob(0, 3, "KIKIS_DELIVERY_SERVICE_BD", "/output")
	job.SourceFile = "00200.mpls"
	job.TrackMetadata = TrackMetadata{SizeBytes: 29746000000}

	if err := engine.Submit(job); err != nil {
		t.Fatalf("submit failed: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && job.Snapshot().Status != StatusFailed {
		time.Sleep(5 * time.Millisecond)
	}

	line := cap.findAt(slog.LevelInfo, "rip: starting")
	if line == "" {
		t.Fatal("a rip started without stating what it was for")
	}
	for _, want := range []string{
		"00200.mpls",  // the playlist we mean to copy
		"title_index", // the number we are about to hand makemkvcon
		"29746000000", // what the scan said that title weighs
		"KIKIS_DELIVERY_SERVICE_BD",
	} {
		if !strings.Contains(line, want) {
			t.Errorf("the intent line does not carry %q:\n%s", want, line)
		}
	}
}
