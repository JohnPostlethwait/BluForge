package fsutil

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// pinUmask fixes the mask MkdirTemp applies for the duration of the test,
// without touching the process umask that the rest of the suite shares.
func pinUmask(t *testing.T, m int) {
	t.Helper()
	prev := Umask()
	SetUmask(m)
	t.Cleanup(func() { SetUmask(prev) })
}

// TestMkdirTempAppliesUmask is the regression test for the .rip-* directories
// landing at 0o700: os.MkdirTemp hardcodes that mode and a umask cannot widen
// it, so every temp dir was private to the container user while the rest of the
// output tree was group- and world-accessible.
func TestMkdirTempAppliesUmask(t *testing.T) {
	tests := []struct {
		name  string
		umask int
		want  os.FileMode
	}{
		{name: "shared group writable", umask: 0o002, want: 0o775},
		{name: "no mask", umask: 0o000, want: 0o777},
		{name: "group read only", umask: 0o022, want: 0o755},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pinUmask(t, tt.umask)

			dir, err := MkdirTemp(t.TempDir(), ".rip-")
			if err != nil {
				t.Fatalf("MkdirTemp: %v", err)
			}

			info, err := os.Stat(dir)
			if err != nil {
				t.Fatalf("stat: %v", err)
			}
			if got := info.Mode().Perm(); got != tt.want {
				t.Errorf("mode = %04o, want %04o", got, tt.want)
			}
		})
	}
}

// The bug in the field: a directory nobody but root could remove. Group
// membership has to be enough to delete an orphaned rip directory.
func TestMkdirTempIsGroupWritable(t *testing.T) {
	pinUmask(t, 0o002)

	dir, err := MkdirTemp(t.TempDir(), ".rip-")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}

	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm&0o070 != 0o070 {
		t.Errorf("mode = %04o, want group rwx set so the share group can clean up", perm)
	}
}

func TestMkdirTempCreatesUsableDir(t *testing.T) {
	pinUmask(t, 0o002)

	dir, err := MkdirTemp(t.TempDir(), "t0-")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}

	if err := os.WriteFile(filepath.Join(dir, "title.mkv"), []byte("x"), 0o666); err != nil {
		t.Fatalf("write into temp dir: %v", err)
	}
}

func TestMkdirTempPropagatesCreateError(t *testing.T) {
	if _, err := MkdirTemp(filepath.Join(t.TempDir(), "does-not-exist"), ".rip-"); err == nil {
		t.Fatal("expected an error when the parent directory is missing")
	}
}

func TestCaptureUmaskRestoresProcessUmask(t *testing.T) {
	// Set a known umask, confirm CaptureUmask reports it and leaves the process
	// umask where it found it rather than at the 0 it needs mid-read.
	const want = 0o027
	prev := syscall.Umask(want)
	defer syscall.Umask(prev)

	if got := CaptureUmask(); got != want {
		t.Errorf("CaptureUmask() = %04o, want %04o", got, want)
	}
	if got := Umask(); got != want {
		t.Errorf("Umask() = %04o, want %04o", got, want)
	}

	// syscall.Umask returns the previous value, so this both checks and restores.
	if got := syscall.Umask(want); got != want {
		t.Errorf("process umask = %04o after CaptureUmask, want %04o", got, want)
	}
}
