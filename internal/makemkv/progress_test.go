package makemkv

import (
	"context"
	"strings"
	"sync"
	"testing"
)

// PRGC/PRGT name the operation makemkvcon is on. They were parsed and thrown
// away, which left nothing to tell the user during a scan that says nothing
// else for thirty minutes.
func TestParseKeepsTheOperationName(t *testing.T) {
	ev, err := ParseLine(`PRGC:5018,1,"Analyzing seamless segments"`)
	if err != nil {
		t.Fatalf("ParseLine: %v", err)
	}
	if ev.Operation != "Analyzing seamless segments" {
		t.Errorf("Operation = %q, want %q", ev.Operation, "Analyzing seamless segments")
	}
}

func TestParseKeepsTheTotalOperationName(t *testing.T) {
	ev, err := ParseLine(`PRGT:5017,0,"Scanning CD-ROM devices"`)
	if err != nil {
		t.Fatalf("ParseLine: %v", err)
	}
	if ev.Operation != "Scanning CD-ROM devices" {
		t.Errorf("Operation = %q, want %q", ev.Operation, "Scanning CD-ROM devices")
	}
}

// A malformed progress line must not fail the scan; it carries no data worth
// aborting for.
func TestParseToleratesAProgressLineWithoutAName(t *testing.T) {
	if _, err := ParseLine(`PRGC:5018`); err != nil {
		t.Errorf("ParseLine: %v", err)
	}
}

// ScanDiscWithProgress is what the orchestrator needs: a drive scan that
// narrates itself. Without it the streaming path exists but nothing reaches it.
func TestScanDiscWithProgressReportsEvents(t *testing.T) {
	runner := &streamingRunner{lines: []string{
		`PRGT:5017,0,"Scanning CD-ROM devices"`,
		`MSG:2003,0,1,"Error occurred while reading","%1","x"`,
		`TCOUNT:1`,
		`CINFO:2,0,"SOME_DISC"`,
		`TINFO:0,2,0,"Feature"`,
		`TINFO:0,16,0,"00800.mpls"`,
	}}
	ex := NewExecutor(WithRunner(runner))

	var mu sync.Mutex
	var ops []string
	scan, err := ex.ScanDiscWithProgress(context.Background(), 0, func(ev Event) {
		mu.Lock()
		defer mu.Unlock()
		if ev.Operation != "" {
			ops = append(ops, ev.Operation)
		}
	})
	if err != nil {
		t.Fatalf("ScanDiscWithProgress: %v", err)
	}
	if len(scan.Titles) != 1 {
		t.Errorf("got %d titles, want 1", len(scan.Titles))
	}
	if len(ops) == 0 || !strings.Contains(ops[0], "Scanning") {
		t.Errorf("the operation name never reached the callback: %v", ops)
	}

	// It must still scan the drive, not something else.
	if len(runner.calls) == 0 {
		t.Fatal("no command was run")
	}
	last := runner.calls[len(runner.calls)-1]
	if last[len(last)-1] != "disc:0" {
		t.Errorf("scanned %q, want disc:0", last[len(last)-1])
	}
}
