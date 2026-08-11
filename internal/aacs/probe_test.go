package aacs

import (
	"os"
	"path/filepath"
	"testing"
)

// The histogram is what makes a real disc's report diagnosable: "scrambled" is a
// conclusion, but the distribution of scrambling values is the evidence.
func TestProbeReportsTSCHistogram(t *testing.T) {
	root := writeDisc(t, buildM2TS(enoughPackets, 0x02), true)

	rep, err := Probe(root, false)
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if len(rep.Samples) == 0 {
		t.Fatal("Probe returned no samples")
	}

	var total [4]int
	for _, s := range rep.Samples {
		for i, n := range s.TSCHistogram {
			total[i] += n
		}
	}
	if total[2] == 0 {
		t.Errorf("histogram = %v, want counts under index 2 (tsc=10)", total)
	}
	if total[0] != 0 {
		t.Errorf("histogram = %v, want no clear packets in a fully scrambled stream", total)
	}
}

func TestProbeReportsAACSDirectoryContents(t *testing.T) {
	root := writeDisc(t, buildM2TS(enoughPackets, 0), true)

	rep, err := Probe(root, false)
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if !rep.AACSDirPresent {
		t.Error("AACSDirPresent = false, want true")
	}
	if len(rep.AACSEntries) == 0 {
		t.Error("AACSEntries is empty, want the files found in the AACS directory")
	}
	if rep.Verdict != VerdictUnencrypted {
		t.Errorf("Verdict = %q, want %q", rep.Verdict, VerdictUnencrypted)
	}
	if rep.StreamSize == 0 {
		t.Error("StreamSize = 0, want the sampled file's size")
	}
}

// The trace exists so real-disc structure can be replayed as a test fixture. It
// must carry packet headers only — never payload, which is the actual film.
func TestProbeTraceCarriesHeadersOnly(t *testing.T) {
	root := writeDisc(t, buildM2TS(enoughPackets, 0), true)

	rep, err := Probe(root, true)
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}

	found := false
	for _, s := range rep.Samples {
		for _, h := range s.HeaderTrace {
			found = true
			// 8 bytes rendered as hex: 4-byte TP_extra_header + 4-byte TS header.
			if len(h) != 16 {
				t.Fatalf("trace entry %q is %d hex chars, want 16 (8 header bytes, no payload)", h, len(h))
			}
		}
	}
	if !found {
		t.Error("trace requested but no header entries were produced")
	}
}

func TestProbeWithoutTraceOmitsIt(t *testing.T) {
	root := writeDisc(t, buildM2TS(enoughPackets, 0), true)

	rep, err := Probe(root, false)
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	for _, s := range rep.Samples {
		if len(s.HeaderTrace) != 0 {
			t.Errorf("trace present without -trace: %v", s.HeaderTrace)
		}
	}
}

// A DVD has no BDMV/STREAM. The probe should say so rather than error.
func TestProbeOnNonBluRayLayout(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "VIDEO_TS"), 0o777); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	rep, err := Probe(root, false)
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if rep.Verdict != VerdictNotApplicable {
		t.Errorf("Verdict = %q, want %q", rep.Verdict, VerdictNotApplicable)
	}
}
