package makemkv

import (
	"context"
	"strings"
	"sync"
	"testing"
)

// streamingRunner delivers output line by line, as the real runner does.
type streamingRunner struct {
	lines []string
	calls [][]string
	mu    sync.Mutex
}

func (s *streamingRunner) Run(_ context.Context, args ...string) (*strings.Reader, error) {
	s.mu.Lock()
	s.calls = append(s.calls, args)
	s.mu.Unlock()
	return strings.NewReader(strings.Join(s.lines, "\n")), nil
}

func (s *streamingRunner) RunStream(_ context.Context, onLine func(string), args ...string) error {
	s.mu.Lock()
	s.calls = append(s.calls, args)
	s.mu.Unlock()
	for _, l := range s.lines {
		onLine(l)
	}
	return nil
}

// A scan of a damaged disc can run for many minutes while the drive retries
// unreadable sectors. Buffering its output until exit left the UI saying "this
// may take a moment" and the log saying nothing at all, with no way to tell
// working from hung.
func TestScanSourceReportsProgressWhileRunning(t *testing.T) {
	runner := &streamingRunner{lines: []string{
		`MSG:1005,0,1,"MakeMKV started","%1","MakeMKV"`,
		`MSG:2003,0,1,"Error occurred while reading '/BDMV/STREAM/00008.m2ts'","%1","x"`,
		`TCOUNT:1`,
		`CINFO:2,0,"SOME_DISC"`,
		`TINFO:0,2,0,"Feature"`,
		`TINFO:0,16,0,"00800.mpls"`,
	}}
	ex := NewExecutor(WithRunner(runner))

	var seen []Event
	scan, err := ex.ScanSourceWithProgress(context.Background(), FileSource("/scratch/x"),
		func(ev Event) { seen = append(seen, ev) })
	if err != nil {
		t.Fatalf("ScanSourceWithProgress: %v", err)
	}

	if len(seen) == 0 {
		t.Fatal("no events were reported during the scan")
	}
	var sawMessage bool
	for _, ev := range seen {
		if ev.Type == "MSG" && ev.Message != nil && ev.Message.Code == 2003 {
			sawMessage = true
		}
	}
	if !sawMessage {
		t.Error("the read error was not reported while the scan ran")
	}

	// Streaming must not cost the result.
	if len(scan.Titles) != 1 {
		t.Errorf("got %d titles, want 1", len(scan.Titles))
	}
	if scan.DiscName != "SOME_DISC" {
		t.Errorf("DiscName = %q, want SOME_DISC", scan.DiscName)
	}
	// RawOutput feeds TheDiscDB contributions and must survive streaming.
	if !strings.Contains(scan.RawOutput, "TCOUNT:1") {
		t.Errorf("RawOutput lost the scan output: %q", scan.RawOutput)
	}
}

// A runner without streaming support still works; its output is parsed at exit.
func TestScanSourceFallsBackToBufferedOutput(t *testing.T) {
	runner := &recordingRunner{output: scanDiscOutput}
	ex := NewExecutor(WithRunner(runner))

	scan, err := ex.ScanSourceWithProgress(context.Background(), FileSource("/scratch/x"), nil)
	if err != nil {
		t.Fatalf("ScanSourceWithProgress: %v", err)
	}
	if len(scan.Titles) == 0 {
		t.Error("no titles from the buffered path")
	}
}
