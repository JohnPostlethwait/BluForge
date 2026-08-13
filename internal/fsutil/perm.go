// Package fsutil centralises the permission policy for directories BluForge
// creates in the media output tree.
//
// The rule is that the process umask is the only knob. Directories are created
// with mode 0o777 and the kernel masks it down, so an operator sets UMASK once
// on the container and every rip directory, scratch directory and organised
// media directory lands on the same permissions.
//
// os.MkdirTemp is the exception that motivated this package: it hardcodes mode
// 0o700 (see os/tempfile.go), and a umask can only clear bits, never add them.
// With the UMASK=0000 that the Unraid compose file shipped, every other
// directory came out drwxrwxrwx while the .rip-* scratch directories came out
// drwx------ owned by the container user, leaving orphaned temp directories
// that nobody but root could delete. MkdirTemp below restores them to the same
// policy as everything else.
package fsutil

import (
	"log/slog"
	"os"
	"sync/atomic"
	"syscall"
)

// DirMode is the mode directories are requested with. The umask decides what
// actually lands on disk; see the package comment.
const DirMode = 0o777

// umask holds the process umask captured by CaptureUmask. It defaults to the
// conventional 0o022 so that code paths which never call CaptureUmask (tests,
// cmd/discprobe) still produce sane 0o755 directories rather than 0o777.
var umask atomic.Int32

func init() { umask.Store(0o022) }

// CaptureUmask reads the process umask and remembers it for MkdirTemp.
//
// Reading the umask requires setting it, so this must be called once from main
// before any goroutine that touches the filesystem starts: for the duration of
// the two syscalls the process umask is 0, and a concurrent create would slip
// through unmasked.
func CaptureUmask() int {
	m := syscall.Umask(0)
	syscall.Umask(m)
	umask.Store(int32(m))
	return m
}

// Umask returns the umask captured by CaptureUmask.
func Umask() int { return int(umask.Load()) }

// SetUmask sets the mask MkdirTemp applies without touching the process umask.
// CaptureUmask is what production code wants; this exists for tests, which must
// pin a mask without mutating process-wide state their neighbours share.
func SetUmask(m int) { umask.Store(int32(m)) }

// MkdirTemp creates a temporary directory like os.MkdirTemp, but applies the
// same umask-governed permissions as every other directory BluForge creates
// instead of os.MkdirTemp's hardcoded 0o700.
//
// The returned error covers only the failure to create the directory. A failed
// chmod is logged and swallowed deliberately: on a CIFS/SMB mount without unix
// extensions the mount's dir_mode governs permissions and chmod returns EPERM,
// and refusing to rip onto such a share would be a worse bug than permissions
// the mount was going to override anyway. Returning both a usable directory and
// a non-nil error would also invite the caller to abandon a directory it had
// just created, which is the leak this package exists to stop.
func MkdirTemp(dir, pattern string) (string, error) {
	name, err := os.MkdirTemp(dir, pattern)
	if err != nil {
		return "", err
	}
	mode := os.FileMode(DirMode &^ Umask())
	if err := os.Chmod(name, mode); err != nil {
		slog.Warn("could not set permissions on temp dir; leaving them as created",
			"dir", name, "mode", mode, "err", err)
	}
	return name, nil
}
