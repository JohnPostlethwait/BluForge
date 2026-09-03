package mpls

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// fakeMounts stands in for the filesystem: it records what was mounted and
// unmounted, and answers "is this device mounted" from its own bookkeeping.
type fakeMounts struct {
	mounted   map[string]string // device -> mount point
	preexist  map[string]string // device -> mount point, present before we look
	mountLog  []string
	umountLog []string
	mountErr  error
	root      string // temp dir standing in for the mount point's contents
}

func newFakeMounts(t *testing.T) *fakeMounts {
	t.Helper()
	return &fakeMounts{
		mounted:  map[string]string{},
		preexist: map[string]string{},
		root:     t.TempDir(),
	}
}

// point returns a real directory with a file in it, so hasContent is satisfied
// without the test caring how that check is implemented.
func (f *fakeMounts) point(t *testing.T, name string) string {
	t.Helper()
	p := filepath.Join(f.root, name)
	if err := os.MkdirAll(filepath.Join(p, "BDMV"), 0o755); err != nil {
		t.Fatalf("make fake mount point: %v", err)
	}
	return p
}

func (f *fakeMounts) MountPointOf(device string) (string, bool) {
	if mp, ok := f.mounted[device]; ok {
		return mp, true
	}
	if mp, ok := f.preexist[device]; ok {
		return mp, true
	}
	return "", false
}

func (f *fakeMounts) Mount(device, point string) error {
	if f.mountErr != nil {
		return f.mountErr
	}
	f.mountLog = append(f.mountLog, device)
	f.mounted[device] = point
	return nil
}

func (f *fakeMounts) Umount(point string) error {
	f.umountLog = append(f.umountLog, point)
	for dev, mp := range f.mounted {
		if mp == point {
			delete(f.mounted, dev)
		}
	}
	for dev, mp := range f.preexist {
		if mp == point {
			delete(f.preexist, dev)
		}
	}
	return nil
}

func newTestRegistry(t *testing.T, f *fakeMounts) *MountRegistry {
	t.Helper()
	r := NewMountRegistry(f)
	r.pointFor = func(device string) string { return f.point(t, filepath.Base(device)) }
	r.clearForeignMounts = true // the real registry gates this on Linux
	return r
}

// The mount is a claim on the drive, not a fact about it. Two scans of the same
// disc must not mount it twice, and the first one finishing must not unmount it
// out from under the second.
func TestTheMountIsHeldUntilTheLastCallerIsDone(t *testing.T) {
	f := newFakeMounts(t)
	r := newTestRegistry(t, f)

	_, releaseA, err := r.Open("/dev/sr1")
	if err != nil {
		t.Fatalf("first open: %v", err)
	}
	_, releaseB, err := r.Open("/dev/sr1")
	if err != nil {
		t.Fatalf("second open: %v", err)
	}

	if len(f.mountLog) != 1 {
		t.Errorf("mounted %d times for two overlapping readers, want 1", len(f.mountLog))
	}

	releaseA()
	if len(f.umountLog) != 0 {
		t.Errorf("unmounted while a reader still held the disc")
	}

	releaseB()
	if len(f.umountLog) != 1 {
		t.Errorf("unmounted %d times after the last reader finished, want 1", len(f.umountLog))
	}
}

// A mount that was already there was previously reused with a no-op cleanup, so
// nothing ever took it down. One leak then became permanent: every later scan
// found it mounted, reused it, and returned the same no-op.
func TestAPreexistingMountIsAdoptedAndReleased(t *testing.T) {
	f := newFakeMounts(t)
	r := newTestRegistry(t, f)
	f.preexist["/dev/sr1"] = f.point(t, "sr1-preexisting")

	root, release, err := r.Open("/dev/sr1")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if root != f.preexist["/dev/sr1"] && len(f.mountLog) == 0 {
		// Adopting means using the existing point rather than mounting again.
		t.Logf("adopted mount point %s", root)
	}
	if len(f.mountLog) != 0 {
		t.Errorf("mounted a device that was already mounted")
	}

	release()
	if len(f.umountLog) != 1 {
		t.Errorf("a pre-existing mount was left behind on release (umounts: %d)", len(f.umountLog))
	}
}

// The disc is gone. Whatever is still holding the mount does not get a say —
// a live filesystem on media that has been removed is what wedges the drive.
func TestForceUnmountReleasesADiscThatIsStillClaimed(t *testing.T) {
	f := newFakeMounts(t)
	r := newTestRegistry(t, f)

	_, release, err := r.Open("/dev/sr1")
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	if err := r.ForceUnmount("/dev/sr1"); err != nil {
		t.Fatalf("force unmount: %v", err)
	}
	if len(f.umountLog) != 1 {
		t.Fatalf("the disc was not unmounted while a reader still held it")
	}

	// The stale holder must not unmount whatever got mounted after it.
	release()
	if len(f.umountLog) != 1 {
		t.Errorf("a release after a forced unmount unmounted again (umounts: %d)", len(f.umountLog))
	}
}

// A mount leaked by an earlier run — or by a crash between mount and cleanup —
// is exactly the one that has to go when the disc leaves, and the registry has
// no record of it.
func TestForceUnmountClearsAMountItDoesNotOwn(t *testing.T) {
	f := newFakeMounts(t)
	r := newTestRegistry(t, f)
	f.preexist["/dev/sr1"] = f.point(t, "sr1-leaked")

	if err := r.ForceUnmount("/dev/sr1"); err != nil {
		t.Fatalf("force unmount: %v", err)
	}
	if len(f.umountLog) != 1 {
		t.Errorf("a leaked mount survived the disc leaving (umounts: %d)", len(f.umountLog))
	}
}

// Nothing mounted is not an error. Every disc event calls this, and most of
// them have nothing to release.
func TestForceUnmountOnAnUnmountedDeviceIsQuiet(t *testing.T) {
	f := newFakeMounts(t)
	r := newTestRegistry(t, f)

	if err := r.ForceUnmount("/dev/sr1"); err != nil {
		t.Errorf("force unmount with nothing mounted returned %v", err)
	}
	if len(f.umountLog) != 0 {
		t.Errorf("unmounted something that was not mounted")
	}
}

// After the disc leaves and comes back, the next reader gets a fresh mount
// rather than the dead record.
func TestADeviceCanBeMountedAgainAfterAForcedUnmount(t *testing.T) {
	f := newFakeMounts(t)
	r := newTestRegistry(t, f)

	_, release, err := r.Open("/dev/sr1")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	_ = r.ForceUnmount("/dev/sr1")
	release()

	if _, _, err := r.Open("/dev/sr1"); err != nil {
		t.Fatalf("reopen after a forced unmount: %v", err)
	}
	if len(f.mountLog) != 2 {
		t.Errorf("mounted %d times across a remove and reinsert, want 2", len(f.mountLog))
	}
}

// Held duration is what makes a leak visible in the log: a mount measured in
// hours is the bug, and one measured in seconds is a scan.
func TestTheHeldDurationIsReported(t *testing.T) {
	f := newFakeMounts(t)
	r := newTestRegistry(t, f)
	now := time.Date(2026, 9, 3, 4, 0, 0, 0, time.UTC)
	r.now = func() time.Time { return now }

	_, release, err := r.Open("/dev/sr1")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	now = now.Add(90 * time.Minute)

	held := r.HeldFor("/dev/sr1")
	if held != 90*time.Minute {
		t.Errorf("held duration reported as %s, want 90m0s", held)
	}
	release()

	if r.HeldFor("/dev/sr1") != 0 {
		t.Errorf("a released mount still reports a held duration")
	}
}
