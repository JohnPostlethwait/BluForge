package discdb

import (
	"testing"

	"github.com/johnpostlethwait/bluforge/internal/makemkv"
)

// makeScan is a helper to build a DiscScan with named source files.
func makeScan(discName string, sourceFiles ...string) *makemkv.DiscScan {
	titles := make([]makemkv.TitleInfo, len(sourceFiles))
	for i, sf := range sourceFiles {
		titles[i] = makemkv.TitleInfo{
			Index:      i,
			Attributes: map[int]string{16: sf},
		}
	}
	return &makemkv.DiscScan{
		DiscName:   discName,
		TitleCount: len(sourceFiles),
		Titles:     titles,
	}
}

func TestMatchBySourceFile(t *testing.T) {
	scan := makeScan("TestDisc", "00001.mpls", "00010.mpls")

	disc := Disc{
		Titles: []DiscTitle{
			{
				SourceFile: "00001.mpls",
				ItemType:   "movie",
				Item:       &DiscItemReference{Title: "MainMovie", Type: "movie"},
			},
			{
				SourceFile: "00010.mpls",
				ItemType:   "extra",
				Item:       &DiscItemReference{Title: "Extra", Type: "extra"},
			},
		},
	}

	matches := MatchTitles(scan, disc)

	if len(matches) != 2 {
		t.Fatalf("expected 2 matches, got %d", len(matches))
	}

	// Find match for 00001.mpls
	var main, extra ContentMatch
	for _, m := range matches {
		if m.SourceFile == "00001.mpls" {
			main = m
		} else if m.SourceFile == "00010.mpls" {
			extra = m
		}
	}

	if !main.Matched {
		t.Errorf("expected 00001.mpls to be matched")
	}
	if main.ContentTitle != "MainMovie" {
		t.Errorf("expected ContentTitle=MainMovie, got %q", main.ContentTitle)
	}
	if main.ContentType != "movie" {
		t.Errorf("expected ContentType=movie, got %q", main.ContentType)
	}

	if !extra.Matched {
		t.Errorf("expected 00010.mpls to be matched")
	}
	if extra.ContentTitle != "Extra" {
		t.Errorf("expected ContentTitle=Extra, got %q", extra.ContentTitle)
	}
	if extra.ContentType != "extra" {
		t.Errorf("expected ContentType=extra, got %q", extra.ContentType)
	}
}

func TestMatchExtrasDropSeasonEpisode(t *testing.T) {
	scan := makeScan("TestDisc", "00100.mpls", "00002.m2ts")

	disc := Disc{
		Titles: []DiscTitle{
			{
				SourceFile: "00100.mpls",
				ItemType:   "video",
				Season:     "1",
				Episode:    "2",
				Item:       &DiscItemReference{Title: "Male Unbonding", Type: "episode", Season: "1", Episode: "2"},
			},
			{
				SourceFile: "00002.m2ts",
				ItemType:   "video",
				Season:     "1",
				Episode:    "2",
				Item:       &DiscItemReference{Title: "Deleted Scenes: Male Unbonding", Type: "extra"},
			},
		},
	}

	matches := MatchTitles(scan, disc)

	if len(matches) != 2 {
		t.Fatalf("expected 2 matches, got %d", len(matches))
	}

	var episode, extra ContentMatch
	for _, m := range matches {
		if m.SourceFile == "00100.mpls" {
			episode = m
		} else {
			extra = m
		}
	}

	// Episode should keep season/episode.
	if episode.ContentType != "episode" {
		t.Errorf("expected episode ContentType=episode, got %q", episode.ContentType)
	}
	if episode.Season != "1" || episode.Episode != "2" {
		t.Errorf("expected episode Season=1/Episode=2, got %q/%q", episode.Season, episode.Episode)
	}

	// Extra should have season/episode cleared even though DiscTitle has them.
	if extra.ContentType != "extra" {
		t.Errorf("expected extra ContentType=extra, got %q", extra.ContentType)
	}
	if extra.Season != "" || extra.Episode != "" {
		t.Errorf("expected extra Season/Episode to be empty, got %q/%q", extra.Season, extra.Episode)
	}
	if extra.ContentTitle != "Deleted Scenes: Male Unbonding" {
		t.Errorf("expected extra ContentTitle='Deleted Scenes: Male Unbonding', got %q", extra.ContentTitle)
	}
}

func TestMatchNoSourceFileMatch(t *testing.T) {
	scan := makeScan("TestDisc", "99999.mpls")

	disc := Disc{
		Titles: []DiscTitle{
			{
				SourceFile: "00001.mpls",
				ItemType:   "movie",
				Item:       &DiscItemReference{Title: "SomeMovie", Type: "movie"},
			},
		},
	}

	matches := MatchTitles(scan, disc)

	if len(matches) != 1 {
		t.Fatalf("expected 1 match entry, got %d", len(matches))
	}

	m := matches[0]
	if m.SourceFile != "99999.mpls" {
		t.Errorf("expected SourceFile=99999.mpls, got %q", m.SourceFile)
	}
	if m.Matched {
		t.Errorf("expected Matched=false for unmatched source file")
	}
}

func TestScoreReleaseMatch(t *testing.T) {
	scan := makeScan("TestDisc", "00001.mpls", "00002.mpls")

	goodRelease := Release{
		Title: "Good Release",
		Discs: []Disc{
			{
				Titles: []DiscTitle{
					{SourceFile: "00001.mpls"},
					{SourceFile: "00002.mpls"},
				},
			},
		},
	}

	badRelease := Release{
		Title: "Bad Release",
		Discs: []Disc{
			{
				Titles: []DiscTitle{
					{SourceFile: "99991.mpls"},
					{SourceFile: "99992.mpls"},
				},
			},
		},
	}

	goodScore := ScoreRelease(scan, goodRelease)
	badScore := ScoreRelease(scan, badRelease)

	if goodScore <= badScore {
		t.Errorf("expected good release (%d) to score higher than bad release (%d)", goodScore, badScore)
	}
	// Good release: 2 matching files = 20 points + 5 bonus (title counts match) = 25
	if goodScore < 20 {
		t.Errorf("expected good release score >= 20, got %d", goodScore)
	}
	// Bad release: 0 matching files = 0 points (may get +5 bonus if title count matches,
	// but no source file points).
	if badScore >= goodScore {
		t.Errorf("expected bad release score (%d) to be less than good release score (%d)", badScore, goodScore)
	}
}

func TestBuildDiscKey(t *testing.T) {
	scan := makeScan("MyDisc", "00001.mpls", "00002.mpls", "00010.mpls")

	key := BuildDiscKey(scan)
	if key == "" {
		t.Fatal("expected non-empty disc key")
	}

	// Same scan produces same key (determinism).
	key2 := BuildDiscKey(scan)
	if key != key2 {
		t.Errorf("expected same key for same scan, got %q and %q", key, key2)
	}

	// Key should be 32 hex chars (16 bytes).
	if len(key) != 32 {
		t.Errorf("expected key length 32, got %d", len(key))
	}
}

func TestBestRelease_SingleItemSingleRelease(t *testing.T) {
	scan := makeScan("TestDisc", "00001.mpls", "00002.mpls")

	items := []MediaItem{
		{
			ID:    1,
			Title: "Test Movie",
			Year:  2024,
			Type:  "movie",
			Releases: []Release{
				{
					ID:    10,
					Title: "Test Release",
					Discs: []Disc{
						{
							ID: 100,
							Titles: []DiscTitle{
								{SourceFile: "00001.mpls"},
								{SourceFile: "00002.mpls"},
							},
						},
					},
				},
			},
		},
	}

	result, score := BestRelease(scan, items)
	if result == nil {
		t.Fatalf("expected non-nil result")
	}
	if score <= 0 {
		t.Errorf("expected score > 0, got %d", score)
	}
	if result.MediaItem.ID != 1 {
		t.Errorf("expected MediaItem.ID=1, got %d", result.MediaItem.ID)
	}
	if result.Release.ID != 10 {
		t.Errorf("expected Release.ID=10, got %d", result.Release.ID)
	}
	if result.Disc.ID != 100 {
		t.Errorf("expected Disc.ID=100, got %d", result.Disc.ID)
	}
}

func TestBestRelease_HighestScoreWins(t *testing.T) {
	scan := makeScan("TestDisc", "00001.mpls")

	items := []MediaItem{
		{
			ID:    1,
			Title: "Good Match",
			Releases: []Release{
				{
					ID:    10,
					Title: "Good Release",
					Discs: []Disc{
						{
							Titles: []DiscTitle{
								{SourceFile: "00001.mpls"},
							},
						},
					},
				},
			},
		},
		{
			ID:    2,
			Title: "Bad Match",
			Releases: []Release{
				{
					ID:    20,
					Title: "Bad Release",
					Discs: []Disc{
						{
							Titles: []DiscTitle{
								{SourceFile: "99999.mpls"},
							},
						},
					},
				},
			},
		},
	}

	result, _ := BestRelease(scan, items)
	if result == nil {
		t.Fatalf("expected non-nil result")
	}
	if result.MediaItem.ID != 1 {
		t.Errorf("expected first item (ID=1) to win, got MediaItem.ID=%d", result.MediaItem.ID)
	}
	if result.Release.ID != 10 {
		t.Errorf("expected Release.ID=10, got %d", result.Release.ID)
	}
}

func TestBestRelease_EmptyItems(t *testing.T) {
	scan := makeScan("TestDisc", "00001.mpls")

	result, score := BestRelease(scan, []MediaItem{})
	if result != nil {
		t.Errorf("expected nil result for empty items, got %+v", result)
	}
	if score != 0 {
		t.Errorf("expected score=0 for empty items, got %d", score)
	}
}

func TestBestRelease_NoReleases(t *testing.T) {
	scan := makeScan("TestDisc", "00001.mpls")

	items := []MediaItem{
		{
			ID:       1,
			Title:    "No Releases Movie",
			Releases: []Release{},
		},
	}

	result, score := BestRelease(scan, items)
	if result != nil {
		t.Errorf("expected nil result for item with no releases, got %+v", result)
	}
	if score != 0 {
		t.Errorf("expected score=0, got %d", score)
	}
}

func TestBestRelease_ReleaseWithNoDiscs(t *testing.T) {
	scan := makeScan("TestDisc", "00001.mpls")

	items := []MediaItem{
		{
			ID:    1,
			Title: "Movie With Empty Release",
			Releases: []Release{
				{
					ID:    10,
					Title: "Empty Release",
					Discs: []Disc{},
				},
			},
		},
	}

	result, _ := BestRelease(scan, items)
	if result == nil {
		t.Fatalf("expected non-nil result")
	}
	// Disc should be zero-value since there are no discs in the release.
	if result.Disc.ID != 0 {
		t.Errorf("expected zero-value Disc.ID, got %d", result.Disc.ID)
	}
	if len(result.Disc.Titles) != 0 {
		t.Errorf("expected zero-value Disc with no titles, got %d titles", len(result.Disc.Titles))
	}
}

func TestScoreRelease_ZeroDiscs(t *testing.T) {
	scan := makeScan("Empty")

	release := Release{
		Title: "No Discs",
		Discs: []Disc{},
	}

	score := ScoreRelease(scan, release)
	// scan.TitleCount == 0, totalDiscTitles == 0, so title count bonus applies: +5.
	if score != 5 {
		t.Errorf("expected score=5 (title count bonus only), got %d", score)
	}
}

func TestScoreRelease_PerfectMatch(t *testing.T) {
	scan := makeScan("TestDisc", "00001.mpls", "00002.mpls", "00003.mpls")

	release := Release{
		Title: "Perfect Release",
		Discs: []Disc{
			{
				Titles: []DiscTitle{
					{SourceFile: "00001.mpls"},
					{SourceFile: "00002.mpls"},
					{SourceFile: "00003.mpls"},
				},
			},
		},
	}

	score := ScoreRelease(scan, release)
	// 3 matching files * 10 = 30, plus title count match bonus = 5, total = 35.
	if score != 35 {
		t.Errorf("expected score=35, got %d", score)
	}
}
