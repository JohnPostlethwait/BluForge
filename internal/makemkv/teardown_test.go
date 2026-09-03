//go:build !windows

package makemkv

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// waitForFile polls until path exists, so a test never races the shell it just
// started.
func waitForFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", path)
}

// shortGrace makes the escalation observable without a ten second test.
func shortGrace(t *testing.T, d time.Duration) {
	t.Helper()
	prev := terminateGrace
	terminateGrace = d
	t.Cleanup(func() { terminateGrace = prev })
}

// MakeMKV holds the drive open and locks the tray for the length of an
// operation, and releases both on its way out. SIGKILL cannot be caught, so a
// makemkvcon killed at a timeout never gets to do that.
//
// This is hygiene rather than a cure: a process already blocked on a dead
// bridge is in uninterruptible sleep and takes no signal at all, SIGTERM or
// SIGKILL alike. It matters for the process that can still answer. See
// configureTeardown for why the stronger claim was withdrawn.
func TestACancelledCommandIsAskedToStopBeforeItIsKilled(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "m")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cmd := exec.CommandContext(ctx, "sh", "-c",
		`trap 'touch "$0.term"; exit 0' TERM; touch "$0.ready"; i=0; while [ $i -lt 200 ]; do sleep 0.05; i=$((i+1)); done`,
		marker)
	configureTeardown(cmd)

	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	waitForFile(t, marker+".ready")

	cancel()
	_ = cmd.Wait()

	if _, err := os.Stat(marker + ".term"); err != nil {
		t.Error("the process was killed outright — it never got the chance to release the drive")
	}
}

// Asking is not the same as waiting forever. A makemkvcon that will not go has
// to be killed, or a wedged drive holds the executor mutex for good.
func TestAProcessThatIgnoresTheRequestIsStillKilled(t *testing.T) {
	shortGrace(t, 300*time.Millisecond)

	marker := filepath.Join(t.TempDir(), "m")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cmd := exec.CommandContext(ctx, "sh", "-c",
		`trap "" TERM; touch "$0.ready"; i=0; while [ $i -lt 200 ]; do sleep 0.05; i=$((i+1)); done`,
		marker)
	configureTeardown(cmd)

	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	waitForFile(t, marker+".ready")

	cancel()
	done := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("a process that ignored the request was never killed; it would hold the drive and the executor mutex indefinitely")
	}
}

// Killing only the process we started leaves anything it spawned holding the
// device, which is the same wedge by another route. The command runs in its own
// process group and the whole group is asked to stop.
func TestTheWholeProcessGroupIsToldToStop(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "m")
	script := filepath.Join(dir, "parent.sh")

	body := `m="$1"
( trap "touch \"$m.grand\"; exit 0" TERM
  touch "$m.gready"
  i=0; while [ $i -lt 200 ]; do sleep 0.05; i=$((i+1)); done ) &
touch "$m.ready"
wait
`
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cmd := exec.CommandContext(ctx, "sh", script, marker)
	configureTeardown(cmd)

	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	waitForFile(t, marker+".ready")
	waitForFile(t, marker+".gready")

	cancel()
	_ = cmd.Wait()

	// The grandchild writes its marker from its own TERM handler; it only runs
	// if the signal reached past the process we started.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(marker + ".grand"); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Error("a process spawned by the command was left running — it would keep the drive open after we thought we had let go")
}
