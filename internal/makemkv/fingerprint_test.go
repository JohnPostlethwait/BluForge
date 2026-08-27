package makemkv

import "testing"

// title builds a TitleInfo carrying just the attributes the fingerprint reads.
func title(sourceFile, duration, sizeBytes string) TitleInfo {
	return TitleInfo{Attributes: map[int]string{
		9:  duration,
		11: sizeBytes,
		16: sourceFile,
	}}
}

func scanOf(discName string, titles ...TitleInfo) *DiscScan {
	return &DiscScan{DiscName: discName, TitleCount: len(titles), Titles: titles}
}

// The bug this exists for: a two-disc set whose discs share a volume label. The
// name is the only thing they have in common, so the fingerprint has to read
// what is actually on the disc.
func TestScanFingerprintDistinguishesSameNamedDiscs(t *testing.T) {
	main := scanOf("PERFECT_BLUE",
		title("00800.mpls", "1:20:15", "24696061952"),
		title("00801.mpls", "0:01:30", "184549376"),
	)
	bonus := scanOf("PERFECT_BLUE",
		title("00010.mpls", "0:22:41", "4294967296"),
		title("00011.mpls", "0:14:02", "2147483648"),
	)

	if ScanFingerprint(main) == ScanFingerprint(bonus) {
		t.Error("two discs sharing a volume label fingerprinted the same")
	}
}

// Two scans of the same disc must agree, or the cache would be discarded on
// every rescan and the check would be worthless.
func TestScanFingerprintIsStableAcrossScans(t *testing.T) {
	first := scanOf("PERFECT_BLUE", title("00800.mpls", "1:20:15", "24696061952"))
	second := scanOf("PERFECT_BLUE", title("00800.mpls", "1:20:15", "24696061952"))

	if ScanFingerprint(first) != ScanFingerprint(second) {
		t.Error("two scans of the same disc fingerprinted differently")
	}
}

func TestScanFingerprintNoticesTitleCount(t *testing.T) {
	one := scanOf("DISC", title("00800.mpls", "1:20:15", "24696061952"))
	two := scanOf("DISC",
		title("00800.mpls", "1:20:15", "24696061952"),
		title("00801.mpls", "0:01:30", "184549376"),
	)

	if ScanFingerprint(one) == ScanFingerprint(two) {
		t.Error("a differing title count fingerprinted the same")
	}
}

// Two discs can carry the same playlist filenames — a main feature and its
// extended cut both ship 00800.mpls. Runtime is what separates them.
func TestScanFingerprintNoticesDuration(t *testing.T) {
	short := scanOf("DISC", title("00800.mpls", "1:20:15", "24696061952"))
	long := scanOf("DISC", title("00800.mpls", "2:14:03", "24696061952"))

	if ScanFingerprint(short) == ScanFingerprint(long) {
		t.Error("a differing runtime fingerprinted the same")
	}
}

func TestScanFingerprintNoticesSourceFiles(t *testing.T) {
	a := scanOf("DISC", title("00800.mpls", "1:20:15", "24696061952"))
	b := scanOf("DISC", title("00081.mpls", "1:20:15", "24696061952"))

	if ScanFingerprint(a) == ScanFingerprint(b) {
		t.Error("a differing source playlist fingerprinted the same")
	}
}

// Titles arrive in whatever order makemkvcon emitted them; that is not disc
// identity. Reordering the same titles must not read as a different disc.
func TestScanFingerprintIgnoresTitleOrder(t *testing.T) {
	first := title("00800.mpls", "1:20:15", "24696061952")
	second := title("00801.mpls", "0:01:30", "184549376")

	if ScanFingerprint(scanOf("DISC", first, second)) != ScanFingerprint(scanOf("DISC", second, first)) {
		t.Error("reordering the same titles changed the fingerprint")
	}
}

// A scan that came back with nothing describes no disc. Fingerprinting it to a
// constant would make every empty scan look like the same disc — the trap
// migration 008 records the disc key falling into.
func TestScanFingerprintIsEmptyWithoutTitles(t *testing.T) {
	if got := ScanFingerprint(scanOf("DISC")); got != "" {
		t.Errorf("fingerprint of a titleless scan = %q, want \"\"", got)
	}
	if got := ScanFingerprint(nil); got != "" {
		t.Errorf("fingerprint of a nil scan = %q, want \"\"", got)
	}
}

// The disc name alone is not identity, but it is still evidence: two discs that
// differ only by label are different discs.
func TestScanFingerprintNoticesDiscName(t *testing.T) {
	a := scanOf("PERFECT_BLUE", title("00800.mpls", "1:20:15", "24696061952"))
	b := scanOf("PERFECT_BLUE_BONUS", title("00800.mpls", "1:20:15", "24696061952"))

	if ScanFingerprint(a) == ScanFingerprint(b) {
		t.Error("a differing disc name fingerprinted the same")
	}
}
