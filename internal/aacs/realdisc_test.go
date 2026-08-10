package aacs

import "testing"

// Golden signatures measured on real UHD discs, 2026-08-09.
//
// Two discs from the same era, near-identical in every structural respect —
// same AACS entry list (5MB MKB_RO.inf, content certificates, revocation list,
// three content-hash tables), 10 streams each, a ~49GB 00001.m2ts feature —
// differing only in whether the payload is actually encrypted. One is a disc
// whose AACS directory is spurious; the other rips normally.
//
// These tests assert the classifier reproduces what was observed on the real
// hardware. The fixtures are synthetic because the discs are copyrighted films,
// but the numbers below are measurements, not guesses.
//
// Both halves matter, and each catches a different way of being wrong:
//
//   - The unencrypted disc locks a 192-byte stride with sync at offset 4. A
//     detector built on the 188-byte assumption finds no sync and never
//     recovers the disc it exists to recover.
//   - The encrypted disc locks no stride at all, because AACS encrypts all but
//     the first 16 bytes of each 6144-byte aligned unit, leaving the sync bytes
//     of packets 1-31 inside the ciphertext. It therefore reports zero
//     scrambled packets — out of zero readable packets. Concluding
//     "no scrambling bits set, so it is unencrypted" calls this disc spurious
//     and fires a ~100GB backup at content that genuinely needs a key.

// realDiscSignature is what the probe reported per sample point.
type realDiscSignature struct {
	stride                int
	syncOffset            int
	packetsChecked        int
	scrambledPackets      int
	alignedUnitBoundaries int
	alignedUnitHits       int
}

// Measured on the disc that prompted this whole investigation: a full AACS
// directory over an unencrypted payload.
var realSpuriousAACSDisc = realDiscSignature{
	stride:                192,
	syncOffset:            4,
	packetsChecked:        432,
	scrambledPackets:      0,
	alignedUnitBoundaries: 14,
	alignedUnitHits:       14,
}

// Measured on a normal UHD disc from the same set that rips without incident.
var realEncryptedDisc = realDiscSignature{
	stride:                0, // no lock is possible: the sync bytes are ciphertext
	syncOffset:            0,
	packetsChecked:        0,
	scrambledPackets:      0,
	alignedUnitBoundaries: 14,
	alignedUnitHits:       14,
}

func TestMatchesRealSpuriousAACSDisc(t *testing.T) {
	root := writeDisc(t, buildM2TS(enoughPackets, 0), true)

	rep, err := Probe(root, false)
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if rep.Verdict != VerdictUnencrypted {
		t.Fatalf("Verdict = %q, want %q — the real disc is recoverable", rep.Verdict, VerdictUnencrypted)
	}
	if len(rep.Samples) != sampleCount {
		t.Fatalf("took %d samples, want %d", len(rep.Samples), sampleCount)
	}
	for i, s := range rep.Samples {
		assertSignature(t, i, s, realSpuriousAACSDisc)
	}
}

func TestMatchesRealEncryptedDisc(t *testing.T) {
	// 600 aligned units reproduces the sampling geometry of the real 49GB file.
	root := writeDisc(t, buildAACSEncrypted(600), true)

	rep, err := Probe(root, false)
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if rep.Verdict != VerdictScrambled {
		t.Fatalf("Verdict = %q, want %q — recovery must never run on this disc", rep.Verdict, VerdictScrambled)
	}
	if len(rep.Samples) == 0 {
		t.Fatal("Probe returned no samples")
	}
	for i, s := range rep.Samples {
		assertSignature(t, i, s, realEncryptedDisc)
	}
}

// The encrypted disc reports zero scrambled packets. Anything treating that as
// evidence of an unencrypted payload — as the original investigation notes did —
// destroys the distinction the whole feature rests on.
func TestZeroScrambledPacketsIsNotEvidenceOfPlaintext(t *testing.T) {
	root := writeDisc(t, buildAACSEncrypted(600), true)

	insp, err := InspectStreams(root)
	if err != nil {
		t.Fatalf("InspectStreams: %v", err)
	}
	if insp.ScrambledPackets != 0 {
		t.Fatalf("ScrambledPackets = %d; this test is meaningless unless it is 0", insp.ScrambledPackets)
	}
	if insp.Verdict == VerdictUnencrypted {
		t.Error("an encrypted disc with 0 readable packets was classified unencrypted")
	}
}

func assertSignature(t *testing.T, i int, got SampleReport, want realDiscSignature) {
	t.Helper()
	if got.Stride != want.stride {
		t.Errorf("sample %d: stride = %d, want %d", i, got.Stride, want.stride)
	}
	if got.SyncOffset != want.syncOffset {
		t.Errorf("sample %d: sync offset = %d, want %d", i, got.SyncOffset, want.syncOffset)
	}
	if got.PacketsChecked != want.packetsChecked {
		t.Errorf("sample %d: packets checked = %d, want %d", i, got.PacketsChecked, want.packetsChecked)
	}
	if got.ScrambledPackets != want.scrambledPackets {
		t.Errorf("sample %d: scrambled packets = %d, want %d", i, got.ScrambledPackets, want.scrambledPackets)
	}
	if got.AlignedUnitBoundaries != want.alignedUnitBoundaries {
		t.Errorf("sample %d: aligned-unit boundaries = %d, want %d", i, got.AlignedUnitBoundaries, want.alignedUnitBoundaries)
	}
	if got.AlignedUnitHits != want.alignedUnitHits {
		t.Errorf("sample %d: aligned-unit hits = %d, want %d", i, got.AlignedUnitHits, want.alignedUnitHits)
	}
}
