//go:build linux

package makemkv

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
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
// baseDevices, then execs makemkvcon. makemkvcon can open only what is in /dev,
// so the drive is the one drive it can reach.
//
// The helper runs with CAP_SYS_ADMIN, so it does one fixed thing: the drive must
// be a /dev/srN node and the program must be makemkvcon. It runs nothing else.
//
// It returns only on failure; on success the process becomes makemkvcon.
func RunIsolateHelper(args []string) error {
	if len(args) < 3 || args[1] != "--" {
		return errors.New("usage: " + IsolateSubcommand + " /dev/srN -- makemkvcon args...")
	}
	device, argv := args[0], args[2:]
	if device != NoDrive && !opticalNodeRE.MatchString(device) {
		return fmt.Errorf("isolate: %s is not an optical drive node", device)
	}
	if argv[0] != "makemkvcon" {
		return fmt.Errorf("isolate: runs makemkvcon only, not %q", argv[0])
	}
	// The program is a fixed path, never looked up through the caller's PATH:
	// the helper holds CAP_SYS_ADMIN until the exec, so nothing the caller
	// controls should choose what it runs. The entrypoint installs makemkvcon
	// here.
	prog := makemkvconPath
	if _, err := os.Stat(prog); err != nil {
		return fmt.Errorf("isolate: %w", err)
	}

	nodes, err := isolatedNodes(device)
	if err != nil {
		return fmt.Errorf("isolate: %w", err)
	}

	if err := isolateDev(nodes); err != nil {
		return fmt.Errorf("isolate %s: %w", device, err)
	}
	if err := dropInheritable(); err != nil {
		return fmt.Errorf("isolate: %w", err)
	}
	return syscall.Exec(prog, argv, os.Environ())
}

// isolatedNodes lists the /dev entries an isolated makemkvcon gets: the drive's
// block and SCSI generic nodes and baseDevices, or baseDevices alone for
// NoDrive, which reads a disc image or copy and must reach no drive.
func isolatedNodes(device string) ([]string, error) {
	nodes := append([]string(nil), baseDevices...)
	if device == NoDrive {
		return nodes, nil
	}
	name := filepath.Base(device)
	nodes = append(nodes, name)
	sgs, err := os.ReadDir(filepath.Join("/sys/block", name, "device", "scsi_generic"))
	if err != nil {
		return nil, fmt.Errorf("find the SCSI generic node for %s: %w", device, err)
	}
	for _, sg := range sgs {
		nodes = append(nodes, sg.Name())
	}
	return nodes, nil
}

// makemkvconPath is where the entrypoint installs makemkvcon, and the only
// program the helper runs.
const makemkvconPath = "/usr/bin/makemkvcon"

// opticalNodeRE is the only form of drive the helper will isolate.
var opticalNodeRE = regexp.MustCompile(`^/dev/sr[0-9]+$`)

// dropInheritable empties this thread's inheritable capability set before the
// exec. makemkvcon has no file capability, so it would start with nothing in
// effect anyway. With the inheritable set empty it also cannot run this helper
// again to take CAP_SYS_ADMIN up.
func dropInheritable() error {
	hdr := unix.CapUserHeader{Version: unix.LINUX_CAPABILITY_VERSION_3}
	var data [2]unix.CapUserData
	if err := unix.Capget(&hdr, &data[0]); err != nil {
		return fmt.Errorf("read capabilities: %w", err)
	}
	data[0].Inheritable, data[1].Inheritable = 0, 0
	if err := unix.Capset(&hdr, &data[0]); err != nil {
		return fmt.Errorf("drop inheritable capabilities: %w", err)
	}
	return nil
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
	// A fixed parent rather than $TMPDIR, for the same reason the program path
	// is fixed: the caller's environment does not steer the privileged part.
	real, err := os.MkdirTemp("/tmp", "bluforge-realdev-")
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
	if err := dropInheritable(); err != nil {
		return err
	}
	return syscall.Exec("/bin/true", []string{"true"}, os.Environ())
}
