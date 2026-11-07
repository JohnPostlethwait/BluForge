package drives

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Slot is an attached optical drive, as the kernel describes it.
type Slot struct {
	// Index is N of /dev/srN, BluForge's number for the drive.
	Index int
	// Device is the drive's block node, e.g. /dev/sr1.
	Device string
	// Vendor and Model are the drive's SCSI inquiry strings, as the kernel
	// recorded them when the drive attached.
	Vendor string
	Model  string
}

var srName = regexp.MustCompile(`^sr([0-9]+)$`)

// sysfsBlock is where the kernel lists block devices.
const sysfsBlock = "/sys/class/block"

// sysfsSlots lists the attached drives from sysfs.
//
// This replaces the five-second `makemkvcon info disc:9999`, which opened every
// drive — including ones mid-rip — to say which were there. sysfs answers
// that from what the kernel already knows, without a command to any drive.
func sysfsSlots() ([]Slot, error) { return readSlots(sysfsBlock) }

func readSlots(root string) ([]Slot, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	var slots []Slot
	for _, e := range entries {
		m := srName.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}
		n, err := strconv.Atoi(m[1])
		if err != nil {
			continue
		}
		dev := filepath.Join(root, e.Name(), "device")
		slots = append(slots, Slot{
			Index:  n,
			Device: "/dev/" + e.Name(),
			Vendor: readAttr(filepath.Join(dev, "vendor")),
			Model:  readAttr(filepath.Join(dev, "model")),
		})
	}
	sort.Slice(slots, func(i, j int) bool { return slots[i].Index < slots[j].Index })
	return slots, nil
}

func readAttr(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}
