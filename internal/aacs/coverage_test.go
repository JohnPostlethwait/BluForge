package aacs

import (
	"os"
	"path/filepath"
	"testing"
)

// Measured on a real 49GB UHD stream: the final sample landed at 39150612480,
// leaving the last ~9.8GB unread. A disc whose authoring changes partway
// through — or whose tail belongs to a differently-keyed CPS unit — would be
// judged entirely on its first four fifths.
func TestSampleOffsetsReachTheEndOfTheFile(t *testing.T) {
	const size = 48938354688
	chunk := int64(packetsPerSample*m2tsPacketSize + alignedUnitSize)

	offsets := sampleOffsets(size, chunk)
	if len(offsets) < 2 {
		t.Fatalf("got %d offsets, want several", len(offsets))
	}

	last := offsets[len(offsets)-1]
	unread := size - (last + chunk)
	if unread > alignedUnitSize {
		t.Errorf("last sample ends %d bytes short of the file end, want the tail covered", unread)
	}
}

func TestSampleOffsetsStayAlignedToAlignedUnits(t *testing.T) {
	for _, size := range []int64{48938354688, 49301803008, 1 << 30, 5_000_000} {
		for _, off := range sampleOffsets(size, int64(packetsPerSample*m2tsPacketSize+alignedUnitSize)) {
			if off%alignedUnitSize != 0 {
				t.Errorf("size %d: offset %d is not aligned-unit aligned", size, off)
			}
		}
	}
}

func TestSampleOffsetsDistinct(t *testing.T) {
	offsets := sampleOffsets(48938354688, int64(packetsPerSample*m2tsPacketSize+alignedUnitSize))
	seen := map[int64]bool{}
	for _, off := range offsets {
		if seen[off] {
			t.Errorf("duplicate sample offset %d", off)
		}
		seen[off] = true
	}
	if len(offsets) != sampleCount {
		t.Errorf("got %d offsets, want %d", len(offsets), sampleCount)
	}
}

// writeMultiStreamDisc lays down several streams with independent contents.
func writeMultiStreamDisc(t *testing.T, streams map[string][]byte) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "BDMV", "STREAM")
	if err := os.MkdirAll(dir, 0o777); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "AACS"), 0o777); err != nil {
		t.Fatalf("mkdir aacs: %v", err)
	}
	for name, data := range streams {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o666); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return root
}

// A disc carries a CPS unit structure, so streams need not share one encryption
// state. If the largest file reads clean but another is encrypted, recovering
// the disc produces a backup that cannot be ripped — the conservative answer is
// to refuse.
func TestScrambledSecondaryStreamBlocksRecovery(t *testing.T) {
	// The clean stream must be the larger of the two, or the test passes for the
	// wrong reason: inspecting only the biggest file would find the encrypted
	// one anyway. buildAACSEncrypted(600) is 600*6144 = 3.7MB.
	clean := buildM2TS(enoughPackets*4, 0) // 24000*192 = 4.6MB
	encrypted := buildAACSEncrypted(600)
	if len(clean) <= len(encrypted) {
		t.Fatalf("fixture sizes defeat the test: clean %d bytes, encrypted %d", len(clean), len(encrypted))
	}
	root := writeMultiStreamDisc(t, map[string][]byte{
		"00001.m2ts": clean,     // largest
		"00002.m2ts": encrypted, // smaller, and only found if more than one stream is read
	})

	got, err := InspectStreams(root)
	if err != nil {
		t.Fatalf("InspectStreams: %v", err)
	}
	if got.Verdict == VerdictUnencrypted {
		t.Errorf("Verdict = %q; a disc with an encrypted stream must not be recovered (reason: %s)",
			got.Verdict, got.Reason)
	}
}

// Tiny menu streams are normal and carry too few packets to classify. They must
// not drag an otherwise clear disc into "unknown".
func TestSmallUnclassifiableStreamsDoNotBlockRecovery(t *testing.T) {
	root := writeMultiStreamDisc(t, map[string][]byte{
		"00001.m2ts": buildM2TS(enoughPackets*2, 0), // the feature
		"00002.m2ts": buildM2TS(8, 0),               // a menu, far too small to judge
	})

	got, err := InspectStreams(root)
	if err != nil {
		t.Fatalf("InspectStreams: %v", err)
	}
	if got.Verdict != VerdictUnencrypted {
		t.Errorf("Verdict = %q, want %q (reason: %s)", got.Verdict, VerdictUnencrypted, got.Reason)
	}
}

func TestInspectionReportsEveryStreamItRead(t *testing.T) {
	root := writeMultiStreamDisc(t, map[string][]byte{
		"00001.m2ts": buildM2TS(enoughPackets*2, 0),
		"00002.m2ts": buildM2TS(enoughPackets, 0),
	})

	got, err := InspectStreams(root)
	if err != nil {
		t.Fatalf("InspectStreams: %v", err)
	}
	if len(got.FilesInspected) < 2 {
		t.Errorf("FilesInspected = %v, want every stream that was actually read", got.FilesInspected)
	}
}
