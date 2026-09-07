//go:build linux

package drivemanager

import (
	"errors"

	"golang.org/x/sys/unix"
)

// CDROM_DRIVE_STATUS ioctl request and the current-slot selector, from
// <linux/cdrom.h>. Not exported by x/sys/unix.
const (
	cdromDriveStatus = 0x5326     // CDROM_DRIVE_STATUS
	cdslCurrent      = 0x7fffffff // CDSL_CURRENT = (int)(~0 >> 1)
)

// sysProbe is the real opticalProbe. Each call opens the device node
// non-blocking and asks the kernel for the drive status, then closes it.
type sysProbe struct{}

func newSysProbe() opticalProbe { return sysProbe{} }

func (sysProbe) Probe(devicePath string) DriveStatus {
	// O_NONBLOCK is the whole point: a plain O_RDONLY open of /dev/srN blocks
	// for tens of seconds while the kernel spins the disc up and probes for
	// media (the stall opticalaccess.go warns about). Non-blocking returns at
	// once and still answers CDROM_DRIVE_STATUS, and it does not need exclusive
	// access, so it works while makemkvcon is reading the drive.
	fd, err := unix.Open(devicePath, unix.O_RDONLY|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		// The node is gone: unplugged, or removed from the container.
		if errors.Is(err, unix.ENOENT) || errors.Is(err, unix.ENODEV) || errors.Is(err, unix.ENXIO) {
			return StatusGone
		}
		// Anything else (e.g. a transient EBUSY) carries no decision.
		return StatusUnknown
	}
	defer unix.Close(fd)

	// CDROM_DRIVE_STATUS returns the status as the ioctl's return value, not
	// through a pointer, so the raw syscall return is read directly.
	ret, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(fd), uintptr(cdromDriveStatus), uintptr(cdslCurrent))
	if errno != 0 {
		return StatusUnknown
	}
	return statusFromCDS(int(ret))
}
