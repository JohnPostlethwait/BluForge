package contribute

import (
	"encoding/json"
	"strings"
	"testing"

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

	content := GenerateReleaseJSON(ri, "testuser")

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
	if got.Contributor.GitHub != "testuser" {
		t.Errorf("Contributor.GitHub: want %q, got %q", "testuser", got.Contributor.GitHub)
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

	if len(got.Titles) != 2 {
		t.Fatalf("expected 2 titles, got %d", len(got.Titles))
	}

	// Verify first title.
	t0 := got.Titles[0]
	if t0.Name != "The Matrix" {
		t.Errorf("Titles[0].Name: want %q, got %q", "The Matrix", t0.Name)
	}
	if t0.Duration != "1:45:32" {
		t.Errorf("Titles[0].Duration: want %q, got %q", "1:45:32", t0.Duration)
	}
	if t0.ChapterCount != "28" {
		t.Errorf("Titles[0].ChapterCount: want %q, got %q", "28", t0.ChapterCount)
	}
	if t0.SizeHuman != "32.2 GB" {
		t.Errorf("Titles[0].SizeHuman: want %q, got %q", "32.2 GB", t0.SizeHuman)
	}
	if t0.SizeBytes != "34567890123" {
		t.Errorf("Titles[0].SizeBytes: want %q, got %q", "34567890123", t0.SizeBytes)
	}
	if t0.SourceFile != "00800.mpls" {
		t.Errorf("Titles[0].SourceFile: want %q, got %q", "00800.mpls", t0.SourceFile)
	}

	// Verify streams mapped to tracks.
	if len(t0.Tracks) != 2 {
		t.Fatalf("Titles[0].Tracks: expected 2, got %d", len(t0.Tracks))
	}
	videoTrack := t0.Tracks[0]
	if videoTrack.Type != "video" {
		t.Errorf("Tracks[0].Type: want %q, got %q", "video", videoTrack.Type)
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
	if audioTrack.LangCode != "eng" {
		t.Errorf("Tracks[1].LangCode: want %q, got %q", "eng", audioTrack.LangCode)
	}
	if audioTrack.LangName != "English" {
		t.Errorf("Tracks[1].LangName: want %q, got %q", "English", audioTrack.LangName)
	}
	if audioTrack.CodecShort != "TrueHD Atmos" {
		t.Errorf("Tracks[1].CodecShort: want %q, got %q", "TrueHD Atmos", audioTrack.CodecShort)
	}

	// Second title has no streams.
	t1 := got.Titles[1]
	if t1.Name != "Behind the Scenes" {
		t.Errorf("Titles[1].Name: want %q, got %q", "Behind the Scenes", t1.Name)
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
