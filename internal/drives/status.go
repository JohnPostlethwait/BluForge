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
// A drive in use by a holder is not probed, and reads StatusUnknown: no
// information. The probe used to run beside the holder so a disc pulled
// mid-rip was seen at once, but it is a command to the drive like any other.
// On 2026-11-20 the BU40N reset twice under a rip, 35 seconds after makemkvcon
// went quiet, while this probe reached it every second. A disc pulled during a
// hold now shows up as the holder's work failing, and the probe resumes when
// the hold ends.
//
// The check and the probe are not atomic: a hold that starts between them
// can overlap one probe. Closing that would make Do wait on a probe, and a
// probe of a hung drive blocks in the kernel.
func (b *Broker) ProbeStatus(device string) Status {
	if b.occupied(device) {
		return StatusUnknown
	}
	return b.probe(device)
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
