package makemkv

import "testing"

// The DRV line's second field carries MakeMKV's own drive state, and parseDRV
// discarded it. It is the only field that says outright whether a slot holds
// hardware and whether that hardware holds a disc; without it both had to be
// guessed from the name and media flags, and both guesses are wrong in cases
// that occur in practice.
//
// Values are AP_DriveState from MakeMKV's apdefs.h:
//
//	AP_DriveStateEmptyClosed = 0
//	AP_DriveStateEmptyOpen   = 1
//	AP_DriveStateInserted    = 2
//	AP_DriveStateLoading     = 3
//	AP_DriveStateNoDrive     = 256
//	AP_DriveStateUnmounting  = 257
//
// Note the published field list at makemkv.com describes this field as "set to
// 1 if drive is present". That is stale — the same document lists six fields
// where makemkvcon emits seven — and it does not match observed output, where
// a drive with no disc reports 0 and an absent slot reports 256.
func TestParseDRVKeepsTheDriveState(t *testing.T) {
	tests := []struct {
		name string
		line string
		want int
	}{
		{"disc inserted", `0,2,999,1,"BD-RE ASUS","DEADPOOL_2","/dev/sr0"`, DriveStateInserted},
		{"drive present, no disc", `1,0,999,0,"BD-RE ASUS","","/dev/sr1"`, DriveStateEmptyClosed},
		{"phantom slot", `2,256,999,0,"","",""`, DriveStateNoDrive},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ev, err := parseDRV(tc.line)
			if err != nil {
				t.Fatalf("parseDRV: %v", err)
			}
			if ev.Drive.State != tc.want {
				t.Errorf("State = %d, want %d", ev.Drive.State, tc.want)
			}
		})
	}
}

// A slot with no hardware is the only thing that should be skipped. Deciding
// that from the drive name means a real drive that momentarily reports a blank
// name is mistaken for an empty slot — and then for an unplugged drive.
func TestDrivePresentDistinguishesHardwareFromAPhantomSlot(t *testing.T) {
	tests := []struct {
		name string
		info DriveInfo
		want bool
	}{
		{"phantom slot", DriveInfo{State: DriveStateNoDrive}, false},
		{"drive with a disc", DriveInfo{State: DriveStateInserted, DriveName: "BD-RE", DevicePath: "/dev/sr0"}, true},
		{"drive with no disc", DriveInfo{State: DriveStateEmptyClosed, DriveName: "BD-RE", DevicePath: "/dev/sr1"}, true},
		{"tray open", DriveInfo{State: DriveStateEmptyOpen, DriveName: "BD-RE", DevicePath: "/dev/sr1"}, true},
		{"present but momentarily unnamed", DriveInfo{State: DriveStateEmptyClosed, DevicePath: "/dev/sr1"}, true},
		{"nothing at all", DriveInfo{}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.info.Present(); got != tc.want {
				t.Errorf("Present() = %v, want %v", got, tc.want)
			}
		})
	}
}

// A disc with no volume label is common, and reading presence from the disc
// name meant BluForge showed an empty drive with a disc sitting in it.
func TestHasDiscReadsTheStateNotTheLabel(t *testing.T) {
	tests := []struct {
		name string
		info DriveInfo
		want bool
	}{
		{"labelled disc", DriveInfo{State: DriveStateInserted, DiscName: "DEADPOOL_2", Flags: 1}, true},
		{"unlabelled disc", DriveInfo{State: DriveStateInserted, DiscName: "", Flags: 0}, true},
		{"empty drive", DriveInfo{State: DriveStateEmptyClosed, DiscName: ""}, false},
		{"tray open", DriveInfo{State: DriveStateEmptyOpen}, false},
		{"still loading", DriveInfo{State: DriveStateLoading}, false},
		{"no drive", DriveInfo{State: DriveStateNoDrive}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.info.HasDisc(); got != tc.want {
				t.Errorf("HasDisc() = %v, want %v", got, tc.want)
			}
		})
	}
}
