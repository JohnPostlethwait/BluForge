package mpls

import (
	"encoding/binary"
	"testing"
)

// buildMPLS constructs a minimal valid MPLS binary with the given PlayItems.
// Each PlayItem entry in items is a slice of (codingType, lang) pairs for its
// audio streams, plus a separate slice for subtitle streams.
func buildMPLS(t *testing.T, audioStreams [][]string, subtitleStreams [][]string) []byte {
	t.Helper()

	// Helper: build one StreamEntry (stream_type=0x01, dummy PID) + StreamCodingInfo.
	buildAudioStream := func(lang string) []byte {
		// StreamEntry: 1-byte length (=3) + 3 bytes data = 4 bytes total.
		se := []byte{3, 0x01, 0x11, 0x00}
		// StreamCodingInfo: 1-byte ciLen (=5) + 5 bytes data = 6 bytes total.
		//   data: coding_type(0x81=AC-3) + audio_fmt(1 byte) + lang(3 bytes)
		ci := make([]byte, 6)
		ci[0] = 5 // ciLen: 5 bytes follow
		ci[1] = 0x81
		ci[2] = 0x12 // audio_format(4bits) + sample_rate(4bits)
		copy(ci[3:6], []byte(lang))
		return append(se, ci...)
	}

	buildSubtitleStream := func(lang string) []byte {
		// StreamEntry: 1-byte length (=3) + 3 bytes data = 4 bytes total.
		se := []byte{3, 0x01, 0x12, 0x00}
		// StreamCodingInfo: 1-byte ciLen (=4) + 4 bytes data = 5 bytes total.
		//   data: coding_type(0x90=PGS) + lang(3 bytes)
		ci := make([]byte, 5)
		ci[0] = 4 // ciLen: 4 bytes follow
		ci[1] = 0x90
		copy(ci[2:5], []byte(lang))
		return append(se, ci...)
	}

	// Build STN_Table.
	buildSTN := func(audio, subs []string) []byte {
		var streams []byte
		for _, lang := range audio {
			streams = append(streams, buildAudioStream(lang)...)
		}
		for _, lang := range subs {
			streams = append(streams, buildSubtitleStream(lang)...)
		}
		// STN_Table header: length(2) + reserved(2) + counts(7) + reserved(5) = 16 bytes
		hdr := make([]byte, 16)
		stnLen := 14 + len(streams) // everything after the 2-byte length field
		binary.BigEndian.PutUint16(hdr[0:2], uint16(stnLen))
		// video=0, audio=len(audio), PG=len(subs), rest=0
		hdr[5] = byte(len(audio))
		hdr[6] = byte(len(subs))
		return append(hdr, streams...)
	}

	// Build a PlayItem for one title.
	buildPlayItem := func(audio, subs []string) []byte {
		stn := buildSTN(audio, subs)
		// PlayItem fixed header: 30 bytes + 2 bytes still_time/reserved = 32 bytes.
		hdr := make([]byte, 32)
		copy(hdr[0:5], []byte("00001")) // Clip_Information_file_name
		copy(hdr[5:9], []byte("M2TS"))  // Clip_codec_identifier
		// bytes 9-10: flags (is_multi_angle=0, connection_condition=1)
		hdr[10] = 0x01
		// byte 29: still_mode = 0 (already 0)
		// bytes 30-31: still_time/reserved = 0 (already 0)
		data := append(hdr, stn...)
		// Prepend the 2-byte length field.
		out := make([]byte, 2)
		binary.BigEndian.PutUint16(out, uint16(len(data)))
		return append(out, data...)
	}

	// Build PlayList section.
	var items []byte
	numItems := len(audioStreams)
	if len(subtitleStreams) > numItems {
		numItems = len(subtitleStreams)
	}
	for i := 0; i < numItems; i++ {
		var audio, subs []string
		if i < len(audioStreams) {
			audio = audioStreams[i]
		}
		if i < len(subtitleStreams) {
			subs = subtitleStreams[i]
		}
		items = append(items, buildPlayItem(audio, subs)...)
	}

	// PlayList header: length(4) + reserved(2) + numItems(2) + numSubPaths(2).
	plHdr := make([]byte, 10)
	plLen := 6 + len(items) // everything after the 4-byte length field
	binary.BigEndian.PutUint32(plHdr[0:4], uint32(plLen))
	binary.BigEndian.PutUint16(plHdr[6:8], uint16(numItems))

	playList := append(plHdr, items...)

	// File header layout:
	//   [0:4]   type_indicator          "MPLS"
	//   [4:8]   version_number          "0300"
	//   [8:12]  PlayList_start_address
	//   [12:16] PlayListMark_start_address
	//   [16:20] ExtensionData_start_address
	//   [20:44] reserved
	fileHdr := make([]byte, 44)
	copy(fileHdr[0:4], "MPLS")
	copy(fileHdr[4:8], "0300")
	playListOffset := uint32(44) // PlayList immediately follows header (no AppInfo)
	binary.BigEndian.PutUint32(fileHdr[8:12], playListOffset)
	// PlayListMark and ExtensionData point past the end — parser doesn't read them.
	// Use distinct values so tests catch offset mix-ups.
	pastEnd := playListOffset + uint32(len(playList))
	binary.BigEndian.PutUint32(fileHdr[12:16], pastEnd)
	binary.BigEndian.PutUint32(fileHdr[16:20], pastEnd)

	return append(fileHdr, playList...)
}

func TestParseMPLS_SingleTitle(t *testing.T) {
	data := buildMPLS(t, [][]string{{"eng", "jpn"}}, [][]string{{"eng", "spa"}})
	items, err := ParseMPLS(data)
	if err != nil {
		t.Fatalf("ParseMPLS: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 PlayItem, got %d", len(items))
	}
	got := items[0]
	if len(got.Audio) != 2 || got.Audio[0].LangCode != "eng" || got.Audio[1].LangCode != "jpn" {
		t.Errorf("audio langs: got %v, want [eng jpn]", got.Audio)
	}
	if len(got.Subtitle) != 2 || got.Subtitle[0].LangCode != "eng" || got.Subtitle[1].LangCode != "spa" {
		t.Errorf("subtitle langs: got %v, want [eng spa]", got.Subtitle)
	}
	// Verify coding types are captured.
	if got.Audio[0].CodingType != 0x81 {
		t.Errorf("audio[0] coding type: got 0x%02x, want 0x81 (AC-3)", got.Audio[0].CodingType)
	}
	if got.Subtitle[0].CodingType != 0x90 {
		t.Errorf("subtitle[0] coding type: got 0x%02x, want 0x90 (PGS)", got.Subtitle[0].CodingType)
	}
}

func TestParseMPLS_MultiTitle(t *testing.T) {
	data := buildMPLS(t,
		[][]string{{"eng"}, {"fre"}},
		[][]string{{"eng"}, {"fre"}},
	)
	items, err := ParseMPLS(data)
	if err != nil {
		t.Fatalf("ParseMPLS: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 PlayItems, got %d", len(items))
	}
	if items[0].Audio[0].LangCode != "eng" {
		t.Errorf("item[0] audio: got %q, want eng", items[0].Audio[0].LangCode)
	}
	if items[1].Audio[0].LangCode != "fre" {
		t.Errorf("item[1] audio: got %q, want fre", items[1].Audio[0].LangCode)
	}
}

func TestParseMPLS_InvalidMagic(t *testing.T) {
	_, err := ParseMPLS([]byte("NOTMPLS00000000000000"))
	if err == nil {
		t.Fatal("expected error for invalid magic, got nil")
	}
}

func TestParseMPLS_TooShort(t *testing.T) {
	_, err := ParseMPLS([]byte("MPL"))
	if err == nil {
		t.Fatal("expected error for short file, got nil")
	}
}

func TestDecodeLang(t *testing.T) {
	cases := []struct {
		in   []byte
		want string
	}{
		{[]byte("eng"), "eng"},
		{[]byte("ENG"), "eng"}, // lowercase normalisation
		{[]byte("und"), ""},    // undetermined → empty
		{[]byte{0, 0, 0}, ""}, // null → empty
		{[]byte("fra"), "fra"},
	}
	for _, tc := range cases {
		got := decodeLang(tc.in)
		if got != tc.want {
			t.Errorf("decodeLang(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
