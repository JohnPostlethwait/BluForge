package web

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/johnpostlethwait/bluforge/internal/discdb"
	"github.com/johnpostlethwait/bluforge/internal/makemkv"
)

// enrichTitlesWithMatches builds TitleJSON entries for all titles in scan,
// enriched with match data from disc. Matched titles have Selected=true;
// unmatched titles have Selected=false.
func enrichTitlesWithMatches(scan *makemkv.DiscScan, disc discdb.Disc) []TitleJSON {
	matches := discdb.MatchTitles(scan, disc)

	// Build lookup by TitleIndex for fast access.
	matchByIndex := make(map[int]discdb.ContentMatch, len(matches))
	for _, m := range matches {
		matchByIndex[m.TitleIndex] = m
	}

	titles := make([]TitleJSON, 0, len(scan.Titles))
	for _, t := range scan.Titles {
		tj := TitleJSON{
			Index:      t.Index,
			Name:       t.Name(),
			Duration:   t.Duration(),
			Size:       t.SizeHuman(),
			SourceFile: t.SourceFile(),
		}
		if m, ok := matchByIndex[t.Index]; ok && m.Matched {
			tj.Matched = true
			tj.Selected = true
			tj.ContentTitle = m.ContentTitle
			tj.ContentType = m.ContentType
			tj.Season = m.Season
			tj.Episode = m.Episode
			tj.OutputName = buildOutputName(m)
		}
		titles = append(titles, tj)
	}
	return titles
}

// findDiscForRelease finds the first disc of the release identified by releaseID
// (as a decimal string) within items. Returns nil if not found or the release
// has no discs.
func findDiscForRelease(items []discdb.MediaItem, releaseID string) *discdb.Disc {
	id, err := strconv.Atoi(releaseID)
	if err != nil {
		return nil
	}
	for _, item := range items {
		for _, rel := range item.Releases {
			if rel.ID == id {
				if len(rel.Discs) == 0 {
					return nil
				}
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

// TitleJSON is the JSON representation of a disc title for Alpine.js stores.
type TitleJSON struct {
	Index        int    `json:"index"`
	Name         string `json:"name"`
	Duration     string `json:"duration"`
	Size         string `json:"size"`
	SourceFile   string `json:"sourceFile"`
	OutputName   string `json:"outputName,omitempty"`
	Selected     bool   `json:"selected"`
	Matched      bool   `json:"matched"`
	ContentTitle string `json:"contentTitle,omitempty"`
	ContentType  string `json:"contentType,omitempty"`
	Season       string `json:"season,omitempty"`
	Episode      string `json:"episode,omitempty"`
}

// buildOutputName returns a human-readable preview of the output filename
// based on match data. Returns empty string if not matched.
func buildOutputName(m discdb.ContentMatch) string {
	if !m.Matched || m.ContentTitle == "" {
		return ""
	}
	if m.Season != "" && m.Episode != "" && !strings.EqualFold(m.ContentType, "extra") {
		return fmt.Sprintf("S%sE%s - %s.mkv", padLeft(m.Season, 2), padLeft(m.Episode, 2), m.ContentTitle)
	}
	return m.ContentTitle + ".mkv"
}

// padLeft zero-pads s to at least width characters.
func padLeft(s string, width int) string {
	for len(s) < width {
		s = "0" + s
	}
	return s
}

// SelectedReleaseJSON is the JSON representation of a user-selected release.
type SelectedReleaseJSON struct {
	MediaItemID string `json:"mediaItemID"`
	ReleaseID   string `json:"releaseID"`
	Title       string `json:"title"`
	Year        string `json:"year"`
	Type        string `json:"type"`
}

// SearchResultJSON is the JSON representation of a search result row.
type SearchResultJSON struct {
	MediaTitle   string `json:"mediaTitle"`
	MediaYear    int    `json:"mediaYear"`
	MediaType    string `json:"mediaType"`
	ReleaseTitle string `json:"releaseTitle"`
	ReleaseUPC   string `json:"releaseUPC"`
	ReleaseASIN  string `json:"releaseASIN"`
	RegionCode   string `json:"regionCode"`
	Format       string `json:"format"`
	DiscCount    int    `json:"discCount"`
	ReleaseID    string `json:"releaseID"`
	MediaItemID  string `json:"mediaItemID"`
}

// DriveStoreJSON is the full Alpine.store('drive') shape for the drive detail page.
type DriveStoreJSON struct {
	DriveIndex      int                  `json:"driveIndex"`
	DriveName       string               `json:"driveName"`
	DiscName        string               `json:"discName"`
	State           string               `json:"state"`
	CurrentStep     int                  `json:"currentStep"`
	Scanning        bool                 `json:"scanning"`
	ScanError       string               `json:"scanError"`
	HasMapping      bool                 `json:"hasMapping"`
	MatchedMedia    string               `json:"matchedMedia"`
	MatchedRelease  string               `json:"matchedRelease"`
	Titles          []TitleJSON          `json:"titles"`
	SelectedRelease *SelectedReleaseJSON `json:"selectedRelease"`
	SearchResults   []SearchResultJSON   `json:"searchResults"`
	RipProgress     interface{}          `json:"ripProgress"`
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
			rows = append(rows, SearchResultJSON{
				MediaTitle:   item.Title,
				MediaYear:    item.Year,
				MediaType:    item.Type,
				ReleaseTitle: rel.Title,
				ReleaseUPC:   rel.UPC,
				ReleaseASIN:  rel.ASIN,
				RegionCode:   rel.RegionCode,
				Format:       format,
				DiscCount:    len(rel.Discs),
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

// scanToTitleJSON converts a makemkv.DiscScan's titles into TitleJSON slices.
func scanToTitleJSON(scan *makemkv.DiscScan) []TitleJSON {
	titles := make([]TitleJSON, 0, len(scan.Titles))
	for _, t := range scan.Titles {
		titles = append(titles, TitleJSON{
			Index:      t.Index,
			Name:       t.Name(),
			Duration:   t.Duration(),
			Size:       t.SizeHuman(),
			SourceFile: t.SourceFile(),
			Selected:   true,
		})
	}
	return titles
}

