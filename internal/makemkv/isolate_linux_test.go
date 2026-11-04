//go:build linux

package makemkv

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
)

// listDevMode is a test-only helper mode: build the isolated /dev with the base
// nodes, print the /dev listing, then exec a program that prints its own
// effective capabilities.
const listDevMode = "--test-list-dev"

// TestMain lets the test binary act as the isolation helper, as bluforge's own
// main does, so the real helper can be exercised by the tests below.
func TestMain(m *testing.M) {
	if len(os.Args) > 2 && os.Args[1] == IsolateSubcommand {
		var err error
		switch os.Args[2] {
		case "--probe":
			err = RunIsolateProbe()
		case listDevMode:
			err = listIsolatedDev()
		default:
			err = RunIsolateHelper(os.Args[2:])
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, "helper:", err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func listIsolatedDev() error {
	if err := isolateDev(baseDevices); err != nil {
		return err
	}
	entries, err := os.ReadDir("/dev")
	if err != nil {
		return err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	fmt.Println(strings.Join(names, " "))
	// Exec, as the helper does for makemkvcon, so what prints is the program's
	// capability set after the exec rather than the helper's own.
	return syscall.Exec("/bin/grep", []string{"grep", "^CapEff:", "/proc/self/status"}, os.Environ())
}

// requireIsolation skips unless the test is meant to set up mount namespaces
// for real: it needs CAP_SYS_ADMIN, which only a privileged container has.
func requireIsolation(t *testing.T) {
	t.Helper()
	if os.Getenv("BLUFORGE_ISOLATION_TEST") != "1" {
		t.Skip("set BLUFORGE_ISOLATION_TEST=1 in a privileged container to run")
	}
}

// The probe runs the same mounts and privilege drop as a rip.
func TestProbeIsolationPasses(t *testing.T) {
	requireIsolation(t)
	if err := ProbeIsolation(context.Background()); err != nil {
		t.Fatalf("ProbeIsolation: %v", err)
	}
}

// Inside the helper's namespace /dev holds the base nodes and nothing else, and
// the caller's /dev is untouched.
func TestIsolatedDevHoldsOnlyTheBaseNodes(t *testing.T) {
	requireIsolation(t)
	before, err := os.ReadDir("/dev")
	if err != nil {
		t.Fatal(err)
	}
	helper, err := helperPath()
	if err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(helper, IsolateSubcommand, listDevMode)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("helper: %v: %s", err, out)
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if lines[0] != "full null random tty urandom zero" {
		t.Errorf("isolated /dev = %q, want only the base nodes", lines[0])
	}
	// A non-root program must come out of the helper holding no capability:
	// CAP_SYS_ADMIN is the helper's, for the mounts, and nobody else's.
	if os.Geteuid() != 0 {
		if len(lines) < 2 || strings.Trim(strings.TrimSpace(strings.TrimPrefix(lines[1], "CapEff:")), "0") != "" {
			t.Errorf("program capabilities after the helper = %v, want none", lines[1:])
		}
	}

	after, err := os.ReadDir("/dev")
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Errorf("the caller's /dev changed: %d entries before, %d after", len(before), len(after))
	}
}
