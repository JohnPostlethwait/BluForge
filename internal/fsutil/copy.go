package fsutil

import (
	"io"
	"os"
	"path/filepath"
)

// CopyFile copies the contents of src to dst, creating dst's parent directory
// and preserving src's file mode.
//
// It refuses to follow a symlink at src: a symlink left at a path we copy from
// could otherwise redirect the read somewhere it should not reach. Both callers
// benefit — the organizer copies ripped titles into the media tree, and the rip
// failure path copies makemkvcon's debug log out of a temp dir before it is
// deleted.
func CopyFile(src, dst string) error {
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

	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
