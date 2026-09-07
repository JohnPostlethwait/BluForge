package drivemanager

import "testing"

// statusFromCDS maps the kernel's CDROM_DRIVE_STATUS return codes to our
// coarser DriveStatus. The mapping is the whole contract of the probe that a
// test can pin without hardware; the open/ioctl plumbing around it is verified
// on a real drive.
func TestStatusFromCDS(t *testing.T) {
	cases := []struct {
		name string
		cds  int
		want DriveStatus
	}{
		{"disc ok", cdsDiscOK, StatusDiscOK},
		{"no disc", cdsNoDisc, StatusNoDisc},
		{"tray open", cdsTrayOpen, StatusTrayOpen},
		{"not ready", cdsDriveNotReady, StatusNotReady},
		{"no info", cdsNoInfo, StatusUnknown},
		{"unrecognised", 99, StatusUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := statusFromCDS(tc.cds); got != tc.want {
				t.Fatalf("statusFromCDS(%d) = %v, want %v", tc.cds, got, tc.want)
			}
		})
	}
}
