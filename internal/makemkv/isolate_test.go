package makemkv

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// Jotunheim, 2026-11-04. makemkvcon opens every drive it can see, whatever
// source it is given, so with per-drive locks alone a rip starting on one drive
// sent commands to a drive another makemkvcon was ripping. The BU40N's USB
// bridge wedged into a reset storm. Without isolation, a second drive's rip
// must wait for the first to finish.
func TestWithoutIsolationRipsOnTwoDrivesRunOneAtATime(t *testing.T) {
	fr := newFakeRip(t)
	ex := NewExecutor(WithRunner(newGatedRunner()), WithUnmountBefore(noUnmount))

	rip0 := startRip(ex, 0, t.TempDir())
	fr.waitStarted(t, "disc:0")
	rip1 := startRip(ex, 1, t.TempDir())

	time.Sleep(300 * time.Millisecond)
	if _, err := os.Stat(filepath.Join(fr.dir, "started-disc:1")); err == nil {
		t.Fatal("a second makemkvcon started on drive 1 while drive 0 ripped, without isolation")
	}

	fr.release(t, "disc:0")
	waitClosed(t, rip0, "the rip of drive 0")
	fr.waitStarted(t, "disc:1")
	fr.release(t, "disc:1")
	waitClosed(t, rip1, "the rip of drive 1")
}

// An isolated command must name its drive by device node: inside the private
// /dev it is the only drive, so makemkvcon numbers it disc:0, and the drive's
// own index would name nothing.
func TestIsolatedScanAddressesTheDriveByItsDeviceNode(t *testing.T) {
	g := newGatedRunner()
	var got [][]string
	ex := NewExecutor(WithRunner(&argsRunner{inner: g, got: &got}), WithUnmountBefore(noUnmount), WithDriveIsolation(true))

	_, _ = ex.ScanDisc(context.Background(), 1)

	for _, args := range got {
		if slices.Contains(args, "info") && !slices.Contains(args, "disc:9999") {
			if !slices.Contains(args, "dev:/dev/sr1") {
				t.Errorf("scan args = %v, want the source dev:/dev/sr1", args)
			}
			return
		}
	}
	t.Fatalf("no scan was run; commands: %v", got)
}

// A disc whose device path is unknown cannot be isolated. It must not run
// beside other drives' work: it would reach them all.
func TestWithoutADevicePathAScanClaimsEveryDrive(t *testing.T) {
	ex := NewExecutor(WithRunner(newGatedRunner()), WithDriveIsolation(true))
	release, target, _ := ex.claim(context.Background(), DiscSource(3), "")
	defer release()

	if target != "disc:3" {
		t.Errorf("target = %q, want disc:3", target)
	}
	if ex.locks.all.TryRLock() {
		ex.locks.all.RUnlock()
		t.Error("a command that cannot be isolated did not claim every drive")
	}
}

// The helper is this binary re-run with the drive and the makemkvcon argv.
func TestHelperCommandRunsMakemkvconUnderTheHelper(t *testing.T) {
	args := []string{"-r", "info", "dev:/dev/sr1"}
	cmd := helperCommand(context.Background(), "/dev/sr1", args)

	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	want := append([]string{self, IsolateSubcommand, "/dev/sr1", "--", "makemkvcon"}, args...)
	if !slices.Equal(cmd.Args, want) {
		t.Errorf("args = %v, want %v", cmd.Args, want)
	}
}

// Commands not addressed to a device run makemkvcon directly: a listing must
// see every drive.
func TestAListingIsNotIsolated(t *testing.T) {
	cmd := makemkvconCommand(context.Background(), "-r", "--cache=1", "info", "disc:9999")
	if filepath.Base(cmd.Path) != "makemkvcon" && cmd.Args[0] != "makemkvcon" {
		t.Errorf("listing ran %v, want makemkvcon directly", cmd.Args)
	}
}

// argsRunner records each command's arguments before handing it on.
type argsRunner struct {
	inner *gatedRunner
	got   *[][]string
}

func (a *argsRunner) Run(ctx context.Context, args ...string) (*strings.Reader, error) {
	*a.got = append(*a.got, append([]string(nil), args...))
	return a.inner.Run(ctx, args...)
}
