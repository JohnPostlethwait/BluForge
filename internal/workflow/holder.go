package workflow

import (
	"context"
	"errors"

	"github.com/johnpostlethwait/bluforge/internal/ddrescue"
)

// DriveHolder holds a drive for work that reads it other than through
// makemkvcon: its disc's filesystem, or ddrescue. Implemented by the drives
// broker, the one owner of every drive.
//
// Calls made from inside fn with the ctx it is given run under the same hold,
// so a holder that mounts the disc and then rescues from it, or scans a link
// tree into it, keeps the drive throughout and never waits on itself.
type DriveHolder interface {
	// WithDiscRoot holds drive idx, mounts its disc and runs fn with the root.
	// unmount takes the mount down early; it is taken down when fn returns
	// regardless. A failure to mount is a *drives.MountError.
	WithDiscRoot(ctx context.Context, idx int, purpose string, fn func(ctx context.Context, root string, unmount func()) error) error
	// Rescue runs ddrescue from drive idx: the device itself when
	// opts.Source is empty, otherwise a file on the disc root a caller of
	// WithDiscRoot has open. runner nil means the real ddrescue.
	Rescue(ctx context.Context, idx int, runner ddrescue.Runner, opts ddrescue.Options, onProgress func(ddrescue.Progress)) error
	// DiscSize returns the size of the disc in drive idx.
	DiscSize(ctx context.Context, idx int) (int64, error)
}

// errNoHolder reports a drive read asked of an orchestrator built without one.
var errNoHolder = errors.New("workflow: no drive holder to read the disc through")

// withDiscRoot runs fn holding drive idx with its disc mounted.
func (o *Orchestrator) withDiscRoot(ctx context.Context, idx int, purpose string, fn func(ctx context.Context, root string, unmount func()) error) error {
	if o.holder == nil {
		return errNoHolder
	}
	return o.holder.WithDiscRoot(ctx, idx, purpose, fn)
}
