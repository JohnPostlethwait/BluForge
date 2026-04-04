package makemkv

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/johnpostlethwait/bluforge/internal/mpls"
)

// mockCmdRunner returns fixed canned output for every Run call.
type mockCmdRunner struct {
	output string
	err    error
}

func (m *mockCmdRunner) Run(_ context.Context, _ ...string) (*strings.Reader, error) {
	return strings.NewReader(m.output), m.err
}

// ---- TestExecutorListDrives ------------------------------------------------

const twoDriverOutput = `DRV:0,2,999,1,"BD-RE HL-DT-ST BD-RE  WH16NS40","DEADPOOL_2","/dev/sr0"
DRV:1,2,999,0,"BD-RE ASUS BW-16D1HT","AVENGERS_ENDGAME","/dev/sr1"
`

func TestExecutorListDrives(t *testing.T) {
	mock := &mockCmdRunner{output: twoDriverOutput}
	ex := NewExecutor(WithRunner(mock))

	drives, err := ex.ListDrives(context.Background())
	if err != nil {
		t.Fatalf("ListDrives returned unexpected error: %v", err)
	}
	if len(drives) != 2 {
		t.Fatalf("expected 2 drives, got %d", len(drives))
	}

	// First drive
	if drives[0].Index != 0 {
		t.Errorf("drive[0].Index: expected 0, got %d", drives[0].Index)
	}
	if drives[0].DiscName != "DEADPOOL_2" {
		t.Errorf("drive[0].DiscName: expected DEADPOOL_2, got %q", drives[0].DiscName)
	}

	// Second drive
	if drives[1].Index != 1 {
		t.Errorf("drive[1].Index: expected 1, got %d", drives[1].Index)
	}
	if drives[1].DiscName != "AVENGERS_ENDGAME" {
		t.Errorf("drive[1].DiscName: expected AVENGERS_ENDGAME, got %q", drives[1].DiscName)
	}
}

// ---- TestExecutorScanDisc --------------------------------------------------

// Attribute IDs used by makemkvcon:
//
//	1  = disc type
//	2  = disc name
//	9  = duration
//	27 = output filename
//	16 = source file (MPLS playlist name)
//	33 = device path
const scanDiscOutput = `TCOUT:2
CINFO:2,0,"DEADPOOL_2"
CINFO:1,0,"Blu-ray disc"
TINFO:0,2,0,"Deadpool 2"
TINFO:0,9,0,"1:59:45"
TINFO:0,16,0,"00100.mpls"
TINFO:0,27,0,"title_t00.mkv"
TINFO:0,33,0,"/dev/sr0"
TINFO:1,2,0,"Special Features"
TINFO:1,9,0,"0:05:30"
TINFO:1,16,0,"00200.mpls"
TINFO:1,27,0,"title_t01.mkv"
TINFO:1,33,0,"/dev/sr0"
SINFO:0,0,1,0,"V_MPEG4/ISO/AVC"
SINFO:0,1,1,0,"A_AC3"
MSG:1005,0,1,"Operation successfully completed","",""
`

func TestExecutorScanDisc(t *testing.T) {
	mock := &mockCmdRunner{output: scanDiscOutput}
	ex := NewExecutor(WithRunner(mock))

	scan, err := ex.ScanDisc(context.Background(), 0)
	if err != nil {
		t.Fatalf("ScanDisc returned unexpected error: %v", err)
	}

	// Disc-level metadata.
	if scan.DiscName != "DEADPOOL_2" {
		t.Errorf("DiscName: expected DEADPOOL_2, got %q", scan.DiscName)
	}
	if scan.DiscType != "Blu-ray disc" {
		t.Errorf("DiscType: expected \"Blu-ray disc\", got %q", scan.DiscType)
	}
	if scan.TitleCount != 2 {
		t.Errorf("TitleCount: expected 2, got %d", scan.TitleCount)
	}
	if len(scan.Titles) != 2 {
		t.Fatalf("len(Titles): expected 2, got %d", len(scan.Titles))
	}

	// Find title 0 by index (order in slice is not guaranteed because we
	// iterate a map).
	var t0, t1 *TitleInfo
	for i := range scan.Titles {
		switch scan.Titles[i].Index {
		case 0:
			t0 = &scan.Titles[i]
		case 1:
			t1 = &scan.Titles[i]
		}
	}
	if t0 == nil {
		t.Fatal("title index 0 not found in scan")
	}
	if t1 == nil {
		t.Fatal("title index 1 not found in scan")
	}

	// Title 0 attributes.
	if t0.Name() != "Deadpool 2" {
		t.Errorf("t0.Name: expected \"Deadpool 2\", got %q", t0.Name())
	}
	if t0.Duration() != "1:59:45" {
		t.Errorf("t0.Duration: expected \"1:59:45\", got %q", t0.Duration())
	}
	if t0.Filename() != "title_t00.mkv" {
		t.Errorf("t0.Filename: expected \"title_t00.mkv\", got %q", t0.Filename())
	}
	if t0.SourceFile() != "00100.mpls" {
		t.Errorf("t0.SourceFile (attr 16): expected \"00100.mpls\", got %q", t0.SourceFile())
	}

	// Title 0 should have 2 streams attached.
	if len(t0.Streams) != 2 {
		t.Errorf("t0 stream count: expected 2, got %d", len(t0.Streams))
	}

	// Title 1 attributes.
	if t1.Name() != "Special Features" {
		t.Errorf("t1.Name: expected \"Special Features\", got %q", t1.Name())
	}
	if t1.SourceFile() != "00200.mpls" {
		t.Errorf("t1.SourceFile (attr 16): expected \"00200.mpls\", got %q", t1.SourceFile())
	}

	// One message should be captured.
	if len(scan.Messages) != 1 {
		t.Errorf("Messages count: expected 1, got %d", len(scan.Messages))
	}
}

// TestScanDiscStreamLanguages verifies that multiple SINFO attributes for the
// same stream (codec, langCode, langName, channels, etc.) are accumulated
// correctly in buildDiscScan. This is the critical path for the frontend's
// audio/subtitle language selection chips.
func TestScanDiscStreamLanguages(t *testing.T) {
	const output = `TCOUT:1
CINFO:2,0,"SEINFELD_S8D1"
CINFO:1,0,"DVD disc"
TINFO:0,2,0,"Episode 1"
TINFO:0,9,0,"0:22:00"
TINFO:0,10,0,"1.2 GB"
TINFO:0,16,0,"title_t00.vts"
TINFO:0,27,0,"title_t00.mkv"
TINFO:0,33,0,"/dev/sr0"
SINFO:0,0,1,0,"V_MPEG2"
SINFO:0,0,6,0,"Mpeg2"
SINFO:0,1,1,0,"A_AC3"
SINFO:0,1,3,0,"eng"
SINFO:0,1,4,0,"English"
SINFO:0,1,6,0,"AC3"
SINFO:0,1,14,0,"2.0"
SINFO:0,2,1,0,"A_AC3"
SINFO:0,2,3,0,"fra"
SINFO:0,2,4,0,"French"
SINFO:0,2,6,0,"AC3"
SINFO:0,2,14,0,"2.0"
SINFO:0,3,1,0,"S_VOBSUB"
SINFO:0,3,3,0,"eng"
SINFO:0,3,4,0,"English"
SINFO:0,4,1,0,"S_VOBSUB"
SINFO:0,4,3,0,"spa"
SINFO:0,4,4,0,"Spanish"
MSG:1005,0,1,"Operation successfully completed","",""
`
	mock := &mockCmdRunner{output: output}
	ex := NewExecutor(WithRunner(mock))

	scan, err := ex.ScanDisc(context.Background(), 0)
	if err != nil {
		t.Fatalf("ScanDisc returned unexpected error: %v", err)
	}

	if len(scan.Titles) != 1 {
		t.Fatalf("expected 1 title, got %d", len(scan.Titles))
	}
	title := &scan.Titles[0]

	// 1 video + 2 audio + 2 subtitle = 5 streams.
	if len(title.Streams) != 5 {
		t.Fatalf("expected 5 streams, got %d", len(title.Streams))
	}

	// Verify that multi-attribute SINFO accumulation works: each stream should
	// have ALL its attributes (type, langCode, langName, codec, channels).
	audioLangs := title.AudioLanguages()
	if len(audioLangs) != 2 {
		t.Errorf("expected 2 audio languages, got %v", audioLangs)
	}
	wantAudio := map[string]bool{"eng": true, "fra": true}
	for _, lc := range audioLangs {
		if !wantAudio[lc] {
			t.Errorf("unexpected audio lang %q", lc)
		}
	}

	subLangs := title.SubtitleLanguages()
	if len(subLangs) != 2 {
		t.Errorf("expected 2 subtitle languages, got %v", subLangs)
	}
	wantSub := map[string]bool{"eng": true, "spa": true}
	for _, lc := range subLangs {
		if !wantSub[lc] {
			t.Errorf("unexpected subtitle lang %q", lc)
		}
	}

	// Verify individual stream attributes are fully populated.
	for _, s := range title.Streams {
		if s.Type() == "audio" {
			if s.LangCode() == "" {
				t.Errorf("audio stream %d: LangCode is empty", s.StreamIndex)
			}
			if s.LangName() == "" {
				t.Errorf("audio stream %d: LangName is empty", s.StreamIndex)
			}
			if s.CodecShort() == "" {
				t.Errorf("audio stream %d: CodecShort is empty", s.StreamIndex)
			}
			if s.Channels() == "" {
				t.Errorf("audio stream %d: Channels is empty", s.StreamIndex)
			}
		}
		if s.Type() == "subtitle" {
			if s.LangCode() == "" {
				t.Errorf("subtitle stream %d: LangCode is empty", s.StreamIndex)
			}
			if s.LangName() == "" {
				t.Errorf("subtitle stream %d: LangName is empty", s.StreamIndex)
			}
		}
	}
}

// TestExecutorScanDiscNonZeroExit verifies that ScanDisc still returns titles
// when makemkvcon exits non-zero (e.g. AACS warnings on Blu-ray discs).
func TestExecutorScanDiscNonZeroExit(t *testing.T) {
	mock := &mockCmdRunner{
		output: scanDiscOutput,
		err:    fmt.Errorf("exit status 1"),
	}
	ex := NewExecutor(WithRunner(mock))

	scan, err := ex.ScanDisc(context.Background(), 0)
	if err != nil {
		t.Fatalf("ScanDisc should succeed when output contains titles despite non-zero exit: %v", err)
	}
	if len(scan.Titles) != 2 {
		t.Errorf("expected 2 titles, got %d", len(scan.Titles))
	}
	if scan.DiscName != "DEADPOOL_2" {
		t.Errorf("DiscName: expected DEADPOOL_2, got %q", scan.DiscName)
	}
}

// TestExecutorScanDiscNonZeroExitNoData verifies that ScanDisc returns the
// command error when there is no usable disc data in the output.
func TestExecutorScanDiscNonZeroExitNoData(t *testing.T) {
	mock := &mockCmdRunner{
		output: `MSG:5010,0,1,"Failed to open disc","",""` + "\n",
		err:    fmt.Errorf("exit status 1"),
	}
	ex := NewExecutor(WithRunner(mock))

	_, err := ex.ScanDisc(context.Background(), 0)
	if err == nil {
		t.Fatal("ScanDisc should return error when command fails with no usable output")
	}
}

// TestExecutorScanDiscFailedToOpen verifies that ScanDisc returns an error
// when makemkvcon reports "Failed to open disc" (MSG 5010), even if the
// command exits with status 0.
func TestExecutorScanDiscFailedToOpen(t *testing.T) {
	output := `DRV:0,2,999,1,"BD-RE PIONEER","Seinfeld Season 1","/dev/sr0"
MSG:3346,0,0,"LibreDrive compatible drive is required to open this disc","",""
MSG:5010,0,0,"Failed to open disc","Failed to open disc"
TCOUNT:0
`
	mock := &mockCmdRunner{output: output}
	ex := NewExecutor(WithRunner(mock))

	_, err := ex.ScanDisc(context.Background(), 0)
	if err == nil {
		t.Fatal("ScanDisc should return error when makemkvcon reports 'Failed to open disc'")
	}
	if !strings.Contains(err.Error(), "Failed to open disc") {
		t.Errorf("error should contain 'Failed to open disc', got: %v", err)
	}
}

// TestApplyMPLSLanguages_CreatesStreamsWhenEmpty verifies that when a title has
// no streams (makemkvcon omitted SINFO lines), applyMPLSLanguages creates
// StreamInfo objects from the MPLS data with correct type, lang, and codec.
func TestApplyMPLSLanguages_CreatesStreamsWhenEmpty(t *testing.T) {
	title := &TitleInfo{
		Index:      0,
		Attributes: map[int]string{},
		Streams:    nil, // no SINFO data
	}

	tl := mpls.PlayItemLanguages{
		Audio: []mpls.StreamEntry{
			{LangCode: "eng", CodingType: 0x83}, // TrueHD
			{LangCode: "jpn", CodingType: 0x81}, // AC3
		},
		Subtitle: []mpls.StreamEntry{
			{LangCode: "eng", CodingType: 0x90}, // PGS
			{LangCode: "spa", CodingType: 0x90}, // PGS
		},
	}

	created := applyMPLSLanguages(title, tl)
	if created != 4 {
		t.Fatalf("expected 4 streams created, got %d", created)
	}
	if len(title.Streams) != 4 {
		t.Fatalf("expected 4 streams on title, got %d", len(title.Streams))
	}

	// Audio stream 0: English TrueHD.
	s0 := &title.Streams[0]
	if s0.Type() != "audio" {
		t.Errorf("stream 0: expected type audio, got %q", s0.Type())
	}
	if s0.LangCode() != "eng" {
		t.Errorf("stream 0: expected langCode eng, got %q", s0.LangCode())
	}
	if s0.LangName() != "English" {
		t.Errorf("stream 0: expected langName English, got %q", s0.LangName())
	}
	if s0.CodecShort() != "TrueHD" {
		t.Errorf("stream 0: expected codec TrueHD, got %q", s0.CodecShort())
	}

	// Audio stream 1: Japanese AC3.
	s1 := &title.Streams[1]
	if s1.Type() != "audio" {
		t.Errorf("stream 1: expected type audio, got %q", s1.Type())
	}
	if s1.LangCode() != "jpn" {
		t.Errorf("stream 1: expected langCode jpn, got %q", s1.LangCode())
	}
	if s1.CodecShort() != "AC3" {
		t.Errorf("stream 1: expected codec AC3, got %q", s1.CodecShort())
	}

	// Subtitle stream 2: English PGS.
	s2 := &title.Streams[2]
	if s2.Type() != "subtitle" {
		t.Errorf("stream 2: expected type subtitle, got %q", s2.Type())
	}
	if s2.LangCode() != "eng" {
		t.Errorf("stream 2: expected langCode eng, got %q", s2.LangCode())
	}

	// Subtitle stream 3: Spanish PGS.
	s3 := &title.Streams[3]
	if s3.LangCode() != "spa" {
		t.Errorf("stream 3: expected langCode spa, got %q", s3.LangCode())
	}

	// Lossless detection should work via the codec short name.
	if !title.HasLosslessAudio() {
		t.Error("expected HasLosslessAudio() to return true (TrueHD stream present)")
	}
}

// TestApplyMPLSLanguages_EnrichesExistingStreams verifies the existing behavior:
// when streams exist from SINFO, MPLS data enriches them with language codes.
func TestApplyMPLSLanguages_EnrichesExistingStreams(t *testing.T) {
	title := &TitleInfo{
		Index:      0,
		Attributes: map[int]string{},
		Streams: []StreamInfo{
			{TitleIndex: 0, StreamIndex: 0, Attributes: map[int]string{AttrType: "A_AC3"}},
			{TitleIndex: 0, StreamIndex: 1, Attributes: map[int]string{AttrType: "S_HDMV/PGS"}},
		},
	}

	tl := mpls.PlayItemLanguages{
		Audio:    []mpls.StreamEntry{{LangCode: "eng", CodingType: 0x81}},
		Subtitle: []mpls.StreamEntry{{LangCode: "fra", CodingType: 0x90}},
	}

	updated := applyMPLSLanguages(title, tl)
	if updated != 2 {
		t.Fatalf("expected 2 streams updated, got %d", updated)
	}
	// Streams should still be 2 (not 4 — no new streams created).
	if len(title.Streams) != 2 {
		t.Fatalf("expected 2 streams (unchanged count), got %d", len(title.Streams))
	}
	if title.Streams[0].LangCode() != "eng" {
		t.Errorf("stream 0: expected langCode eng, got %q", title.Streams[0].LangCode())
	}
	if title.Streams[1].LangCode() != "fra" {
		t.Errorf("stream 1: expected langCode fra, got %q", title.Streams[1].LangCode())
	}
}

// TestPickRichestMPLS verifies that we select the MPLS entry with the most
// combined audio + subtitle streams.
func TestPickRichestMPLS(t *testing.T) {
	langs := map[string]mpls.PlayItemLanguages{
		"00100.mpls": {
			Audio:    []mpls.StreamEntry{{LangCode: "eng", CodingType: 0x81}},
			Subtitle: []mpls.StreamEntry{},
		},
		"00200.mpls": {
			Audio: []mpls.StreamEntry{
				{LangCode: "eng", CodingType: 0x83},
				{LangCode: "jpn", CodingType: 0x81},
			},
			Subtitle: []mpls.StreamEntry{
				{LangCode: "eng", CodingType: 0x90},
				{LangCode: "spa", CodingType: 0x90},
			},
		},
		"00300.mpls": {
			Audio:    []mpls.StreamEntry{{LangCode: "eng", CodingType: 0x81}},
			Subtitle: []mpls.StreamEntry{{LangCode: "eng", CodingType: 0x90}},
		},
	}

	best := pickRichestMPLS(langs)
	if len(best.Audio) != 2 {
		t.Errorf("expected 2 audio streams from richest, got %d", len(best.Audio))
	}
	if len(best.Subtitle) != 2 {
		t.Errorf("expected 2 subtitle streams from richest, got %d", len(best.Subtitle))
	}
}

// TestPickRichestMPLS_Empty verifies the fallback returns an empty result
// when no MPLS data is available.
func TestPickRichestMPLS_Empty(t *testing.T) {
	best := pickRichestMPLS(map[string]mpls.PlayItemLanguages{})
	if len(best.Audio) != 0 || len(best.Subtitle) != 0 {
		t.Errorf("expected empty result for empty input, got %d audio / %d sub", len(best.Audio), len(best.Subtitle))
	}
}

// TestParseTCOUNTLine verifies that TCOUNT (alternate spelling of TCOUT) is
// parsed correctly.
func TestParseTCOUNTLine(t *testing.T) {
	ev, err := ParseLine("TCOUNT:5")
	if err != nil {
		t.Fatalf("ParseLine(TCOUNT) error: %v", err)
	}
	if ev.Type != "TCOUT" {
		t.Errorf("expected type TCOUT, got %q", ev.Type)
	}
	if ev.Count != 5 {
		t.Errorf("expected count 5, got %d", ev.Count)
	}
}
