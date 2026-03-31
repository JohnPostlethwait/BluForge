package organizer

import (
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Organizer builds destination paths and moves files atomically.
type Organizer struct{}

// New returns a ready Organizer.
func New() *Organizer {
	return &Organizer{}
}

// BuildPath returns a sanitized path: <dirName>/<fileName>.mkv
// dirName is typically the matched media title or the disc name.
// fileName is the title name (e.g. "S01E02 - Male Unbonding") or source file.
// The .mkv extension is always appended; any existing extension on fileName is
// stripped first.
func (o *Organizer) BuildPath(dirName, fileName string) string {
	dir := SanitizeFilename(dirName)
	if dir == "" {
		dir = "Unknown"
	}
	base := SanitizeFilename(strings.TrimSuffix(fileName, filepath.Ext(fileName)))
	if base == "" {
		base = "title"
	}
	return filepath.Join(dir, base+".mkv")
}

// AtomicMove moves src to dst, creating parent directories as needed.
// It tries os.Rename first (atomic on the same filesystem) and falls back to
// a copy-then-delete for cross-device moves.
func AtomicMove(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}

	if err := os.Rename(src, dst); err == nil {
		return nil
	}

	// Cross-device fallback: copy then delete.
	if err := copyFile(src, dst); err != nil {
		return err
	}
	return os.Remove(src)
}

// FileExists reports whether path exists on disk.
func FileExists(path string) bool {
	info, err := os.Lstat(path)
	if err != nil {
		return false
	}
	// Reject symlinks to prevent symlink-based path attacks.
	return info.Mode()&os.ModeSymlink == 0
}

// copyFile copies the content of src to dst, preserving permissions.
// It refuses to follow symlinks to prevent symlink-based attacks.
func copyFile(src, dst string) error {
	info, err := os.Lstat(src)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return os.ErrPermission
	}

	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode())
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err = io.Copy(out, in); err != nil {
		return err
	}
	return out.Close()
}
