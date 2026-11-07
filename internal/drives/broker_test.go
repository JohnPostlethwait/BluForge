package drives

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/johnpostlethwait/bluforge/internal/ddrescue"
	"github.com/johnpostlethwait/bluforge/internal/makemkv"
)

// fakeEngine records what ran and can hold a command open until released.
type fakeEngine struct {
	isolated bool

	mu       sync.Mutex
	sources  []makemkv.Source
	running  int32
	maxAtOnc int32
	gate     chan struct{} // when non-nil, every command waits on it
}

func (f *fakeEngine) run(src makemkv.Source) {
	f.mu.Lock()
	f.sources = append(f.sources, src)
	f.mu.Unlock()
	n := atomic.AddInt32(&f.running, 1)
	for {
		m := atomic.LoadInt32(&f.maxAtOnc)
		if n <= m || atomic.CompareAndSwapInt32(&f.maxAtOnc, m, n) {
			break
		}
	}
	if f.gate != nil {
		<-f.gate
	}
	atomic.AddInt32(&f.running, -1)
}

func (f *fakeEngine) ScanSourceWithProgress(_ context.Context, src makemkv.Source, _ func(makemkv.Event)) (*makemkv.DiscScan, error) {
	f.run(src)
	return &makemkv.DiscScan{DriveIndex: src.DriveIndex}, nil
}
func (f *fakeEngine) StartRip(_ context.Context, src makemkv.Source, _ int, _, _ string, _ func(makemkv.Event), _ *makemkv.SelectionOpts) error {
	f.run(src)
	return nil
}
func (f *fakeEngine) Backup(_ context.Context, src makemkv.Source, _ string, _ func(makemkv.Event)) error {
	f.run(src)
	return nil
}
func (f *fakeEngine) Identify(_ context.Context, src makemkv.Source) (makemkv.DriveInfo, error) {
	f.run(src)
	return makemkv.DriveInfo{Index: src.DriveIndex, DevicePath: src.Device}, nil
}
func (f *fakeEngine) Isolated() bool { return f.isolated }

func twoDrives() ([]Slot, error) {
	return []Slot{{Index: 0, Device: "/dev/sr0"}, {Index: 1, Device: "/dev/sr1"}}, nil
}

type mounts struct {
	mu       sync.Mutex
	open     int
	released int
	forced   []string
	root     string
}

func (m *mounts) opener(string) (string, func(), error) {
	m.mu.Lock()
	m.open++
	m.mu.Unlock()
	return m.root, func() {
		m.mu.Lock()
		m.released++
		m.mu.Unlock()
	}, nil
}

func (m *mounts) force(device string) error {
	m.mu.Lock()
	m.forced = append(m.forced, device)
	m.mu.Unlock()
	return nil
}

func newTestBroker(t *testing.T, eng *fakeEngine) (*Broker, *mounts) {
	t.Helper()
	m := &mounts{root: t.TempDir()}
	b := New(eng.isolated, nil, WithEngine(eng), WithSlots(twoDrives),
		WithRootOpener(m.opener), WithForceUnmount(m.force))
	return b, m
}

func waitFor(t *testing.T, cond func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// The ball: while one holder has a drive, nothing else touches it.
func TestNothingTouchesAHeldDrive(t *testing.T) {
	eng := &fakeEngine{isolated: true}
	b, _ := newTestBroker(t, eng)

	held := make(chan struct{})
	letGo := make(chan struct{})
	go func() {
		_ = b.Do(context.Background(), 0, "rip", func(context.Context, *Drive) error {
			close(held)
			<-letGo
			return nil
		})
	}()
	<-held

	scanned := make(chan struct{})
	go func() {
		_, _ = b.ScanDisc(context.Background(), 0)
		close(scanned)
	}()
	select {
	case <-scanned:
		t.Fatal("a scan reached a drive another holder had")
	case <-time.After(100 * time.Millisecond):
	}
	close(letGo)
	select {
	case <-scanned:
	case <-time.After(2 * time.Second):
		t.Fatal("the scan never got the drive after the holder let go")
	}
}

// With isolation each makemkvcon sees only its own drive, so two drives work
// at once.
func TestIsolatedDrivesWorkAtTheSameTime(t *testing.T) {
	eng := &fakeEngine{isolated: true, gate: make(chan struct{})}
	b, _ := newTestBroker(t, eng)

	var wg sync.WaitGroup
	for _, idx := range []int{0, 1} {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			_, _ = b.ScanDisc(context.Background(), idx)
		}(idx)
	}
	waitFor(t, func() bool { return atomic.LoadInt32(&eng.running) == 2 }, "both drives to be scanning")
	close(eng.gate)
	wg.Wait()
}

// Without isolation each makemkvcon opens every drive, so only one runs.
func TestUnisolatedDrivesTakeTurns(t *testing.T) {
	eng := &fakeEngine{isolated: false, gate: make(chan struct{})}
	b, _ := newTestBroker(t, eng)

	var wg sync.WaitGroup
	for _, idx := range []int{0, 1} {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			_, _ = b.ScanDisc(context.Background(), idx)
		}(idx)
	}
	waitFor(t, func() bool { return atomic.LoadInt32(&eng.running) == 1 }, "one scan to start")
	time.Sleep(100 * time.Millisecond)
	if n := atomic.LoadInt32(&eng.running); n != 1 {
		t.Fatalf("%d makemkvcon ran at once without isolation", n)
	}
	close(eng.gate)
	wg.Wait()
	if eng.maxAtOnc != 1 {
		t.Errorf("at most %d ran at once, want 1", eng.maxAtOnc)
	}
}

// A holder that calls back into the broker for its own drive keeps the ball
// rather than waiting on itself — the deadlock salvage once sat in for forty
// minutes.
func TestAHolderCallingBackForItsDriveKeepsIt(t *testing.T) {
	for _, isolated := range []bool{true, false} {
		eng := &fakeEngine{isolated: isolated}
		b, _ := newTestBroker(t, eng)

		done := make(chan error, 1)
		go func() {
			done <- b.Do(context.Background(), 0, "salvage", func(ctx context.Context, d *Drive) error {
				if _, err := b.ScanDisc(ctx, 0); err != nil {
					return err
				}
				return b.Backup(ctx, 0, t.TempDir(), nil)
			})
		}()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("isolated=%v: %v", isolated, err)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("isolated=%v: a holder waited on its own drive", isolated)
		}
	}
}

// Asking for a second drive while holding one is refused: that is how two
// holders would deadlock each other.
func TestAHolderCannotTakeASecondDrive(t *testing.T) {
	b, _ := newTestBroker(t, &fakeEngine{isolated: true})
	err := b.Do(context.Background(), 0, "rip", func(ctx context.Context, _ *Drive) error {
		_, err := b.ScanDisc(ctx, 1)
		return err
	})
	if !errors.Is(err, ErrOtherDriveHeld) {
		t.Errorf("err = %v, want ErrOtherDriveHeld", err)
	}
}

// The ball cannot be kept after letting go of it.
func TestADriveCannotBeUsedAfterRelease(t *testing.T) {
	b, _ := newTestBroker(t, &fakeEngine{isolated: true})
	var kept *Drive
	_ = b.Do(context.Background(), 0, "scan", func(_ context.Context, d *Drive) error {
		kept = d
		return nil
	})
	if _, err := kept.Scan(context.Background(), nil); !errors.Is(err, ErrReleased) {
		t.Errorf("err = %v, want ErrReleased", err)
	}
	if _, _, err := kept.OpenRoot(context.Background()); !errors.Is(err, ErrReleased) {
		t.Errorf("OpenRoot err = %v, want ErrReleased", err)
	}
}

// No mount outlives its holder: a live filesystem under media that changes is
// what wedged drives in September.
func TestAMountComesDownWithItsHolder(t *testing.T) {
	b, m := newTestBroker(t, &fakeEngine{isolated: true})
	_ = b.WithDiscRoot(context.Background(), 1, "inspect", func(context.Context, string, func()) error {
		return nil
	})
	if m.open != 1 || m.released != 1 {
		t.Errorf("opened %d, released %d; want 1 and 1", m.open, m.released)
	}
	if len(m.forced) != 1 || m.forced[0] != "/dev/sr1" {
		t.Errorf("forced unmounts %v, want /dev/sr1 confirmed down", m.forced)
	}
}

// ddrescue reads the held drive's device, or a file on a root it has open,
// and nothing else.
func TestARescueReadsOnlyTheHeldDrive(t *testing.T) {
	eng := &fakeEngine{isolated: true}
	b, m := newTestBroker(t, eng)
	r := &recordingRescuer{}
	b.rescuer = r

	dest := filepath.Join(t.TempDir(), "out")
	opts := ddrescue.Options{Dest: dest, MapFile: dest + ".map"}
	if err := b.Rescue(context.Background(), 1, nil, opts, nil); err != nil {
		t.Fatalf("Rescue: %v", err)
	}
	if r.source() != "/dev/sr1" {
		t.Errorf("rescued from %q, want the held drive /dev/sr1", r.source())
	}

	opts.Source = "/etc/passwd"
	if err := b.Rescue(context.Background(), 1, nil, opts, nil); err == nil {
		t.Error("a rescue from a file off the disc was allowed")
	}

	file := filepath.Join(m.root, "BDMV", "STREAM", "00000.m2ts")
	_ = os.MkdirAll(filepath.Dir(file), 0o777)
	err := b.WithDiscRoot(context.Background(), 1, "salvage", func(ctx context.Context, root string, _ func()) error {
		o := opts
		o.Source = file
		return b.Rescue(ctx, 1, nil, o, nil)
	})
	if err != nil {
		t.Errorf("a rescue from the open disc was refused: %v", err)
	}
}

// A link tree reads its drive through a mount, so it holds the drive and
// mounts the disc for as long as makemkvcon reads it.
func TestALinkTreeHoldsItsDrive(t *testing.T) {
	eng := &fakeEngine{isolated: true}
	b, m := newTestBroker(t, eng)

	held := make(chan struct{})
	letGo := make(chan struct{})
	go func() {
		_ = b.Do(context.Background(), 0, "rip", func(context.Context, *Drive) error {
			close(held)
			<-letGo
			return nil
		})
	}()
	<-held

	scanned := make(chan struct{})
	go func() {
		_, _ = b.ScanSource(context.Background(), makemkv.LinkedSource(0, t.TempDir()))
		close(scanned)
	}()
	select {
	case <-scanned:
		t.Fatal("a link tree was read while another holder had its drive")
	case <-time.After(100 * time.Millisecond):
	}
	close(letGo)
	<-scanned
	if m.open != 1 || m.released != 1 {
		t.Errorf("link tree scan opened %d mounts and released %d, want 1 and 1", m.open, m.released)
	}
}

// A copy on disk reads no drive: isolated, its makemkvcon runs beside a held
// drive; without isolation it would open every drive, so it waits.
func TestACopyReadsNoDrive(t *testing.T) {
	for _, isolated := range []bool{true, false} {
		eng := &fakeEngine{isolated: isolated}
		b, _ := newTestBroker(t, eng)

		held := make(chan struct{})
		letGo := make(chan struct{})
		go func() {
			_ = b.Do(context.Background(), 0, "rip", func(context.Context, *Drive) error {
				close(held)
				<-letGo
				return nil
			})
		}()
		<-held

		scanned := make(chan struct{})
		go func() {
			_, _ = b.ScanSource(context.Background(), makemkv.ISOSource("/scratch/disc.iso"))
			close(scanned)
		}()
		var ranBeside bool
		select {
		case <-scanned:
			ranBeside = true
		case <-time.After(100 * time.Millisecond):
		}
		close(letGo)
		<-scanned
		if ranBeside != isolated {
			t.Errorf("isolated=%v: copy ran beside a held drive = %v", isolated, ranBeside)
		}
	}
}

// Waiting for a drive is cancellable.
func TestWaitingForADriveCanBeCancelled(t *testing.T) {
	b, _ := newTestBroker(t, &fakeEngine{isolated: true})
	held := make(chan struct{})
	letGo := make(chan struct{})
	defer close(letGo)
	go func() {
		_ = b.Do(context.Background(), 0, "rip", func(context.Context, *Drive) error {
			close(held)
			<-letGo
			return nil
		})
	}()
	<-held

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := b.ScanDisc(ctx, 0); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v, want the wait to end with its context", err)
	}
}

func TestAnUnknownDriveIsRefused(t *testing.T) {
	b, _ := newTestBroker(t, &fakeEngine{isolated: true})
	if _, err := b.ScanDisc(context.Background(), 7); !errors.Is(err, ErrUnknownDrive) {
		t.Errorf("err = %v, want ErrUnknownDrive", err)
	}
}

// Drives are listed from sysfs, and numbered by their node.
func TestSlotsComeFromSysfs(t *testing.T) {
	root := t.TempDir()
	for _, d := range []struct{ name, vendor, model string }{
		{"sr2", "PIONEER ", "BD-RW   BDR-XD08U"},
		{"sr1", "HL-DT-ST", "BD-RE BU40N"},
		{"sda", "ATA", "disk"},
	} {
		dev := filepath.Join(root, d.name, "device")
		_ = os.MkdirAll(dev, 0o777)
		_ = os.WriteFile(filepath.Join(dev, "vendor"), []byte(d.vendor+"\n"), 0o644)
		_ = os.WriteFile(filepath.Join(dev, "model"), []byte(d.model+"\n"), 0o644)
	}
	slots, err := readSlots(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(slots) != 2 || slots[0].Index != 1 || slots[0].Device != "/dev/sr1" ||
		slots[1].Index != 2 || slots[1].Vendor != "PIONEER" {
		t.Errorf("slots = %+v", slots)
	}
}

type recordingRescuer struct {
	mu   sync.Mutex
	args []string
}

func (r *recordingRescuer) Run(_ context.Context, args []string, _ func(string)) error {
	r.mu.Lock()
	r.args = args
	r.mu.Unlock()
	return nil
}

// source is the first non-flag argument ddrescue was given.
func (r *recordingRescuer) source() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, a := range r.args {
		if len(a) > 0 && a[0] != '-' {
			return a
		}
	}
	return ""
}
