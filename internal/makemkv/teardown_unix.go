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
// In production a drive that had answered twenty-eight consecutive polls in
// four to five seconds each went to permanently unresponsive in a single step,
// and that step was the first listing we killed. Every listing afterwards
// blocked and was killed in turn.
//
// So: the command gets its own process group, cancellation asks the whole group
// to stop, and only a process that will not go is killed. Go escalates to
// SIGKILL on the direct child once terminateGrace expires, which is the
// backstop rather than the first move.
func configureTeardown(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return terminate(cmd) }
	cmd.WaitDelay = terminateGrace
}

// terminate asks a command's process group to stop.
//
// The negative pid addresses the group, which is the point: killing the process
// we started while a child of it still has the drive open leaves the drive
// exactly as wedged as killing nothing would.
//
// A process that has already exited is not an error — it is the outcome we
// wanted — and os.ErrProcessDone tells Wait to report the context's error
// rather than this one.
func terminate(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return os.ErrProcessDone
	}

	err := syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
	if err == nil {
		return nil
	}
	if errors.Is(err, syscall.ESRCH) {
		return os.ErrProcessDone
	}

	// No group to signal — fall back to the process itself rather than leaving
	// it running.
	return cmd.Process.Signal(syscall.SIGTERM)
}
