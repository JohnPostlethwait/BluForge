package drives

// Status is what one cheap probe of a drive reports.
//
// It is deliberately coarser than the truth the kernel could give: the watcher
// only needs to tell "a disc is readable" from "the slot is empty or open" from
// "ask again later". NotReady and Unknown are the last of those — a busy drive
// mid-scan reports NotReady, and treating that as an eject is exactly the
// false-positive that cancelled real work in past releases.
type Status int

const (
	// StatusUnknown means the probe could not tell — no decision is made on it.
	StatusUnknown Status = iota
	// StatusDiscOK means a disc is loaded and readable.
	StatusDiscOK
	// StatusNoDisc means the tray is closed with no media.
	StatusNoDisc
	// StatusTrayOpen means the tray is open.
	StatusTrayOpen
	// StatusNotReady means the drive is present but busy or spinning up.
	StatusNotReady
	// StatusGone means the device node itself is absent (ENODEV/ENOENT).
	StatusGone
)

// ProbeStatus reads a drive's status with CDROM_DRIVE_STATUS.
//
// This is the one read of a drive that runs beside its holder, on purpose: it
// is how a disc pulled mid-rip is seen, so the rip can be stopped before
// makemkvcon hammers media that is gone. It opens the node non-blocking and
// the kernel answers with TEST UNIT READY; nothing else. If a hang is ever
// traced to one of these landing mid-command, it moves under the hold too.
func (b *Broker) ProbeStatus(device string) Status { return probeStatus(device) }

// CDROM_DRIVE_STATUS return codes, from <linux/cdrom.h>. They are not exported
// by golang.org/x/sys/unix, so they are defined here against the kernel header.
const (
	cdsNoInfo        = 0 // CDS_NO_INFO — drive could not report
	cdsNoDisc        = 1 // CDS_NO_DISC — tray closed, no media
	cdsTrayOpen      = 2 // CDS_TRAY_OPEN
	cdsDriveNotReady = 3 // CDS_DRIVE_NOT_READY — present but busy/spinning up
	cdsDiscOK        = 4 // CDS_DISC_OK — media loaded and readable
)

// statusFromCDS maps a CDROM_DRIVE_STATUS return code to a Status. Anything
// unrecognised, including CDS_NO_INFO, is Unknown so no decision is made on it.
func statusFromCDS(cds int) Status {
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
