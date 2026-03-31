package organizer

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBuildPath_Matched(t *testing.T) {
	o := New()
	got := o.BuildPath("Seinfeld", "S01E02 - Male Unbonding")
	want := filepath.Join("Seinfeld", "S01E02 - Male Unbonding.mkv")
	if got != want {
		t.Errorf("BuildPath = %q, want %q", got, want)
	}
}

func TestBuildPath_Unmatched(t *testing.T) {
	o := New()
	got := o.BuildPath("Seinfeld Season 1", "00300.mpls")
	want := filepath.Join("Seinfeld Season 1", "00300.mkv")
	if got != want {
		t.Errorf("BuildPath = %q, want %q", got, want)
	}
}

func TestBuildPath_Sanitizes(t *testing.T) {
	o := New()
	got := o.BuildPath("What If...?", "The Movie?.mkv")
	want := filepath.Join("What If...", "The Movie.mkv")
	if got != want {
		t.Errorf("BuildPath = %q, want %q", got, want)
	}
}

func TestBuildPath_EmptyFallbacks(t *testing.T) {
	o := New()
	got := o.BuildPath("", "")
	want := filepath.Join("Unknown", "title.mkv")
	if got != want {
		t.Errorf("BuildPath = %q, want %q", got, want)
	}
}

func TestAtomicMove(t *testing.T) {
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

	if _, err := os.Stat(src); err == nil {
		t.Errorf("source file still exists after AtomicMove")
	}

	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("failed to read destination file: %v", err)
	}
	if string(got) != string(content) {
		t.Errorf("destination content = %q, want %q", got, content)
	}
}
