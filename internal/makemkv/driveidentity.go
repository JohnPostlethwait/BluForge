package makemkv

import (
	"regexp"
	"strings"
)

// firmwareToken matches a drive firmware revision as makemkvcon prints it in a
// drive name: "3.10", "1.02", "3.02". Revisions in another shape are not
// recognised, and stay part of the identity.
var firmwareToken = regexp.MustCompile(`^[0-9]+\.[0-9A-Za-z]+$`)

// DriveIdentity returns a key for a physical drive, from the drive name
// makemkvcon reports in its DRV line.
//
// The key has to survive the drive being unplugged, replugged or reset. The
// drive index (disc:N) and the device node (/dev/srN) both move when that
// happens, so neither can be used. The drive name is the only thing in the
// listing that belongs to the hardware.
//
// DriveInfo exposes that name as one string, which makemkvcon builds as
// "<type> <vendor> <product> <firmware> <serial>", e.g.
// "BD-RE ASUS BW-16D1HT 3.10 KLFO82D3028". The product can itself contain
// spaces ("BD-RE  WH16NS40"), so the fields are read from the end.
//
// The firmware revision is left out. Flashing a drive changes it, and a drive
// the user ignored must stay ignored after a firmware update. What remains is
// the model plus the serial, which tells two drives of the same model apart.
//
// The firmware is only dropped when it is recognisably a revision: the
// second-to-last field when a serial follows, or the last field when the drive
// reports no serial. Anything else is kept whole, so a name in an unexpected
// shape is still matched exactly. Whitespace runs are collapsed, so a
// double space in the product name cannot make two readings differ.
func DriveIdentity(driveName string) string {
	f := strings.Fields(driveName)
	n := len(f)
	switch {
	case n >= 5 && firmwareToken.MatchString(f[n-2]) && !firmwareToken.MatchString(f[n-1]):
		// <type> <vendor> <product...> <firmware> <serial>
		key := append(append([]string{}, f[:n-2]...), f[n-1])
		return strings.Join(key, " ")
	case n >= 4 && firmwareToken.MatchString(f[n-1]):
		// <type> <vendor> <product...> <firmware>, no serial.
		return strings.Join(f[:n-1], " ")
	default:
		return strings.Join(f, " ")
	}
}

// Identity returns DriveIdentity of the drive's name.
func (d DriveInfo) Identity() string {
	return DriveIdentity(d.DriveName)
}
