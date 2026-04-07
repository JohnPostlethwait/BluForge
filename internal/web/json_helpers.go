package web

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/johnpostlethwait/bluforge/internal/discdb"
	"github.com/johnpostlethwait/bluforge/internal/makemkv"
)

// parseSizeBytes parses a decimal integer from s (e.g. "4294967296") and returns
// it as an int64. Returns 0 if s is empty or not parseable.
func parseSizeBytes(s string) int64 {
	var n int64
	fmt.Sscanf(s, "%d", &n)
	return n
}

// enrichTitlesWithMatches builds TitleJSON entries for all titles in scan,
// enriched with match data from disc. Matched titles have Selected=true;
// unmatched titles have Selected=false.
func enrichTitlesWithMatches(scan *makemkv.DiscScan, disc discdb.Disc) []TitleJSON {
	matches := discdb.MatchTitles(scan, disc)

	// Build lookup by TitleIndex for fast access. Track whether any title
	// has assigned DiscDB content in the same pass.
	matchByIndex := make(map[int]discdb.ContentMatch, len(matches))
	hasAnyIdentified := false
	for _, m := range matches {
		matchByIndex[m.TitleIndex] = m
		if m.Matched {
			hasAnyIdentified = true
		}
	}

	titles := make([]TitleJSON, 0, len(scan.Titles))
	for _, t := range scan.Titles {
		var streams []StreamJSON
		for i := range t.Streams {
			streams = append(streams, streamToJSON(&t.Streams[i]))
		}
		sizeBytes := parseSizeBytes(t.SizeBytes())
		tj := TitleJSON{
			Index:      t.Index,
			Name:       t.Name(),
			Duration:   t.Duration(),
			Size:       t.SizeHuman(),
			SizeBytes:  sizeBytes,
			SourceFile: t.SourceFile(),
			Streams:    streams,
		}
		if m, ok := matchByIndex[t.Index]; ok && m.Matched {
			tj.Matched = true
			tj.Selected = true
			tj.ContentTitle = m.ContentTitle
			tj.ContentType = normalizeContentType(m.ContentType)
			tj.Season = m.Season
			tj.Episode = m.Episode
			tj.OutputName = buildOutputName(m)
		} else if !hasAnyIdentified {
			// Fully-stub disc: select everything so the user isn't left with
			// nothing checked.
			tj.Selected = true
		}
		titles = append(titles, tj)
	}
	return titles
}

// findDiscForRelease finds the first disc of the release identified by releaseID
// (as a decimal string) within items. Returns nil if not found or the release
// has no discs.
func findDiscForRelease(items []discdb.MediaItem, releaseID string, discID string) *discdb.Disc {
	relID, err := strconv.Atoi(releaseID)
	if err != nil {
		return nil
	}
	for _, item := range items {
		for _, rel := range item.Releases {
			if rel.ID == relID {
				if len(rel.Discs) == 0 {
					return nil
				}
				// If a specific disc was selected, find it by ID.
				if discID != "" {
					if dID, err := strconv.Atoi(discID); err == nil {
						for _, d := range rel.Discs {
							if d.ID == dID {
								disc := d
								return &disc
							}
						}
					}
				}
				// Fallback to first disc (backward compat / single-disc releases).
				disc := rel.Discs[0]
				return &disc
			}
		}
	}
	return nil
}

// DriveJSON is the JSON representation of a drive for Alpine.js stores.
type DriveJSON struct {
	Index        int    `json:"index"`
	Name         string `json:"name"`
	DiscName     string `json:"discName"`
	State        string `json:"state"`
	WorkflowStep int    `json:"workflowStep"` // 0=no disc, 1-5=wizard step
	RipProgress  int    `json:"ripProgress"`  // -1=not ripping, 0-100=progress
}

// StreamJSON is the JSON representation of a single audio/video/subtitle stream.
type StreamJSON struct {
	StreamIndex int    `json:"streamIndex"`
	Type        string `json:"type"`               // "video", "audio", "subtitle"
	LangCode    string `json:"langCode"`
	LangName    string `json:"langName"`
	Codec       string `json:"codec"`              // short name like "TrueHD", "AC3"
	Channels    string `json:"channels,omitempty"` // audio only
	IsDefault   bool   `json:"isDefault"`
	IsForced    bool   `json:"isForced"`
}

// LangOptionJSON represents a language choice at the disc level.
type LangOptionJSON struct {
	Code     string `json:"code"`     // ISO 639-2 (e.g., "eng")
	Name     string `json:"name"`     // Human readable (e.g., "English")
	Selected bool   `json:"selected"` // pre-checked based on settings defaults
}

// TitleJSON is the JSON representation of a disc title for Alpine.js stores.
type TitleJSON struct {
	Index        int          `json:"index"`
	Name         string       `json:"name"`
	Duration     string       `json:"duration"`
	Size         string       `json:"size"`
	SizeBytes    int64        `json:"sizeBytes,omitempty"`
	SourceFile   string       `json:"sourceFile"`
	OutputName   string       `json:"outputName,omitempty"`
	Selected     bool         `json:"selected"`
	Matched      bool         `json:"matched"`
	ContentTitle string       `json:"contentTitle,omitempty"`
	ContentType  string       `json:"contentType,omitempty"`
	Season       string       `json:"season,omitempty"`
	Episode      string       `json:"episode,omitempty"`
	Streams      []StreamJSON `json:"streams,omitempty"`
}

// buildOutputName returns a human-readable preview of the output filename
// based on match data. Returns empty string if not matched.
func buildOutputName(m discdb.ContentMatch) string {
	if !m.Matched || m.ContentTitle == "" {
		return ""
	}
	if m.Season != "" && m.Episode != "" && discdb.IsEpisodeType(m.ContentType) {
		sn, _ := strconv.Atoi(m.Season)
		ep, _ := strconv.Atoi(m.Episode)
		return fmt.Sprintf("S%02dE%02d - %s.mkv", sn, ep, m.ContentTitle)
	}
	return m.ContentTitle + ".mkv"
}

// SelectedReleaseJSON is the JSON representation of a user-selected release.
type SelectedReleaseJSON struct {
	MediaItemID string `json:"mediaItemID"`
	ReleaseID   string `json:"releaseID"`
	DiscID      string `json:"discID"`
	Title       string `json:"title"`
	Year        string `json:"year"`
	Type        string `json:"type"`
	UPC         string `json:"upc,omitempty"`
	ASIN        string `json:"asin,omitempty"`
	RegionCode  string `json:"regionCode,omitempty"`
	Locale      string `json:"locale,omitempty"`
}

// DiscJSON is the JSON representation of a disc within a release.
type DiscJSON struct {
	ID    string `json:"id"`
	Index int    `json:"index"`
	Name  string `json:"name"`
}

// SearchResultJSON is the JSON representation of a search result row.
type SearchResultJSON struct {
	MediaTitle   string     `json:"mediaTitle"`
	MediaYear    int        `json:"mediaYear"`
	MediaType    string     `json:"mediaType"`
	ReleaseTitle string     `json:"releaseTitle"`
	ReleaseUPC   string     `json:"releaseUPC,omitempty"`
	ReleaseASIN  string     `json:"releaseASIN,omitempty"`
	RegionCode   string     `json:"regionCode,omitempty"`
	Locale       string     `json:"locale,omitempty"`
	Format       string     `json:"format"`
	DiscCount    int        `json:"discCount"`
	Discs        []DiscJSON `json:"discs"`
	ReleaseID    string     `json:"releaseID"`
	MediaItemID  string     `json:"mediaItemID"`
}

// DriveStoreJSON is the full Alpine.store('drive') shape for the drive detail page.
type DriveStoreJSON struct {
	DriveIndex        int                  `json:"driveIndex"`
	DriveName         string               `json:"driveName"`
	DiscName          string               `json:"discName"`
	State             string               `json:"state"`
	CurrentStep       int                  `json:"currentStep"`
	Scanning          bool                 `json:"scanning"`
	ScanError         string               `json:"scanError"`
	HasMapping        bool                 `json:"hasMapping"`
	MatchedMedia      string               `json:"matchedMedia"`
	MatchedRelease    string               `json:"matchedRelease"`
	MatchedDiscID     string               `json:"matchedDiscID"`
	Titles            []TitleJSON          `json:"titles"`
	SelectedRelease   *SelectedReleaseJSON `json:"selectedRelease"`
	SearchResults     []SearchResultJSON   `json:"searchResults"`
	AudioLanguages    []LangOptionJSON     `json:"audioLanguages"`
	SubtitleLanguages []LangOptionJSON     `json:"subtitleLanguages"`
	HasLosslessAudio  bool                 `json:"hasLosslessAudio"`
	KeepForcedSubs    bool                 `json:"keepForcedSubs"`
	KeepLossless      bool                 `json:"keepLossless"`
	RipActive         bool                 `json:"ripActive"`
	ActiveJobCount    int                  `json:"activeJobCount"`
}

// DashboardJobJSON is a compact job representation for the dashboard.
type DashboardJobJSON struct {
	ID          int64  `json:"id"`
	DiscName    string `json:"discName"`
	TitleName   string `json:"titleName"`
	Status      string `json:"status"`
	FinishedAt  string `json:"finishedAt,omitempty"`
}

// DrivesStoreJSON is the Alpine.store('drives') shape for the dashboard page.
type DrivesStoreJSON struct {
	Ready          bool               `json:"ready"`
	List           []DriveJSON        `json:"list"`
	ActiveCount    int                `json:"activeCount"`
	QueuedCount    int                `json:"queuedCount"`
	CompletedToday int                `json:"completedToday"`
	RecentJobs     []DashboardJobJSON `json:"recentJobs"`
}

// mediaItemsToSearchJSON converts MediaItems to SearchResultJSON rows.
func mediaItemsToSearchJSON(items []discdb.MediaItem) []SearchResultJSON {
	var rows []SearchResultJSON
	for _, item := range items {
		for _, rel := range item.Releases {
			format := ""
			if len(rel.Discs) > 0 {
				format = rel.Discs[0].Format
			}
			discs := make([]DiscJSON, 0, len(rel.Discs))
			for _, d := range rel.Discs {
				discs = append(discs, DiscJSON{
					ID:    strconv.Itoa(d.ID),
					Index: d.Index,
					Name:  d.Name,
				})
			}
			rows = append(rows, SearchResultJSON{
				MediaTitle:   item.Title,
				MediaYear:    item.Year,
				MediaType:    item.Type,
				ReleaseTitle: rel.Title,
				ReleaseUPC:   rel.UPC,
				ReleaseASIN:  rel.ASIN,
				RegionCode:   rel.RegionCode,
				Locale:       rel.Locale,
				Format:       format,
				DiscCount:    len(rel.Discs),
				Discs:        discs,
				ReleaseID:    strconv.Itoa(rel.ID),
				MediaItemID:  strconv.Itoa(item.ID),
			})
		}
	}
	if rows == nil {
		rows = []SearchResultJSON{}
	}
	return rows
}

// streamToJSON converts a makemkv.StreamInfo to a StreamJSON value.
func streamToJSON(s *makemkv.StreamInfo) StreamJSON {
	return StreamJSON{
		StreamIndex: s.StreamIndex,
		Type:        s.Type(),
		LangCode:    s.LangCode(),
		LangName:    s.LangName(),
		Codec:       s.CodecShort(),
		Channels:    s.Channels(),
		IsDefault:   s.IsDefault(),
		IsForced:    s.IsForced(),
	}
}

// scanToTitleJSON converts a makemkv.DiscScan's titles into TitleJSON slices.
func scanToTitleJSON(scan *makemkv.DiscScan) []TitleJSON {
	titles := make([]TitleJSON, 0, len(scan.Titles))
	for _, t := range scan.Titles {
		var streams []StreamJSON
		for i := range t.Streams {
			streams = append(streams, streamToJSON(&t.Streams[i]))
		}
		sizeBytes := parseSizeBytes(t.SizeBytes())
		titles = append(titles, TitleJSON{
			Index:      t.Index,
			Name:       t.Name(),
			Duration:   t.Duration(),
			Size:       t.SizeHuman(),
			SizeBytes:  sizeBytes,
			SourceFile: t.SourceFile(),
			Selected:   true,
			Streams:    streams,
		})
	}
	return titles
}

// extractDiscLanguages aggregates unique audio and subtitle languages across all
// titles in scan. The Selected field is set to true for languages that appear in
// preferredLangs (a comma-separated list of ISO 639-2 codes).
func extractDiscLanguages(scan *makemkv.DiscScan, preferredAudio, preferredSubtitle string) (audio, subtitle []LangOptionJSON) {
	seenAudio := make(map[string]bool)
	seenSub := make(map[string]bool)

	// Build lookup sets for preferred languages.
	preferAudioSet := make(map[string]bool)
	for _, code := range splitLangCodes(preferredAudio) {
		preferAudioSet[code] = true
	}
	preferSubSet := make(map[string]bool)
	for _, code := range splitLangCodes(preferredSubtitle) {
		preferSubSet[code] = true
	}

	for i := range scan.Titles {
		t := &scan.Titles[i]
		for j := range t.Streams {
			s := &t.Streams[j]
			lc := s.LangCode()
			if lc == "" {
				continue
			}
			switch {
			case s.IsAudio() && !seenAudio[lc]:
				seenAudio[lc] = true
				audio = append(audio, LangOptionJSON{
					Code:     lc,
					Name:     s.LangName(),
					Selected: len(preferAudioSet) == 0 || preferAudioSet[lc],
				})
			case s.IsSubtitle() && !seenSub[lc]:
				seenSub[lc] = true
				subtitle = append(subtitle, LangOptionJSON{
					Code:     lc,
					Name:     s.LangName(),
					Selected: len(preferSubSet) == 0 || preferSubSet[lc],
				})
			}
		}
	}

	if audio == nil {
		audio = []LangOptionJSON{}
	}
	if subtitle == nil {
		subtitle = []LangOptionJSON{}
	}
	return audio, subtitle
}

// splitLangCodes splits a comma-separated list of language codes into a slice,
// trimming whitespace and dropping empty entries.
func splitLangCodes(s string) []string {
	if s == "" {
		return nil
	}
	var out []string
	for _, part := range strings.Split(s, ",") {
		if code := strings.TrimSpace(part); code != "" {
			out = append(out, code)
		}
	}
	return out
}

// normalizeContentType maps raw TheDiscDB ItemType values to a canonical lowercase
// set that matches the CSS badge classes: movie, episode, series, extra, unknown.
// Returns "" for empty input so omitempty JSON tags suppress the field.
func normalizeContentType(ct string) string {
	switch strings.ToLower(ct) {
	case "movie":
		return "movie"
	case "episode":
		return "episode"
	case "series":
		return "series"
	case "extra", "deletedscene", "featurette", "behindthescenes",
		"interview", "scene", "short", "trailer":
		return "extra"
	case "":
		return ""
	default:
		return "unknown"
	}
}

// discHasLosslessAudio reports whether any title in scan has lossless audio.
func discHasLosslessAudio(scan *makemkv.DiscScan) bool {
	for i := range scan.Titles {
		if scan.Titles[i].HasLosslessAudio() {
			return true
		}
	}
	return false
}

