package mpls

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// An empty mount point is not a disc. A drive reset -- which a USB drive under
// sustained error handling does -- can leave its entry in /proc/mounts pointing
// at a directory with nothing behind it, and the container creates an empty
// /mnt/<dev> for every optical device anyway. Returning one of those sent a
// salvage into /mnt/sr0 and failed two steps later about a missing BDMV, naming
// a path the user had never seen.
func TestAnEmptyMountPointIsNotADisc(t *testing.T) {
	err := hasContent(t.TempDir())
	if err == nil {
		t.Fatal("an empty directory was accepted as a mounted disc")
	}
	if !strings.Contains(err.Error(), "empty") {
		t.Errorf("error does not say what is wrong: %v", err)
	}
}

// A mounted disc always has something in it.
func TestADirectoryWithContentIsAccepted(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "BDMV", "STREAM"), 0o777); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := hasContent(dir); err != nil {
		t.Errorf("a disc tree was rejected: %v", err)
	}
}

// The check must not require BDMV specifically: DVDs carry VIDEO_TS, and a
// backup folder is a legitimate root too.
func TestContentNeedNotBeBlurayShaped(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "VIDEO_TS"), 0o777); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := hasContent(dir); err != nil {
		t.Errorf("a DVD tree was rejected: %v", err)
	}
}

// A path that cannot be read at all is reported as such rather than as empty.
func TestAMissingMountPointIsReported(t *testing.T) {
	err := hasContent(filepath.Join(t.TempDir(), "nothing-here"))
	if err == nil {
		t.Fatal("a missing directory was accepted")
	}
	if !strings.Contains(err.Error(), "cannot be read") {
		t.Errorf("error does not say it could not be read: %v", err)
	}
}
