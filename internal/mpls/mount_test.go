package mpls

import (
	"strings"
	"testing"
)

// A bare `mount <device>` only succeeds when an fstab entry exists — which the
// Docker entrypoint writes for the drives present at container start. A drive
// that appears later, or any environment without those entries, needs the
// filesystem and options given explicitly. Without the fallback, recovery on
// such a drive fails at the mount and reports "could not determine" forever.
func TestMountAttemptsIncludeExplicitUDF(t *testing.T) {
	attempts := mountAttempts("/dev/sr1", "/mnt/sr1")

	if len(attempts) < 2 {
		t.Fatalf("got %d mount attempts, want the fstab form plus an explicit fallback", len(attempts))
	}

	// The fstab form is tried first: it honours whatever options the entrypoint
	// configured, including the "user" option a non-root process needs.
	if len(attempts[0]) != 2 || attempts[0][0] != "mount" || attempts[0][1] != "/dev/sr1" {
		t.Errorf("first attempt = %v, want the bare fstab-driven form", attempts[0])
	}

	joined := strings.Join(attempts[len(attempts)-1], " ")
	for _, want := range []string{"-t", "udf", "ro", "/dev/sr1", "/mnt/sr1"} {
		if !strings.Contains(joined, want) {
			t.Errorf("fallback attempt %q is missing %q", joined, want)
		}
	}
}

func TestMountAttemptsAreReadOnly(t *testing.T) {
	// An optical disc is read-only anyway, but never issuing a writable mount
	// keeps a recovery attempt from being the thing that alters a disc.
	for _, attempt := range mountAttempts("/dev/sr0", "/mnt/sr0") {
		joined := strings.Join(attempt, " ")
		if strings.Contains(joined, "rw") {
			t.Errorf("attempt %q requests a writable mount", joined)
		}
	}
}
