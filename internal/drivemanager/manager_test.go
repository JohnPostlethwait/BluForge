package drivemanager

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/johnpostlethwait/bluforge/internal/drives"
	"github.com/johnpostlethwait/bluforge/internal/makemkv"
)

// fakeSource stands in for the drives broker.
type fakeSource struct {
	mu         sync.Mutex
	slots      []drives.Slot
	infos      map[int]makemkv.DriveInfo
	status     map[string]drives.Status
	identified []int
	// loading counts identifies still to answer "loading", per drive.
	loading map[int]int
}

func newFakeSource() *fakeSource {
	return &fakeSource{infos: map[int]makemkv.DriveInfo{}, status: map[string]drives.Status{}}
}

func (f *fakeSource) Slots() ([]drives.Slot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]drives.Slot(nil), f.slots...), nil
}

func (f *fakeSource) Identify(_ context.Context, idx int) (makemkv.DriveInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.identified = append(f.identified, idx)
	if f.loading[idx] > 0 {
		f.loading[idx]--
		info := f.infos[idx]
		info.DiscName, info.State = "", makemkv.DriveStateLoading
		return info, nil
	}
	return f.infos[idx], nil
}

func (f *fakeSource) ProbeStatus(device string) drives.Status {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.status[device]
}

func (f *fakeSource) attach(idx int, name, disc string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	dev := "/dev/sr" + string(rune('0'+idx))
	f.slots = append(f.slots, drives.Slot{Index: idx, Device: dev})
	state := makemkv.DriveStateEmptyClosed
	if disc != "" {
		state = makemkv.DriveStateInserted
	}
	f.infos[idx] = makemkv.DriveInfo{Index: idx, DriveName: name, DiscName: disc, DevicePath: dev, State: state}
}

func (f *fakeSource) setDisc(idx int, disc string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	info := f.infos[idx]
	info.DiscName = disc
	info.State = makemkv.DriveStateEmptyClosed
	if disc != "" {
		info.State = makemkv.DriveStateInserted
	}
	f.infos[idx] = info
}

// loadingFor puts disc in drive idx, answering "loading" to the next n
// identifies, as makemkvcon does while the disc spins up.
func (f *fakeSource) loadingFor(idx int, disc string, n int) {
	f.setDisc(idx, disc)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.loading == nil {
		f.loading = map[int]int{}
	}
	f.loading[idx] = n
}

func (f *fakeSource) setStatus(device string, s drives.Status) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.status[device] = s
}

type recorder struct {
	mu     sync.Mutex
	events []DriveEvent
}

func (r *recorder) on(ev DriveEvent) {
	r.mu.Lock()
	r.events = append(r.events, ev)
	r.mu.Unlock()
}

func (r *recorder) count(t EventType) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, ev := range r.events {
		if ev.Type == t {
			n++
		}
	}
	return n
}

func (r *recorder) last(t EventType) (DriveEvent, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := len(r.events) - 1; i >= 0; i-- {
		if r.events[i].Type == t {
			return r.events[i], true
		}
	}
	return DriveEvent{}, false
}

func eventually(t *testing.T, cond func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// At startup each drive is identified on its own and reported as it is.
func TestDrivesAreFoundAndIdentified(t *testing.T) {
	src := newFakeSource()
	src.attach(1, "BD-RE HL-DT-ST BD-RE BU40N 1.03 SER1", "EARTHQUAKE_UPK1")
	src.attach(2, "BD-RE PIONEER BD-RW BDR-XD08U 1.04 SER2", "")
	rec := &recorder{}
	m := NewManager(src, rec.on)

	m.refreshSlots(context.Background(), true)

	if d := m.GetDrive(1); d == nil || d.State() != StateDetected || d.DiscName() != "EARTHQUAKE_UPK1" {
		t.Errorf("drive 1 = %+v, want Earthquake detected", d)
	}
	if d := m.GetDrive(2); d == nil || d.State() != StateEmpty {
		t.Errorf("drive 2 = %+v, want empty", d)
	}
	ev, ok := rec.last(EventDiscInserted)
	if !ok || ev.DriveIndex != 1 || ev.DevicePath != "/dev/sr1" {
		t.Errorf("insert event = %+v, want drive 1 at /dev/sr1", ev)
	}
}

// An ignored drive is identified once, to learn its name, and then left alone:
// not tracked, not watched, not identified again.
func TestAnIgnoredDriveIsLeftAlone(t *testing.T) {
	src := newFakeSource()
	src.attach(0, "BD-RE ASUS BW-16D1HT 3.10 KLFO82D3028", "SOMETHING")
	rec := &recorder{}
	m := NewManager(src, rec.on)
	m.SetIgnoredDrives([]string{"BD-RE ASUS BW-16D1HT 3.02 KLFO82D3028"})

	m.refreshSlots(context.Background(), true)
	m.refreshSlots(context.Background(), true)

	if m.GetDrive(0) != nil {
		t.Error("an ignored drive is tracked")
	}
	if rec.count(EventDiscInserted) != 0 {
		t.Error("an ignored drive's disc was announced")
	}
	if len(src.identified) != 1 {
		t.Errorf("identified the ignored drive %d times, want once", len(src.identified))
	}
	if listed := m.ListedDrives(); len(listed) != 1 {
		t.Errorf("listed %d drives, want the ignored one for the settings page", len(listed))
	}
}

// A drive gone from sysfs is gone: the kernel's own record needs no debounce.
func TestADriveGoneFromSysfsIsDisconnected(t *testing.T) {
	src := newFakeSource()
	src.attach(1, "BD-RE HL-DT-ST BD-RE BU40N 1.03 SER1", "EARTHQUAKE_UPK1")
	rec := &recorder{}
	m := NewManager(src, rec.on)
	m.refreshSlots(context.Background(), true)

	src.mu.Lock()
	src.slots = nil
	src.mu.Unlock()
	m.refreshSlots(context.Background(), true)

	if m.GetDrive(1) != nil {
		t.Error("an unplugged drive is still tracked")
	}
	ev, ok := rec.last(EventDriveDisconnect)
	if !ok || ev.DevicePath != "/dev/sr1" || ev.DiscName != "EARTHQUAKE_UPK1" {
		t.Errorf("disconnect event = %+v", ev)
	}
}

// A drive plugged in later is found and identified.
func TestADriveAttachedLaterIsFound(t *testing.T) {
	src := newFakeSource()
	m := NewManager(src, (&recorder{}).on)
	m.refreshSlots(context.Background(), true)

	src.attach(2, "BD-RE PIONEER BD-RW BDR-XD08U 1.04 SER2", "")
	m.refreshSlots(context.Background(), true)

	if m.GetDrive(2) == nil {
		t.Error("a drive attached after startup was not found")
	}
}

// The watcher drives disc changes: a disc leaving is an eject, one arriving is
// identified under its drive's hold and announced. Two discs of a set sharing
// one label are told apart this way, because the tray opening between them is
// seen.
func TestTheWatcherSeesADiscSwap(t *testing.T) {
	src := newFakeSource()
	src.attach(1, "BD-RE HL-DT-ST BD-RE BU40N 1.03 SER1", "PERFECT_BLUE")
	src.setStatus("/dev/sr1", drives.StatusDiscOK)
	rec := &recorder{}
	m := NewManager(src, rec.on)
	now := time.Unix(0, 0)
	m.now = func() time.Time { return now }
	m.refreshSlots(context.Background(), true)
	m.watchOnce(context.Background()) // baseline

	src.setStatus("/dev/sr1", drives.StatusTrayOpen)
	m.watchOnce(context.Background())
	now = now.Add(3 * time.Second)
	m.watchOnce(context.Background())
	if rec.count(EventDiscEjected) != 1 {
		t.Fatalf("ejects = %d, want 1", rec.count(EventDiscEjected))
	}

	src.setDisc(1, "PERFECT_BLUE")
	src.setStatus("/dev/sr1", drives.StatusDiscOK)
	m.watchOnce(context.Background())
	now = now.Add(3 * time.Second)
	m.watchOnce(context.Background())
	eventually(t, func() bool { return rec.count(EventDiscInserted) == 2 }, "the second disc to be announced")
}

// makemkvcon reports a disc that is still spinning up as loading. That is not
// an empty drive: the disc is asked about again until makemkvcon says what it
// is. Taking it for empty left a disc in the drive that BluForge never saw,
// because the watcher had already confirmed the arrival and would not ask again.
func TestADiscStillLoadingIsAskedAboutAgain(t *testing.T) {
	src := newFakeSource()
	src.attach(1, "BD-RE HL-DT-ST BD-RE BU40N 1.03 SER1", "")
	src.setStatus("/dev/sr1", drives.StatusNoDisc)
	rec := &recorder{}
	m := NewManager(src, rec.on)
	m.identifyRetry = 0
	now := time.Unix(0, 0)
	m.now = func() time.Time { return now }
	m.refreshSlots(context.Background(), true)
	m.watchOnce(context.Background()) // baseline: empty

	src.loadingFor(1, "IP_MAN", 3)
	src.setStatus("/dev/sr1", drives.StatusDiscOK)
	m.watchOnce(context.Background())
	now = now.Add(3 * time.Second)
	m.watchOnce(context.Background())

	eventually(t, func() bool { return rec.count(EventDiscInserted) == 1 }, "the disc to be announced once loaded")
	if ev, _ := rec.last(EventDiscInserted); ev.DiscName != "IP_MAN" {
		t.Errorf("disc = %q, want IP_MAN", ev.DiscName)
	}
	if got := m.GetDrive(1).State(); got != StateDetected {
		t.Errorf("state = %q, want %q", got, StateDetected)
	}
}

// A busy drive reports not-ready; that is not an eject.
func TestABusyDriveIsNotAnEject(t *testing.T) {
	src := newFakeSource()
	src.attach(1, "BD-RE HL-DT-ST BD-RE BU40N 1.03 SER1", "EARTHQUAKE_UPK1")
	src.setStatus("/dev/sr1", drives.StatusDiscOK)
	rec := &recorder{}
	m := NewManager(src, rec.on)
	now := time.Unix(0, 0)
	m.now = func() time.Time { return now }
	m.refreshSlots(context.Background(), true)
	m.watchOnce(context.Background())

	src.setStatus("/dev/sr1", drives.StatusNotReady)
	for i := 0; i < 5; i++ {
		now = now.Add(10 * time.Second)
		m.watchOnce(context.Background())
	}
	if rec.count(EventDiscEjected) != 0 {
		t.Error("a busy drive was taken for an eject")
	}
}

// SetDriveState reports a state nothing physical can: recovery.
func TestSetDriveStateAnnouncesIt(t *testing.T) {
	src := newFakeSource()
	src.attach(1, "BD-RE HL-DT-ST BD-RE BU40N 1.03 SER1", "EARTHQUAKE_UPK1")
	rec := &recorder{}
	m := NewManager(src, rec.on)
	m.refreshSlots(context.Background(), true)

	m.SetDriveState(1, StateRecovering)
	ev, ok := rec.last(EventStateChange)
	if !ok || ev.State != StateRecovering || ev.DevicePath != "/dev/sr1" {
		t.Errorf("state change = %+v", ev)
	}
}
