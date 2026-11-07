package makemkv

import (
	"context"
	"errors"
)

// ErrNoDevice reports a disc source with no device node. A drive is read only
// through its holder in internal/drives, which fills the device in.
var ErrNoDevice = errors.New("makemkv: disc source has no device; read drives through internal/drives")

// resolve returns the source argument to give makemkvcon and the context to
// run it under.
//
// The executor holds no locks. Which commands may run, and when, is decided by
// internal/drives, the one owner of every drive: it calls the executor only
// while it holds the drive a command reads, or every drive when the command
// would see them all.
//
// With isolation, a disc command runs with its own drive alone in /dev, and
// anything read through the filesystem with no drive at all. Without
// it, makemkvcon runs directly and sees every drive; internal/drives then holds
// every drive for it.
func (e *Executor) resolve(ctx context.Context, src Source) (string, context.Context, error) {
	switch {
	case src.IsDisc():
		if src.Device == "" {
			return "", ctx, ErrNoDevice
		}
		if e.isolate {
			return src.Arg(), withIsolation(ctx, src.Device), nil
		}
		return src.Arg(), ctx, nil
	case e.isolate:
		// A copy or a link tree is read through the filesystem, never a device
		// node, so its makemkvcon needs no drive in sight.
		return src.Arg(), withIsolation(ctx, NoDrive), nil
	default:
		return src.Arg(), ctx, nil
	}
}

// Isolated reports whether each makemkvcon command is confined to the drive it
// reads. internal/drives runs work on different drives at once only when it is.
func (e *Executor) Isolated() bool { return e.isolate }
