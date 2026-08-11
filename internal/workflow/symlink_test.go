package workflow

import (
	"os"
	"path/filepath"
	"testing"
)

// A disc whose AACS directory is spurious does not need copying. MakeMKV keys
// off the directory being absent from the tree it is pointed at, so a tree of
// symlinks with AACS left out is enough: it reads the disc directly, and only
// the titles that were selected. Verified against real hardware — MakeMKV
// logged "AACS directory not present, assuming unencrypted disc" and ripped a
// title through the links.
func TestBuildSymlinkTreeOmitsAACS(t *testing.T) {
	discRoot := t.TempDir()
	for _, dir := range []string{"BDMV", "CERTIFICATE", "AACS"} {
		if err := os.MkdirAll(filepath.Join(discRoot, dir), 0o777); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	if err := os.WriteFile(filepath.Join(discRoot, "BDMV", "index.bdmv"), []byte("x"), 0o666); err != nil {
		t.Fatalf("write: %v", err)
	}

	linkDir := filepath.Join(t.TempDir(), "links")
	if err := buildSymlinkTree(discRoot, linkDir); err != nil {
		t.Fatalf("buildSymlinkTree: %v", err)
	}

	if _, err := os.Stat(filepath.Join(linkDir, "AACS")); !os.IsNotExist(err) {
		t.Error("AACS was linked into the tree; MakeMKV would demand a volume key")
	}
	// The linked entries must resolve through to the disc.
	if _, err := os.Stat(filepath.Join(linkDir, "BDMV", "index.bdmv")); err != nil {
		t.Errorf("BDMV does not resolve through the link: %v", err)
	}
	if _, err := os.Stat(filepath.Join(linkDir, "CERTIFICATE")); err != nil {
		t.Errorf("CERTIFICATE was not linked: %v", err)
	}
}

// The entries must be links, not copies — the whole point is that this costs
// kilobytes rather than ~100GB.
func TestBuildSymlinkTreeLinksRatherThanCopies(t *testing.T) {
	discRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(discRoot, "BDMV"), 0o777); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	linkDir := filepath.Join(t.TempDir(), "links")
	if err := buildSymlinkTree(discRoot, linkDir); err != nil {
		t.Fatalf("buildSymlinkTree: %v", err)
	}

	info, err := os.Lstat(filepath.Join(linkDir, "BDMV"))
	if err != nil {
		t.Fatalf("lstat: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Error("BDMV is not a symlink")
	}
}

// A disc with no AACS directory at all is still linkable; nothing special.
func TestBuildSymlinkTreeWithoutAACS(t *testing.T) {
	discRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(discRoot, "BDMV"), 0o777); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	linkDir := filepath.Join(t.TempDir(), "links")
	if err := buildSymlinkTree(discRoot, linkDir); err != nil {
		t.Fatalf("buildSymlinkTree: %v", err)
	}
	if _, err := os.Stat(filepath.Join(linkDir, "BDMV")); err != nil {
		t.Errorf("BDMV missing: %v", err)
	}
}

// Rebuilding over an existing tree must not fail on links that are already
// there — a second recovery attempt for the same disc is normal.
func TestBuildSymlinkTreeIsRepeatable(t *testing.T) {
	discRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(discRoot, "BDMV"), 0o777); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	linkDir := filepath.Join(t.TempDir(), "links")

	for i := 0; i < 2; i++ {
		if err := buildSymlinkTree(discRoot, linkDir); err != nil {
			t.Fatalf("buildSymlinkTree attempt %d: %v", i, err)
		}
	}
	if _, err := os.Stat(filepath.Join(linkDir, "BDMV")); err != nil {
		t.Errorf("BDMV missing after rebuild: %v", err)
	}
}
