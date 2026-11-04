package makemkv

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// gatedRunner is a makemkvcon stand-in for the commands that go through the
// executor's runner (listings, scans, backups). A command on a gated target
// blocks until the test releases it; anything else answers at once.
type gatedRunner struct {
	mu      sync.Mutex
	started map[string]chan struct{} // closed when a command on target starts
	release map[string]chan struct{} // close to let a command on target finish
	ran     []string                 // targets run, in order
	lists   int
}

func newGatedRunner(targets ...string) *gatedRunner {
	g := &gatedRunner{
		started: make(map[string]chan struct{}),
		release: make(map[string]chan struct{}),
	}
	for _, t := range targets {
		g.started[t] = make(chan struct{})
		g.release[t] = make(chan struct{})
	}
	return g
}

// commandTarget finds the disc:N argument a command acts on.
func commandTarget(args []string) string {
	for _, a := range args {
		if strings.HasPrefix(a, "disc:") {
			return a
		}
	}
	return ""
}

func (g *gatedRunner) Run(ctx context.Context, args ...string) (*strings.Reader, error) {
	target := commandTarget(args)
	g.mu.Lock()
	if target == "disc:9999" {
		g.lists++
		g.mu.Unlock()
		return strings.NewReader(twoDriverOutput), nil
	}
	g.ran = append(g.ran, target)
	started, release := g.started[target], g.release[target]
	g.mu.Unlock()
	if started != nil {
		close(started)
		select {
		case <-release:
		case <-ctx.Done():
			return strings.NewReader(""), ctx.Err()
		}
	}
	return strings.NewReader(`CINFO:2,0,"A_DISC"` + "\n"), nil
}

func (g *gatedRunner) listCount() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.lists
}

func (g *gatedRunner) ranTargets() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]string(nil), g.ran...)
}

// fakeRip puts a makemkvcon on PATH for StartRip, which runs the binary
// directly. Each run records its HOME, announces itself by creating
// started-<target>, and waits for release-<target> before exiting.
type fakeRip struct{ dir string }

func newFakeRip(t *testing.T) *fakeRip {
	t.Helper()
	dir := t.TempDir()
	script := `#!/bin/sh
for a in "$@"; do case "$a" in disc:*) target="$a";; esac; done
echo "$HOME" >> "` + dir + `/homes"
: > "` + dir + `/started-$target"
while [ ! -e "` + dir + `/release-$target" ]; do sleep 0.02; done
exit 0
`
	if err := os.WriteFile(filepath.Join(dir, "makemkvcon"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return &fakeRip{dir: dir}
}

func (f *fakeRip) waitStarted(t *testing.T, target string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(filepath.Join(f.dir, "started-"+target)); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("the rip on %s never started", target)
}

func (f *fakeRip) release(t *testing.T, target string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(f.dir, "release-"+target), nil, 0o644); err != nil {
		t.Fatal(err)
	}
}

func (f *fakeRip) homes(t *testing.T) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(f.dir, "homes"))
	if err != nil {
		t.Fatal(err)
	}
	return strings.Fields(string(data))
}

func waitClosed(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(3 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
	}
}

func noUnmount(string) error { return nil }

// startRip runs StartRip on drive in the background, returning when it ends.
func startRip(ex *Executor, drive int, outDir string) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		_ = ex.StartRip(context.Background(), DiscSource(drive), 0, "", outDir, nil, nil)
		close(done)
	}()
	return done
}

// Two drives are two devices. A rip on one must not stop a scan of the other:
// that is the whole point of owning more than one drive.
func TestScanOfAnotherDriveRunsWhileOneDriveRips(t *testing.T) {
	fr := newFakeRip(t)
	g := newGatedRunner()
	ex := NewExecutor(WithRunner(g), WithUnmountBefore(noUnmount))

	ripDone := startRip(ex, 0, t.TempDir())
	fr.waitStarted(t, "disc:0")

	scanDone := make(chan struct{})
	go func() {
		_, _ = ex.ScanDisc(context.Background(), 1)
		close(scanDone)
	}()
	waitClosed(t, scanDone, "the scan of drive 1 while drive 0 rips")

	fr.release(t, "disc:0")
	waitClosed(t, ripDone, "the rip of drive 0")
}

// Two rips on two drives run side by side.
func TestRipsOnTwoDrivesRunTogether(t *testing.T) {
	fr := newFakeRip(t)
	ex := NewExecutor(WithRunner(newGatedRunner()), WithUnmountBefore(noUnmount))

	rip0 := startRip(ex, 0, t.TempDir())
	fr.waitStarted(t, "disc:0")
	rip1 := startRip(ex, 1, t.TempDir())
	fr.waitStarted(t, "disc:1")

	fr.release(t, "disc:0")
	fr.release(t, "disc:1")
	waitClosed(t, rip0, "the rip of drive 0")
	waitClosed(t, rip1, "the rip of drive 1")
}

// Each rip runs under its own HOME. makemkvcon writes its --debug log to
// $HOME/MakeMKV_log.txt, and the failure reason is read back from there; two
// rips sharing one HOME would write into, and read back, the same file.
func TestEachRipGetsItsOwnHome(t *testing.T) {
	fr := newFakeRip(t)
	ex := NewExecutor(WithRunner(newGatedRunner()), WithUnmountBefore(noUnmount))

	rip0 := startRip(ex, 0, t.TempDir())
	fr.waitStarted(t, "disc:0")
	rip1 := startRip(ex, 1, t.TempDir())
	fr.waitStarted(t, "disc:1")
	fr.release(t, "disc:0")
	fr.release(t, "disc:1")
	<-rip0
	<-rip1

	homes := fr.homes(t)
	if len(homes) != 2 {
		t.Fatalf("recorded HOMEs %v, want two", homes)
	}
	if homes[0] == homes[1] {
		t.Errorf("both rips ran with HOME=%s; each needs its own", homes[0])
	}
	for _, h := range homes {
		if h == os.Getenv("HOME") {
			t.Errorf("a rip ran with the shared HOME %s", h)
		}
	}
}

// One drive still does one thing at a time: a scan of a drive that is ripping
// waits for the rip to finish.
func TestScanOfARippingDriveWaits(t *testing.T) {
	fr := newFakeRip(t)
	g := newGatedRunner()
	ex := NewExecutor(WithRunner(g), WithUnmountBefore(noUnmount))

	ripDone := startRip(ex, 0, t.TempDir())
	fr.waitStarted(t, "disc:0")

	scanDone := make(chan struct{})
	go func() {
		_, _ = ex.ScanDisc(context.Background(), 0)
		close(scanDone)
	}()
	select {
	case <-scanDone:
		t.Fatal("a scan of drive 0 ran while drive 0 was ripping")
	case <-time.After(200 * time.Millisecond):
	}

	fr.release(t, "disc:0")
	waitClosed(t, ripDone, "the rip of drive 0")
	waitClosed(t, scanDone, "the scan of drive 0 after its rip")
}

// A drive listing probes every drive, so it must not run while any drive is
// being read: an unlocked poll took a ddrescue rescue from 14 MB/s to 2.4 MB/s.
func TestListingWaitsForEveryBusyDrive(t *testing.T) {
	g := newGatedRunner("disc:0")
	ex := NewExecutor(WithRunner(g), WithUnmountBefore(noUnmount))

	backupDone := make(chan struct{})
	go func() {
		_ = ex.Backup(context.Background(), 0, t.TempDir(), nil)
		close(backupDone)
	}()
	waitClosed(t, g.started["disc:0"], "the backup of drive 0 to start")
	before := g.listCount()

	if _, ok, _ := ex.TryListDrives(context.Background()); ok {
		t.Fatal("TryListDrives listed while drive 0 was busy")
	}

	listDone := make(chan struct{})
	go func() {
		_, _ = ex.ListDrives(context.Background())
		close(listDone)
	}()
	select {
	case <-listDone:
		t.Fatal("ListDrives ran while drive 0 was busy")
	case <-time.After(200 * time.Millisecond):
	}

	close(g.release["disc:0"])
	waitClosed(t, backupDone, "the backup of drive 0")
	waitClosed(t, listDone, "the listing once drive 0 was free")
	if got := g.listCount() - before; got != 1 {
		t.Errorf("listings run = %d, want 1", got)
	}
}

// Starting work on a free drive must not wait for a listing while another
// drive is busy, because a listing would sit behind that drive's rip for its
// whole length. The device path comes from the last completed listing instead.
func TestDevicePathComesFromTheLastListingWhileAnotherDriveIsBusy(t *testing.T) {
	g := newGatedRunner("disc:0")
	var mu sync.Mutex
	var unmounted []string
	ex := NewExecutor(WithRunner(g), WithUnmountBefore(func(p string) error {
		mu.Lock()
		unmounted = append(unmounted, p)
		mu.Unlock()
		return nil
	}))

	if _, err := ex.ListDrives(context.Background()); err != nil {
		t.Fatal(err)
	}

	backupDone := make(chan struct{})
	go func() {
		_ = ex.Backup(context.Background(), 0, t.TempDir(), nil)
		close(backupDone)
	}()
	waitClosed(t, g.started["disc:0"], "the backup of drive 0 to start")

	scanDone := make(chan struct{})
	go func() {
		_, _ = ex.ScanDisc(context.Background(), 1)
		close(scanDone)
	}()
	waitClosed(t, scanDone, "the scan of drive 1 while drive 0 is busy")

	mu.Lock()
	got := append([]string(nil), unmounted...)
	mu.Unlock()
	if len(got) != 1 || got[0] != "/dev/sr1" {
		t.Errorf("unmounted %v; want [/dev/sr1] released before scanning drive 1", got)
	}

	close(g.release["disc:0"])
	waitClosed(t, backupDone, "the backup of drive 0")
}

// Work held through LockDrive — a salvage's ddrescue, which runs outside this
// executor — still holds every drive, as before.
func TestLockDriveStillExcludesEveryDrive(t *testing.T) {
	g := newGatedRunner()
	ex := NewExecutor(WithRunner(g), WithUnmountBefore(noUnmount))

	ex.LockDrive()
	scanDone := make(chan struct{})
	go func() {
		_, _ = ex.ScanDisc(context.Background(), 1)
		close(scanDone)
	}()
	select {
	case <-scanDone:
		t.Fatal("a scan ran while LockDrive was held")
	case <-time.After(200 * time.Millisecond):
	}
	ex.UnlockDrive()
	waitClosed(t, scanDone, "the scan once LockDrive was released")
}

// A folder source has no drive of its own here, so it keeps the old
// behaviour: it runs alone.
func TestFolderScanRunsAlone(t *testing.T) {
	g := newGatedRunner("disc:0")
	ex := NewExecutor(WithRunner(g), WithUnmountBefore(noUnmount))

	backupDone := make(chan struct{})
	go func() {
		_ = ex.Backup(context.Background(), 0, t.TempDir(), nil)
		close(backupDone)
	}()
	waitClosed(t, g.started["disc:0"], "the backup of drive 0 to start")

	scanDone := make(chan struct{})
	go func() {
		_, _ = ex.ScanSource(context.Background(), FileSource(t.TempDir()))
		close(scanDone)
	}()
	select {
	case <-scanDone:
		t.Fatal("a folder scan ran while drive 0 was busy")
	case <-time.After(200 * time.Millisecond):
	}

	close(g.release["disc:0"])
	waitClosed(t, backupDone, "the backup of drive 0")
	waitClosed(t, scanDone, "the folder scan")
}
