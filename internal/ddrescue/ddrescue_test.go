package ddrescue

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// recordingRunner captures the arguments ddrescue would have been given.
type recordingRunner struct {
	args  []string
	lines []string
	err   error
}

func (r *recordingRunner) Run(_ context.Context, args []string, onLine func(string)) error {
	r.args = args
	for _, l := range r.lines {
		if onLine != nil {
			onLine(l)
		}
	}
	return r.err
}

func (r *recordingRunner) arg(prefix string) (string, bool) {
	for _, a := range r.args {
		if strings.HasPrefix(a, prefix) {
			return a, true
		}
	}
	return "", false
}

func opts(t *testing.T) Options {
	t.Helper()
	dir := t.TempDir()
	return Options{
		Source:  filepath.Join(dir, "00000.m2ts"),
		Dest:    filepath.Join(dir, "out", "00000.m2ts"),
		MapFile: filepath.Join(dir, "00000.map"),
	}
}

// The rescue has to end. A scratch that has not yielded after a few passes will
// not yield after a thousand, and an unbounded run means a drive grinding all
// night for a few more frames.
func TestRescueIsBounded(t *testing.T) {
	r := &recordingRunner{}
	if err := Rescue(context.Background(), r, opts(t), nil); err != nil {
		t.Fatalf("Rescue: %v", err)
	}

	if _, ok := r.arg("--retry-passes="); !ok {
		t.Errorf("no retry cap in %v", r.args)
	}
	if _, ok := r.arg("--timeout="); !ok {
		t.Errorf("no timeout in %v", r.args)
	}
}

// --fill-mode is a separate ddrescue mode for writing a pattern over chosen
// blocks, not an option for a copy. Passing it during a rescue was wrong: an
// ordinary copy already leaves unreadable regions as holes that read back as
// zeros, which is exactly what makes MakeMKV's reads succeed.
func TestRescueDoesNotAskForFillMode(t *testing.T) {
	r := &recordingRunner{}
	if err := Rescue(context.Background(), r, opts(t), nil); err != nil {
		t.Fatalf("Rescue: %v", err)
	}
	if _, ok := r.arg("--fill-mode"); ok {
		t.Errorf("a copy was given --fill-mode: %v", r.args)
	}
}

// A backup that copied 48GB before it hit the damage should not be re-read from
// the beginning: that is fifty minutes of drive time for bytes already in hand.
func TestRescueResumesFromWhatIsAlreadyCopied(t *testing.T) {
	r := &recordingRunner{}
	o := opts(t)
	o.StartOffset = 48354557952
	if err := Rescue(context.Background(), r, o, nil); err != nil {
		t.Fatalf("Rescue: %v", err)
	}

	in, ok := r.arg("--input-position=")
	if !ok || !strings.HasSuffix(in, "48354557952") {
		t.Errorf("input position wrong or missing: %v", r.args)
	}
	// Both sides must move together, or the recovered bytes land at the wrong
	// place in the file and corrupt what was already good.
	out, ok := r.arg("--output-position=")
	if !ok || !strings.HasSuffix(out, "48354557952") {
		t.Errorf("output position wrong or missing: %v", r.args)
	}
}

// Rescuing the whole file must not pass a position at all, rather than zero.
func TestRescueFromTheStartPassesNoPosition(t *testing.T) {
	r := &recordingRunner{}
	if err := Rescue(context.Background(), r, opts(t), nil); err != nil {
		t.Fatalf("Rescue: %v", err)
	}
	if _, ok := r.arg("--input-position="); ok {
		t.Errorf("a whole-file rescue passed a position: %v", r.args)
	}
}

// The map file is what makes a rescue resumable. Without it a second attempt
// re-reads a disc that took an hour the first time.
func TestRescueRefusesWithoutAMapFile(t *testing.T) {
	o := opts(t)
	o.MapFile = ""
	err := Rescue(context.Background(), &recordingRunner{}, o, nil)
	if err == nil || !strings.Contains(err.Error(), "map file") {
		t.Errorf("err = %v, want a complaint about the map file", err)
	}
}

// The destination lives inside a backup tree that may not exist yet.
func TestRescueCreatesTheDestinationDirectory(t *testing.T) {
	o := opts(t)
	if err := Rescue(context.Background(), &recordingRunner{}, o, nil); err != nil {
		t.Fatalf("Rescue: %v", err)
	}
	if _, err := os.Stat(filepath.Dir(o.Dest)); err != nil {
		t.Errorf("destination directory was not created: %v", err)
	}
}

// Giving up on part of a scratched disc is the expected outcome, not a failure —
// reporting it as one would throw away the recovery we just spent an hour on.
func TestRescueTreatsBadAreasAsSuccess(t *testing.T) {
	r := &recordingRunner{lines: []string{
		"non-tried:       0 B,  bad-sector:   172032 B,    error rate:     146 B/s",
	}}
	if err := Rescue(context.Background(), r, opts(t), nil); err != nil {
		t.Errorf("a rescue with bad areas reported failure: %v", err)
	}
}

// A rescue that could not run at all is a real error.
func TestRescueReportsAFailureToRun(t *testing.T) {
	r := &recordingRunner{err: errors.New("executable file not found")}
	err := Rescue(context.Background(), r, opts(t), nil)
	if err == nil {
		t.Fatal("a ddrescue that never ran reported success")
	}
	if !strings.Contains(err.Error(), "00000.m2ts") {
		t.Errorf("error does not say which file: %v", err)
	}
}

// The running totals are what tell a user it is working.
func TestProgressReportsRescuedAndBadBytes(t *testing.T) {
	// Real ddrescue 1.27 output from the Rambo salvage. "bad areas" counts
	// regions and "bad-sector" measures them; confusing the two reported 38
	// bytes lost when the true figure was 168 kB.
	var rescued, bad, areas int64
	r := &recordingRunner{lines: []string{
		"non-tried:       0 B,  bad-sector:   172032 B,    error rate:     146 B/s",
		"  rescued:   68716 MB,   bad areas:       38,        run time:  1h 53m 44s",
	}}

	if err := Rescue(context.Background(), r, opts(t), func(p Progress) {
		if p.BytesRescued > 0 {
			rescued = p.BytesRescued
		}
		if p.BytesBad > 0 {
			bad = p.BytesBad
		}
		if p.BadAreas > 0 {
			areas = p.BadAreas
		}
	}); err != nil {
		t.Fatalf("Rescue: %v", err)
	}

	if rescued != 68716*1000*1000 {
		t.Errorf("BytesRescued = %d, want 68716 MB", rescued)
	}
	if bad != 172032 {
		t.Errorf("BytesBad = %d, want 172032 — the size, not the region count", bad)
	}
	if areas != 38 {
		t.Errorf("BadAreas = %d, want 38", areas)
	}
}

func TestProgressToleratesUnfamiliarOutput(t *testing.T) {
	p := parseProgress("GNU ddrescue 1.27")
	if p.BytesRescued != 0 || p.BytesBad != 0 {
		t.Errorf("invented numbers from a banner line: %+v", p)
	}
}

// Defaults have to be real values, or a caller that omits them gets an
// unbounded run.
func TestDefaultsAreSane(t *testing.T) {
	if DefaultRetries <= 0 {
		t.Error("DefaultRetries must be positive")
	}
	if DefaultTimeout < time.Minute {
		t.Errorf("DefaultTimeout = %s, too short to recover anything", DefaultTimeout)
	}
}
