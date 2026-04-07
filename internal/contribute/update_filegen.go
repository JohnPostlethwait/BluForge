package contribute

import (
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/johnpostlethwait/bluforge/internal/makemkv"
)

// MergeDiscJSON merges user label changes into an existing TheDiscDB disc JSON.
//
// Merge rules:
//   - Disc-level fields (Index, Slug, Name, Format, ContentHash): preserved from existing.
//   - Existing title Item.Type/Title: overwritten by user label if label.Type != "".
//   - Existing title Tracks, Chapters, SegmentMap, Duration, Size: preserved from existing.
//   - Titles in existing but not in scan: passed through untouched.
//   - New titles (SourceFile not in existing): appended as full entries from scan + label.
//   - Labels with Type == "" on new titles: skipped (omitted).
//
// existingJSON is the raw JSON content fetched from the GitHub repository.
func MergeDiscJSON(existingJSON string, scan *makemkv.DiscScan, labels []TitleLabel) (DiscJSON, error) {
	var existing DiscJSON
	if err := json.Unmarshal([]byte(existingJSON), &existing); err != nil {
		return DiscJSON{}, fmt.Errorf("merge: unmarshal existing disc json: %w", err)
	}

	// Build index: SourceFile → position in existing.Titles.
	existingIdx := make(map[string]int, len(existing.Titles))
	for i, t := range existing.Titles {
		existingIdx[t.SourceFile] = i
	}

	// Build index: TitleIndex → scan title.
	scanByIndex := make(map[int]*makemkv.TitleInfo, len(scan.Titles))
	for i := range scan.Titles {
		scanByIndex[scan.Titles[i].Index] = &scan.Titles[i]
	}

	for _, label := range labels {
		t, ok := scanByIndex[label.TitleIndex]
		if !ok {
			continue
		}
		sf := t.SourceFile()

		if i, exists := existingIdx[sf]; exists {
			// Existing title: update Item.* only if user assigned a type.
			if label.Type != "" {
				if existing.Titles[i].Item == nil {
					existing.Titles[i].Item = &DiscTitleItemJSON{Chapters: []ChapterJSON{}}
				}
				existing.Titles[i].Item.Type = label.Type
				existing.Titles[i].Item.Title = label.Name
			}
		} else if label.Type != "" {
			// New title: build a full entry from scan data + label.
			var sizeBytes int64
			if s := t.SizeBytes(); s != "" {
				sizeBytes, _ = strconv.ParseInt(s, 10, 64)
			}
			tracks := make([]TrackJSON, 0, len(t.Streams))
			for j := range t.Streams {
				tracks = append(tracks, streamToTrack(j, &t.Streams[j]))
			}
			newTitle := DiscTitleJSON{
				Index:       t.Index,
				Comment:     t.Name(),
				SourceFile:  sf,
				SegmentMap:  t.SegmentMap(),
				Duration:    t.Duration(),
				Size:        sizeBytes,
				DisplaySize: t.SizeHuman(),
				Item: &DiscTitleItemJSON{
					Type:     label.Type,
					Title:    label.Name,
					Chapters: []ChapterJSON{},
				},
				Tracks: tracks,
			}
			existing.Titles = append(existing.Titles, newTitle)
		}
	}

	return existing, nil
}
