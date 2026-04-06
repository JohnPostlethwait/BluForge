package contribute

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/johnpostlethwait/bluforge/internal/makemkv"
)

// GenerateReleaseJSON produces the TheDiscDB release.json content for a contribution.
func GenerateReleaseJSON(ri ReleaseInfo, githubUser string) string {
	r := ReleaseJSON{
		Slug:       ri.Slug,
		UPC:        ri.UPC,
		Year:       ri.Year,
		Locale:     "en-us",
		RegionCode: ri.RegionCode,
		Title:      ri.Format,
		SortTitle:  fmt.Sprintf("%d %s", ri.Year, ri.Format),
		DateAdded:  time.Now().UTC().Format(time.RFC3339),
		Contributors: []ContributorJSON{
			{Name: githubUser, Source: "github"},
		},
	}
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		// json.MarshalIndent only fails for unmarshalable types (e.g. channels,
		// funcs). Our struct is plain strings — this should never happen.
		panic(fmt.Sprintf("contribute: marshal release.json: %v", err))
	}
	return string(data) + "\n"
}

// GenerateDiscJSON produces the TheDiscDB disc01.json content from a disc scan.
//
// Stream type detection uses the codec ID prefix convention (attr 1):
//   - V_ prefix → "video"
//   - A_ prefix → "audio"
//   - S_ prefix → "subtitle"
//
// Stream attributes used:
//
//	1  = CodecID (type prefix)
//	3  = LanguageCode
//	4  = Language
//	6  = Name (CodecShort)
//	19 = Resolution
//	20 = AspectRatio
//
// Title attributes used:
//
//	9  = Duration
//	10 = SizeHuman (→ DisplaySize)
//	11 = SizeBytes (→ Size)
//	16 = SourceFile / SegmentMap
func GenerateDiscJSON(scan *makemkv.DiscScan, format string) string {
	discSlug := strings.ToLower(strings.ReplaceAll(format, " ", "-"))
	disc := DiscJSON{
		Index:       1,
		Slug:        discSlug,
		Name:        format,
		Format:      format,
		ContentHash: "",
		Titles:      make([]DiscTitleJSON, 0, len(scan.Titles)),
	}

	for i := range scan.Titles {
		t := &scan.Titles[i]

		var sizeBytes int64
		if s := t.SizeBytes(); s != "" {
			sizeBytes, _ = strconv.ParseInt(s, 10, 64)
		}

		dt := DiscTitleJSON{
			Index:       t.Index,
			Comment:     t.Name(),
			SourceFile:  t.SourceFile(),
			SegmentMap:  t.SegmentMap(),
			Duration:    t.Duration(),
			Size:        sizeBytes,
			DisplaySize: t.SizeHuman(),
			Tracks:      make([]TrackJSON, 0, len(t.Streams)),
		}

		for j := range t.Streams {
			s := &t.Streams[j]
			track := streamToTrack(j, s)
			dt.Tracks = append(dt.Tracks, track)
		}

		disc.Titles = append(disc.Titles, dt)
	}

	data, err := json.MarshalIndent(disc, "", "  ")
	if err != nil {
		panic(fmt.Sprintf("contribute: marshal disc.json: %v", err))
	}
	return string(data) + "\n"
}

// streamToTrack converts a makemkv StreamInfo into a TrackJSON.
func streamToTrack(idx int, s *makemkv.StreamInfo) TrackJSON {
	codecID := s.Attributes[1]
	trackType := ""
	switch {
	case strings.HasPrefix(codecID, "V_"):
		trackType = "video"
	case strings.HasPrefix(codecID, "A_"):
		trackType = "audio"
	case strings.HasPrefix(codecID, "S_"):
		trackType = "subtitle"
	}

	// Fallback for UHD discs where MakeMKV uses human-readable codec names
	// instead of Matroska-style prefixes (e.g. "DTS-HD MA" instead of "A_DTS").
	if trackType == "" && codecID != "" {
		lower := strings.ToLower(codecID)
		switch {
		case strings.Contains(lower, "mpeg") ||
			strings.Contains(lower, "hevc") ||
			strings.Contains(lower, "avc") ||
			strings.Contains(lower, "vc-1") ||
			strings.Contains(lower, "h.26"):
			trackType = "video"
		case strings.Contains(lower, "dts") ||
			strings.Contains(lower, "truehd") ||
			strings.Contains(lower, "atmos") ||
			strings.Contains(lower, "ac3") ||
			strings.Contains(lower, "ac-3") ||
			strings.Contains(lower, "lpcm") ||
			strings.Contains(lower, "pcm") ||
			strings.Contains(lower, "aac") ||
			strings.Contains(lower, "flac"):
			trackType = "audio"
		case strings.Contains(lower, "pgs") ||
			strings.Contains(lower, "srt") ||
			strings.Contains(lower, "ssa") ||
			strings.Contains(lower, "sub") ||
			strings.Contains(lower, "hdmv"):
			trackType = "subtitle"
		}
	}

	return TrackJSON{
		Index:        idx,
		Name:         s.Attributes[6],
		Type:         trackType,
		Resolution:   s.Attributes[19],
		AspectRatio:  s.Attributes[20],
		LanguageCode: s.Attributes[3],
		Language:     s.Attributes[4],
	}
}

// GenerateSummary produces a human-readable disc01-summary.txt from labeled titles.
// Each title block is separated by a blank line.
func GenerateSummary(scan *makemkv.DiscScan, labels []TitleLabel) string {
	// Build an index from title index → label for O(1) lookup.
	labelByIndex := make(map[int]TitleLabel, len(labels))
	for _, l := range labels {
		labelByIndex[l.TitleIndex] = l
	}

	var blocks []string
	for i := range scan.Titles {
		t := &scan.Titles[i]
		label := labelByIndex[t.Index]
		if label.Type == "" {
			continue
		}

		var sb strings.Builder
		sb.WriteString("Name: " + label.Name + "\n")
		sb.WriteString("Source file name: " + t.SourceFile() + "\n")
		sb.WriteString("Duration: " + t.Duration() + "\n")
		sb.WriteString("Chapters count: " + t.ChapterCount() + "\n")
		sb.WriteString("Size: " + t.SizeHuman() + "\n")
		sb.WriteString("Segment count: 1\n")
		sb.WriteString("Segment Map: " + t.SegmentMap() + "\n")
		if label.Season != "" {
			sb.WriteString("Season: " + label.Season + "\n")
		}
		if label.Episode != "" {
			sb.WriteString("Episode: " + label.Episode + "\n")
		}
		sb.WriteString("Type: " + label.Type + "\n")
		sb.WriteString("File name: " + label.FileName)

		blocks = append(blocks, sb.String())
	}

	return strings.Join(blocks, "\n\n") + "\n"
}

// MediaDirPath returns the TheDiscDB media directory path for a title.
// "Movie"/"movie" maps to "data/movie/Title (Year)"; "Series"/"series" maps to
// "data/series/Title (Year)". All other types are lowercased as-is.
func MediaDirPath(mediaType, title string, year int) string {
	dirType := strings.ToLower(mediaType)
	return fmt.Sprintf("data/%s/%s (%d)", dirType, title, year)
}

// ReleaseSlug returns the release slug for a given year and format.
// "UHD" (case-insensitive) maps to "4k"; all other formats are lowercased
// with spaces replaced by hyphens.
func ReleaseSlug(year int, format string) string {
	var suffix string
	if strings.EqualFold(format, "UHD") {
		suffix = "4k"
	} else {
		suffix = strings.ToLower(strings.ReplaceAll(format, " ", "-"))
	}
	return fmt.Sprintf("%d-%s", year, suffix)
}
