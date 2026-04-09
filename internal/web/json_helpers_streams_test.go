package web

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/johnpostlethwait/bluforge/internal/makemkv"
)

// TestStreamDataFlowEndToEnd verifies that stream data (audio/subtitle with
// language codes) survives the full conversion pipeline: DiscScan with streams →
// scanToTitleJSON → JSON marshal, and that extractDiscLanguages correctly
// aggregates languages. This is the exact code path used when the scan handler
// returns titles to the frontend.
func TestStreamDataFlowEndToEnd(t *testing.T) {
	// Build a DiscScan that mimics what buildDiscScan produces from SINFO lines.
	scan := &makemkv.DiscScan{
		DriveIndex: 0,
		DiscName:   "SEINFELD_S8D1",
		TitleCount: 2,
		Titles: []makemkv.TitleInfo{
			{
				Index: 0,
				Attributes: map[int]string{
					2:  "Season 8 Episode 1",
					9:  "0:22:00",
					10: "1.2 GB",
					16: "title_t00.vts",
					27: "title_t00.mkv",
				},
				Streams: []makemkv.StreamInfo{
					{TitleIndex: 0, StreamIndex: 0, Attributes: map[int]string{1: "V_MPEG2", 6: "Mpeg2"}},
					{TitleIndex: 0, StreamIndex: 1, Attributes: map[int]string{1: "A_AC3", 3: "eng", 4: "English", 6: "AC3", 14: "2.0"}},
					{TitleIndex: 0, StreamIndex: 2, Attributes: map[int]string{1: "A_AC3", 3: "fra", 4: "French", 6: "AC3", 14: "2.0"}},
					{TitleIndex: 0, StreamIndex: 3, Attributes: map[int]string{1: "S_VOBSUB", 3: "eng", 4: "English"}},
					{TitleIndex: 0, StreamIndex: 4, Attributes: map[int]string{1: "S_VOBSUB", 3: "spa", 4: "Spanish"}},
				},
			},
			{
				Index: 1,
				Attributes: map[int]string{
					2:  "Special Features",
					9:  "0:05:00",
					10: "200 MB",
					16: "title_t01.vts",
					27: "title_t01.mkv",
				},
				Streams: []makemkv.StreamInfo{
					{TitleIndex: 1, StreamIndex: 0, Attributes: map[int]string{1: "V_MPEG2"}},
					{TitleIndex: 1, StreamIndex: 1, Attributes: map[int]string{1: "A_AC3", 3: "eng", 4: "English", 6: "AC3"}},
				},
			},
		},
	}

	// Step 1: Convert to TitleJSON (same function used by scan handler).
	titles := scanToTitleJSON(scan)
	if len(titles) != 2 {
		t.Fatalf("expected 2 TitleJSON entries, got %d", len(titles))
	}

	// Step 2: Find title 0 and verify streams.
	var t0 TitleJSON
	for _, tj := range titles {
		if tj.Index == 0 {
			t0 = tj
			break
		}
	}

	if len(t0.Streams) == 0 {
		t.Fatalf("title 0 has 0 StreamJSON entries — streams lost in scanToTitleJSON")
	}

	// We expect: 1 video, 2 audio (eng, fra), 2 subtitle (eng, spa) = 5 streams.
	if len(t0.Streams) != 5 {
		t.Errorf("title 0: expected 5 streams, got %d", len(t0.Streams))
		for i, s := range t0.Streams {
			t.Logf("  stream[%d]: type=%q langCode=%q langName=%q codec=%q", i, s.Type, s.LangCode, s.LangName, s.Codec)
		}
	}

	// Verify audio streams have language codes.
	audioCount := 0
	for _, s := range t0.Streams {
		if s.Type == "audio" {
			audioCount++
			if s.LangCode == "" {
				t.Errorf("audio stream %d has empty LangCode — language data lost", s.StreamIndex)
			}
			if s.LangName == "" {
				t.Errorf("audio stream %d has empty LangName", s.StreamIndex)
			}
			if s.Codec == "" {
				t.Errorf("audio stream %d has empty Codec", s.StreamIndex)
			}
		}
	}
	if audioCount != 2 {
		t.Errorf("expected 2 audio streams, got %d", audioCount)
	}

	// Verify subtitle streams have language codes.
	subCount := 0
	for _, s := range t0.Streams {
		if s.Type == "subtitle" {
			subCount++
			if s.LangCode == "" {
				t.Errorf("subtitle stream %d has empty LangCode — language data lost", s.StreamIndex)
			}
		}
	}
	if subCount != 2 {
		t.Errorf("expected 2 subtitle streams, got %d", subCount)
	}

	// Step 3: JSON marshal and verify the streams field is present and populated.
	jsonBytes, err := json.Marshal(titles)
	if err != nil {
		t.Fatalf("JSON marshal failed: %v", err)
	}
	jsonStr := string(jsonBytes)

	// The "streams" key must appear in the JSON (not omitted by omitempty).
	if !strings.Contains(jsonStr, `"streams"`) {
		t.Errorf("JSON output does not contain 'streams' key — omitempty dropped it:\n%s", jsonStr)
	}
	if !strings.Contains(jsonStr, `"langCode":"eng"`) {
		t.Errorf("JSON output does not contain langCode 'eng':\n%s", jsonStr)
	}
	if !strings.Contains(jsonStr, `"langCode":"fra"`) {
		t.Errorf("JSON output does not contain langCode 'fra':\n%s", jsonStr)
	}
	if !strings.Contains(jsonStr, `"langCode":"spa"`) {
		t.Errorf("JSON output does not contain langCode 'spa':\n%s", jsonStr)
	}

	// Step 4: Verify extractDiscLanguages produces the right language options.
	audioLangs, subLangs := extractDiscLanguages(scan, "", "")
	if len(audioLangs) == 0 {
		t.Errorf("extractDiscLanguages returned 0 audio languages — will show 'not available' fallback")
	}
	if len(subLangs) == 0 {
		t.Errorf("extractDiscLanguages returned 0 subtitle languages — will show 'not available' fallback")
	}

	// Verify expected language codes.
	audioLangCodes := make(map[string]bool)
	for _, l := range audioLangs {
		audioLangCodes[l.Code] = true
	}
	if !audioLangCodes["eng"] {
		t.Errorf("extractDiscLanguages missing audio lang 'eng'")
	}
	if !audioLangCodes["fra"] {
		t.Errorf("extractDiscLanguages missing audio lang 'fra'")
	}

	subLangCodes := make(map[string]bool)
	for _, l := range subLangs {
		subLangCodes[l.Code] = true
	}
	if !subLangCodes["eng"] {
		t.Errorf("extractDiscLanguages missing subtitle lang 'eng'")
	}
	if !subLangCodes["spa"] {
		t.Errorf("extractDiscLanguages missing subtitle lang 'spa'")
	}
}

// TestStreamDataFlowEndToEnd_MPLSCreated verifies the full data path when
// streams are created by MPLS enrichment (no SINFO lines from makemkvcon).
// This is the UHD disc scenario where the UI was showing "Language information
// not available" because MPLS-created streams weren't reaching the frontend.
func TestStreamDataFlowEndToEnd_MPLSCreated(t *testing.T) {
	// Simulate a UHD disc scan where MPLS enrichment has already created
	// streams (no SINFO from makemkvcon). These streams look exactly like
	// what createStreamsFromMPLS produces.
	scan := &makemkv.DiscScan{
		DriveIndex: 0,
		DiscName:   "SEINFELD_S8_UHD",
		TitleCount: 1,
		Titles: []makemkv.TitleInfo{
			{
				Index: 0,
				Attributes: map[int]string{
					2:  "Season 8 Disc 1",
					9:  "3:30:00",
					10: "25.0 GB",
					16: "00100.mpls",
				},
				Streams: []makemkv.StreamInfo{
					{TitleIndex: 0, StreamIndex: 0, Attributes: map[int]string{
						1: "A_DTSHD", 3: "eng", 4: "English", 6: "DTS-HD MA",
					}},
					{TitleIndex: 0, StreamIndex: 1, Attributes: map[int]string{
						1: "A_AC3", 3: "eng", 4: "English", 6: "AC3",
					}},
					{TitleIndex: 0, StreamIndex: 2, Attributes: map[int]string{
						1: "S_HDMV/PGS", 3: "eng", 4: "English", 6: "PGS",
					}},
					{TitleIndex: 0, StreamIndex: 3, Attributes: map[int]string{
						1: "S_HDMV/PGS", 3: "eng", 4: "English", 6: "PGS",
					}},
				},
			},
		},
	}

	// Verify scanToTitleJSON includes the MPLS-created streams.
	titles := scanToTitleJSON(scan)
	if len(titles) != 1 {
		t.Fatalf("expected 1 title, got %d", len(titles))
	}
	if len(titles[0].Streams) != 4 {
		t.Fatalf("expected 4 streams in TitleJSON, got %d", len(titles[0].Streams))
	}

	// Verify stream types and language codes survived serialization.
	for _, s := range titles[0].Streams {
		if s.LangCode != "eng" {
			t.Errorf("stream %d: expected langCode 'eng', got %q", s.StreamIndex, s.LangCode)
		}
		if s.LangName == "" {
			t.Errorf("stream %d: langName is empty", s.StreamIndex)
		}
		if s.Type != "audio" && s.Type != "subtitle" {
			t.Errorf("stream %d: expected type audio or subtitle, got %q", s.StreamIndex, s.Type)
		}
	}

	// Verify extractDiscLanguages finds the MPLS-created streams.
	audioLangs, subLangs := extractDiscLanguages(scan, "", "")
	if len(audioLangs) == 0 {
		t.Error("extractDiscLanguages returned 0 audio languages — UI will show 'not available'")
	}
	if len(subLangs) == 0 {
		t.Error("extractDiscLanguages returned 0 subtitle languages — UI will show 'not available'")
	}

	// Verify JSON output has language data.
	jsonBytes, err := json.Marshal(titles)
	if err != nil {
		t.Fatalf("JSON marshal failed: %v", err)
	}
	jsonStr := string(jsonBytes)
	if !strings.Contains(jsonStr, `"langCode":"eng"`) {
		t.Errorf("JSON output missing langCode 'eng': %s", jsonStr)
	}
	if !strings.Contains(jsonStr, `"type":"audio"`) {
		t.Errorf("JSON output missing type 'audio': %s", jsonStr)
	}
}
