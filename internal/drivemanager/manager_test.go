package drivemanager

import (
	"context"
	"errors"
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
	// empty counts identifies still to answer "empty", per drive.
	empty map[int]int
	// failing makes identify fail, per drive.
	failing map[int]error
	// stall, when set, holds the next identify until it is closed, whatever
	// its context says: makemkvcon winding down after being cancelled.
	stall chan struct{}
	// notReady is the reason a drive gives for not being ready, per device.
	// A device not in it answers TEST UNIT READY as ready.
	notReady map[string]string
	// hang holds every status probe of a device until it is closed: a drive
	// that has stopped answering.
	hang map[string]chan struct{}
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
	if stall := f.stall; stall != nil {
		f.stall = nil
		f.identified = append(f.identified, idx)
		info := f.infos[idx]
		f.mu.Unlock()
		<-stall
		return info, nil
	}
	defer f.mu.Unlock()
	f.identified = append(f.identified, idx)
	if err := f.failing[idx]; err != nil {
		return makemkv.DriveInfo{}, err
	}
	if f.loading[idx] > 0 {
		f.loading[idx]--
		info := f.infos[idx]
		info.DiscName, info.State = "", makemkv.DriveStateLoading
		return info, nil
	}
	if f.empty[idx] > 0 {
		f.empty[idx]--
		info := f.infos[idx]
		info.DiscName, info.State = "", makemkv.DriveStateEmptyClosed
		return info, nil
	}
	return f.infos[idx], nil
}

func (f *fakeSource) ProbeStatus(device string) drives.Status {
	f.mu.Lock()
	hang := f.hang[device]
	f.mu.Unlock()
	if hang != nil {
		<-hang
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.status[device]
}

func (f *fakeSource) ProbeReady(device string) drives.Readiness {
	f.mu.Lock()
	defer f.mu.Unlock()
	if reason, ok := f.notReady[device]; ok {
		return drives.Readiness{Reason: reason}
	}
	return drives.Readiness{Ready: true}
}

func (f *fakeSource) setNotReady(device, reason string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.notReady == nil {
		f.notReady = map[string]string{}
	}
	f.notReady[device] = reason
}

func (f *fakeSource) setReady(device string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.notReady, device)
}

func (f *fakeSource) identifies() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.identified)
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

// emptyFor puts disc in drive idx, answering "empty" to the next n identifies,
// as makemkvcon does for a Pioneer still spinning a disc up that the drive
// already reports loaded.
func (f *fakeSource) emptyFor(idx int, disc string, n int) {
	f.loadingFor(idx, disc, 0)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.empty == nil {
		f.empty = map[int]int{}
	}
	f.empty[idx] = n
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
	m.watchOnce(context.Background()).Wait() // baseline

	src.setStatus("/dev/sr1", drives.StatusTrayOpen)
	m.watchOnce(context.Background()).Wait()
	now = now.Add(3 * time.Second)
	m.watchOnce(context.Background()).Wait()
	if rec.count(EventDiscEjected) != 1 {
		t.Fatalf("ejects = %d, want 1", rec.count(EventDiscEjected))
	}

	src.setDisc(1, "PERFECT_BLUE")
	src.setStatus("/dev/sr1", drives.StatusDiscOK)
	m.watchOnce(context.Background()).Wait()
	now = now.Add(3 * time.Second)
	m.watchOnce(context.Background()).Wait()
	eventually(t, func() bool { return rec.count(EventDiscInserted) == 2 }, "the second disc to be announced")
}

// The status ioctl reports a disc while the drive is still spinning it up. A
// drive on Jotunheim stopped answering for good when makemkvcon was started on
// a reinserted disc a second later. An arrived disc is read only once the
// drive answers TEST UNIT READY, and makemkvcon is asked once.
func TestAnArrivedDiscWaitsForTheDriveToBeReady(t *testing.T) {
	src := newFakeSource()
	src.attach(1, "BD-RE HL-DT-ST BD-RE BU40N 1.03 SER1", "")
	src.setStatus("/dev/sr1", drives.StatusNoDisc)
	rec := &recorder{}
	m := NewManager(src, rec.on)
	now := time.Unix(0, 0)
	m.now = func() time.Time { return now }
	m.refreshSlots(context.Background(), true)
	m.watchOnce(context.Background()).Wait() // baseline: empty
	before := src.identifies()

	src.setDisc(1, "IP_MAN")
	src.setStatus("/dev/sr1", drives.StatusDiscOK)
	src.setNotReady("/dev/sr1", "NOT READY 04/01")
	m.watchOnce(context.Background()).Wait()
	for i := 0; i < 10; i++ {
		now = now.Add(3 * time.Second)
		m.watchOnce(context.Background()).Wait()
	}
	if n := src.identifies(); n != before {
		t.Fatalf("makemkvcon was started %d times on a drive that was not ready", n-before)
	}
	if got := m.GetDrive(1).State(); got != StateReading {
		t.Errorf("state while waiting = %q, want %q", got, StateReading)
	}

	src.setReady("/dev/sr1")
	m.watchOnce(context.Background()).Wait()
	eventually(t, func() bool { return rec.count(EventDiscInserted) == 1 }, "the disc to be announced once ready")
	if ev, _ := rec.last(EventDiscInserted); ev.DiscName != "IP_MAN" {
		t.Errorf("disc = %q, want IP_MAN", ev.DiscName)
	}
	for i := 0; i < 3; i++ {
		m.watchOnce(context.Background()).Wait()
	}
	if n := src.identifies(); n != before+1 {
		t.Errorf("identified %d times after the disc arrived, want 1", n-before)
	}
}

// A disc taken out while its drive is still getting ready is never read.
func TestADiscPulledBeforeTheDriveIsReadyIsNotRead(t *testing.T) {
	src := newFakeSource()
	src.attach(1, "BD-RE HL-DT-ST BD-RE BU40N 1.03 SER1", "")
	src.setStatus("/dev/sr1", drives.StatusNoDisc)
	rec := &recorder{}
	m := NewManager(src, rec.on)
	now := time.Unix(0, 0)
	m.now = func() time.Time { return now }
	m.refreshSlots(context.Background(), true)
	m.watchOnce(context.Background()).Wait()
	before := src.identifies()

	src.setDisc(1, "IP_MAN")
	src.setStatus("/dev/sr1", drives.StatusDiscOK)
	src.setNotReady("/dev/sr1", "NOT READY 04/01")
	m.watchOnce(context.Background()).Wait()
	now = now.Add(3 * time.Second)
	m.watchOnce(context.Background()).Wait()

	src.setStatus("/dev/sr1", drives.StatusTrayOpen)
	src.setReady("/dev/sr1")
	m.watchOnce(context.Background()).Wait()
	now = now.Add(3 * time.Second)
	m.watchOnce(context.Background()).Wait()
	m.watchOnce(context.Background()).Wait()

	if n := src.identifies(); n != before {
		t.Errorf("identified %d times, want 0", n-before)
	}
	if got := m.GetDrive(1).State(); got != StateEmpty {
		t.Errorf("state = %q, want %q", got, StateEmpty)
	}
}

// A drive that has stopped answering blocks its status probe in the kernel.
// The other drives are still watched: a disc pulled from one of them is seen.
func TestAHungDriveDoesNotBlindTheWatcher(t *testing.T) {
	src := newFakeSource()
	src.attach(1, "BD-RE HL-DT-ST BD-RE BU40N 1.03 SER1", "IP_MAN")
	src.attach(2, "BD-RE PIONEER BD-RW BDR-XD08U 1.00 SER2", "THE_WIRE_S4_D1")
	src.setStatus("/dev/sr1", drives.StatusDiscOK)
	src.setStatus("/dev/sr2", drives.StatusDiscOK)
	rec := &recorder{}
	m := NewManager(src, rec.on)
	now := time.Unix(0, 0)
	m.now = func() time.Time { return now }
	m.refreshSlots(context.Background(), true)
	m.watchOnce(context.Background()).Wait() // baseline

	hang := make(chan struct{})
	src.mu.Lock()
	src.hang = map[string]chan struct{}{"/dev/sr1": hang}
	src.mu.Unlock()
	hung := m.watchOnce(context.Background())
	eventually(t, func() bool {
		m.mu.RLock()
		defer m.mu.RUnlock()
		return m.probing["/dev/sr1"] && !m.probing["/dev/sr2"]
	}, "sr1's probe to hang and sr2's to return")

	src.setStatus("/dev/sr2", drives.StatusTrayOpen)
	for i := 0; i < 3; i++ {
		now = now.Add(3 * time.Second)
		// sr1's probe is still blocked, so this waits on sr2's alone.
		m.watchOnce(context.Background()).Wait()
	}
	if ev, ok := rec.last(EventDiscEjected); !ok || ev.DriveIndex != 2 {
		t.Error("the eject on the other drive was not seen while one drive hung")
	}

	close(hang)
	hung.Wait()
}

// arriveOn starts drive 1 empty, runs insert to put a disc in it, and has the
// watcher confirm the arrival.
func arriveOn(t *testing.T, src *fakeSource, insert func()) (*Manager, *recorder) {
	t.Helper()
	src.attach(1, "BD-RE HL-DT-ST BD-RE BU40N 1.03 SER1", "")
	src.setStatus("/dev/sr1", drives.StatusNoDisc)
	rec := &recorder{}
	m := NewManager(src, rec.on)
	now := time.Unix(0, 0)
	m.now = func() time.Time { return now }
	m.refreshSlots(context.Background(), true)
	m.watchOnce(context.Background()).Wait() // baseline: empty

	insert()
	src.setStatus("/dev/sr1", drives.StatusDiscOK)
	m.watchOnce(context.Background()).Wait()
	now = now.Add(3 * time.Second)
	m.watchOnce(context.Background()).Wait()
	return m, rec
}

// Identifying a UHD takes most of a minute. The drive says it is reading the
// disc for all of it, rather than looking empty with a disc in it.
func TestADiscBeingIdentifiedShowsAsReading(t *testing.T) {
	src := newFakeSource()
	m, rec := arriveOn(t, src, func() { src.setDisc(1, "IP_MAN_3") })

	eventually(t, func() bool { return rec.count(EventDiscInserted) == 1 }, "the disc to be announced")
	rec.mu.Lock()
	sawReading := false
	for _, ev := range rec.events {
		if ev.Type == EventStateChange && ev.DriveIndex == 1 && ev.State == StateReading {
			sawReading = true
		}
	}
	rec.mu.Unlock()
	if !sawReading {
		t.Error("the drive never reported it was reading the disc")
	}
	if got := m.GetDrive(1).State(); got != StateDetected {
		t.Errorf("state = %q, want %q", got, StateDetected)
	}
}

// An identify that fails leaves the drive empty, never stuck reading.
func TestAFailedIdentifyDoesNotLeaveTheDriveReading(t *testing.T) {
	src := newFakeSource()
	m, rec := arriveOn(t, src, func() {
		src.mu.Lock()
		src.failing = map[int]error{1: errors.New("makemkvcon: exit status 1")}
		src.mu.Unlock()
	})

	eventually(t, func() bool { return m.GetDrive(1).State() == StateEmpty && rec.count(EventStateChange) >= 2 },
		"the drive to go back to empty")
}

// A disc swapped in while the last disc's identify is still winding down from
// its cancel is identified anyway. Refusing it because an identify was
// "running" left the new disc unseen for good.
func TestASwapDuringAnIdentifyIsStillIdentified(t *testing.T) {
	src := newFakeSource()
	stall := make(chan struct{})
	defer close(stall)
	m, rec := arriveOn(t, src, func() {
		src.setDisc(1, "IP_MAN")
		src.mu.Lock()
		src.stall = stall
		src.mu.Unlock()
	})
	now := time.Unix(3, 0)
	m.now = func() time.Time { return now }
	eventually(t, func() bool {
		src.mu.Lock()
		defer src.mu.Unlock()
		return src.stall == nil
	}, "the first identify to stall")

	// The first identify is stalled. The disc leaves, and another goes in.
	src.setStatus("/dev/sr1", drives.StatusTrayOpen)
	m.watchOnce(context.Background()).Wait()
	now = now.Add(3 * time.Second)
	m.watchOnce(context.Background()).Wait()

	src.setDisc(1, "IP_MAN_2")
	src.setStatus("/dev/sr1", drives.StatusDiscOK)
	m.watchOnce(context.Background()).Wait()
	now = now.Add(3 * time.Second)
	m.watchOnce(context.Background()).Wait()

	eventually(t, func() bool {
		ev, ok := rec.last(EventDiscInserted)
		return ok && ev.DiscName == "IP_MAN_2"
	}, "the swapped-in disc to be announced")
	if got := m.GetDrive(1).State(); got != StateDetected {
		t.Errorf("state = %q, want %q", got, StateDetected)
	}
}

// makemkvcon is never run again against a drive that answered it oddly. Doing
// so, over and over against a drive spinning a disc up, is what this design
// replaced with waiting for the drive to be ready.
func TestAnArrivedDiscIsNeverAskedAboutTwice(t *testing.T) {
	for name, insert := range map[string]func(*fakeSource){
		"loading": func(src *fakeSource) { src.loadingFor(1, "IP_MAN", 3) },
		"empty":   func(src *fakeSource) { src.emptyFor(1, "THE_WIRE_S4_D1", 3) },
	} {
		t.Run(name, func(t *testing.T) {
			src := newFakeSource()
			m, _ := arriveOn(t, src, func() { insert(src) })
			// One identify at startup, one for the disc.
			eventually(t, func() bool {
				return src.identifies() == 2 && m.GetDrive(1).State() == StateEmpty
			}, "the identify to end")
			for i := 0; i < 3; i++ {
				m.watchOnce(context.Background()).Wait()
			}
			if n := src.identifies(); n != 2 {
				t.Errorf("makemkvcon was asked about the disc %d times, want 1", n-1)
			}
		})
	}
}

// An empty drive that makemkvcon also calls empty is empty. Nothing is asked
// twice.
func TestAnEmptyDriveIsAskedOnce(t *testing.T) {
	src := newFakeSource()
	src.attach(1, "BD-RE HL-DT-ST BD-RE BU40N 1.03 SER1", "")
	src.setStatus("/dev/sr1", drives.StatusNoDisc)
	m := NewManager(src, (&recorder{}).on)
	m.refreshSlots(context.Background(), true)

	src.mu.Lock()
	n := len(src.identified)
	src.mu.Unlock()
	if n != 1 {
		t.Errorf("identified %d times, want 1", n)
	}
	if got := m.GetDrive(1).State(); got != StateEmpty {
		t.Errorf("state = %q, want %q", got, StateEmpty)
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
	m.watchOnce(context.Background()).Wait()

	src.setStatus("/dev/sr1", drives.StatusNotReady)
	for i := 0; i < 5; i++ {
		now = now.Add(10 * time.Second)
		m.watchOnce(context.Background()).Wait()
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
