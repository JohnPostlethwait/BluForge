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
			Attributes: map[int]string{33: sf},
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
				Item:       &ContentItem{Title: "MainMovie", Type: "movie"},
			},
			{
				SourceFile: "00010.mpls",
				ItemType:   "extra",
				Item:       &ContentItem{Title: "Extra", Type: "extra"},
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

func TestMatchNoSourceFileMatch(t *testing.T) {
	scan := makeScan("TestDisc", "99999.mpls")

	disc := Disc{
		Titles: []DiscTitle{
			{
				SourceFile: "00001.mpls",
				ItemType:   "movie",
				Item:       &ContentItem{Title: "SomeMovie", Type: "movie"},
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
