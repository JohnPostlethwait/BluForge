package workflow

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// discWithStructure builds a disc tree with the structural files MakeMKV needs
// alongside the streams.
func discWithStructure(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for path, size := range map[string]int{
		"BDMV/STREAM/00000.m2ts":   4096,
		"BDMV/PLAYLIST/00800.mpls": 512,
		"BDMV/CLIPINF/00000.clpi":  256,
		"discatt.dat":              128,
	} {
		full := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o777); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(full, make([]byte, size), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	return root
}

// A backup that stopped early leaves the small structural files short as well
// as the streams. MakeMKV then cannot parse the disc at all — it opens the
// folder and fails immediately, enumerating nothing, however perfect the
// streams are. Comparing only BDMV/STREAM left those files unrepaired forever.
func TestSalvageRescuesStructuralFilesNotJustStreams(t *testing.T) {
	root := discWithStructure(t)
	backup := t.TempDir()

	// The streams came across whole; a playlist did not.
	if err := os.MkdirAll(filepath.Join(backup, "BDMV/STREAM"), 0o777); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(backup, "BDMV/STREAM/00000.m2ts"), make([]byte, 4096), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	short, err := incompleteFiles(root, backup)
	if err != nil {
		t.Fatalf("incompleteFiles: %v", err)
	}

	var names []string
	for _, s := range short {
		names = append(names, filepath.ToSlash(s.name))
	}
	joined := strings.Join(names, " ")

	for _, want := range []string{"00800.mpls", "00000.clpi", "discatt.dat"} {
		if !strings.Contains(joined, want) {
			t.Errorf("%s was not noticed as missing: %v", want, names)
		}
	}
	// The complete stream must not be rescued again: that is hours of drive
	// time for bytes already in hand.
	if strings.Contains(joined, "00000.m2ts") {
		t.Errorf("a complete stream was queued for rescue: %v", names)
	}
}

// A backup that came across whole needs nothing rescued at all.
func TestSalvageFindsNothingShortInACompleteCopy(t *testing.T) {
	root := discWithStructure(t)
	backup := t.TempDir()

	if err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		dst := filepath.Join(backup, rel)
		if err := os.MkdirAll(filepath.Dir(dst), 0o777); err != nil {
			return err
		}
		return os.WriteFile(dst, make([]byte, info.Size()), 0o644)
	}); err != nil {
		t.Fatalf("copy: %v", err)
	}

	short, err := incompleteFiles(root, backup)
	if err != nil {
		t.Fatalf("incompleteFiles: %v", err)
	}
	if len(short) != 0 {
		t.Errorf("a complete copy reported %d files short: %v", len(short), short)
	}
}

// A file that is short resumes from what is already there rather than starting
// the file over.
func TestSalvageResumesAShortFileFromItsCurrentLength(t *testing.T) {
	root := discWithStructure(t)
	backup := t.TempDir()

	if err := os.MkdirAll(filepath.Join(backup, "BDMV/STREAM"), 0o777); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(backup, "BDMV/STREAM/00000.m2ts"), make([]byte, 1024), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	short, err := incompleteFiles(root, backup)
	if err != nil {
		t.Fatalf("incompleteFiles: %v", err)
	}
	for _, s := range short {
		if strings.HasSuffix(filepath.ToSlash(s.name), "00000.m2ts") {
			if s.have != 1024 {
				t.Errorf("resume offset = %d, want 1024", s.have)
			}
			if s.want != 4096 {
				t.Errorf("target size = %d, want 4096", s.want)
			}
		}
	}
}
