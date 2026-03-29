package organizer

import (
	"os"
	"path/filepath"
	"testing"
)

// defaultOrganizer returns an Organizer with the standard production templates.
func defaultOrganizer() *Organizer {
	return New(
		"Movies/{{.Title}} ({{.Year}})/{{.Title}} ({{.Year}})",
		"TV/{{.Show}}/Season {{.Season}}/{{.Show}} - S{{.Season}}E{{.Episode}} - {{.EpisodeTitle}}",
	)
}

func TestBuildMoviePath(t *testing.T) {
	o := defaultOrganizer()
	meta := MovieMeta{Title: "Deadpool 2", Year: "2018"}
	got, err := o.BuildMoviePath(meta)
	if err != nil {
		t.Fatalf("BuildMoviePath returned error: %v", err)
	}
	want := filepath.Join("Movies", "Deadpool 2 (2018)", "Deadpool 2 (2018).mkv")
	if got != want {
		t.Errorf("BuildMoviePath = %q, want %q", got, want)
	}
}

func TestBuildMoviePathWithPart(t *testing.T) {
	o := defaultOrganizer()
	meta := MovieMeta{Title: "Kill Bill", Year: "2003", Part: "1"}
	got, err := o.BuildMoviePath(meta)
	if err != nil {
		t.Fatalf("BuildMoviePath returned error: %v", err)
	}
	want := filepath.Join("Movies", "Kill Bill (2003)", "Kill Bill (2003) - Part 1.mkv")
	if got != want {
		t.Errorf("BuildMoviePath = %q, want %q", got, want)
	}
}

func TestBuildSeriesPath(t *testing.T) {
	o := defaultOrganizer()
	meta := SeriesMeta{
		Show:         "Breaking Bad",
		Season:       "01",
		Episode:      "01",
		EpisodeTitle: "Pilot",
	}
	got, err := o.BuildSeriesPath(meta)
	if err != nil {
		t.Fatalf("BuildSeriesPath returned error: %v", err)
	}
	want := filepath.Join("TV", "Breaking Bad", "Season 01", "Breaking Bad - S01E01 - Pilot.mkv")
	if got != want {
		t.Errorf("BuildSeriesPath = %q, want %q", got, want)
	}
}

func TestBuildPathSanitizesOutput(t *testing.T) {
	o := defaultOrganizer()
	// "What If...?" — the "?" should be stripped by SanitizeFilename.
	meta := MovieMeta{Title: "What If...?", Year: "2021"}
	got, err := o.BuildMoviePath(meta)
	if err != nil {
		t.Fatalf("BuildMoviePath returned error: %v", err)
	}
	// "?" is invalid; sanitized title becomes "What If..."
	want := filepath.Join("Movies", "What If... (2021)", "What If... (2021).mkv")
	if got != want {
		t.Errorf("BuildMoviePath = %q, want %q", got, want)
	}
}

func TestAtomicMove(t *testing.T) {
	// Create a temp directory to work in.
	dir := t.TempDir()

	src := filepath.Join(dir, "source.mkv")
	dst := filepath.Join(dir, "subdir", "destination.mkv")

	content := []byte("fake mkv content")
	if err := os.WriteFile(src, content, 0o644); err != nil {
		t.Fatalf("failed to create source file: %v", err)
	}

	if err := AtomicMove(src, dst); err != nil {
		t.Fatalf("AtomicMove returned error: %v", err)
	}

	// Source should be gone.
	if _, err := os.Stat(src); err == nil {
		t.Errorf("source file still exists after AtomicMove")
	}

	// Destination should have the original content.
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("failed to read destination file: %v", err)
	}
	if string(got) != string(content) {
		t.Errorf("destination content = %q, want %q", got, content)
	}
}
