package aacs

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// SampleReport is the raw evidence from one sample point.
type SampleReport struct {
	Offset int64 `json:"offset"`
	// Stride is 192 (BDAV) or 188 (plain TS); 0 means no lock was achieved,
	// which on an AACS disc is itself the expected result.
	Stride                int      `json:"stride"`
	SyncOffset            int      `json:"sync_offset"`
	PacketsChecked        int      `json:"packets_checked"`
	ScrambledPackets      int      `json:"scrambled_packets"`
	TSCHistogram          [4]int   `json:"tsc_histogram"`
	AlignedUnitBoundaries int      `json:"aligned_unit_boundaries"`
	AlignedUnitHits       int      `json:"aligned_unit_hits"`
	HeaderTrace           []string `json:"header_trace,omitempty"`
}

// Report is everything the probe observed about one disc.
//
// It contains structural metadata and packet headers only — never payload
// bytes, which are the film itself. A report is safe to share.
type Report struct {
	Root           string         `json:"root"`
	AACSDirPresent bool           `json:"aacs_dir_present"`
	AACSEntries    []string       `json:"aacs_entries,omitempty"`
	MKB            *MKBInfo       `json:"mkb,omitempty"`
	MKBError       string         `json:"mkb_error,omitempty"`
	StreamFile     string         `json:"stream_file"`
	StreamSize     int64          `json:"stream_size"`
	StreamCount    int            `json:"stream_count"`
	FilesInspected []string       `json:"files_inspected,omitempty"`
	Samples        []SampleReport `json:"samples"`
	Verdict        Verdict        `json:"verdict"`
	Reason         string         `json:"reason"`
}

// Probe examines a disc and reports the evidence behind its verdict.
//
// InspectStreams answers "is this encrypted?" and stops as soon as it knows.
// Probe answers "what does this disc actually look like?" — it samples every
// point and keeps the per-sample detail, which is what a real disc needs to
// contribute before its behaviour can be turned into a test fixture.
//
// withTrace additionally captures the 8 header bytes of each packet.
func Probe(root string, withTrace bool) (Report, error) {
	rep := Report{Root: root, AACSDirPresent: HasAACSDir(root)}

	if entries, err := os.ReadDir(filepath.Join(root, "AACS")); err == nil {
		for _, e := range entries {
			name := e.Name()
			if info, err := e.Info(); err == nil {
				name = fmt.Sprintf("%s (%d bytes)", name, info.Size())
			}
			rep.AACSEntries = append(rep.AACSEntries, name)
		}
	}

	// Best effort: a parse failure is recorded rather than hidden, since the
	// record layout is taken from the spec and not yet confirmed against every
	// pressing. RawHeader survives either way.
	if mkb, err := ReadMKBInfo(root); err == nil {
		rep.MKB = &mkb
	} else {
		rep.MKBError = err.Error()
		if mkb.RawHeader != "" {
			rep.MKB = &mkb
		}
	}

	rep.StreamCount = countStreams(root)

	path, size, err := largestStream(root)
	if err != nil {
		return rep, err
	}
	if path == "" {
		rep.Verdict = VerdictNotApplicable
		rep.Reason = "no BDMV/STREAM content found (not a Blu-ray layout)"
		return rep, nil
	}
	rep.StreamFile = path
	rep.StreamSize = size

	f, err := os.Open(path)
	if err != nil {
		return rep, fmt.Errorf("aacs: open %s: %w", path, err)
	}
	defer f.Close()

	chunk := make([]byte, packetsPerSample*m2tsPacketSize+alignedUnitSize)
	for _, off := range sampleOffsets(size, int64(len(chunk))) {
		n, err := f.ReadAt(chunk, off)
		if err != nil && err != io.EOF {
			return rep, fmt.Errorf("aacs: read %s at %d: %w", path, off, err)
		}
		if n < alignedUnitSize {
			continue
		}

		s := analyseSample(chunk[:n], withTrace)
		rep.Samples = append(rep.Samples, SampleReport{
			Offset:                off,
			Stride:                s.stride,
			SyncOffset:            s.syncOffset,
			PacketsChecked:        s.checked,
			ScrambledPackets:      s.scrambled,
			TSCHistogram:          s.tscHist,
			AlignedUnitBoundaries: s.auBoundaries,
			AlignedUnitHits:       s.auHits,
			HeaderTrace:           s.headerTrace,
		})
	}

	// Run the real classifier so the report shows what BluForge would decide,
	// not a second implementation that could drift from it.
	insp, err := InspectStreams(root)
	if err != nil {
		return rep, err
	}
	rep.Verdict = insp.Verdict
	rep.Reason = insp.Reason
	// The verdict weighs several streams while the sample detail above covers
	// the largest; listing what was actually read keeps that visible.
	rep.FilesInspected = insp.FilesInspected

	return rep, nil
}

func countStreams(root string) int {
	entries, err := os.ReadDir(filepath.Join(root, "BDMV", "STREAM"))
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range entries {
		if !e.IsDir() {
			n++
		}
	}
	return n
}
