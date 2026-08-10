// Package aacs determines whether the payload of a Blu-ray disc is actually
// encrypted, independent of whether the disc carries an AACS directory.
//
// Some retail discs ship with a complete AACS directory over an unencrypted
// payload — a mastering defect. MakeMKV keys off the directory alone, demands a
// volume key that does not exist, and fails with an error indistinguishable
// from a genuine missing-key case. Reading the stream is the only way to tell
// the two apart, and the answer decides whether an expensive recovery is worth
// attempting.
package aacs

import (
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// MPEG-TS / BDAV layout constants.
const (
	syncByte = 0x47

	// tsPacketSize is a bare transport stream packet.
	tsPacketSize = 188

	// m2tsPacketSize is what Blu-ray actually uses: a 4-byte TP_extra_header
	// timecode followed by a 188-byte TS packet. The sync byte therefore sits
	// at offset 4 of each 192-byte unit, not at offset 0.
	m2tsPacketSize = 192

	// alignedUnitSize is the AACS encryption granule: 32 source packets. The
	// first 16 bytes of each unit stay plaintext; the rest is ciphertext.
	alignedUnitSize = 6144
)

// Sampling parameters. Several hundred packets are read from several offsets
// spread through the file — sampling only the start would miss a stream that
// changes character partway through.
const (
	sampleCount      = 5
	packetsPerSample = 400

	// minSamplesLocked is how many sample points must yield a clean stride lock
	// before the stream can be declared unencrypted.
	minSamplesLocked = 3

	// minPacketsChecked guards against declaring a tiny file unencrypted.
	minPacketsChecked = 500

	// minConsecutiveSync is the run length required to call a stride locked.
	// At 10 packets, a false lock in random data is about a 256^-9 event.
	minConsecutiveSync = 10

	// minAlignedUnitRatio is the fraction of 6144-byte boundaries that must
	// carry a sync byte before absent stride lock is read as AACS encryption
	// rather than as noise.
	minAlignedUnitRatio = 0.9
)

// Verdict is the conclusion drawn about a disc's payload.
type Verdict string

const (
	// VerdictUnencrypted means no scrambling was found: an AACS directory on
	// this disc is spurious and the backup-and-strip recovery applies.
	VerdictUnencrypted Verdict = "unencrypted"
	// VerdictScrambled means the payload really is encrypted. Recovery cannot
	// help; this is a genuine unknown-volume-key case.
	VerdictScrambled Verdict = "scrambled"
	// VerdictUnknown means no confident determination was possible. Treated
	// exactly like VerdictScrambled by callers — ambiguity must not authorise
	// an expensive, pointless backup.
	VerdictUnknown Verdict = "unknown"
	// VerdictNotApplicable means there was nothing to inspect, e.g. a DVD.
	VerdictNotApplicable Verdict = "n/a"
)

// Inspection is the result of examining a disc's largest stream.
type Inspection struct {
	Verdict          Verdict
	File             string // stream file sampled, "" when none was
	Stride           int    // 192 or 188; 0 when no lock was achieved
	SamplesTaken     int
	SamplesLocked    int
	PacketsChecked   int
	ScrambledPackets int
	Reason           string // why the verdict came out as it did
}

// HasAACSDir reports whether root contains an AACS directory. Its presence is
// what makes MakeMKV demand a volume key, so it is recorded for every disc.
func HasAACSDir(root string) bool {
	fi, err := os.Stat(filepath.Join(root, "AACS"))
	return err == nil && fi.IsDir()
}

// InspectStreams samples the largest .m2ts under root/BDMV/STREAM and reports
// whether its packets are scrambled.
//
// An error is returned only when the disc could not be read at all. A disc that
// was read but could not be classified comes back as VerdictUnknown with a
// populated Reason, because "I could not tell" is a result the caller must act
// on, not an exception.
func InspectStreams(root string) (Inspection, error) {
	path, size, err := largestStream(root)
	if err != nil {
		return Inspection{}, err
	}
	if path == "" {
		return Inspection{
			Verdict: VerdictNotApplicable,
			Reason:  "no BDMV/STREAM content found (not a Blu-ray layout)",
		}, nil
	}

	f, err := os.Open(path)
	if err != nil {
		return Inspection{}, fmt.Errorf("aacs: open %s: %w", path, err)
	}
	defer f.Close()

	insp := Inspection{File: path}
	chunk := make([]byte, packetsPerSample*m2tsPacketSize+alignedUnitSize)

	for _, off := range sampleOffsets(size, int64(len(chunk))) {
		n, err := f.ReadAt(chunk, off)
		if err != nil && err != io.EOF {
			return Inspection{}, fmt.Errorf("aacs: read %s at %d: %w", path, off, err)
		}
		if n < alignedUnitSize {
			continue
		}
		insp.SamplesTaken++

		s := analyseSample(chunk[:n], false)
		if s.stride == 0 {
			if s.alignedUnitRatio() >= minAlignedUnitRatio {
				insp.Verdict = VerdictScrambled
				insp.Reason = fmt.Sprintf(
					"no packet-stride sync lock, but %d of %d aligned-unit boundaries carry a sync byte — "+
						"consistent with AACS encryption of 6144-byte units",
					s.auHits, s.auBoundaries)
				return insp, nil
			}
			continue
		}

		insp.SamplesLocked++
		insp.Stride = s.stride
		insp.PacketsChecked += s.checked
		insp.ScrambledPackets += s.scrambled
	}

	switch {
	case insp.ScrambledPackets > 0:
		insp.Verdict = VerdictScrambled
		insp.Reason = fmt.Sprintf("%d of %d sampled packets have transport_scrambling_control set",
			insp.ScrambledPackets, insp.PacketsChecked)

	case insp.SamplesLocked >= minSamplesLocked && insp.PacketsChecked >= minPacketsChecked:
		insp.Verdict = VerdictUnencrypted
		insp.Reason = fmt.Sprintf("%d packets across %d sample points at a %d-byte stride, none scrambled",
			insp.PacketsChecked, insp.SamplesLocked, insp.Stride)

	default:
		insp.Verdict = VerdictUnknown
		insp.Reason = fmt.Sprintf(
			"inconclusive: %d of %d sample points locked, %d packets checked (need %d locked and %d packets)",
			insp.SamplesLocked, insp.SamplesTaken, insp.PacketsChecked, minSamplesLocked, minPacketsChecked)
	}

	return insp, nil
}

// sample holds the analysis of one buffer read from the stream.
type sample struct {
	stride       int // 0 when no lock was achieved
	syncOffset   int
	checked      int
	scrambled    int
	auHits       int // aligned-unit boundaries carrying a sync byte
	auBoundaries int
	// tscHist counts packets by transport_scrambling_control value. The verdict
	// is a conclusion; this is the evidence behind it, which is what makes a
	// surprising result from a real disc diagnosable.
	tscHist [4]int
	// headerTrace holds the 8 header bytes of each packet — the 4-byte
	// TP_extra_header plus the 4-byte TS header — and never any payload.
	headerTrace  []string
	traceEnabled bool
}

func (s sample) alignedUnitRatio() float64 {
	if s.auBoundaries == 0 {
		return 0
	}
	return float64(s.auHits) / float64(s.auBoundaries)
}

// analyseSample locks a packet stride within buf and reads the scrambling bits
// of every packet it can reach. When no stride locks it falls back to counting
// sync bytes at aligned-unit boundaries, which is the fingerprint of AACS.
//
// buf must begin at an aligned-unit boundary for the fallback count to mean
// anything; sampleOffsets guarantees that.
func analyseSample(buf []byte, trace bool) sample {
	s := sample{traceEnabled: trace}

	// AACS leaves the first 16 bytes of every 6144-byte unit in the clear, so
	// the sync byte of each unit's first packet survives encryption. A high hit
	// rate here with no stride lock says "encrypted", not "not a stream".
	for off := 0; off+4 < len(buf); off += alignedUnitSize {
		s.auBoundaries++
		if buf[off+4] == syncByte {
			s.auHits++
		}
	}

	// Blu-ray is 192; plain .ts is 188. Try the real-world case first.
	for _, stride := range []int{m2tsPacketSize, tsPacketSize} {
		start, ok := lockStride(buf, stride)
		if !ok {
			continue
		}
		s.stride = stride
		s.syncOffset = start
		for p := start; p+tsPacketSize <= len(buf); p += stride {
			if buf[p] != syncByte {
				break
			}
			s.checked++
			// transport_scrambling_control is the top two bits of byte 3.
			// Masking before shifting (0x03 then >>6) would always yield zero
			// and report every disc as clear.
			tsc := (buf[p+3] >> 6) & 0x03
			s.tscHist[tsc]++
			if tsc != 0 {
				s.scrambled++
			}
			if s.traceEnabled {
				// Header bytes only: the TP_extra_header sits immediately
				// before the sync byte, and the TS header is the 4 bytes from
				// it. Payload is deliberately never captured.
				if p >= 4 {
					s.headerTrace = append(s.headerTrace, hex.EncodeToString(buf[p-4:p+4]))
				}
			}
		}
		return s
	}

	return s
}

// lockStride finds the first offset at which sync bytes repeat at stride for
// minConsecutiveSync packets, which is what distinguishes a real packet
// boundary from a coincidental 0x47 in payload data.
func lockStride(buf []byte, stride int) (int, bool) {
	limit := stride
	if limit > len(buf) {
		limit = len(buf)
	}
	for start := 0; start < limit; start++ {
		if buf[start] != syncByte {
			continue
		}
		if start+minConsecutiveSync*stride > len(buf) {
			return 0, false
		}
		ok := true
		for k := 1; k < minConsecutiveSync; k++ {
			if buf[start+k*stride] != syncByte {
				ok = false
				break
			}
		}
		if ok {
			return start, true
		}
	}
	return 0, false
}

// sampleOffsets spreads sample points through the file, each aligned to a
// 6144-byte boundary so aligned-unit detection stays meaningful. Sampling only
// the file start would miss a stream whose character changes partway through.
func sampleOffsets(size, chunk int64) []int64 {
	if size <= chunk {
		return []int64{0}
	}
	usable := size - chunk
	offsets := make([]int64, 0, sampleCount)
	seen := make(map[int64]bool, sampleCount)
	for i := 0; i < sampleCount; i++ {
		off := usable * int64(i) / int64(sampleCount)
		off -= off % alignedUnitSize
		if seen[off] {
			continue
		}
		seen[off] = true
		offsets = append(offsets, off)
	}
	return offsets
}

// largestStream returns the biggest .m2ts under root/BDMV/STREAM. The largest
// stream is the feature presentation; menus and trailers are not necessarily
// representative of how the disc was authored.
//
// A missing STREAM directory is not an error — DVDs legitimately have none.
func largestStream(root string) (string, int64, error) {
	streamDir := filepath.Join(root, "BDMV", "STREAM")
	entries, err := os.ReadDir(streamDir)
	if err != nil {
		if os.IsNotExist(err) {
			return "", 0, nil
		}
		return "", 0, fmt.Errorf("aacs: read %s: %w", streamDir, err)
	}

	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && strings.EqualFold(filepath.Ext(e.Name()), ".m2ts") {
			names = append(names, e.Name())
		}
	}
	// Sort for determinism so an equal-size tie always resolves the same way.
	sort.Strings(names)

	var bestPath string
	var bestSize int64
	for _, name := range names {
		fi, err := os.Stat(filepath.Join(streamDir, name))
		if err != nil {
			continue
		}
		if fi.Size() > bestSize {
			bestSize = fi.Size()
			bestPath = filepath.Join(streamDir, name)
		}
	}
	return bestPath, bestSize, nil
}
