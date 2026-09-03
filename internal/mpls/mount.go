package mpls

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// mountRunner is the filesystem side of mounting a disc, kept behind an
// interface so the registry's bookkeeping can be tested without root.
type mountRunner interface {
	// MountPointOf reports where device is currently mounted, if anywhere.
	MountPointOf(device string) (string, bool)
	Mount(device, point string) error
	Umount(point string) error
}

// MountRegistry owns every disc mount BluForge makes, so that a mount cannot
// outlive the disc it was taken on.
//
// This exists because of what happens when it does. A mounted optical disc is
// pinned by the kernel: the UDF driver holds a live reference to the block
// device. Change the media underneath that and every subsequent I/O fails, the
// driver retries, and a USB bridge takes a continuous storm of failing commands
// — "usb 1-1: reset high-speed USB device using xhci_hcd", over and over, until
// the drive stops answering entirely and nothing short of a power cycle brings
// it back. Processes blocked on that I/O sit in uninterruptible sleep, where no
// signal reaches them.
//
// The previous arrangement had no owner at all. A mount was handed to whichever
// recovery record asked for it and released only when a rip that claimed that
// record finished; every path meaning "this disc is gone" set a boolean and
// left the mount live. Worse, a device found already mounted was reused with a
// no-op cleanup, so one leak became permanent — every later scan adopted it and
// none of them would ever take it down.
type MountRegistry struct {
	mu     sync.Mutex
	runner mountRunner
	active map[string]*heldMount

	// now is the clock, injectable so held durations can be asserted without
	// sleeping.
	now func() time.Time
	// pointFor chooses the mount point for a device. Overridden in tests.
	pointFor func(device string) string
	// clearForeignMounts allows ForceUnmount to take down a mount this process
	// never made — the leak left by an earlier run, which is the one that would
	// otherwise be adopted forever.
	//
	// Off outside Linux, because there the device-to-mount lookup is a guess:
	// findMountDarwin returns any volume under /Volumes carrying a BDMV
	// directory, without reference to the device at all. Guessing is acceptable
	// when the answer is used to read. It is not acceptable when the answer is
	// used to unmount.
	clearForeignMounts bool
}

// heldMount is one device's mount and the claims outstanding on it.
type heldMount struct {
	device string
	point  string
	refs   int
	since  time.Time
	// adopted marks a mount this process found rather than made. It is still
	// unmounted on release: leaving it is what made a single leak permanent.
	adopted bool
	// dead marks a mount already taken down by ForceUnmount. Releases arriving
	// afterwards must do nothing — by then the device may have been mounted
	// again for a different disc, and unmounting that would be a fresh bug.
	dead bool
}

// NewMountRegistry creates a registry over the given runner.
func NewMountRegistry(runner mountRunner) *MountRegistry {
	return &MountRegistry{
		runner:             runner,
		active:             make(map[string]*heldMount),
		now:                time.Now,
		pointFor:           defaultMountPoint,
		clearForeignMounts: runtime.GOOS == "linux",
	}
}

// defaultMountPoint is the conventional location for a device, e.g. /mnt/sr1.
func defaultMountPoint(device string) string {
	return filepath.Join("/mnt", filepath.Base(device))
}

// discMounts is the registry every caller shares. Mounts are process-wide
// whether or not the code tracking them is.
var discMounts = NewMountRegistry(systemMounts{})

// Open makes the disc at device readable as a directory tree, returning its
// root and a release function that must always be called.
//
// Concurrent readers share one mount: the disc is mounted on the first claim
// and unmounted when the last is released.
func (r *MountRegistry) Open(device string) (string, func(), error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if h, ok := r.active[device]; ok && !h.dead {
		h.refs++
		return h.point, r.releaseFunc(h), nil
	}

	h, err := r.mountLocked(device)
	if err != nil {
		return "", func() {}, err
	}
	return h.point, r.releaseFunc(h), nil
}

// mountLocked mounts a device, or adopts an existing mount of it. Callers hold
// r.mu.
func (r *MountRegistry) mountLocked(device string) (*heldMount, error) {
	point, adopted := r.runner.MountPointOf(device)
	if adopted {
		// A mount entry is not proof of a readable disc. A drive under
		// sustained error handling resets, and leaves its entry in /proc/mounts
		// pointing at a directory with nothing behind it.
		if err := hasContent(point); err != nil {
			slog.Warn("mpls: mount point holds no disc, remounting",
				"device", device, "mount_point", point, "error", err)
			if umountErr := r.runner.Umount(point); umountErr != nil {
				slog.Warn("mpls: could not clear the stale mount",
					"device", device, "mount_point", point, "error", umountErr)
			}
			adopted = false
		}
	}

	if !adopted {
		point = r.pointFor(device)
		if err := os.MkdirAll(point, 0o755); err != nil {
			return nil, fmt.Errorf("mpls: create mount point %s: %w", point, err)
		}
		if err := r.runner.Mount(device, point); err != nil {
			return nil, fmt.Errorf("mpls: mount %s: %w", device, err)
		}
		if err := hasContent(point); err != nil {
			_ = r.runner.Umount(point)
			return nil, fmt.Errorf("mpls: mounted %s at %s but %w", device, point, err)
		}
	}

	h := &heldMount{device: device, point: point, refs: 1, since: r.now(), adopted: adopted}
	r.active[device] = h

	slog.Info("mpls: disc mounted", "device", device, "mount_point", point, "adopted", adopted)
	return h, nil
}

// releaseFunc returns a release for one claim on h. It is safe to call more
// than once, and does nothing once the mount has been forced down.
func (r *MountRegistry) releaseFunc(h *heldMount) func() {
	var once sync.Once
	return func() {
		once.Do(func() { r.release(h) })
	}
}

func (r *MountRegistry) release(h *heldMount) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if h.dead {
		return
	}
	h.refs--
	if h.refs > 0 {
		return
	}

	r.unmountLocked(h, "released")
}

// ForceUnmount takes a disc's mount down regardless of who is still holding it,
// and clears a mount this process does not know about.
//
// Called when the disc leaves — an eject, an insert, a drive disconnect. A
// reader still holding a claim does not get a say: whatever it is part-way
// through has already lost the media, and a live filesystem on media that is
// gone is the condition that wedges the drive.
func (r *MountRegistry) ForceUnmount(device string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if h, ok := r.active[device]; ok && !h.dead {
		refs := h.refs
		r.unmountLocked(h, "the disc left the drive")
		if refs > 0 {
			slog.Warn("mpls: unmounted a disc that was still claimed",
				"device", device, "claims", refs)
		}
		return nil
	}

	// Not ours. A mount left by an earlier process, or by a crash between the
	// mount and its cleanup, is exactly the one that has to go now — it is the
	// leak that would otherwise be adopted forever.
	if !r.clearForeignMounts {
		return nil
	}
	point, mounted := r.runner.MountPointOf(device)
	if !mounted {
		return nil
	}
	slog.Warn("mpls: clearing a disc mount this process did not make",
		"device", device, "mount_point", point)
	if err := r.runner.Umount(point); err != nil {
		return fmt.Errorf("mpls: umount %s: %w", point, err)
	}
	return nil
}

// unmountLocked takes the mount down and forgets it. Callers hold r.mu.
func (r *MountRegistry) unmountLocked(h *heldMount, reason string) {
	h.dead = true
	delete(r.active, h.device)

	held := r.now().Sub(h.since)
	if err := r.runner.Umount(h.point); err != nil {
		slog.Warn("mpls: umount failed",
			"device", h.device, "mount_point", h.point, "reason", reason,
			"held", held.String(), "error", err)
		return
	}
	slog.Info("mpls: disc unmounted",
		"device", h.device, "mount_point", h.point, "reason", reason,
		"held", held.String())
}

// HeldFor reports how long a device's mount has been held, or zero when it is
// not mounted by this process.
func (r *MountRegistry) HeldFor(device string) time.Duration {
	r.mu.Lock()
	defer r.mu.Unlock()

	h, ok := r.active[device]
	if !ok || h.dead {
		return 0
	}
	return r.now().Sub(h.since)
}

// ForceUnmount releases any mount BluForge holds on a device. See
// MountRegistry.ForceUnmount.
func ForceUnmount(device string) error { return discMounts.ForceUnmount(device) }

// systemMounts is the real filesystem.
type systemMounts struct{}

func (systemMounts) MountPointOf(device string) (string, bool) {
	mp, err := findMountPoint(device)
	if err != nil {
		return "", false
	}
	return mp, true
}

// Mount tries the fstab form first, because that is where the Docker entrypoint
// records the "user" option a non-root process needs. An explicit read-only UDF
// mount follows for drives with no fstab entry — one that appeared after the
// container started, or any environment without those entries.
func (systemMounts) Mount(device, point string) error {
	attempts := [][]string{
		{"mount", device},
		{"mount", "-t", "udf", "-o", "ro", device, point},
	}

	var lastErr error
	var lastOut string
	for _, argv := range attempts {
		out, err := exec.Command(argv[0], argv[1:]...).CombinedOutput()
		if err == nil {
			return nil
		}
		lastErr = err
		lastOut = strings.TrimSpace(string(out))
		slog.Debug("mpls: mount attempt failed", "argv", argv, "error", err, "output", lastOut)
	}
	return fmt.Errorf("%w (%s)", lastErr, lastOut)
}

func (systemMounts) Umount(point string) error {
	out, err := exec.Command("umount", point).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}
