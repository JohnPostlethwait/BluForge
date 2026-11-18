package drives

import "fmt"

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

// String names a Status for the log.
func (s Status) String() string {
	switch s {
	case StatusDiscOK:
		return "disc ok"
	case StatusNoDisc:
		return "no disc"
	case StatusTrayOpen:
		return "tray open"
	case StatusNotReady:
		return "not ready"
	case StatusGone:
		return "gone"
	default:
		return "unknown"
	}
}

// Readiness is a drive's answer to TEST UNIT READY: whether it will take
// commands about its disc now, and if not, why.
//
// It is a finer question than Status. CDROM_DRIVE_STATUS reports a disc as OK
// both when the drive is ready and when it merely says a disc is in the tray
// while it is still spinning up and reading the disc's structure. Only the
// first is a drive makemkvcon may be pointed at.
type Readiness struct {
	// Ready is true when the drive answered TEST UNIT READY with good status.
	Ready bool
	// Reason says why a drive is not ready: the sense it answered with
	// ("NOT READY 04/01"), or what kept the question from being asked. Empty
	// when Ready.
	Reason string
}

// ProbeReady asks drive device TEST UNIT READY.
//
// Like ProbeStatus it runs beside the drive's holder, and it sends the drive
// nothing the status probe does not: CDROM_DRIVE_STATUS starts with TEST UNIT
// READY too. It is asked only while a disc that has just arrived waits to be
// read, so a drive still spinning a disc up is not handed makemkvcon.
func (b *Broker) ProbeReady(device string) Readiness { return probeReady(device) }

// senseKeys names the SCSI sense keys.
var senseKeys = [16]string{
	"NO SENSE", "RECOVERED ERROR", "NOT READY", "MEDIUM ERROR",
	"HARDWARE ERROR", "ILLEGAL REQUEST", "UNIT ATTENTION", "DATA PROTECT",
	"BLANK CHECK", "VENDOR SPECIFIC", "COPY ABORTED", "ABORTED COMMAND",
	"KEY 0C", "VOLUME OVERFLOW", "MISCOMPARE", "KEY 0F",
}

// scsiCheckCondition is the SCSI status a drive answers with when it has sense
// data to give.
const scsiCheckCondition = 0x02

// readinessFrom turns the outcome of a TEST UNIT READY into a Readiness.
// status is the SCSI status byte, transport is set when the host or driver
// reported a problem getting the command to the drive, and sense is the sense
// data the drive returned.
func readinessFrom(status byte, transport bool, sense []byte) Readiness {
	if transport {
		return Readiness{Reason: "the command did not reach the drive"}
	}
	if status == 0 {
		return Readiness{Ready: true}
	}
	if status != scsiCheckCondition {
		return Readiness{Reason: fmt.Sprintf("SCSI status %#02x", status)}
	}
	key, asc, ascq, ok := parseSense(sense)
	if !ok {
		return Readiness{Reason: "CHECK CONDITION without sense data"}
	}
	return Readiness{Reason: fmt.Sprintf("%s %02X/%02X", senseKeys[key], asc, ascq)}
}

// parseSense reads the sense key, ASC and ASCQ from fixed (0x70/0x71) or
// descriptor (0x72/0x73) format sense data.
func parseSense(sense []byte) (key, asc, ascq byte, ok bool) {
	if len(sense) == 0 {
		return 0, 0, 0, false
	}
	switch sense[0] & 0x7f {
	case 0x70, 0x71:
		if len(sense) < 14 {
			return 0, 0, 0, false
		}
		return sense[2] & 0x0f, sense[12], sense[13], true
	case 0x72, 0x73:
		if len(sense) < 4 {
			return 0, 0, 0, false
		}
		return sense[1] & 0x0f, sense[2], sense[3], true
	}
	return 0, 0, 0, false
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
