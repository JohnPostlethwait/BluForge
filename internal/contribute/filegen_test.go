package contribute

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/johnpostlethwait/bluforge/internal/makemkv"
	"github.com/johnpostlethwait/bluforge/internal/tmdb"
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
	// ImageUrl should be omitted (TheDiscDB fills it in during import).
	if got.ImageUrl != "" {
		t.Errorf("ImageUrl: want empty (omitted), got %q", got.ImageUrl)
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

func TestGenerateReleaseJSON_WithASINAndReleaseDate(t *testing.T) {
	ri := ReleaseInfo{
		UPC:         "883929543236",
		RegionCode:  "A",
		Year:        1999,
		Format:      "Blu-ray",
		Slug:        "1999-blu-ray",
		ASIN:        "B0CCZQNJ3R",
		ReleaseDate: "1999-09-21",
	}

	content := GenerateReleaseJSON(ri, "testuser")

	var got ReleaseJSON
	if err := json.Unmarshal([]byte(content), &got); err != nil {
		t.Fatalf("invalid JSON: %v\ncontent:\n%s", err, content)
	}

	if got.Asin != "B0CCZQNJ3R" {
		t.Errorf("Asin: want %q, got %q", "B0CCZQNJ3R", got.Asin)
	}
	if got.ReleaseDate != "1999-09-21T00:00:00Z" {
		t.Errorf("ReleaseDate: want %q, got %q", "1999-09-21T00:00:00Z", got.ReleaseDate)
	}
}

func TestGenerateReleaseJSON_OmitsASINAndReleaseDateWhenEmpty(t *testing.T) {
	ri := ReleaseInfo{
		Year:   2024,
		Format: "Blu-ray",
		// ASIN and ReleaseDate intentionally empty
	}

	content := GenerateReleaseJSON(ri, "user")

	if strings.Contains(content, `"Asin"`) {
		t.Error("expected Asin to be omitted when empty")
	}
	if strings.Contains(content, `"ReleaseDate"`) {
		t.Error("expected ReleaseDate to be omitted when empty")
	}
}

func TestGenerateReleaseJSON_MalformedReleaseDateOmitted(t *testing.T) {
	ri := ReleaseInfo{
		Year:        2024,
		Format:      "Blu-ray",
		ReleaseDate: "not-a-date",
	}

	content := GenerateReleaseJSON(ri, "user")

	if strings.Contains(content, `"ReleaseDate"`) {
		t.Error("expected ReleaseDate to be omitted when unparseable")
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
	if videoTrack.Type != "Video" {
		t.Errorf("Tracks[0].Type: want %q, got %q", "Video", videoTrack.Type)
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
	if audioTrack.Type != "Audio" {
		t.Errorf("Tracks[1].Type: want %q, got %q", "Audio", audioTrack.Type)
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
		{"Movie", "The Matrix", 1999, "data/movie/The Matrix (1999)"},
		{"movie", "Inception", 2010, "data/movie/Inception (2010)"},
		{"Series", "Breaking Bad", 2008, "data/series/Breaking Bad (2008)"},
		{"series", "The Wire", 2002, "data/series/The Wire (2002)"},
		{"Show", "Chernobyl", 2019, "data/show/Chernobyl (2019)"},
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
		{"V_MPEG4/ISO/AVC", "Video"},
		{"A_TRUEHD", "Audio"},
		{"S_HDMV/PGS", "Subtitles"},
		// UHD human-readable names
		{"Mpeg4 AVC High@L5.1", "Video"},
		{"HEVC", "Video"},
		{"DTS-HD MA", "Audio"},
		{"TrueHD Atmos", "Audio"},
		{"AC3", "Audio"},
		{"PGS", "Subtitles"},
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
	scan := testScan() // 2 titles: index 0 (00800.mpls) and index 1 (00801.mpls)
	// Only provide label for title 0; title 1 has no label (empty type = omit).
	labels := []TitleLabel{
		{TitleIndex: 0, Type: "MainMovie", Name: "The Matrix", FileName: "The Matrix.mkv"},
	}
	summary := GenerateSummary(scan, labels)

	// Title 0 should be present.
	if !strings.Contains(summary, "Name: The Matrix") {
		t.Error("summary should contain title 0's name")
	}
	// Title 1 should be omitted (no type assigned).
	if strings.Contains(summary, "Source file name: 00801.mpls") {
		t.Error("summary should not contain title 1 (no type = omitted)")
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
	want := "data/movie/Spider-Man: No Way Home (2021)"
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
	if strings.Contains(content, `"ImageUrl"`) {
		t.Error("expected ImageUrl to be omitted when empty")
	}
	if got.Year != 2024 {
		t.Errorf("Year: want 2024, got %d", got.Year)
	}
}

func TestGenerateMetadataJSON(t *testing.T) {
	details := &tmdb.MediaDetails{
		ID:             603,
		Title:          "The Matrix",
		Overview:       "A computer hacker learns the truth about reality.",
		Tagline:        "Welcome to the Real World.",
		RuntimeMinutes: 136,
		ReleaseDate:    "1999-03-31",
		PosterPath:     "/f89U3ADr1oiB1s9GkdPOEpXUk5H.jpg",
		ImdbID:         "tt0133093",
	}

	before := time.Now().UTC().Truncate(time.Second)
	content := GenerateMetadataJSON(details, "movie", "The Matrix", 1999)
	after := time.Now().UTC().Add(time.Second).Truncate(time.Second)

	var got MetadataJSON
	if err := json.Unmarshal([]byte(content), &got); err != nil {
		t.Fatalf("GenerateMetadataJSON produced invalid JSON: %v\ncontent:\n%s", err, content)
	}

	if got.Title != "The Matrix" {
		t.Errorf("Title: want %q, got %q", "The Matrix", got.Title)
	}
	if got.FullTitle != "The Matrix" {
		t.Errorf("FullTitle: want %q, got %q", "The Matrix", got.FullTitle)
	}
	if got.SortTitle != "The Matrix" {
		t.Errorf("SortTitle: want %q, got %q", "The Matrix", got.SortTitle)
	}
	if got.Slug != "the-matrix-1999" {
		t.Errorf("Slug: want %q, got %q", "the-matrix-1999", got.Slug)
	}
	if got.Type != "Movie" {
		t.Errorf("Type: want %q, got %q", "Movie", got.Type)
	}
	if got.Year != 1999 {
		t.Errorf("Year: want 1999, got %d", got.Year)
	}
	if got.ImageUrl != "Movie/the-matrix-1999/cover.jpg" {
		t.Errorf("ImageUrl: want %q, got %q", "Movie/the-matrix-1999/cover.jpg", got.ImageUrl)
	}
	if got.ExternalIds.Tmdb != "603" {
		t.Errorf("ExternalIds.Tmdb: want %q, got %q", "603", got.ExternalIds.Tmdb)
	}
	if got.ExternalIds.Imdb != "tt0133093" {
		t.Errorf("ExternalIds.Imdb: want %q, got %q", "tt0133093", got.ExternalIds.Imdb)
	}
	if got.Plot != "A computer hacker learns the truth about reality." {
		t.Errorf("Plot mismatch")
	}
	if got.Tagline != "Welcome to the Real World." {
		t.Errorf("Tagline mismatch")
	}
	if got.RuntimeMinutes != 136 {
		t.Errorf("RuntimeMinutes: want 136, got %d", got.RuntimeMinutes)
	}
	// ReleaseDate should be RFC3339 format of 1999-03-31.
	if got.ReleaseDate != "1999-03-31T00:00:00Z" {
		t.Errorf("ReleaseDate: want %q, got %q", "1999-03-31T00:00:00Z", got.ReleaseDate)
	}
	// DateAdded must be valid RFC3339 within the test window.
	dateAdded, err := time.Parse(time.RFC3339, got.DateAdded)
	if err != nil {
		t.Errorf("DateAdded %q is not valid RFC3339: %v", got.DateAdded, err)
	} else if dateAdded.Before(before) || dateAdded.After(after) {
		t.Errorf("DateAdded %q not within test window", got.DateAdded)
	}
	// Groups must be present as an empty array, not null.
	if got.Groups == nil {
		t.Error("Groups should be an empty array, not nil")
	}
}

func TestGenerateMetadataJSON_Series(t *testing.T) {
	details := &tmdb.MediaDetails{
		ID:          1396,
		Title:       "Breaking Bad",
		Overview:    "A chemistry teacher turned drug lord.",
		ReleaseDate: "2008-01-20",
		ImdbID:      "tt0903747",
	}
	content := GenerateMetadataJSON(details, "series", "Breaking Bad", 2008)

	var got MetadataJSON
	if err := json.Unmarshal([]byte(content), &got); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if got.Type != "Series" {
		t.Errorf("Type: want %q, got %q", "Series", got.Type)
	}
	if got.ImageUrl != "Series/breaking-bad-2008/cover.jpg" {
		t.Errorf("ImageUrl: want %q, got %q", "Series/breaking-bad-2008/cover.jpg", got.ImageUrl)
	}
}

func TestGenerateMetadataJSON_EmptyReleaseDate(t *testing.T) {
	details := &tmdb.MediaDetails{
		ID:          9999,
		Title:       "Unknown Film",
		ReleaseDate: "",
	}
	content := GenerateMetadataJSON(details, "movie", "Unknown Film", 2020)

	var got MetadataJSON
	if err := json.Unmarshal([]byte(content), &got); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	// Empty release date should produce empty string, not a zero-value date.
	if got.ReleaseDate != "" {
		t.Errorf("ReleaseDate: want empty, got %q", got.ReleaseDate)
	}
}

func TestTitleImageURL(t *testing.T) {
	got := TitleImageURL("movie", "the-matrix-1999")
	want := "Movie/the-matrix-1999/cover.jpg"
	if got != want {
		t.Errorf("TitleImageURL: want %q, got %q", want, got)
	}
}

// ---------------------------------------------------------------------------
// TheDiscDB Conformance Tests
//
// These tests validate that our generated JSON output matches the real standards
// established in the TheDiscDB data repository (https://github.com/TheDiscDb/data).
// Mock data is modeled after actual entries in that repository.
// ---------------------------------------------------------------------------

// TestConformance_ReleaseJSON validates release.json output against TheDiscDB standards.
func TestConformance_ReleaseJSON(t *testing.T) {
	ri := ReleaseInfo{
		UPC:         "883929543236",
		RegionCode:  "A",
		Year:        1999,
		Format:      "Blu-ray",
		Slug:        "1999-blu-ray",
		ASIN:        "B0CCZQNJ3R",
		ReleaseDate: "1999-09-21",
	}

	content := GenerateReleaseJSON(ri, "testuser")

	// Parse as generic map to validate raw JSON field names.
	var raw map[string]any
	if err := json.Unmarshal([]byte(content), &raw); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	// All top-level keys must be PascalCase per TheDiscDB convention.
	wantKeys := []string{"Slug", "Asin", "Upc", "Year", "Locale", "RegionCode", "Title", "SortTitle", "ReleaseDate", "DateAdded", "Contributors"}
	for _, k := range wantKeys {
		if _, ok := raw[k]; !ok {
			t.Errorf("missing PascalCase key %q in release.json", k)
		}
	}

	// ImageUrl must NOT be present — TheDiscDB fills it in during import.
	if _, ok := raw["ImageUrl"]; ok {
		t.Error("ImageUrl must be omitted from release.json (TheDiscDB fills it in during import)")
	}

	// Locale must be "en-us".
	if raw["Locale"] != "en-us" {
		t.Errorf("Locale: want %q, got %v", "en-us", raw["Locale"])
	}

	// ReleaseDate must be RFC3339.
	if rd, ok := raw["ReleaseDate"].(string); ok {
		if _, err := time.Parse(time.RFC3339, rd); err != nil {
			t.Errorf("ReleaseDate %q is not valid RFC3339: %v", rd, err)
		}
	}

	// DateAdded must be RFC3339.
	if da, ok := raw["DateAdded"].(string); ok {
		if _, err := time.Parse(time.RFC3339, da); err != nil {
			t.Errorf("DateAdded %q is not valid RFC3339: %v", da, err)
		}
	}

	// Contributors must be an array with {Name, Source} entries.
	contribs, ok := raw["Contributors"].([]any)
	if !ok || len(contribs) == 0 {
		t.Fatal("Contributors must be a non-empty array")
	}
	c0, ok := contribs[0].(map[string]any)
	if !ok {
		t.Fatal("Contributors[0] must be an object")
	}
	if c0["Source"] != "github" {
		t.Errorf("Contributors[0].Source: want %q, got %v", "github", c0["Source"])
	}

	// Empty optional fields must be omitted via omitempty.
	riNoOptional := ReleaseInfo{
		Year:       2024,
		Format:     "Blu-ray",
		RegionCode: "A",
	}
	contentNoOpt := GenerateReleaseJSON(riNoOptional, "user")
	if strings.Contains(contentNoOpt, `"Asin"`) {
		t.Error("empty Asin must be omitted via omitempty")
	}
	if strings.Contains(contentNoOpt, `"Upc"`) {
		t.Error("empty Upc must be omitted via omitempty")
	}
	if strings.Contains(contentNoOpt, `"ReleaseDate"`) {
		t.Error("empty ReleaseDate must be omitted via omitempty")
	}
	if strings.Contains(contentNoOpt, `"ImageUrl"`) {
		t.Error("empty ImageUrl must be omitted via omitempty")
	}
}

// TestConformance_DiscJSON validates disc01.json output against TheDiscDB standards.
func TestConformance_DiscJSON(t *testing.T) {
	// Build a realistic scan modeled after real TheDiscDB disc entries.
	scan := &makemkv.DiscScan{
		DiscName:   "THE_MATRIX",
		TitleCount: 2,
		Titles: []makemkv.TitleInfo{
			{
				Index: 0,
				Attributes: map[int]string{
					2: "The Matrix", 8: "28", 9: "2:16:18",
					10: "32.2 GB", 11: "34567890123", 16: "00800.mpls",
				},
				Streams: []makemkv.StreamInfo{
					{TitleIndex: 0, StreamIndex: 0, Attributes: map[int]string{
						1: "V_MPEG4/ISO/AVC", 6: "Mpeg4 AVC High@L4.1",
						19: "1920x1080", 20: "16:9",
					}},
					{TitleIndex: 0, StreamIndex: 1, Attributes: map[int]string{
						1: "A_DTS", 6: "DTS-HD MA",
						3: "eng", 4: "English",
					}},
					{TitleIndex: 0, StreamIndex: 2, Attributes: map[int]string{
						1: "S_HDMV/PGS", 6: "PGS",
						3: "eng", 4: "English",
					}},
				},
			},
			{
				Index: 1,
				Attributes: map[int]string{
					2: "Behind the Scenes", 9: "0:23:15",
					10: "4.1 GB", 11: "4400000000", 16: "00801.mpls",
				},
			},
		},
	}

	content := GenerateDiscJSON(scan, "Blu-ray")

	// Parse as generic map to validate raw JSON structure.
	var raw map[string]any
	if err := json.Unmarshal([]byte(content), &raw); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	// Disc-level PascalCase fields must be present.
	for _, k := range []string{"Index", "Slug", "Name", "Format", "ContentHash", "Titles"} {
		if _, ok := raw[k]; !ok {
			t.Errorf("missing disc-level key %q", k)
		}
	}

	// ContentHash must be present (empty string acceptable for new contributions).
	if _, ok := raw["ContentHash"]; !ok {
		t.Error("ContentHash must be present in disc JSON")
	}

	// Titles must be an array (never null).
	titles, ok := raw["Titles"].([]any)
	if !ok {
		t.Fatal("Titles must be a JSON array, not null")
	}
	if len(titles) != 2 {
		t.Fatalf("expected 2 titles, got %d", len(titles))
	}

	// Validate track types use PascalCase per TheDiscDB convention.
	var got DiscJSON
	if err := json.Unmarshal([]byte(content), &got); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	validTrackTypes := map[string]bool{"Video": true, "Audio": true, "Subtitles": true, "": true}
	for i, title := range got.Titles {
		for j, track := range title.Tracks {
			if !validTrackTypes[track.Type] {
				t.Errorf("Titles[%d].Tracks[%d].Type: %q is not a valid TheDiscDB track type (want Video/Audio/Subtitles)", i, j, track.Type)
			}
		}
	}

	// Verify specific track types from our test data.
	if len(got.Titles[0].Tracks) != 3 {
		t.Fatalf("expected 3 tracks in title 0, got %d", len(got.Titles[0].Tracks))
	}
	if got.Titles[0].Tracks[0].Type != "Video" {
		t.Errorf("video track: want %q, got %q", "Video", got.Titles[0].Tracks[0].Type)
	}
	if got.Titles[0].Tracks[1].Type != "Audio" {
		t.Errorf("audio track: want %q, got %q", "Audio", got.Titles[0].Tracks[1].Type)
	}
	if got.Titles[0].Tracks[2].Type != "Subtitles" {
		t.Errorf("subtitle track: want %q, got %q", "Subtitles", got.Titles[0].Tracks[2].Type)
	}

	// Video tracks must have Resolution and AspectRatio.
	vt := got.Titles[0].Tracks[0]
	if vt.Resolution == "" {
		t.Error("video track must have Resolution")
	}
	if vt.AspectRatio == "" {
		t.Error("video track must have AspectRatio")
	}

	// Audio tracks must have LanguageCode and Language.
	at := got.Titles[0].Tracks[1]
	if at.LanguageCode == "" {
		t.Error("audio track must have LanguageCode")
	}
	if at.Language == "" {
		t.Error("audio track must have Language")
	}

	// Subtitle tracks must have LanguageCode and Language.
	st := got.Titles[0].Tracks[2]
	if st.LanguageCode == "" {
		t.Error("subtitle track must have LanguageCode")
	}
	if st.Language == "" {
		t.Error("subtitle track must have Language")
	}

	// Titles array for empty disc must be [] not null.
	emptyScan := &makemkv.DiscScan{DiscName: "EMPTY", TitleCount: 0, Titles: nil}
	emptyContent := GenerateDiscJSON(emptyScan, "Blu-ray")
	if strings.Contains(emptyContent, `"Titles": null`) {
		t.Error("Titles must serialize as [] not null for empty discs")
	}
	if !strings.Contains(emptyContent, `"Titles": []`) {
		t.Error("Titles must be an empty JSON array for empty discs")
	}
}

// TestConformance_MetadataJSON validates metadata.json output against TheDiscDB standards.
func TestConformance_MetadataJSON(t *testing.T) {
	details := &tmdb.MediaDetails{
		ID:             603,
		Title:          "The Matrix",
		Overview:       "A computer hacker learns the truth about reality.",
		Tagline:        "Welcome to the Real World.",
		RuntimeMinutes: 136,
		ReleaseDate:    "1999-03-31",
		PosterPath:     "/f89U3ADr1oiB1s9GkdPOEpXUk5H.jpg",
		ImdbID:         "tt0133093",
	}

	content := GenerateMetadataJSON(details, "movie", "The Matrix", 1999)

	// Parse as generic map for raw key validation.
	var raw map[string]any
	if err := json.Unmarshal([]byte(content), &raw); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	// All top-level keys must be PascalCase.
	wantKeys := []string{"Title", "FullTitle", "SortTitle", "Slug", "Type", "Year", "ImageUrl", "ExternalIds", "Groups", "Plot", "RuntimeMinutes", "ReleaseDate", "DateAdded"}
	for _, k := range wantKeys {
		if _, ok := raw[k]; !ok {
			t.Errorf("missing PascalCase key %q in metadata.json", k)
		}
	}

	var got MetadataJSON
	if err := json.Unmarshal([]byte(content), &got); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	// Type must be PascalCase: "Movie" or "Series".
	if got.Type != "Movie" {
		t.Errorf("Type: want %q, got %q", "Movie", got.Type)
	}

	// ImageUrl must follow pattern: "{Type}/{slug}/cover.jpg".
	wantImageUrl := "Movie/the-matrix-1999/cover.jpg"
	if got.ImageUrl != wantImageUrl {
		t.Errorf("ImageUrl: want %q, got %q", wantImageUrl, got.ImageUrl)
	}

	// Slug must be kebab-case with year suffix.
	if got.Slug != "the-matrix-1999" {
		t.Errorf("Slug: want %q, got %q", "the-matrix-1999", got.Slug)
	}

	// ExternalIds must have string Tmdb and Imdb fields.
	if got.ExternalIds.Tmdb != "603" {
		t.Errorf("ExternalIds.Tmdb: want %q, got %q", "603", got.ExternalIds.Tmdb)
	}
	if got.ExternalIds.Imdb != "tt0133093" {
		t.Errorf("ExternalIds.Imdb: want %q, got %q", "tt0133093", got.ExternalIds.Imdb)
	}

	// Groups must be an empty array [], never null.
	groups, ok := raw["Groups"].([]any)
	if !ok {
		t.Error("Groups must be a JSON array, not null")
	} else if len(groups) != 0 {
		t.Errorf("Groups: want empty array, got %d elements", len(groups))
	}

	// ReleaseDate must be RFC3339.
	if _, err := time.Parse(time.RFC3339, got.ReleaseDate); err != nil {
		t.Errorf("ReleaseDate %q is not valid RFC3339: %v", got.ReleaseDate, err)
	}

	// DateAdded must be RFC3339.
	if _, err := time.Parse(time.RFC3339, got.DateAdded); err != nil {
		t.Errorf("DateAdded %q is not valid RFC3339: %v", got.DateAdded, err)
	}

	// Verify Series type produces PascalCase "Series".
	seriesContent := GenerateMetadataJSON(details, "series", "Breaking Bad", 2008)
	var seriesGot MetadataJSON
	if err := json.Unmarshal([]byte(seriesContent), &seriesGot); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if seriesGot.Type != "Series" {
		t.Errorf("Series Type: want %q, got %q", "Series", seriesGot.Type)
	}
	if seriesGot.ImageUrl != "Series/breaking-bad-2008/cover.jpg" {
		t.Errorf("Series ImageUrl: want %q, got %q", "Series/breaking-bad-2008/cover.jpg", seriesGot.ImageUrl)
	}
}

// TestConformance_JSONFieldNames validates that all generated JSON uses PascalCase keys,
// matching TheDiscDB convention. This catches future additions that accidentally use
// camelCase or snake_case.
func TestConformance_JSONFieldNames(t *testing.T) {
	isPascalCase := func(s string) bool {
		if len(s) == 0 {
			return false
		}
		// First character must be uppercase.
		return s[0] >= 'A' && s[0] <= 'Z'
	}

	t.Run("release.json", func(t *testing.T) {
		ri := ReleaseInfo{
			UPC: "123", RegionCode: "A", Year: 2024, Format: "Blu-ray",
			Slug: "2024-blu-ray", ASIN: "B123", ReleaseDate: "2024-01-01",
		}
		content := GenerateReleaseJSON(ri, "user")
		var raw map[string]any
		if err := json.Unmarshal([]byte(content), &raw); err != nil {
			t.Fatalf("invalid JSON: %v", err)
		}
		for key := range raw {
			if !isPascalCase(key) {
				t.Errorf("release.json key %q is not PascalCase", key)
			}
		}
	})

	t.Run("disc.json", func(t *testing.T) {
		scan := testScan()
		content := GenerateDiscJSON(scan, "Blu-ray")
		var raw map[string]any
		if err := json.Unmarshal([]byte(content), &raw); err != nil {
			t.Fatalf("invalid JSON: %v", err)
		}
		for key := range raw {
			if !isPascalCase(key) {
				t.Errorf("disc.json top-level key %q is not PascalCase", key)
			}
		}
		// Also check nested title and track keys.
		if titles, ok := raw["Titles"].([]any); ok && len(titles) > 0 {
			title := titles[0].(map[string]any)
			for key := range title {
				if !isPascalCase(key) {
					t.Errorf("disc.json title key %q is not PascalCase", key)
				}
			}
			if tracks, ok := title["Tracks"].([]any); ok && len(tracks) > 0 {
				track := tracks[0].(map[string]any)
				for key := range track {
					if !isPascalCase(key) {
						t.Errorf("disc.json track key %q is not PascalCase", key)
					}
				}
			}
		}
	})

	t.Run("metadata.json", func(t *testing.T) {
		details := &tmdb.MediaDetails{
			ID: 603, Title: "The Matrix", ReleaseDate: "1999-03-31", ImdbID: "tt0133093",
		}
		content := GenerateMetadataJSON(details, "movie", "The Matrix", 1999)
		var raw map[string]any
		if err := json.Unmarshal([]byte(content), &raw); err != nil {
			t.Fatalf("invalid JSON: %v", err)
		}
		for key := range raw {
			if !isPascalCase(key) {
				t.Errorf("metadata.json key %q is not PascalCase", key)
			}
		}
		// Check nested ExternalIds keys.
		if eids, ok := raw["ExternalIds"].(map[string]any); ok {
			for key := range eids {
				if !isPascalCase(key) {
					t.Errorf("metadata.json ExternalIds key %q is not PascalCase", key)
				}
			}
		}
	})
}

