package fsutil

import (
	"os"
	"path/filepath"
	"testing"
)

// CopyFile has to copy the whole file and create the destination's parent dir,
// which will not exist the first time a failed rip's log is saved.
func TestCopyFileCopiesTheWholeFileIntoANewDir(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "MakeMKV_log.txt")
	const body = "line one\nthe fatal reason\nline three\n"
	if err := os.WriteFile(src, []byte(body), 0o600); err != nil {
		t.Fatalf("write src: %v", err)
	}

	dst := filepath.Join(dir, "logs", "sub", "rip-job7-x.log")
	if err := CopyFile(src, dst); err != nil {
		t.Fatalf("CopyFile: %v", err)
	}

	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("read dst: %v", err)
	}
	if string(got) != body {
		t.Errorf("copied %q, want the whole source %q", got, body)
	}
}

// A missing (or empty) source path must fail rather than create an empty file
// that looks like a real copy — the "no debug log to save" case at the call site.
func TestCopyFileFailsOnAMissingSource(t *testing.T) {
	if err := CopyFile("", filepath.Join(t.TempDir(), "out.log")); err == nil {
		t.Error("copying from an empty source path was allowed; it should error")
	}
	if err := CopyFile(filepath.Join(t.TempDir(), "nope.txt"), filepath.Join(t.TempDir(), "out.log")); err == nil {
		t.Error("copying from a nonexistent source was allowed; it should error")
	}
}

// The source's permissions carry over to the copy — the behaviour the organizer
// relied on when this logic lived in its private copyFile.
func TestCopyFilePreservesSourceMode(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "in")
	if err := os.WriteFile(src, []byte("x"), 0o640); err != nil {
		t.Fatalf("write src: %v", err)
	}
	dst := filepath.Join(dir, "out")
	if err := CopyFile(src, dst); err != nil {
		t.Fatalf("CopyFile: %v", err)
	}
	info, err := os.Stat(dst)
	if err != nil {
		t.Fatalf("stat dst: %v", err)
	}
	if info.Mode().Perm() != 0o640 {
		t.Errorf("dst mode = %o, want 0640 (source's mode)", info.Mode().Perm())
	}
}

// A symlink at the source is refused rather than followed, so a symlink left at
// a path we copy from cannot redirect the read elsewhere.
func TestCopyFileRefusesASymlinkSource(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	if err := os.WriteFile(target, []byte("secret"), 0o600); err != nil {
		t.Fatalf("write target: %v", err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := CopyFile(link, filepath.Join(dir, "out")); err == nil {
		t.Error("a symlink source was copied; it should be refused")
	}
}
