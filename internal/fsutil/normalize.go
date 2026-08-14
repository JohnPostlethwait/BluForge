package fsutil

import (
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
)

// FileMode is the mode files are requested with, masked by the umask like
// DirMode. 0o666 rather than 0o777 because nothing BluForge writes to the
// output tree is executable.
const FileMode = 0o666

// NormalizeTree applies the umask policy to everything under root.
//
// It exists because BluForge is not the only thing writing to the output tree.
// makemkvcon creates the disc structure during a backup and ddrescue creates
// its output and map files, and neither consults our umask for the directories
// it makes: a backup came back with AACS, BDMV and CERTIFICATE at 0755 inside a
// scratch directory we had created 0777. On a share whose clients map to the
// media group that is read-only, so the copy could neither be written to nor
// cleaned up over SMB, and only root could remove it.
//
// Symlinks are left alone. A recovered disc can be presented as a tree of links
// into the read-only disc mount, and chmod follows a symlink on Linux -- there
// is no lchmod -- so normalising one would reach through to the disc.
//
// Permission failures are counted and reported once rather than per entry: on a
// CIFS mount without unix extensions every chmod returns EPERM while the
// mount's own dir_mode and file_mode govern anyway, and a tree of ten thousand
// files would otherwise produce ten thousand warnings. For the same reason this
// never fails the operation that called it -- the copy is already on disk and
// is worth more than its mode bits.
func NormalizeTree(root string) error {
	if root == "" {
		return nil
	}

	dirMode := os.FileMode(DirMode &^ Umask())
	fileMode := os.FileMode(FileMode &^ Umask())

	var failed int
	var firstErr error

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// An entry we cannot read is not one we can chmod. Keep walking:
			// the rest of the tree is still worth fixing.
			if firstErr == nil {
				firstErr = err
			}
			failed++
			return nil
		}
		// Type() reports the mode bits without following the link.
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}

		mode := fileMode
		if d.IsDir() {
			mode = dirMode
		}
		if chmodErr := os.Chmod(path, mode); chmodErr != nil {
			if firstErr == nil {
				firstErr = chmodErr
			}
			failed++
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}

	if failed > 0 {
		slog.Warn("could not set permissions on part of a tree; leaving them as written",
			"root", root, "entries", failed, "first_error", firstErr)
	}
	return nil
}
