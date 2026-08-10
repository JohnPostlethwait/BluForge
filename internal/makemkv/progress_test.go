package makemkv

import (
	"testing"
	"time"
)

var progressEpoch = time.Date(2026, 8, 10, 20, 15, 0, 0, time.UTC)

// Observed on a real backup: makemkvcon reported 0% then 100% within 100ms,
// during a preliminary phase, before copying 95GB. A monotonic decile tracker
// latches at 100 and then logs nothing for the entire copy — silencing the
// heartbeat exactly when it is needed.
func TestProgressTrackerSurvivesAPreliminaryHundredPercent(t *testing.T) {
	tr := newProgressTracker()

	if !tr.shouldLog(0, progressEpoch) {
		t.Fatal("first progress was not logged")
	}
	if !tr.shouldLog(100, progressEpoch.Add(100*time.Millisecond)) {
		t.Fatal("jump to 100 was not logged")
	}

	// The real copy now starts over from zero.
	if !tr.shouldLog(0, progressEpoch.Add(4*time.Second)) {
		t.Error("restart at 0%% was not treated as a new phase")
	}
	if !tr.shouldLog(10, progressEpoch.Add(10*time.Minute)) {
		t.Error("progress after a phase restart was suppressed")
	}
}

func TestProgressTrackerLogsEachDecile(t *testing.T) {
	tr := newProgressTracker()
	now := progressEpoch

	logged := 0
	for pct := 0; pct <= 100; pct++ {
		now = now.Add(time.Second)
		if tr.shouldLog(pct, now) {
			logged++
		}
	}
	// 0,10,...,100
	if logged != 11 {
		t.Errorf("logged %d times over a full run, want 11 (one per decile)", logged)
	}
}

// A backup that is progressing slowly must still produce log lines, or there is
// no way to tell it from one that has hung.
func TestProgressTrackerHeartbeatsWhenStuckInADecile(t *testing.T) {
	tr := newProgressTracker()
	now := progressEpoch

	if !tr.shouldLog(30, now) {
		t.Fatal("first progress not logged")
	}
	if tr.shouldLog(31, now.Add(10*time.Second)) {
		t.Error("logged again within the same decile moments later")
	}
	if !tr.shouldLog(32, now.Add(progressHeartbeat+time.Second)) {
		t.Error("no heartbeat after a long stretch inside one decile")
	}
}

func TestProgressTrackerIgnoresNegative(t *testing.T) {
	tr := newProgressTracker()
	if tr.shouldLog(-1, progressEpoch) {
		t.Error("logged a negative percentage")
	}
}
