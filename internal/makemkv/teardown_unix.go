//go:build !windows

package makemkv

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
	"time"
)

// terminateGrace is how long makemkvcon is given to put the drive down after
// being asked to stop, before it is killed outright.
//
// Long enough for a close and an unlock, short enough that a wedged process
// does not hold the executor mutex much past the timeout that gave up on it.
//
// A variable rather than a constant so the teardown tests do not have to sleep
// through it.
var terminateGrace = 10 * time.Second

// configureTeardown makes a command stoppable without leaving the drive locked.
//
// exec.CommandContext defaults to SIGKILL on the direct child, which is the
// worst of both: SIGKILL cannot be caught, so makemkvcon never closes its
// handle on the drive or unlocks the tray — MakeMKV holds both for the length
// of an operation on purpose — and anything it spawned survives, still holding
// the device.
//
// So: the command gets its own process group, cancellation asks the whole group
// to stop with SIGINT (see terminate for why that signal), and only a process
// that will not go is killed. Go escalates to SIGKILL on the direct child once
// terminateGrace expires, which is the backstop rather than the first move.
//
// What this buys is the group: nothing makemkvcon spawned is orphaned still
// holding the device. It does not buy makemkvcon a clean shutdown. makemkvcon
// installs no SIGINT handler: a rip cancelled mid-copy on Jotunheim
// (2026-11-11) exited "signal: interrupt", dying on the spot like SIGKILL. No
// signal stops makemkvcon cleanly mid-read, so the only way not to interrupt a
// drive command is not to stop makemkvcon mid-read.
//
// A process already blocked on I/O to a dead USB bridge is in uninterruptible
// sleep and takes no signal at all. See internal/mpls.MountRegistry for the
// mount side of drive hygiene.
func configureTeardown(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return terminate(cmd) }
	cmd.WaitDelay = terminateGrace
}

// terminate asks a command's process group to stop, with SIGINT.
//
// SIGINT was chosen in the belief that makemkvcon handles it as Ctrl-C and
// closes the drive before exiting. It does not; see configureTeardown. An
// unhandled SIGINT terminates the process as SIGTERM would, so the choice is
// harmless, and the Java runtime makemkvcon starts may yet handle it.
//
// The negative pid addresses the group, which is the point: killing the process
// we started while a child of it still has the drive open leaves the drive
// exactly as wedged as killing nothing would. makemkvcon spawns a Java runtime
// for BD-Java discs, and that child holds the device too.
//
// A process that has already exited is not an error — it is the outcome we
// wanted — and os.ErrProcessDone tells Wait to report the context's error
// rather than this one.
func terminate(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return os.ErrProcessDone
	}

	err := syscall.Kill(-cmd.Process.Pid, syscall.SIGINT)
	if err == nil {
		return nil
	}
	if errors.Is(err, syscall.ESRCH) {
		return os.ErrProcessDone
	}

	// No group to signal — fall back to the process itself rather than leaving
	// it running.
	return cmd.Process.Signal(syscall.SIGINT)
}
