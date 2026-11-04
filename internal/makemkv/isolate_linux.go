//go:build linux

package makemkv

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// baseDevices are the nodes makemkvcon needs that are not drives. This is the
// set a hand test on Jotunheim ran makemkvcon with; nothing else in /dev is
// required to list, scan or rip.
var baseDevices = []string{"null", "zero", "full", "random", "urandom", "tty"}

// RunIsolateHelper is the body of the isolation helper:
//
//	bluforge __isolate-drive /dev/srN -- makemkvcon args...
//
// It moves into a new mount namespace and replaces /dev there with a tmpfs
// holding only the drive's block node, the drive's SCSI generic node, and
// baseDevices, then execs the program. makemkvcon can open only what is in /dev,
// so the drive is the one drive it can reach.
//
// It returns only on failure; on success the process becomes the program.
func RunIsolateHelper(args []string) error {
	if len(args) < 3 || args[1] != "--" {
		return errors.New("usage: " + IsolateSubcommand + " /dev/srN -- program args...")
	}
	device, argv := args[0], args[2:]
	name := filepath.Base(device)
	if !strings.HasPrefix(name, "sr") {
		return fmt.Errorf("isolate: %s is not an optical drive node", device)
	}

	// Look the program up before /dev changes; nothing it needs lives there,
	// but a failure here is clearer before the mounts than after.
	prog, err := exec.LookPath(argv[0])
	if err != nil {
		return fmt.Errorf("isolate: %w", err)
	}

	nodes := append([]string{name}, baseDevices...)
	sgs, err := os.ReadDir(filepath.Join("/sys/block", name, "device", "scsi_generic"))
	if err != nil {
		return fmt.Errorf("isolate: find the SCSI generic node for %s: %w", device, err)
	}
	for _, sg := range sgs {
		nodes = append(nodes, sg.Name())
	}

	if err := isolateDev(nodes); err != nil {
		return fmt.Errorf("isolate %s: %w", device, err)
	}
	// makemkvcon carries no file capability, so it starts with none: this
	// helper's CAP_SYS_ADMIN does not survive the exec.
	return syscall.Exec(prog, argv, os.Environ())
}

// isolateDev moves this thread into a new mount namespace and replaces /dev
// there with a tmpfs holding the named nodes bound from the real /dev.
//
// A mount namespace belongs to a thread, so the thread is locked for good: the
// mounts and the exec that follows must all happen on the thread that moved.
// The exec replaces the whole process with that thread's view.
func isolateDev(nodes []string) error {
	runtime.LockOSThread()
	if err := unix.Unshare(unix.CLONE_NEWNS); err != nil {
		return fmt.Errorf("enter a new mount namespace: %w", err)
	}
	// Keep every mount below private, so nothing done here propagates back to
	// the server's namespace.
	if err := unix.Mount("", "/", "", unix.MS_REC|unix.MS_PRIVATE, ""); err != nil {
		return fmt.Errorf("make mounts private: %w", err)
	}
	real, err := os.MkdirTemp("", "bluforge-realdev-")
	if err != nil {
		return err
	}
	if err := unix.Mount("/dev", real, "", unix.MS_BIND|unix.MS_REC, ""); err != nil {
		return fmt.Errorf("bind the real /dev: %w", err)
	}
	if err := unix.Mount("tmpfs", "/dev", "tmpfs", unix.MS_NOSUID|unix.MS_NOEXEC, "mode=0755"); err != nil {
		return fmt.Errorf("mount a private /dev: %w", err)
	}
	for _, n := range nodes {
		src, dst := filepath.Join(real, n), filepath.Join("/dev", n)
		if _, err := os.Stat(src); err != nil {
			return fmt.Errorf("device node %s: %w", n, err)
		}
		f, err := os.Create(dst)
		if err != nil {
			return err
		}
		f.Close()
		if err := unix.Mount(src, dst, "", unix.MS_BIND, ""); err != nil {
			return fmt.Errorf("bind %s: %w", n, err)
		}
	}
	// Drop the path to the real /dev, so the other drives are not reachable
	// through it either.
	if err := unix.Unmount(real, unix.MNT_DETACH); err != nil {
		return fmt.Errorf("detach the real /dev: %w", err)
	}
	return os.Remove(real)
}

// ProbeIsolation reports whether makemkvcon commands can be isolated to their
// drive here. It runs the helper for real with /bin/true as the program and no
// drive: the same mounts and privilege drop a rip uses, so a pass means a rip's
// isolation will work.
//
// It needs the helper to hold CAP_SYS_ADMIN: the entrypoint passes it down as
// inheritable, and the helper binary's file capability takes it up.
func ProbeIsolation(ctx context.Context) error {
	helper, err := helperPath()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, helper, IsolateSubcommand, "--probe")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// RunIsolateProbe is the helper side of ProbeIsolation: build an isolated /dev
// with only the base nodes and exec /bin/true.
func RunIsolateProbe() error {
	if err := isolateDev(baseDevices); err != nil {
		return err
	}
	return syscall.Exec("/bin/true", []string{"true"}, os.Environ())
}
