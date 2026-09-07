package drivemanager

// opticalProbe reads the current status of a single optical device node.
//
// It is the one hardware-touching seam in the physical-drive watcher: the real
// implementation issues a non-blocking CDROM_DRIVE_STATUS ioctl on Linux, and a
// fake stands in for it everywhere the logic is tested.
type opticalProbe interface {
	Probe(devicePath string) DriveStatus
}

// CDROM_DRIVE_STATUS return codes, from <linux/cdrom.h>. They are not exported
// by golang.org/x/sys/unix, so they are defined here against the kernel header.
const (
	cdsNoInfo        = 0 // CDS_NO_INFO — drive could not report
	cdsNoDisc        = 1 // CDS_NO_DISC — tray closed, no media
	cdsTrayOpen      = 2 // CDS_TRAY_OPEN
	cdsDriveNotReady = 3 // CDS_DRIVE_NOT_READY — present but busy/spinning up
	cdsDiscOK        = 4 // CDS_DISC_OK — media loaded and readable
)

// statusFromCDS maps a CDROM_DRIVE_STATUS return code to a DriveStatus.
// Anything unrecognised, including CDS_NO_INFO, is Unknown so no decision is
// made on it.
func statusFromCDS(cds int) DriveStatus {
	switch cds {
	case cdsDiscOK:
		return StatusDiscOK
	case cdsNoDisc:
		return StatusNoDisc
	case cdsTrayOpen:
		return StatusTrayOpen
	case cdsDriveNotReady:
		return StatusNotReady
	default:
		return StatusUnknown
	}
}
