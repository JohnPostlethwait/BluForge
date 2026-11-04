package drivemanager

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/johnpostlethwait/bluforge/internal/makemkv"
)

// heldExecutor is a drive lister whose drives are held by other work. The
// poller's TryListDrives declines; ListDrives waits until the test releases
// the drives, then answers with the listing it was given.
type heldExecutor struct {
	mu       sync.Mutex
	held     bool
	listing  []makemkv.DriveInfo
	released chan struct{}
	waiting  atomic.Int32 // ListDrives calls that had to wait
}

func (h *heldExecutor) TryListDrives(context.Context) ([]makemkv.DriveInfo, bool, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.held {
		return nil, false, nil
	}
	return h.listing, true, nil
}

func (h *heldExecutor) ListDrives(ctx context.Context) ([]makemkv.DriveInfo, error) {
	h.mu.Lock()
	held := h.held
	h.mu.Unlock()
	if held {
		h.waiting.Add(1)
		select {
		case <-h.released:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.listing, nil
}

func (h *heldExecutor) ScanDisc(context.Context, int) (*makemkv.DiscScan, error) {
	return nil, nil
}

func (h *heldExecutor) hold(listing []makemkv.DriveInfo) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.held = true
	h.listing = listing
	h.released = make(chan struct{})
}

func (h *heldExecutor) release() {
	h.mu.Lock()
	h.held = false
	h.mu.Unlock()
	close(h.released)
}

// fakeLabeler answers volume-label reads from a map, failing for anything
// not in it.
type fakeLabeler struct {
	mu     sync.Mutex
	labels map[string]string
	reads  int
}

func (f *fakeLabeler) Label(_ context.Context, devicePath string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reads++
	if l, ok := f.labels[devicePath]; ok {
		return l, nil
	}
	return "", errors.New("no label")
}

func waitForEvents(t *testing.T, mu *sync.Mutex, events *[]DriveEvent, typ EventType, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := countType(*events, typ)
		mu.Unlock()
		if n >= want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	t.Fatalf("%s events = %d, want %d", typ, countType(*events, typ), want)
}

// While another drive rips, the poller cannot list, and a listing that waits
// sits behind that drive's current title — most of an hour for a feature. A
// disc put into a free drive is named from its own volume label instead, read
// from that device alone, so it can be scanned straight away.
func TestDiscInsertedWhileDrivesAreHeldIsNamedFromItsVolumeLabel(t *testing.T) {
	exec := &heldExecutor{listing: withoutDisc()}
	var mu sync.Mutex
	var events []DriveEvent
	m := NewManager(exec, func(ev DriveEvent) {
		mu.Lock()
		events = append(events, ev)
		mu.Unlock()
	})
	m.labeler = &fakeLabeler{labels: map[string]string{"/dev/sr1": "SECOND_DISC"}}
	m.PollOnce(context.Background())

	now := time.Unix(0, 0)
	m.now = func() time.Time { return now }
	probe := newFakeProbe()
	probe.seq["/dev/sr1"] = []DriveStatus{StatusNoDisc, StatusDiscOK, StatusDiscOK}
	m.probe = probe

	exec.hold(withDisc("SECOND_DISC"))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	m.watchOnce(ctx)
	m.watchOnce(ctx)
	now = now.Add(3 * time.Second)
	m.watchOnce(ctx)

	waitForEvents(t, &mu, &events, EventDiscInserted, 1)
	dsm := m.GetDrive(1)
	if got := dsm.DiscName(); got != "SECOND_DISC" {
		t.Errorf("drive 1 disc = %q, want SECOND_DISC", got)
	}
	if dsm.State() != StateDetected {
		t.Errorf("drive 1 state = %v, want detected", dsm.State())
	}
	if got := exec.waiting.Load(); got != 0 {
		t.Errorf("waiting listings = %d, want none when the label was read", got)
	}

	// When the drives free up and the poller lists the same name, that is the
	// disc already announced, not a new one.
	exec.release()
	m.PollOnce(context.Background())
	mu.Lock()
	n := countType(events, EventDiscInserted)
	mu.Unlock()
	if n != 1 {
		t.Errorf("disc_inserted events = %d after the poll, want still 1", n)
	}
}

// A disc whose label cannot be read falls back to a listing that waits for the
// drives, so it is still seen once they free up.
func TestDiscInsertedWhileDrivesAreHeldIsSeenOnceTheyFree(t *testing.T) {
	exec := &heldExecutor{listing: withoutDisc()}
	var mu sync.Mutex
	var events []DriveEvent
	m := NewManager(exec, func(ev DriveEvent) {
		mu.Lock()
		events = append(events, ev)
		mu.Unlock()
	})
	m.labeler = &fakeLabeler{}
	m.PollOnce(context.Background())

	now := time.Unix(0, 0)
	m.now = func() time.Time { return now }
	probe := newFakeProbe()
	probe.seq["/dev/sr1"] = []DriveStatus{StatusNoDisc, StatusDiscOK, StatusDiscOK}
	m.probe = probe

	exec.hold(withDisc("SECOND_DISC"))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	m.watchOnce(ctx) // NoDisc baseline
	m.watchOnce(ctx) // DiscOK pending
	now = now.Add(3 * time.Second)
	m.watchOnce(ctx) // DiscOK confirmed: media arrived

	deadline := time.Now().Add(2 * time.Second)
	for exec.waiting.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if exec.waiting.Load() != 1 {
		t.Fatalf("waiting listings = %d, want 1 after the disc arrived", exec.waiting.Load())
	}

	// Further readings of the same disc do not queue more listings.
	m.watchOnce(ctx)
	m.watchOnce(ctx)

	exec.release()

	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := countType(events, EventDiscInserted)
		mu.Unlock()
		if n == 1 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	mu.Lock()
	n := countType(events, EventDiscInserted)
	mu.Unlock()
	if n != 1 {
		t.Fatalf("disc_inserted events = %d, want 1", n)
	}
	if got := m.GetDrive(1).DiscName(); got != "SECOND_DISC" {
		t.Errorf("drive 1 disc = %q, want SECOND_DISC", got)
	}
	if got := exec.waiting.Load(); got != 1 {
		t.Errorf("waiting listings = %d, want exactly 1", got)
	}
}
