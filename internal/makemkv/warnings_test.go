package makemkv

import "testing"

// A damaged disc produced a tidy list of eight titles and no hint that two
// streams had been dropped. MakeMKV said so plainly; BluForge parsed the
// messages and threw them away.
func TestScanWarningsSurfacesSkippedTitlesAndReadErrors(t *testing.T) {
	msgs := []Message{
		{Code: 1005, Text: "MakeMKV v1.18.3 linux(x64-release) started"},
		{Code: 1011, Text: "Using LibreDrive mode (v02.2 id=393664D791B0)"},
		{Code: 3307, Text: "File 00000.mpls was added as title #4"},
		{Code: 2003, Text: "Error 'Scsi error - MEDIUM ERROR:L-EC UNCORRECTABLE ERROR' occurred while reading '/BDMV/STREAM/00008.m2ts' at offset '0'"},
		{Code: 2003, Text: "Error 'Scsi error - MEDIUM ERROR:L-EC UNCORRECTABLE ERROR' occurred while reading '/BDMV/STREAM/00008.m2ts' at offset '0'"},
		{Code: 3016, Text: "Title #00008.m2ts was skipped"},
		{Code: 5011, Text: "Operation successfully completed"},
	}

	got := ScanWarnings(msgs)

	if len(got) == 0 {
		t.Fatal("no warnings produced for a scan with read errors and a skipped title")
	}

	var sawRead, sawSkip bool
	for _, w := range got {
		if w.Code == 2003 {
			sawRead = true
			if w.Count != 2 {
				t.Errorf("read error Count = %d, want 2 (repeats collapsed)", w.Count)
			}
		}
		if w.Code == 3016 {
			sawSkip = true
		}
	}
	if !sawRead {
		t.Error("read errors were not surfaced")
	}
	if !sawSkip {
		t.Error("the skipped title was not surfaced")
	}
}

// Routine chatter must not be surfaced, or the notice becomes noise the user
// learns to ignore — including the minimum-length skip, which is a setting
// working as configured rather than a problem with the disc.
func TestScanWarningsIgnoresRoutineMessages(t *testing.T) {
	msgs := []Message{
		{Code: 1005, Text: "MakeMKV started"},
		{Code: 1011, Text: "Using LibreDrive mode"},
		{Code: 3006, Text: "Opening files on harddrive"},
		{Code: 3007, Text: "Using direct disc access mode"},
		{Code: 3025, Text: "Title #00014.m2ts has length of 20 seconds which is less than minimum title length of 120 seconds and was therefore skipped"},
		{Code: 3307, Text: "File 00005.mpls was added as title #0"},
		{Code: 3305, Text: "AACS directory not present, assuming unencrypted disc"},
		{Code: 5011, Text: "Operation successfully completed"},
		{Code: 5042, Text: "The program can't find any usable optical drives."},
	}

	if got := ScanWarnings(msgs); len(got) != 0 {
		t.Errorf("surfaced %d warnings for a clean scan: %+v", len(got), got)
	}
}

// Classification is by exclusion: an unrecognised message is surfaced rather
// than hidden. A code nobody has catalogued is exactly the case where silence
// costs the user content without telling them.
func TestScanWarningsSurfacesUnknownCodes(t *testing.T) {
	got := ScanWarnings([]Message{
		{Code: 9999, Text: "Something nobody has seen before"},
	})

	if len(got) != 1 || got[0].Code != 9999 {
		t.Errorf("an unrecognised message was not surfaced: %+v", got)
	}
}

func TestScanWarningsEmpty(t *testing.T) {
	if got := ScanWarnings(nil); len(got) != 0 {
		t.Errorf("warnings from no messages: %+v", got)
	}
}
