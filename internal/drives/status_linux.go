//go:build linux

package drives

import (
	"errors"
	"runtime"
	"unsafe"

	"golang.org/x/sys/unix"
)

// CDROM_DRIVE_STATUS ioctl request and the current-slot selector, from
// <linux/cdrom.h>. Not exported by x/sys/unix.
const (
	cdromDriveStatus = 0x5326     // CDROM_DRIVE_STATUS
	cdslCurrent      = 0x7fffffff // CDSL_CURRENT = (int)(~0 >> 1)
)

// probeStatus opens the device node non-blocking, asks the kernel for the
// drive status, and closes it.
func probeStatus(devicePath string) Status {
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

// SG_IO and its header, from <scsi/sg.h>. Not exported by x/sys/unix.
const (
	sgIO         = 0x2285
	sgDxferNone  = -1
	sgInterface  = 'S'
	senseBufSize = 32
	// turTimeoutMs is the kernel's own timeout for the TEST UNIT READY that
	// CDROM_DRIVE_STATUS sends (SR_TIMEOUT), so this probe can hold a drive no
	// longer than the status probe already does.
	turTimeoutMs = 30_000
)

// sgIOHdr is struct sg_io_hdr. Go lays it out as C does on every Linux
// architecture BluForge builds for: natural alignment, pointers word-sized.
type sgIOHdr struct {
	interfaceID    int32
	dxferDirection int32
	cmdLen         uint8
	mxSbLen        uint8
	iovecCount     uint16
	dxferLen       uint32
	dxferp         unsafe.Pointer
	cmdp           unsafe.Pointer
	sbp            unsafe.Pointer
	timeout        uint32
	flags          uint32
	packID         int32
	usrPtr         unsafe.Pointer
	status         uint8
	maskedStatus   uint8
	msgStatus      uint8
	sbLenWr        uint8
	hostStatus     uint16
	driverStatus   uint16
	resid          int32
	duration       uint32
	info           uint32
}

// driverSense is the driver_status bit that only says sense data came back,
// which is not a transport problem.
const driverSense = 0x08

// probeReady sends TEST UNIT READY to the drive through SG_IO on its node,
// opened non-blocking as probeStatus opens it.
func probeReady(devicePath string) Readiness {
	fd, err := unix.Open(devicePath, unix.O_RDONLY|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return Readiness{Reason: "open: " + err.Error()}
	}
	defer unix.Close(fd)

	cdb := new([6]byte) // TEST UNIT READY is opcode 0 with no fields.
	sense := new([senseBufSize]byte)
	hdr := &sgIOHdr{
		interfaceID:    sgInterface,
		dxferDirection: sgDxferNone,
		cmdLen:         uint8(len(cdb)),
		mxSbLen:        senseBufSize,
		cmdp:           unsafe.Pointer(cdb),
		sbp:            unsafe.Pointer(sense),
		timeout:        turTimeoutMs,
	}
	_, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(fd), uintptr(sgIO), uintptr(unsafe.Pointer(hdr)))
	runtime.KeepAlive(cdb)
	runtime.KeepAlive(sense)
	if errno != 0 {
		return Readiness{Reason: "SG_IO: " + errno.Error()}
	}
	transport := hdr.hostStatus != 0 || hdr.driverStatus&^driverSense != 0
	return readinessFrom(hdr.status, transport, sense[:hdr.sbLenWr])
}
