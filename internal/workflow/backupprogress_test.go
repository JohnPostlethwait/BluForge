package workflow

import (
	"os"
	"path/filepath"
	"testing"
)

// makemkvcon reported 0% then 100% within 100ms of starting a 95GB backup, so
// its own progress numbers cannot be used to drive a progress bar for the copy.
// Bytes on disk against the size measured from the disc can.
func TestBackupPercent(t *testing.T) {
	tests := []struct {
		name    string
		written int64
		needed  int64
		want    int
	}{
		{"nothing yet", 0, 100, 0},
		{"halfway", 50, 100, 50},
		{"almost done", 99, 100, 99},
		// Never report 100 from a size estimate: the estimate carries headroom,
		// and a bar sitting at 100 while the copy continues is worse than one
		// that stops just short.
		{"at the estimate", 100, 100, 99},
		{"over the estimate", 150, 100, 99},
		{"unknown size", 50, 0, 0},
		{"negative guard", -5, 100, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := backupPercent(tt.written, tt.needed); got != tt.want {
				t.Errorf("backupPercent(%d, %d) = %d, want %d", tt.written, tt.needed, got, tt.want)
			}
		})
	}
}

// The size is read from the growing backup directory, so it has to count files
// laid out the way makemkvcon writes them.
func TestTreeSizeCountsNestedFiles(t *testing.T) {
	root := t.TempDir()
	streamDir := filepath.Join(root, "BDMV", "STREAM")
	if err := os.MkdirAll(streamDir, 0o777); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(streamDir, "00001.m2ts"), make([]byte, 4096), 0o666); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "index.bdmv"), make([]byte, 100), 0o666); err != nil {
		t.Fatalf("write: %v", err)
	}

	got, err := treeSize(root)
	if err != nil {
		t.Fatalf("treeSize: %v", err)
	}
	if got != 4196 {
		t.Errorf("treeSize = %d, want 4196", got)
	}
}
