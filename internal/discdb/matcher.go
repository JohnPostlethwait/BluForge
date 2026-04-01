package discdb

import (
	"crypto/sha256"
	"fmt"
	"sort"
	"strings"

	"github.com/johnpostlethwait/bluforge/internal/makemkv"
)

// ContentMatch represents the result of matching a single scan title against a disc's title list.
type ContentMatch struct {
	TitleIndex   int
	SourceFile   string
	Matched      bool
	ContentType  string
	ContentTitle string
	Season       string
	Episode      string
}

// MatchTitles builds a lookup from DiscTitle.SourceFile and matches each scan
// title against it by SourceFile (attribute 33). Returns one ContentMatch per
// scan title.
func MatchTitles(scan *makemkv.DiscScan, disc Disc) []ContentMatch {
	// Build lookup: sourceFile -> DiscTitle
	lookup := make(map[string]DiscTitle, len(disc.Titles))
	for _, dt := range disc.Titles {
		if dt.SourceFile != "" {
			lookup[dt.SourceFile] = dt
		}
	}

	matches := make([]ContentMatch, 0, len(scan.Titles))
	for _, title := range scan.Titles {
		sf := title.SourceFile()
		cm := ContentMatch{
			TitleIndex: title.Index,
			SourceFile: sf,
		}
		if dt, ok := lookup[sf]; ok {
			cm.Matched = true
			cm.ContentType = dt.ItemType
			cm.Season = dt.Season
			cm.Episode = dt.Episode
			if dt.Item != nil {
				cm.ContentTitle = dt.Item.Title
				// Use item-level type when available — this correctly
				// distinguishes extras from episodes.
				if dt.Item.Type != "" {
					cm.ContentType = dt.Item.Type
				}
				// Only episodes should carry season/episode data.
				// The API returns many non-episode types (Extra,
				// DeletedScene, etc.) that may have season/episode
				// set to indicate association, not identity.
				if !IsEpisodeType(cm.ContentType) {
					cm.Season = ""
					cm.Episode = ""
				}
			}
		}
		matches = append(matches, cm)
	}
	return matches
}

// isEpisodeType returns true for content types that should receive
// season/episode formatting (S##E## prefix).
func IsEpisodeType(ct string) bool {
	lower := strings.ToLower(ct)
	return lower == "episode" || lower == "series"
}

// ScoreRelease scores how well a release matches the scan: 10 points per
// matching source file, +5 bonus if title count matches total disc titles.
func ScoreRelease(scan *makemkv.DiscScan, release Release) int {
	score := 0

	// Build set of source files from the scan.
	scanFiles := make(map[string]struct{}, len(scan.Titles))
	for _, t := range scan.Titles {
		sf := t.SourceFile()
		if sf != "" {
			scanFiles[sf] = struct{}{}
		}
	}

	totalDiscTitles := 0
	for _, disc := range release.Discs {
		totalDiscTitles += len(disc.Titles)
		for _, dt := range disc.Titles {
			if _, ok := scanFiles[dt.SourceFile]; ok {
				score += 10
			}
		}
	}

	// +5 bonus if title count matches total disc titles across all discs.
	if scan.TitleCount == totalDiscTitles {
		score += 5
	}

	return score
}

// BestRelease finds the best-matching release across all MediaItems. Returns
// the SearchResult and the score. Returns nil if items is empty.
func BestRelease(scan *makemkv.DiscScan, items []MediaItem) (*SearchResult, int) {
	var best *SearchResult
	bestScore := -1

	for _, item := range items {
		for _, release := range item.Releases {
			s := ScoreRelease(scan, release)
			if s > bestScore {
				bestScore = s
				r := &SearchResult{
					MediaItem: item,
					Release:   release,
				}
				// Attach first disc for convenience.
				if len(release.Discs) > 0 {
					r.Disc = release.Discs[0]
				}
				best = r
			}
		}
	}

	if best == nil {
		return nil, 0
	}
	return best, bestScore
}

// BuildDiscKey returns a SHA256-based key for the disc: SHA256 of
// "discName|titleCount|sortedSourceFiles", returned as the first 16 bytes hex.
func BuildDiscKey(scan *makemkv.DiscScan) string {
	// Collect and sort source files.
	files := make([]string, 0, len(scan.Titles))
	for _, t := range scan.Titles {
		sf := t.SourceFile()
		if sf != "" {
			files = append(files, sf)
		}
	}
	sort.Strings(files)

	input := fmt.Sprintf("%s|%d|%s", scan.DiscName, scan.TitleCount, joinStrings(files, ","))
	sum := sha256.Sum256([]byte(input))
	return fmt.Sprintf("%x", sum[:16])
}

// joinStrings joins a string slice with a separator.
func joinStrings(ss []string, sep string) string {
	if len(ss) == 0 {
		return ""
	}
	result := ss[0]
	for _, s := range ss[1:] {
		result += sep + s
	}
	return result
}
