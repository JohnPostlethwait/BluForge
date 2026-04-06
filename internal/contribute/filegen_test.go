package contribute

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/johnpostlethwait/bluforge/internal/makemkv"
)

func testScan() *makemkv.DiscScan {
	return &makemkv.DiscScan{
		DiscName:   "THE_MATRIX",
		TitleCount: 2,
		Titles: []makemkv.TitleInfo{
			{
				Index: 0,
				Attributes: map[int]string{
					2: "The Matrix", 8: "28", 9: "1:45:32",
					10: "32.2 GB", 11: "34567890123", 16: "00800.mpls",
				},
				Streams: []makemkv.StreamInfo{
					{TitleIndex: 0, StreamIndex: 0, Attributes: map[int]string{
						1: "V_MPEG4/ISO/AVC", 6: "Mpeg4 AVC High@L4.1",
						19: "1920x1080", 20: "16:9",
					}},
					{TitleIndex: 0, StreamIndex: 1, Attributes: map[int]string{
						1: "A_TRUEHD", 6: "TrueHD Atmos",
						3: "eng", 4: "English",
					}},
				},
			},
			{
				Index: 1,
				Attributes: map[int]string{
					2: "Behind the Scenes", 8: "5", 9: "0:23:15",
					10: "4.1 GB", 11: "4400000000", 16: "00801.mpls",
				},
			},
		},
		RawOutput: "TINFO:0,2,0,\"The Matrix\"\nTINFO:1,2,0,\"Behind the Scenes\"",
	}
}

func TestGenerateReleaseJSON(t *testing.T) {
	ri := ReleaseInfo{
		UPC:        "883929543236",
		RegionCode: "A",
		Year:       1999,
		Format:     "Blu-ray",
		Slug:       "1999-blu-ray",
	}

	before := time.Now().UTC().Truncate(time.Second)
	content := GenerateReleaseJSON(ri, "testuser")
	after := time.Now().UTC().Add(time.Second).Truncate(time.Second)

	// Must be valid JSON.
	var got ReleaseJSON
	if err := json.Unmarshal([]byte(content), &got); err != nil {
		t.Fatalf("GenerateReleaseJSON produced invalid JSON: %v\ncontent:\n%s", err, content)
	}

	if got.UPC != ri.UPC {
		t.Errorf("UPC: want %q, got %q", ri.UPC, got.UPC)
	}
	if got.RegionCode != ri.RegionCode {
		t.Errorf("RegionCode: want %q, got %q", ri.RegionCode, got.RegionCode)
	}
	if got.Slug != ri.Slug {
		t.Errorf("Slug: want %q, got %q", ri.Slug, got.Slug)
	}
	if got.Year != ri.Year {
		t.Errorf("Year: want %d, got %d", ri.Year, got.Year)
	}
	if got.Locale != "en-us" {
		t.Errorf("Locale: want %q, got %q", "en-us", got.Locale)
	}
	if got.Title != ri.Format {
		t.Errorf("Title: want %q, got %q", ri.Format, got.Title)
	}
	wantSortTitle := "1999 Blu-ray"
	if got.SortTitle != wantSortTitle {
		t.Errorf("SortTitle: want %q, got %q", wantSortTitle, got.SortTitle)
	}
	// DateAdded must parse as RFC3339 and fall within the test window (RFC3339 is
	// second-precision, so we truncate both ends and add a 1s pad on the upper bound).
	dateAdded, err := time.Parse(time.RFC3339, got.DateAdded)
	if err != nil {
		t.Errorf("DateAdded %q is not valid RFC3339: %v", got.DateAdded, err)
	} else if dateAdded.Before(before) || dateAdded.After(after) {
		t.Errorf("DateAdded %q not within expected test window [%s, %s]", got.DateAdded, before.Format(time.RFC3339), after.Format(time.RFC3339))
	}
	// Contributors array must have exactly one entry with correct shape.
	if len(got.Contributors) != 1 {
		t.Fatalf("Contributors: want 1 entry, got %d", len(got.Contributors))
	}
	if got.Contributors[0].Name != "testuser" {
		t.Errorf("Contributors[0].Name: want %q, got %q", "testuser", got.Contributors[0].Name)
	}
	if got.Contributors[0].Source != "github" {
		t.Errorf("Contributors[0].Source: want %q, got %q", "github", got.Contributors[0].Source)
	}
}

func TestGenerateDiscJSON(t *testing.T) {
	scan := testScan()

	content := GenerateDiscJSON(scan, "Blu-ray")

	// Must be valid JSON.
	var got DiscJSON
	if err := json.Unmarshal([]byte(content), &got); err != nil {
		t.Fatalf("GenerateDiscJSON produced invalid JSON: %v\ncontent:\n%s", err, content)
	}

	// Verify top-level disc fields.
	if got.Index != 1 {
		t.Errorf("Index: want 1, got %d", got.Index)
	}
	if got.Slug != "blu-ray" {
		t.Errorf("Slug: want %q, got %q", "blu-ray", got.Slug)
	}
	if got.Name != "Blu-ray" {
		t.Errorf("Name: want %q, got %q", "Blu-ray", got.Name)
	}
	if got.Format != "Blu-ray" {
		t.Errorf("Format: want %q, got %q", "Blu-ray", got.Format)
	}

	if len(got.Titles) != 2 {
		t.Fatalf("expected 2 titles, got %d", len(got.Titles))
	}

	// Verify first title.
	t0 := got.Titles[0]
	if t0.Comment != "The Matrix" {
		t.Errorf("Titles[0].Comment: want %q, got %q", "The Matrix", t0.Comment)
	}
	if t0.Duration != "1:45:32" {
		t.Errorf("Titles[0].Duration: want %q, got %q", "1:45:32", t0.Duration)
	}
	if t0.DisplaySize != "32.2 GB" {
		t.Errorf("Titles[0].DisplaySize: want %q, got %q", "32.2 GB", t0.DisplaySize)
	}
	if t0.Size != 34567890123 {
		t.Errorf("Titles[0].Size: want %d, got %d", int64(34567890123), t0.Size)
	}
	if t0.SourceFile != "00800.mpls" {
		t.Errorf("Titles[0].SourceFile: want %q, got %q", "00800.mpls", t0.SourceFile)
	}
	if t0.SegmentMap != "00800.mpls" {
		t.Errorf("Titles[0].SegmentMap: want %q, got %q", "00800.mpls", t0.SegmentMap)
	}

	// Verify streams mapped to tracks.
	if len(t0.Tracks) != 2 {
		t.Fatalf("Titles[0].Tracks: expected 2, got %d", len(t0.Tracks))
	}
	videoTrack := t0.Tracks[0]
	if videoTrack.Type != "video" {
		t.Errorf("Tracks[0].Type: want %q, got %q", "video", videoTrack.Type)
	}
	if videoTrack.Name != "Mpeg4 AVC High@L4.1" {
		t.Errorf("Tracks[0].Name: want %q, got %q", "Mpeg4 AVC High@L4.1", videoTrack.Name)
	}
	if videoTrack.Resolution != "1920x1080" {
		t.Errorf("Tracks[0].Resolution: want %q, got %q", "1920x1080", videoTrack.Resolution)
	}
	if videoTrack.AspectRatio != "16:9" {
		t.Errorf("Tracks[0].AspectRatio: want %q, got %q", "16:9", videoTrack.AspectRatio)
	}

	audioTrack := t0.Tracks[1]
	if audioTrack.Type != "audio" {
		t.Errorf("Tracks[1].Type: want %q, got %q", "audio", audioTrack.Type)
	}
	if audioTrack.LanguageCode != "eng" {
		t.Errorf("Tracks[1].LanguageCode: want %q, got %q", "eng", audioTrack.LanguageCode)
	}
	if audioTrack.Language != "English" {
		t.Errorf("Tracks[1].Language: want %q, got %q", "English", audioTrack.Language)
	}
	if audioTrack.Name != "TrueHD Atmos" {
		t.Errorf("Tracks[1].Name: want %q, got %q", "TrueHD Atmos", audioTrack.Name)
	}

	// Second title has no streams.
	t1 := got.Titles[1]
	if t1.Comment != "Behind the Scenes" {
		t.Errorf("Titles[1].Comment: want %q, got %q", "Behind the Scenes", t1.Comment)
	}
}

func TestGenerateSummary(t *testing.T) {
	scan := testScan()
	labels := []TitleLabel{
		{
			TitleIndex: 0,
			Type:       "MainMovie",
			Name:       "The Matrix",
			FileName:   "The Matrix (1999).mkv",
		},
		{
			TitleIndex: 1,
			Type:       "Extra",
			Name:       "Behind the Scenes",
			FileName:   "Behind the Scenes.mkv",
		},
	}

	summary := GenerateSummary(scan, labels)

	// Should contain expected fields for title 0.
	if !strings.Contains(summary, "Name: The Matrix") {
		t.Errorf("summary missing 'Name: The Matrix'")
	}
	if !strings.Contains(summary, "Source file name: 00800.mpls") {
		t.Errorf("summary missing 'Source file name: 00800.mpls'")
	}
	if !strings.Contains(summary, "Duration: 1:45:32") {
		t.Errorf("summary missing 'Duration: 1:45:32'")
	}
	if !strings.Contains(summary, "Chapters count: 28") {
		t.Errorf("summary missing 'Chapters count: 28'")
	}
	if !strings.Contains(summary, "Size: 32.2 GB") {
		t.Errorf("summary missing 'Size: 32.2 GB'")
	}
	if !strings.Contains(summary, "Type: MainMovie") {
		t.Errorf("summary missing 'Type: MainMovie'")
	}
	if !strings.Contains(summary, "File name: The Matrix (1999).mkv") {
		t.Errorf("summary missing 'File name: The Matrix (1999).mkv'")
	}

	// Season/Episode should not appear when empty.
	if strings.Contains(summary, "Season:") {
		t.Errorf("summary should not contain 'Season:' when empty")
	}
	if strings.Contains(summary, "Episode:") {
		t.Errorf("summary should not contain 'Episode:' when empty")
	}

	// Should contain fields for title 1.
	if !strings.Contains(summary, "Name: Behind the Scenes") {
		t.Errorf("summary missing 'Name: Behind the Scenes'")
	}
	if !strings.Contains(summary, "Type: Extra") {
		t.Errorf("summary missing 'Type: Extra'")
	}

	// Blocks should be separated by blank line.
	blocks := strings.Split(strings.TrimSpace(summary), "\n\n")
	if len(blocks) < 2 {
		t.Errorf("expected at least 2 blocks separated by blank line, got %d", len(blocks))
	}
}

func TestGenerateSummaryWithSeasonEpisode(t *testing.T) {
	scan := testScan()
	// Use only the first title.
	scan.Titles = scan.Titles[:1]
	labels := []TitleLabel{
		{
			TitleIndex: 0,
			Type:       "Episode",
			Name:       "Pilot",
			Season:     "01",
			Episode:    "01",
			FileName:   "S01E01.mkv",
		},
	}

	summary := GenerateSummary(scan, labels)

	if !strings.Contains(summary, "Season: 01") {
		t.Errorf("summary missing 'Season: 01'")
	}
	if !strings.Contains(summary, "Episode: 01") {
		t.Errorf("summary missing 'Episode: 01'")
	}
}

func TestMediaDirPath(t *testing.T) {
	tests := []struct {
		mediaType string
		title     string
		year      int
		want      string
	}{
		{"Movie", "The Matrix", 1999, "movie/The Matrix (1999)"},
		{"movie", "Inception", 2010, "movie/Inception (2010)"},
		{"Series", "Breaking Bad", 2008, "series/Breaking Bad (2008)"},
		{"series", "The Wire", 2002, "series/The Wire (2002)"},
		{"Show", "Chernobyl", 2019, "show/Chernobyl (2019)"},
	}

	for _, tc := range tests {
		got := MediaDirPath(tc.mediaType, tc.title, tc.year)
		if got != tc.want {
			t.Errorf("MediaDirPath(%q, %q, %d): want %q, got %q",
				tc.mediaType, tc.title, tc.year, tc.want, got)
		}
	}
}

func TestReleaseSlug(t *testing.T) {
	tests := []struct {
		year   int
		format string
		want   string
	}{
		{2024, "Blu-ray", "2024-blu-ray"},
		{2024, "UHD", "2024-4k"},
		{2023, "DVD", "2023-dvd"},
		{2022, "blu-ray", "2022-blu-ray"},
		{2020, "uhd", "2020-4k"},
	}

	for _, tc := range tests {
		got := ReleaseSlug(tc.year, tc.format)
		if got != tc.want {
			t.Errorf("ReleaseSlug(%d, %q): want %q, got %q", tc.year, tc.format, tc.want, got)
		}
	}
}

func TestStreamToTrackUHDFallback(t *testing.T) {
	tests := []struct {
		codecID  string
		wantType string
	}{
		// Standard Matroska prefixes
		{"V_MPEG4/ISO/AVC", "video"},
		{"A_TRUEHD", "audio"},
		{"S_HDMV/PGS", "subtitle"},
		// UHD human-readable names
		{"Mpeg4 AVC High@L5.1", "video"},
		{"HEVC", "video"},
		{"DTS-HD MA", "audio"},
		{"TrueHD Atmos", "audio"},
		{"AC3", "audio"},
		{"PGS", "subtitle"},
		// Unknown
		{"", ""},
		{"SomeUnknownCodec", ""},
	}

	for _, tc := range tests {
		s := &makemkv.StreamInfo{
			Attributes: map[int]string{1: tc.codecID},
		}
		track := streamToTrack(0, s)
		if track.Type != tc.wantType {
			t.Errorf("streamToTrack(codecID=%q): want Type %q, got %q", tc.codecID, tc.wantType, track.Type)
		}
	}
}

func TestGenerateDiscJSON_EmptyTitles(t *testing.T) {
	scan := &makemkv.DiscScan{
		DiscName:   "EMPTY_DISC",
		TitleCount: 0,
		Titles:     nil,
	}
	content := GenerateDiscJSON(scan, "Blu-ray")

	var got DiscJSON
	if err := json.Unmarshal([]byte(content), &got); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if len(got.Titles) != 0 {
		t.Errorf("expected 0 titles, got %d", len(got.Titles))
	}
	// Top-level disc fields should still be populated
	if got.Format != "Blu-ray" {
		t.Errorf("Format: want %q, got %q", "Blu-ray", got.Format)
	}
}

func TestGenerateDiscJSON_TitleWithNoStreams(t *testing.T) {
	scan := &makemkv.DiscScan{
		DiscName:   "NO_STREAMS",
		TitleCount: 1,
		Titles: []makemkv.TitleInfo{
			{
				Index: 0,
				Attributes: map[int]string{
					2: "Feature", 9: "2:00:00", 10: "40 GB", 11: "40000000000", 16: "00001.mpls",
				},
				Streams: nil,
			},
		},
	}
	content := GenerateDiscJSON(scan, "UHD")

	var got DiscJSON
	if err := json.Unmarshal([]byte(content), &got); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if len(got.Titles) != 1 {
		t.Fatalf("expected 1 title, got %d", len(got.Titles))
	}
	if len(got.Titles[0].Tracks) != 0 {
		t.Errorf("expected 0 tracks, got %d", len(got.Titles[0].Tracks))
	}
	if got.Titles[0].Comment != "Feature" {
		t.Errorf("Comment: want %q, got %q", "Feature", got.Titles[0].Comment)
	}
}

func TestGenerateDiscJSON_MissingAttributes(t *testing.T) {
	// Title with empty attributes map — all fields should default gracefully
	scan := &makemkv.DiscScan{
		DiscName:   "SPARSE_DISC",
		TitleCount: 1,
		Titles: []makemkv.TitleInfo{
			{
				Index:      0,
				Attributes: map[int]string{}, // no attributes at all
				Streams: []makemkv.StreamInfo{
					{
						TitleIndex:  0,
						StreamIndex: 0,
						Attributes:  map[int]string{}, // no stream attributes
					},
				},
			},
		},
	}
	content := GenerateDiscJSON(scan, "DVD")

	var got DiscJSON
	if err := json.Unmarshal([]byte(content), &got); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if len(got.Titles) != 1 {
		t.Fatalf("expected 1 title, got %d", len(got.Titles))
	}
	// All string fields should be empty, not panicking
	t0 := got.Titles[0]
	if t0.Comment != "" {
		t.Errorf("Comment should be empty, got %q", t0.Comment)
	}
	if t0.Duration != "" {
		t.Errorf("Duration should be empty, got %q", t0.Duration)
	}
	if t0.Size != 0 {
		t.Errorf("Size should be 0, got %d", t0.Size)
	}
	// Stream track should have empty fields
	if len(t0.Tracks) != 1 {
		t.Fatalf("expected 1 track, got %d", len(t0.Tracks))
	}
	if t0.Tracks[0].Type != "" {
		t.Errorf("Track Type should be empty for unknown codec, got %q", t0.Tracks[0].Type)
	}
}

func TestGenerateDiscJSON_MalformedSizeBytes(t *testing.T) {
	scan := &makemkv.DiscScan{
		DiscName:   "BAD_SIZE",
		TitleCount: 1,
		Titles: []makemkv.TitleInfo{
			{
				Index: 0,
				Attributes: map[int]string{
					2: "Feature", 9: "1:00:00",
					10: "N/A",          // human-readable size
					11: "not-a-number", // malformed size bytes
					16: "00001.mpls",
				},
			},
		},
	}
	content := GenerateDiscJSON(scan, "Blu-ray")

	var got DiscJSON
	if err := json.Unmarshal([]byte(content), &got); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	// Size should be 0 when parsing fails (strconv.ParseInt returns 0 on error)
	if got.Titles[0].Size != 0 {
		t.Errorf("Size: want 0 for malformed input, got %d", got.Titles[0].Size)
	}
	// DisplaySize should contain the raw value from the scan
	if got.Titles[0].DisplaySize != "N/A" {
		t.Errorf("DisplaySize: want %q, got %q", "N/A", got.Titles[0].DisplaySize)
	}
}

func TestGenerateSummary_TitleWithNoLabel(t *testing.T) {
	scan := testScan() // 2 titles
	// Only provide label for title 0, none for title 1
	labels := []TitleLabel{
		{TitleIndex: 0, Type: "MainMovie", Name: "The Matrix", FileName: "The Matrix.mkv"},
	}
	summary := GenerateSummary(scan, labels)

	// Title 1 should still be included with empty label fields
	if !strings.Contains(summary, "Source file name: 00801.mpls") {
		t.Error("summary should contain title 1's source file")
	}
	// The Name and Type for title 1 should be empty (zero-value TitleLabel)
	if !strings.Contains(summary, "Name: \n") {
		t.Error("summary should contain empty Name for unlabeled title")
	}
	if !strings.Contains(summary, "Type: \n") {
		t.Error("summary should contain empty Type for unlabeled title")
	}
}

func TestReleaseSlug_EdgeCases(t *testing.T) {
	tests := []struct {
		year   int
		format string
		want   string
	}{
		{0, "Blu-ray", "0-blu-ray"},               // zero year
		{2024, "", "2024-"},                         // empty format
		{2024, "UHD Blu-ray", "2024-uhd-blu-ray"},  // space in format (not "UHD" exactly)
	}
	for _, tc := range tests {
		got := ReleaseSlug(tc.year, tc.format)
		if got != tc.want {
			t.Errorf("ReleaseSlug(%d, %q): want %q, got %q", tc.year, tc.format, tc.want, got)
		}
	}
}

func TestMediaDirPath_SpecialCharacters(t *testing.T) {
	// Titles can have colons, apostrophes, etc. — these should pass through unchanged
	// (TheDiscDB handles sanitization on their end)
	got := MediaDirPath("movie", "Spider-Man: No Way Home", 2021)
	want := "movie/Spider-Man: No Way Home (2021)"
	if got != want {
		t.Errorf("MediaDirPath: want %q, got %q", want, got)
	}
}

func TestGenerateReleaseJSON_EmptyOptionalFields(t *testing.T) {
	ri := ReleaseInfo{
		Year:       2024,
		Format:     "Blu-ray",
		RegionCode: "A",
		// UPC intentionally empty
		// Slug intentionally empty
	}
	content := GenerateReleaseJSON(ri, "user")

	var got ReleaseJSON
	if err := json.Unmarshal([]byte(content), &got); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	// UPC should be omitted due to omitempty
	if strings.Contains(content, `"Upc"`) {
		t.Error("expected Upc to be omitted when empty")
	}
	if got.Year != 2024 {
		t.Errorf("Year: want 2024, got %d", got.Year)
	}
}
