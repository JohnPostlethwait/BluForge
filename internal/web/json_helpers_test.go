package web

import (
	"testing"

	"github.com/johnpostlethwait/bluforge/internal/discdb"
	"github.com/johnpostlethwait/bluforge/internal/makemkv"
)

// makeWebScan is a helper to build a DiscScan with named source files and names.
func makeWebScan(discName string, sourceFiles ...string) *makemkv.DiscScan {
	titles := make([]makemkv.TitleInfo, len(sourceFiles))
	for i, sf := range sourceFiles {
		titles[i] = makemkv.TitleInfo{
			Index: i,
			Attributes: map[int]string{
				2:  "Title " + sf,
				9:  "1:00:00",
				10: "10 GB",
				16: sf,
			},
		}
	}
	return &makemkv.DiscScan{
		DiscName:   discName,
		TitleCount: len(sourceFiles),
		Titles:     titles,
	}
}

func TestEnrichTitlesWithMatches(t *testing.T) {
	scan := makeWebScan("TestDisc", "00001.mpls", "00002.mpls", "99999.mpls")

	disc := discdb.Disc{
		Titles: []discdb.DiscTitle{
			{
				SourceFile: "00001.mpls",
				ItemType:   "movie",
				Item:       &discdb.DiscItemReference{Title: "The Main Feature", Type: "movie"},
			},
			{
				SourceFile: "00002.mpls",
				ItemType:   "episode",
				Season:     "1",
				Episode:    "2",
				Item:       &discdb.DiscItemReference{Title: "Episode Two", Type: "episode"},
			},
			// 99999.mpls is NOT in disc titles — should be unmatched
		},
	}

	titles := enrichTitlesWithMatches(scan, disc)

	if len(titles) != 3 {
		t.Fatalf("expected 3 titles, got %d", len(titles))
	}

	// Find each title by SourceFile
	byFile := make(map[string]TitleJSON, len(titles))
	for _, tj := range titles {
		byFile[tj.SourceFile] = tj
	}

	// Matched: 00001.mpls — movie
	main, ok := byFile["00001.mpls"]
	if !ok {
		t.Fatal("missing title for 00001.mpls")
	}
	if !main.Matched {
		t.Errorf("expected 00001.mpls to have Matched=true")
	}
	if !main.Selected {
		t.Errorf("expected matched title 00001.mpls to have Selected=true")
	}
	if main.ContentTitle != "The Main Feature" {
		t.Errorf("expected ContentTitle=%q, got %q", "The Main Feature", main.ContentTitle)
	}
	if main.ContentType != "movie" {
		t.Errorf("expected ContentType=%q, got %q", "movie", main.ContentType)
	}

	// Matched: 00002.mpls — episode with season/episode
	ep, ok := byFile["00002.mpls"]
	if !ok {
		t.Fatal("missing title for 00002.mpls")
	}
	if !ep.Matched {
		t.Errorf("expected 00002.mpls to have Matched=true")
	}
	if !ep.Selected {
		t.Errorf("expected matched title 00002.mpls to have Selected=true")
	}
	if ep.ContentTitle != "Episode Two" {
		t.Errorf("expected ContentTitle=%q, got %q", "Episode Two", ep.ContentTitle)
	}
	if ep.Season != "1" {
		t.Errorf("expected Season=%q, got %q", "1", ep.Season)
	}
	if ep.Episode != "2" {
		t.Errorf("expected Episode=%q, got %q", "2", ep.Episode)
	}

	// Unmatched: 99999.mpls
	unmatched, ok := byFile["99999.mpls"]
	if !ok {
		t.Fatal("missing title for 99999.mpls")
	}
	if unmatched.Matched {
		t.Errorf("expected 99999.mpls to have Matched=false")
	}
	if unmatched.Selected {
		t.Errorf("expected unmatched title 99999.mpls to have Selected=false")
	}
}

func TestBuildOutputName(t *testing.T) {
	tests := []struct {
		name string
		m    discdb.ContentMatch
		want string
	}{
		{
			name: "episode with season and episode",
			m:    discdb.ContentMatch{Matched: true, ContentTitle: "Male Unbonding", ContentType: "series", Season: "1", Episode: "2"},
			want: "S01E02 - Male Unbonding.mkv",
		},
		{
			name: "extra with season and episode gets no prefix",
			m:    discdb.ContentMatch{Matched: true, ContentTitle: "Inside Look: Male Unbonding", ContentType: "extra", Season: "1", Episode: "2"},
			want: "Inside Look: Male Unbonding.mkv",
		},
		{
			name: "Extra capitalized with season and episode gets no prefix",
			m:    discdb.ContentMatch{Matched: true, ContentTitle: "Behind the Scenes", ContentType: "Extra", Season: "1", Episode: "2"},
			want: "Behind the Scenes.mkv",
		},
		{
			name: "DeletedScene with season and episode gets no prefix",
			m:    discdb.ContentMatch{Matched: true, ContentTitle: "Deleted Scenes: Male Unbonding", ContentType: "DeletedScene", Season: "1", Episode: "2"},
			want: "Deleted Scenes: Male Unbonding.mkv",
		},
		{
			name: "movie with no season or episode",
			m:    discdb.ContentMatch{Matched: true, ContentTitle: "The Matrix", ContentType: "movie"},
			want: "The Matrix.mkv",
		},
		{
			name: "unmatched returns empty",
			m:    discdb.ContentMatch{Matched: false},
			want: "",
		},
		{
			name: "matched but no content title returns empty",
			m:    discdb.ContentMatch{Matched: true, ContentTitle: "", ContentType: "series", Season: "1", Episode: "1"},
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildOutputName(tt.m)
			if got != tt.want {
				t.Errorf("buildOutputName() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFindDiscForRelease(t *testing.T) {
	disc1 := discdb.Disc{ID: 10, Name: "Disc One"}
	disc2 := discdb.Disc{ID: 20, Name: "Disc Two"}

	items := []discdb.MediaItem{
		{
			ID:    1,
			Title: "Movie A",
			Releases: []discdb.Release{
				{ID: 100, Title: "Release A1", Discs: []discdb.Disc{disc1}},
				{ID: 101, Title: "Release A2", Discs: []discdb.Disc{disc2}},
			},
		},
		{
			ID:    2,
			Title: "Movie B",
			Releases: []discdb.Release{
				{ID: 200, Title: "Release B1", Discs: []discdb.Disc{}},
			},
		},
	}

	t.Run("found first disc of matching release", func(t *testing.T) {
		disc := findDiscForRelease(items, "100", "")
		if disc == nil {
			t.Fatal("expected disc, got nil")
		}
		if disc.ID != 10 {
			t.Errorf("expected disc.ID=10, got %d", disc.ID)
		}
	})

	t.Run("found disc from second release", func(t *testing.T) {
		disc := findDiscForRelease(items, "101", "")
		if disc == nil {
			t.Fatal("expected disc, got nil")
		}
		if disc.ID != 20 {
			t.Errorf("expected disc.ID=20, got %d", disc.ID)
		}
	})

	t.Run("release with no discs returns nil", func(t *testing.T) {
		disc := findDiscForRelease(items, "200", "")
		if disc != nil {
			t.Errorf("expected nil for release with no discs, got %+v", disc)
		}
	})

	t.Run("unknown release ID returns nil", func(t *testing.T) {
		disc := findDiscForRelease(items, "999", "")
		if disc != nil {
			t.Errorf("expected nil for unknown release ID, got %+v", disc)
		}
	})

	t.Run("non-numeric release ID returns nil", func(t *testing.T) {
		disc := findDiscForRelease(items, "not-a-number", "")
		if disc != nil {
			t.Errorf("expected nil for non-numeric release ID, got %+v", disc)
		}
	})

	t.Run("empty items returns nil", func(t *testing.T) {
		disc := findDiscForRelease(nil, "100", "")
		if disc != nil {
			t.Errorf("expected nil for empty items, got %+v", disc)
		}
	})

	// Multi-disc release tests for disc ID selection.
	multiDiscItems := []discdb.MediaItem{
		{
			ID:    3,
			Title: "Seinfeld",
			Releases: []discdb.Release{
				{
					ID:    300,
					Title: "Season 2",
					Discs: []discdb.Disc{
						{ID: 50, Index: 0, Name: "Disc 1"},
						{ID: 51, Index: 1, Name: "Disc 2"},
						{ID: 52, Index: 2, Name: "Disc 3"},
					},
				},
			},
		},
	}

	t.Run("multi-disc with valid discID returns correct disc", func(t *testing.T) {
		disc := findDiscForRelease(multiDiscItems, "300", "51")
		if disc == nil {
			t.Fatal("expected disc, got nil")
		}
		if disc.ID != 51 {
			t.Errorf("expected disc.ID=51, got %d", disc.ID)
		}
		if disc.Name != "Disc 2" {
			t.Errorf("expected disc.Name='Disc 2', got %q", disc.Name)
		}
	})

	t.Run("multi-disc with empty discID returns first disc", func(t *testing.T) {
		disc := findDiscForRelease(multiDiscItems, "300", "")
		if disc == nil {
			t.Fatal("expected disc, got nil")
		}
		if disc.ID != 50 {
			t.Errorf("expected disc.ID=50 (first disc), got %d", disc.ID)
		}
	})

	t.Run("multi-disc with invalid discID returns first disc", func(t *testing.T) {
		disc := findDiscForRelease(multiDiscItems, "300", "999")
		if disc == nil {
			t.Fatal("expected disc, got nil")
		}
		if disc.ID != 50 {
			t.Errorf("expected disc.ID=50 (fallback to first), got %d", disc.ID)
		}
	})
}
