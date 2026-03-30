package makemkv

import (
	"os"
	"path/filepath"
	"strings"
)

// scsiTypeCDROM is the SCSI device type for CD-ROM/optical drives.
// See: https://www.kernel.org/doc/Documentation/scsi/scsi.txt
const scsiTypeCDROM = "5"

// DetectOpticalDevices returns the device paths (e.g. ["/dev/sr0", "/dev/sr1"])
// of all optical drives detected via the Linux sysfs interface. This is a fast
// kernel query that avoids probing every block device with makemkvcon.
//
// On non-Linux systems (or if /sys/block is missing), an empty slice is
// returned so callers can fall back to a full makemkvcon scan.
func DetectOpticalDevices() []string {
	entries, err := os.ReadDir("/sys/block")
	if err != nil {
		return nil
	}

	var devices []string
	for _, entry := range entries {
		name := entry.Name()
		typePath := filepath.Join("/sys/block", name, "device", "type")
		data, err := os.ReadFile(typePath)
		if err != nil {
			continue
		}
		if strings.TrimSpace(string(data)) == scsiTypeCDROM {
			devices = append(devices, "/dev/"+name)
		}
	}
	return devices
}
