package web

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/johnpostlethwait/bluforge/internal/discdb"
	"github.com/johnpostlethwait/bluforge/internal/makemkv"
)

// makeWebScan is a helper to build a DiscScan with named source files and names.
func makeWebScan(discName string, sourceFiles ...string) *makemkv.DiscScan {
	titles := make([]makemkv.TitleInfo, len(sourceFiles))
	for i, sf := range sourceFiles {
		titles[i] = makemkv.TitleInfo{
			Index: i,
			Attributes: map[int]string{
				2:  "Title " + sf,
				9:  "1:00:00",
				10: "10 GB",
				16: sf,
			},
		}
	}
	return &makemkv.DiscScan{
		DiscName:   discName,
		TitleCount: len(sourceFiles),
		Titles:     titles,
	}
}

func TestEnrichTitlesWithMatches(t *testing.T) {
	scan := makeWebScan("TestDisc", "00001.mpls", "00002.mpls", "99999.mpls")

	disc := discdb.Disc{
		Titles: []discdb.DiscTitle{
			{
				SourceFile: "00001.mpls",
				ItemType:   "movie",
				Item:       &discdb.DiscItemReference{Title: "The Main Feature", Type: "movie"},
			},
			{
				SourceFile: "00002.mpls",
				ItemType:   "episode",
				Season:     "1",
				Episode:    "2",
				Item:       &discdb.DiscItemReference{Title: "Episode Two", Type: "episode"},
			},
			// 99999.mpls is NOT in disc titles — should be unmatched
		},
	}

	titles := enrichTitlesWithMatches(scan, disc)

	if len(titles) != 3 {
		t.Fatalf("expected 3 titles, got %d", len(titles))
	}

	// Find each title by SourceFile
	byFile := make(map[string]TitleJSON, len(titles))
	for _, tj := range titles {
		byFile[tj.SourceFile] = tj
	}

	// Matched: 00001.mpls — movie
	main, ok := byFile["00001.mpls"]
	if !ok {
		t.Fatal("missing title for 00001.mpls")
	}
	if !main.Matched {
		t.Errorf("expected 00001.mpls to have Matched=true")
	}
	if !main.Selected {
		t.Errorf("expected matched title 00001.mpls to have Selected=true")
	}
	if main.ContentTitle != "The Main Feature" {
		t.Errorf("expected ContentTitle=%q, got %q", "The Main Feature", main.ContentTitle)
	}
	if main.ContentType != "movie" {
		t.Errorf("expected ContentType=%q, got %q", "movie", main.ContentType)
	}

	// Matched: 00002.mpls — episode with season/episode
	ep, ok := byFile["00002.mpls"]
	if !ok {
		t.Fatal("missing title for 00002.mpls")
	}
	if !ep.Matched {
		t.Errorf("expected 00002.mpls to have Matched=true")
	}
	if !ep.Selected {
		t.Errorf("expected matched title 00002.mpls to have Selected=true")
	}
	if ep.ContentTitle != "Episode Two" {
		t.Errorf("expected ContentTitle=%q, got %q", "Episode Two", ep.ContentTitle)
	}
	if ep.Season != "1" {
		t.Errorf("expected Season=%q, got %q", "1", ep.Season)
	}
	if ep.Episode != "2" {
		t.Errorf("expected Episode=%q, got %q", "2", ep.Episode)
	}

	// Unmatched: 99999.mpls
	unmatched, ok := byFile["99999.mpls"]
	if !ok {
		t.Fatal("missing title for 99999.mpls")
	}
	if unmatched.Matched {
		t.Errorf("expected 99999.mpls to have Matched=false")
	}
	if unmatched.Selected {
		t.Errorf("expected unmatched title 99999.mpls to have Selected=false")
	}
}

// TestEnrichTitlesWithMatches_StubAndIdentified verifies that when a disc
// has a mix of identified titles (Item set) and stub titles (Item==nil),
// only the identified titles are pre-selected.
func TestEnrichTitlesWithMatches_StubAndIdentified(t *testing.T) {
	// Scan has 3 titles: one identified, one stub, one not in DiscDB at all.
	scan := makeWebScan("TestDisc", "00800.mpls", "00001.mpls", "99999.mpls")

	disc := discdb.Disc{
		Titles: []discdb.DiscTitle{
			// Identified: has Item.
			{
				SourceFile: "00800.mpls",
				ItemType:   "MainMovie",
				Item:       &discdb.DiscItemReference{Title: "The Main Feature", Type: "movie"},
			},
			// Stub: source file in DiscDB but no item assigned.
			{
				SourceFile: "00001.mpls",
				ItemType:   "movie",
				Item:       nil,
			},
			// 99999.mpls not in disc at all.
		},
	}

	titles := enrichTitlesWithMatches(scan, disc)

	byFile := make(map[string]TitleJSON)
	for _, tj := range titles {
		byFile[tj.SourceFile] = tj
	}

	main, ok := byFile["00800.mpls"]
	if !ok {
		t.Fatal("missing title for 00800.mpls")
	}
	if !main.Matched || !main.Selected {
		t.Errorf("00800.mpls (identified): want Matched=true Selected=true, got Matched=%v Selected=%v", main.Matched, main.Selected)
	}

	stub, ok := byFile["00001.mpls"]
	if !ok {
		t.Fatal("missing title for 00001.mpls")
	}
	if stub.Matched || stub.Selected {
		t.Errorf("00001.mpls (stub): want Matched=false Selected=false, got Matched=%v Selected=%v", stub.Matched, stub.Selected)
	}

	unknown, ok := byFile["99999.mpls"]
	if !ok {
		t.Fatal("missing title for 99999.mpls")
	}
	if unknown.Matched || unknown.Selected {
		t.Errorf("99999.mpls (not in DiscDB): want Matched=false Selected=false, got Matched=%v Selected=%v", unknown.Matched, unknown.Selected)
	}
}

// TestEnrichTitlesWithMatches_AllStubs verifies that when every disc title
// is a stub (no item), the fallback kicks in and all scan titles are selected.
// This prevents a fully-unidentified disc from leaving the user with an empty
// checklist.
func TestEnrichTitlesWithMatches_AllStubs(t *testing.T) {
	scan := makeWebScan("TestDisc", "00800.mpls", "00001.mpls")

	disc := discdb.Disc{
		Titles: []discdb.DiscTitle{
			{SourceFile: "00800.mpls", ItemType: "movie", Item: nil},
			{SourceFile: "00001.mpls", ItemType: "extra", Item: nil},
		},
	}

	titles := enrichTitlesWithMatches(scan, disc)

	for _, tj := range titles {
		if !tj.Selected {
			t.Errorf("%q: expected Selected=true (all-stub fallback), got false", tj.SourceFile)
		}
	}
}

func TestBuildOutputName(t *testing.T) {
	tests := []struct {
		name string
		m    discdb.ContentMatch
		want string
	}{
		{
			name: "episode with season and episode",
			m:    discdb.ContentMatch{Matched: true, ContentTitle: "Male Unbonding", ContentType: "series", Season: "1", Episode: "2"},
			want: "S01E02 - Male Unbonding.mkv",
		},
		{
			name: "extra with season and episode gets no prefix",
			m:    discdb.ContentMatch{Matched: true, ContentTitle: "Inside Look: Male Unbonding", ContentType: "extra", Season: "1", Episode: "2"},
			want: "Inside Look: Male Unbonding.mkv",
		},
		{
			name: "Extra capitalized with season and episode gets no prefix",
			m:    discdb.ContentMatch{Matched: true, ContentTitle: "Behind the Scenes", ContentType: "Extra", Season: "1", Episode: "2"},
			want: "Behind the Scenes.mkv",
		},
		{
			name: "DeletedScene with season and episode gets no prefix",
			m:    discdb.ContentMatch{Matched: true, ContentTitle: "Deleted Scenes: Male Unbonding", ContentType: "DeletedScene", Season: "1", Episode: "2"},
			want: "Deleted Scenes: Male Unbonding.mkv",
		},
		{
			name: "movie with no season or episode",
			m:    discdb.ContentMatch{Matched: true, ContentTitle: "The Matrix", ContentType: "movie"},
			want: "The Matrix.mkv",
		},
		{
			name: "unmatched returns empty",
			m:    discdb.ContentMatch{Matched: false},
			want: "",
		},
		{
			name: "matched but no content title returns empty",
			m:    discdb.ContentMatch{Matched: true, ContentTitle: "", ContentType: "series", Season: "1", Episode: "1"},
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildOutputName(tt.m)
			if got != tt.want {
				t.Errorf("buildOutputName() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFindDiscForRelease(t *testing.T) {
	disc1 := discdb.Disc{ID: 10, Name: "Disc One"}
	disc2 := discdb.Disc{ID: 20, Name: "Disc Two"}

	items := []discdb.MediaItem{
		{
			ID:    1,
			Title: "Movie A",
			Releases: []discdb.Release{
				{ID: 100, Title: "Release A1", Discs: []discdb.Disc{disc1}},
				{ID: 101, Title: "Release A2", Discs: []discdb.Disc{disc2}},
			},
		},
		{
			ID:    2,
			Title: "Movie B",
			Releases: []discdb.Release{
				{ID: 200, Title: "Release B1", Discs: []discdb.Disc{}},
			},
		},
	}

	t.Run("found first disc of matching release", func(t *testing.T) {
		disc := findDiscForRelease(items, "100", "")
		if disc == nil {
			t.Fatal("expected disc, got nil")
		}
		if disc.ID != 10 {
			t.Errorf("expected disc.ID=10, got %d", disc.ID)
		}
	})

	t.Run("found disc from second release", func(t *testing.T) {
		disc := findDiscForRelease(items, "101", "")
		if disc == nil {
			t.Fatal("expected disc, got nil")
		}
		if disc.ID != 20 {
			t.Errorf("expected disc.ID=20, got %d", disc.ID)
		}
	})

	t.Run("release with no discs returns nil", func(t *testing.T) {
		disc := findDiscForRelease(items, "200", "")
		if disc != nil {
			t.Errorf("expected nil for release with no discs, got %+v", disc)
		}
	})

	t.Run("unknown release ID returns nil", func(t *testing.T) {
		disc := findDiscForRelease(items, "999", "")
		if disc != nil {
			t.Errorf("expected nil for unknown release ID, got %+v", disc)
		}
	})

	t.Run("non-numeric release ID returns nil", func(t *testing.T) {
		disc := findDiscForRelease(items, "not-a-number", "")
		if disc != nil {
			t.Errorf("expected nil for non-numeric release ID, got %+v", disc)
		}
	})

	t.Run("empty items returns nil", func(t *testing.T) {
		disc := findDiscForRelease(nil, "100", "")
		if disc != nil {
			t.Errorf("expected nil for empty items, got %+v", disc)
		}
	})

	// Multi-disc release tests for disc ID selection.
	multiDiscItems := []discdb.MediaItem{
		{
			ID:    3,
			Title: "Seinfeld",
			Releases: []discdb.Release{
				{
					ID:    300,
					Title: "Season 2",
					Discs: []discdb.Disc{
						{ID: 50, Index: 0, Name: "Disc 1"},
						{ID: 51, Index: 1, Name: "Disc 2"},
						{ID: 52, Index: 2, Name: "Disc 3"},
					},
				},
			},
		},
	}

	t.Run("multi-disc with valid discID returns correct disc", func(t *testing.T) {
		disc := findDiscForRelease(multiDiscItems, "300", "51")
		if disc == nil {
			t.Fatal("expected disc, got nil")
		}
		if disc.ID != 51 {
			t.Errorf("expected disc.ID=51, got %d", disc.ID)
		}
		if disc.Name != "Disc 2" {
			t.Errorf("expected disc.Name='Disc 2', got %q", disc.Name)
		}
	})

	t.Run("multi-disc with empty discID returns first disc", func(t *testing.T) {
		disc := findDiscForRelease(multiDiscItems, "300", "")
		if disc == nil {
			t.Fatal("expected disc, got nil")
		}
		if disc.ID != 50 {
			t.Errorf("expected disc.ID=50 (first disc), got %d", disc.ID)
		}
	})

	t.Run("multi-disc with invalid discID returns first disc", func(t *testing.T) {
		disc := findDiscForRelease(multiDiscItems, "300", "999")
		if disc == nil {
			t.Fatal("expected disc, got nil")
		}
		if disc.ID != 50 {
			t.Errorf("expected disc.ID=50 (fallback to first), got %d", disc.ID)
		}
	})
}

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
