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
// We ask with SIGINT — the interactive Ctrl-C a CLI tool implements to abort
// and clean up — rather than SIGTERM, because killing makemkvcon mid-read with
// SIGTERM/SIGKILL was leaving the USB bridge mid-command and wedging the drive
// on seamless-branching discs. SIGINT gives it the shutdown it knows.
//
// This is hygiene rather than a cure: a process already blocked on a dead
// bridge is in uninterruptible sleep and takes no signal at all. It matters for
// the process that can still answer. See configureTeardown.
func TestACancelledCommandIsAskedToStopBeforeItIsKilled(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "m")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cmd := exec.CommandContext(ctx, "sh", "-c",
		`trap 'touch "$0.int"; exit 0' INT; touch "$0.ready"; i=0; while [ $i -lt 200 ]; do sleep 0.05; i=$((i+1)); done`,
		marker)
	configureTeardown(cmd)

	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	waitForFile(t, marker+".ready")

	cancel()
	_ = cmd.Wait()

	if _, err := os.Stat(marker + ".int"); err != nil {
		t.Error("the process was killed outright — it never got the SIGINT to release the drive")
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
		`trap "" INT; touch "$0.ready"; i=0; while [ $i -lt 200 ]; do sleep 0.05; i=$((i+1)); done`,
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
//
// The grandchild runs in the foreground, not with `&`. A POSIX non-interactive
// shell forces background jobs to ignore SIGINT and a trap cannot re-arm a
// signal that was SIG_IGN on entry — a shell quirk, not how makemkvcon's real
// child behaves. makemkvcon spawns a Java runtime for BD-Java discs and the JVM
// installs its own SIGINT handler, so group-delivered SIGINT reaches it; a
// foreground child with the default disposition models that faithfully.
func TestTheWholeProcessGroupIsToldToStop(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "m")
	script := filepath.Join(dir, "parent.sh")

	// The parent has no trap, so a group SIGINT terminates it by default. The
	// foreground grandchild handles SIGINT and writes its marker; that it runs
	// at all proves the signal reached past the process we started.
	body := `m="$1"
sh -c 'trap "touch \"$0.grand\"; exit 0" INT; touch "$0.gready"; i=0; while [ $i -lt 200 ]; do sleep 0.05; i=$((i+1)); done' "$m"
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
	waitForFile(t, marker+".gready")

	cancel()
	_ = cmd.Wait()

	// The grandchild writes its marker from its own INT handler; it only runs
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
