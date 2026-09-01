//go:build windows

package makemkv

import (
	"os"
	"os/exec"
	"time"
)

// terminateGrace has no effect on Windows, which offers no way to ask a console
// process to stop that is both catchable and reliable from a parent. Declared
// so the package reads the same on both platforms.
var terminateGrace = 10 * time.Second

// configureTeardown leaves exec.CommandContext's default in place.
//
// The drive-locking problem this guards against is real on Windows too, but the
// remedy is not portable: there are no POSIX process groups to signal, and
// TerminateProcess is as uncatchable as SIGKILL. BluForge ships as a Linux
// container; this exists so the package still builds.
func configureTeardown(cmd *exec.Cmd) {}

// terminate kills the process. See configureTeardown for why there is nothing
// gentler to do here.
func terminate(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return os.ErrProcessDone
	}
	return cmd.Process.Kill()
}
