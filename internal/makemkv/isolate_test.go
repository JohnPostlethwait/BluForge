package makemkv

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// With isolation a disc command runs with its drive alone in /dev, addressed
// by its node: inside the private /dev it is the only drive, so makemkvcon
// numbers it disc:0, and the drive's own index would name nothing.
func TestAnIsolatedDiscCommandSeesOnlyItsDrive(t *testing.T) {
	ex := NewExecutor(WithDriveIsolation(true))
	target, ctx, err := ex.resolve(context.Background(), discSrc(1))
	if err != nil {
		t.Fatal(err)
	}
	if target != "dev:/dev/sr1" {
		t.Errorf("target = %q, want dev:/dev/sr1", target)
	}
	if got := isolationFrom(ctx); got != "/dev/sr1" {
		t.Errorf("isolated to %q, want /dev/sr1", got)
	}
}

// A copy or a link tree is read through the filesystem, so with isolation its
// makemkvcon sees no drive at all.
func TestAnIsolatedCopySeesNoDrive(t *testing.T) {
	ex := NewExecutor(WithDriveIsolation(true))
	for _, src := range []Source{
		ISOSource("/scratch/disc.iso"),
		CopySource("/scratch/copy"),
		LinkedSource(1, "/scratch/links"),
	} {
		_, ctx, err := ex.resolve(context.Background(), src)
		if err != nil {
			t.Fatal(err)
		}
		if got := isolationFrom(ctx); got != NoDrive {
			t.Errorf("%v isolated to %q, want no drive", src, got)
		}
	}
}

// Without isolation nothing is confined; internal/drives makes such commands
// take turns instead.
func TestWithoutIsolationNothingIsConfined(t *testing.T) {
	ex := NewExecutor()
	for _, src := range []Source{discSrc(0), ISOSource("/scratch/disc.iso")} {
		_, ctx, err := ex.resolve(context.Background(), src)
		if err != nil {
			t.Fatal(err)
		}
		if got := isolationFrom(ctx); got != "" {
			t.Errorf("%v isolated to %q without isolation", src, got)
		}
		cmd := makemkvconCommand(ctx, "-r", "info", src.Arg())
		if filepath.Base(cmd.Path) != "makemkvcon" && cmd.Args[0] != "makemkvcon" {
			t.Errorf("%v ran %v, want makemkvcon directly", src, cmd.Args)
		}
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

// An isolated command goes through the helper.
func TestAnIsolatedCommandRunsUnderTheHelper(t *testing.T) {
	ctx := withIsolation(context.Background(), "/dev/sr2")
	cmd := makemkvconCommand(ctx, "-r", "info", "dev:/dev/sr2")
	if !slices.Contains(cmd.Args, IsolateSubcommand) || !slices.Contains(cmd.Args, "/dev/sr2") {
		t.Errorf("isolated command ran %v, want the helper confining it to /dev/sr2", cmd.Args)
	}
}
