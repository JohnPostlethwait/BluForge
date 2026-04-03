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

func TestNonCollidingPath_NoCollision(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Iron Man.mkv")
	// File does not exist — should be returned unchanged.
	got := NonCollidingPath(path)
	if got != path {
		t.Errorf("NonCollidingPath = %q, want %q", got, path)
	}
}

func TestNonCollidingPath_OneCollision(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Iron Man.mkv")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(dir, "Iron Man (1).mkv")
	got := NonCollidingPath(path)
	if got != want {
		t.Errorf("NonCollidingPath = %q, want %q", got, want)
	}
}

func TestNonCollidingPath_MultipleCollisions(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"Iron Man.mkv", "Iron Man (1).mkv"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want := filepath.Join(dir, "Iron Man (2).mkv")
	got := NonCollidingPath(filepath.Join(dir, "Iron Man.mkv"))
	if got != want {
		t.Errorf("NonCollidingPath = %q, want %q", got, want)
	}
}
