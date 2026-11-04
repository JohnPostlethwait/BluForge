package makemkv

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// IsolateSubcommand is the hidden argument that makes the bluforge binary act
// as the drive-isolation helper rather than the server. main dispatches it to
// RunIsolateHelper before anything else runs.
const IsolateSubcommand = "__isolate-drive"

// devTargetPrefix is makemkvcon's source form for a drive named by its device
// node. Inside an isolated /dev the drive is the only one makemkvcon can see,
// so it becomes disc:0 there; the device path is the address that stays true.
const devTargetPrefix = "dev:"

// makemkvconCommand builds the command for one makemkvcon invocation.
//
// makemkvcon opens every optical drive it can see on start, whatever source it
// is given. A rip on one drive therefore sent commands to a drive another
// makemkvcon was ripping, and that wedged the BU40N's USB bridge into a reset
// storm on Jotunheim. A command addressed as dev:/dev/srN runs in a private
// mount namespace whose /dev holds that drive and nothing else, so it cannot
// touch any other drive. Every other command runs makemkvcon directly.
func makemkvconCommand(ctx context.Context, args ...string) *exec.Cmd {
	device := isolatedDevice(args)
	if device == "" {
		cmd := exec.CommandContext(ctx, "makemkvcon", args...)
		configureTeardown(cmd)
		return cmd
	}
	return isolatedCommand(ctx, device, args)
}

// isolatedCommand builds a command that runs makemkvcon confined to device. A
// variable so tests, whose binary is not bluforge, can run makemkvcon directly.
var isolatedCommand = helperCommand

// helperBinary is the isolation helper's file name, beside the bluforge
// binary. It is bluforge itself carrying file capability cap_sys_admin+ei (see
// the Dockerfile), so it alone can take up the CAP_SYS_ADMIN the entrypoint
// passes down as inheritable. The server and every other child it starts hold
// no capability in effect.
const helperBinary = "bluforge-isolate"

// IsHelperBinary reports whether this process is the capability-carrying
// helper. main refuses to run the server as it: the helper holds
// CAP_SYS_ADMIN and must do nothing but isolate one makemkvcon.
func IsHelperBinary() bool {
	self, err := os.Executable()
	return err == nil && filepath.Base(self) == helperBinary
}

// helperPath returns the isolation helper to run: the capability-carrying copy
// beside this binary when there is one, otherwise this binary, which can set up
// the namespace itself only when it runs as root.
func helperPath() (string, error) {
	self, err := os.Executable()
	if err != nil {
		return "", err
	}
	if p := filepath.Join(filepath.Dir(self), helperBinary); fileExists(p) {
		return p, nil
	}
	return self, nil
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// helperCommand runs makemkvcon under the isolation helper, which moves itself
// into a new mount namespace before it starts makemkvcon.
func helperCommand(ctx context.Context, device string, args []string) *exec.Cmd {
	self, err := helperPath()
	if err != nil {
		// Without our own path there is no helper to run. Running makemkvcon
		// unconfined instead would be the fault this exists to prevent, so the
		// command fails to start and says why.
		cmd := exec.CommandContext(ctx, "makemkvcon", args...)
		cmd.Err = fmt.Errorf("makemkv: cannot confine makemkvcon to %s: %w", device, err)
		return cmd
	}
	helperArgs := append([]string{IsolateSubcommand, device, "--", "makemkvcon"}, args...)
	cmd := exec.CommandContext(ctx, self, helperArgs...)
	configureTeardown(cmd)
	return cmd
}

// isolatedDevice returns the device a dev: source names, or "".
func isolatedDevice(args []string) string {
	for _, a := range args {
		if strings.HasPrefix(a, devTargetPrefix) {
			return strings.TrimPrefix(a, devTargetPrefix)
		}
	}
	return ""
}
