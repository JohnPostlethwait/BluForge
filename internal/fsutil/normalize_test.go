package fsutil

import (
	"os"
	"path/filepath"
	"testing"
)

// makemkvcon and ddrescue write into the output tree themselves, and neither
// takes any notice of our umask for the directories it creates. A backup came
// back with its AACS, BDMV and CERTIFICATE directories at 0755 inside a scratch
// directory we had created 0777 -- read-only to the share group, so the copy
// could not be cleaned up or written to over SMB.
func TestNormalizeTreeAppliesThePolicy(t *testing.T) {
	pinUmask(t, 0o002)

	root := filepath.Join(t.TempDir(), "backup")
	// Reproduce what makemkvcon leaves behind: 0755 directories, 0644 files.
	discDirs := []string{"AACS", "BDMV/STREAM", "CERTIFICATE"}
	for _, d := range discDirs {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}
	files := []string{"discatt.dat", "BDMV/index.bdmv", "BDMV/STREAM/00000.m2ts"}
	for _, f := range files {
		if err := os.WriteFile(filepath.Join(root, f), []byte("x"), 0o644); err != nil {
			t.Fatalf("write %s: %v", f, err)
		}
	}

	if err := NormalizeTree(root); err != nil {
		t.Fatalf("NormalizeTree: %v", err)
	}

	for _, d := range append(discDirs, ".") {
		info, err := os.Stat(filepath.Join(root, d))
		if err != nil {
			t.Fatalf("stat %s: %v", d, err)
		}
		if got := info.Mode().Perm(); got != 0o775 {
			t.Errorf("dir %s = %04o, want 0775", d, got)
		}
	}
	for _, f := range files {
		info, err := os.Stat(filepath.Join(root, f))
		if err != nil {
			t.Fatalf("stat %s: %v", f, err)
		}
		if got := info.Mode().Perm(); got != 0o664 {
			t.Errorf("file %s = %04o, want 0664", f, got)
		}
	}
}

// A recovered disc can be presented as a tree of symlinks pointing at the
// read-only disc mount. Chmod follows a symlink on Linux, so normalising one
// would try to change the permissions of the disc itself.
func TestNormalizeTreeDoesNotFollowSymlinks(t *testing.T) {
	pinUmask(t, 0o002)

	tmp := t.TempDir()
	target := filepath.Join(tmp, "on-the-disc.m2ts")
	if err := os.WriteFile(target, []byte("x"), 0o444); err != nil {
		t.Fatalf("write target: %v", err)
	}

	root := filepath.Join(tmp, "links")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.Symlink(target, filepath.Join(root, "00000.m2ts")); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	if err := NormalizeTree(root); err != nil {
		t.Fatalf("NormalizeTree: %v", err)
	}

	info, err := os.Stat(target)
	if err != nil {
		t.Fatalf("stat target: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o444 {
		t.Errorf("symlink target = %04o, want 0444 untouched", got)
	}
}

func TestNormalizeTreeOnMissingRoot(t *testing.T) {
	if err := NormalizeTree(filepath.Join(t.TempDir(), "never-created")); err != nil {
		t.Errorf("a missing tree is not an error: %v", err)
	}
}

func TestNormalizeTreeOnEmptyPath(t *testing.T) {
	if err := NormalizeTree(""); err != nil {
		t.Errorf("an empty path is not an error: %v", err)
	}
}

// A single file is normalised too: a rip is one .mkv, written by makemkvcon.
func TestNormalizeTreeOnASingleFile(t *testing.T) {
	pinUmask(t, 0o002)

	path := filepath.Join(t.TempDir(), "title.mkv")
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	if err := NormalizeTree(path); err != nil {
		t.Fatalf("NormalizeTree: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o664 {
		t.Errorf("file = %04o, want 0664", got)
	}
}
