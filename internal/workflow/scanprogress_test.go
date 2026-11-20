package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/johnpostlethwait/bluforge/internal/drives"
	"github.com/johnpostlethwait/bluforge/internal/makemkv"
)

// progressScanner narrates a scan, then blocks until released so a test can
// observe the in-flight state.
type progressScanner struct {
	mu       sync.Mutex
	release  chan struct{}
	started  chan struct{}
	events   []makemkv.Event
	err      error
	scanned  int
	discName string
}

func newProgressScanner(events ...makemkv.Event) *progressScanner {
	return &progressScanner{
		release:  make(chan struct{}),
		started:  make(chan struct{}, 1),
		events:   events,
		discName: "SOME_DISC",
	}
}

func (p *progressScanner) ScanDisc(ctx context.Context, driveIndex int) (*makemkv.DiscScan, error) {
	return p.ScanDiscWithProgress(ctx, driveIndex, nil)
}

func (p *progressScanner) ScanDiscWithProgress(_ context.Context, _ int, onEvent func(makemkv.Event)) (*makemkv.DiscScan, error) {
	p.mu.Lock()
	p.scanned++
	p.mu.Unlock()

	for _, ev := range p.events {
		if onEvent != nil {
			onEvent(ev)
		}
	}
	select {
	case p.started <- struct{}{}:
	default:
	}
	<-p.release
	if p.err != nil {
		return nil, p.err
	}
	return &makemkv.DiscScan{
		DiscName: p.discName,
		Titles:   []makemkv.TitleInfo{{Index: 0, Attributes: map[int]string{2: "Feature", 16: "00800.mpls"}}},
	}, nil
}

func (p *progressScanner) scanCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.scanned
}

// collectBroadcasts records SSE events by name.
type broadcastRecorder struct {
	mu     sync.Mutex
	events []struct{ name, data string }
}

func (b *broadcastRecorder) fn(name, data string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.events = append(b.events, struct{ name, data string }{name, data})
}

func (b *broadcastRecorder) named(name string) []map[string]any {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []map[string]any
	for _, e := range b.events {
		if e.name != name {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(e.data), &m); err == nil {
			out = append(out, m)
		}
	}
	return out
}

// waitFor polls until cond holds, so a test does not depend on goroutine timing.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(asyncDeadline)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// A scan blocked the request for as long as it ran — up to an hour on a disc
// that retries bad sectors. The browser gave up, and killing the request killed
// makemkvcon with it.
func TestStartScanReturnsBeforeTheScanFinishes(t *testing.T) {
	scanner := newProgressScanner()
	orch, _, _ := setupOrchestratorWithScanner(t, scanner)

	if err := orch.StartScan(0); err != nil {
		t.Fatalf("StartScan: %v", err)
	}

	waitFor(t, "the scan to start", func() bool { return orch.ScanStatus(0).Active })

	close(scanner.release)
	waitFor(t, "the scan to finish", func() bool { return !orch.ScanStatus(0).Active })

	if orch.GetCachedScanByDrive(0) == nil {
		t.Error("the finished scan was not cached")
	}
}

// A second click while a scan runs must not start a second makemkvcon.
func TestStartScanIsNotStartedTwiceForOneDrive(t *testing.T) {
	scanner := newProgressScanner()
	orch, _, _ := setupOrchestratorWithScanner(t, scanner)

	if err := orch.StartScan(0); err != nil {
		t.Fatalf("first StartScan: %v", err)
	}
	waitFor(t, "the scan to start", func() bool { return orch.ScanStatus(0).Active })

	if err := orch.StartScan(0); !errors.Is(err, ErrScanInProgress) {
		t.Errorf("second StartScan err = %v, want ErrScanInProgress", err)
	}

	close(scanner.release)
	waitFor(t, "the scan to finish", func() bool { return !orch.ScanStatus(0).Active })

	if n := scanner.scanCount(); n != 1 {
		t.Errorf("ran %d scans, want 1", n)
	}
}

// The banner needs a start time to count from. Sending elapsed seconds instead
// would need a heartbeat; the client can tick a start time by itself.
func TestScanStatusCarriesTheStartTime(t *testing.T) {
	scanner := newProgressScanner()
	orch, _, _ := setupOrchestratorWithScanner(t, scanner)

	before := time.Now()
	if err := orch.StartScan(0); err != nil {
		t.Fatalf("StartScan: %v", err)
	}
	waitFor(t, "the scan to start", func() bool { return orch.ScanStatus(0).Active })

	st := orch.ScanStatus(0)
	if st.StartedAt.Before(before.Add(-time.Second)) || st.StartedAt.After(time.Now().Add(time.Second)) {
		t.Errorf("StartedAt = %v, not within the scan window", st.StartedAt)
	}

	close(scanner.release)
	waitFor(t, "the scan to finish", func() bool { return !orch.ScanStatus(0).Active })
}

// What makemkvcon is doing has to reach the page, or a long scan still looks
// like a hung one.
func TestScanBroadcastsWhatItIsDoing(t *testing.T) {
	rec := &broadcastRecorder{}
	scanner := newProgressScanner(
		makemkv.Event{Type: "PRGT", Operation: "Scanning CD-ROM devices"},
		makemkv.Event{Type: "PRGC", Operation: "Analyzing seamless segments"},
	)
	orch, _, _ := setupOrchestratorWithScannerAndBroadcast(t, scanner, rec.fn)

	if err := orch.StartScan(0); err != nil {
		t.Fatalf("StartScan: %v", err)
	}
	waitFor(t, "the operation to be broadcast", func() bool {
		for _, m := range rec.named("disc_scan") {
			if op, _ := m["operation"].(string); strings.Contains(op, "seamless") {
				return true
			}
		}
		return false
	})

	close(scanner.release)
	waitFor(t, "the done event", func() bool {
		for _, m := range rec.named("disc_scan") {
			if phase, _ := m["phase"].(string); phase == "done" {
				return true
			}
		}
		return false
	})

	// The status must clear before "done" is delivered, or a client that acts
	// on the event immediately sees a scan that is somehow still running.
	if orch.ScanStatus(0).Active {
		t.Error("status still active after the done event")
	}
}

// A failed scan must say so. Leaving the banner up forever is how the last two
// failures were reported to me.
func TestScanBroadcastsFailure(t *testing.T) {
	rec := &broadcastRecorder{}
	scanner := newProgressScanner()
	scanner.err = errors.New("disc scan failed: makemkv: scan disc:0: timed out")
	orch, _, _ := setupOrchestratorWithScannerAndBroadcast(t, scanner, rec.fn)

	if err := orch.StartScan(0); err != nil {
		t.Fatalf("StartScan: %v", err)
	}
	waitFor(t, "the scan to start", func() bool { return orch.ScanStatus(0).Active })
	close(scanner.release)

	waitFor(t, "the failure event", func() bool {
		for _, m := range rec.named("disc_scan") {
			if phase, _ := m["phase"].(string); phase == "failed" {
				msg, _ := m["message"].(string)
				return strings.Contains(msg, "timed out")
			}
		}
		return false
	})

	if orch.ScanStatus(0).Active {
		t.Error("a failed scan left the status active")
	}
}

// makemkvcon prints dozens of messages at once at the end of a scan. Sent one
// event each, they overflowed the page's buffer and the hub closed its stream.
func TestScanMessagesDoNotFloodThePage(t *testing.T) {
	rec := &broadcastRecorder{}
	var events []makemkv.Event
	for i := 0; i < 100; i++ {
		events = append(events, makemkv.Event{Type: "MSG",
			Message: &makemkv.Message{Code: 3307, Text: fmt.Sprintf("message %d", i)}})
	}
	scanner := newProgressScanner(events...)
	orch, _, _ := setupOrchestratorWithScannerAndBroadcast(t, scanner, rec.fn)

	if err := orch.StartScan(0); err != nil {
		t.Fatalf("StartScan: %v", err)
	}
	<-scanner.started
	if n := len(rec.named("disc_scan")); n > 3 {
		t.Errorf("100 messages in a burst sent %d events, want a few", n)
	}
	close(scanner.release)
	waitFor(t, "the scan to finish", func() bool { return !orch.ScanStatus(0).Active })
}

// A page that missed the terminal event has to be able to ask how the scan
// ended. Events are not replayed, so without this a missed "done" left the
// banner up and a missed "failed" could pass off old titles as a fresh read.
func TestScanStatusRemembersHowTheLastScanEnded(t *testing.T) {
	scanner := newProgressScanner()
	scanner.err = errors.New("timed out")
	orch, _, _ := setupOrchestratorWithScanner(t, scanner)

	if err := orch.StartScan(0); err != nil {
		t.Fatalf("StartScan: %v", err)
	}
	waitFor(t, "the scan to start", func() bool { return orch.ScanStatus(0).Active })
	started := orch.ScanStatus(0).StartedAt
	close(scanner.release)
	waitFor(t, "the scan to finish", func() bool { return !orch.ScanStatus(0).Active })

	st := orch.ScanStatus(0)
	if st.LastPhase != "failed" || !strings.Contains(st.LastMessage, "timed out") {
		t.Errorf("last outcome = %q %q, want failed with the error", st.LastPhase, st.LastMessage)
	}
	if !st.LastStartedAt.Equal(started) {
		t.Errorf("LastStartedAt = %v, want the finished scan's start %v", st.LastStartedAt, started)
	}

	scanner.err = nil
	scanner.release = make(chan struct{})
	if err := orch.StartScan(0); err != nil {
		t.Fatalf("second StartScan: %v", err)
	}
	waitFor(t, "the second scan to start", func() bool { return orch.ScanStatus(0).Active })
	close(scanner.release)
	waitFor(t, "the second scan to finish", func() bool { return !orch.ScanStatus(0).Active })

	if st := orch.ScanStatus(0); st.LastPhase != "done" || st.LastMessage != "" {
		t.Errorf("after a good scan, last outcome = %q %q, want done", st.LastPhase, st.LastMessage)
	}
}

// The real scanner, the drives broker, has to satisfy ProgressScanner, or
// scanOnce falls back to the silent path at runtime and the page goes quiet
// again with nothing failing to say so. It must also be the holder and the
// backupper main wires it as.
func TestTheBrokerNarratesItsScans(t *testing.T) {
	var scanner DiscScanner = drives.New(false, nil)
	if _, ok := scanner.(ProgressScanner); !ok {
		t.Error("*drives.Broker no longer satisfies ProgressScanner; scans will run silently")
	}
	var _ DriveHolder = drives.New(false, nil)
	var _ DiscBackupper = drives.New(false, nil)
	var _ DeviceLocator = drives.New(false, nil)
	var _ DiscLabeller = drives.New(false, nil)
}

// A scanner that predates progress reporting must still work.
func TestStartScanWorksWithAPlainScanner(t *testing.T) {
	orch, _, _ := setupOrchestratorWithScanner(t, &mockDriveExecutor{})

	if err := orch.StartScan(0); err != nil {
		t.Fatalf("StartScan: %v", err)
	}
	waitFor(t, "the scan to finish", func() bool {
		return !orch.ScanStatus(0).Active && orch.GetCachedScanByDrive(0) != nil
	})
}
