package workflow

import (
	"context"
	"errors"
	"os"

	"github.com/johnpostlethwait/bluforge/internal/ddrescue"
	"github.com/johnpostlethwait/bluforge/internal/drives"
)

// rootHolder stands in for the drives broker: open plays the mount, and
// device is the file a whole-disc rescue reads.
type rootHolder struct {
	open   func(device string) (string, func(), error)
	device string
}

func holderFor(open func(string) (string, func(), error)) *rootHolder {
	return &rootHolder{open: open, device: "/dev/sr0"}
}

func (h *rootHolder) WithDiscRoot(ctx context.Context, _ int, _ string, fn func(ctx context.Context, root string, unmount func()) error) error {
	root, cleanup, err := h.open(h.device)
	if err != nil {
		return &drives.MountError{Err: err}
	}
	defer cleanup()
	return fn(ctx, root, cleanup)
}

func (h *rootHolder) Rescue(ctx context.Context, _ int, runner ddrescue.Runner, opts ddrescue.Options, onProgress func(ddrescue.Progress)) error {
	if runner == nil {
		return errors.New("test holder: no rescuer")
	}
	if opts.Source == "" {
		opts.Source = h.device
	}
	return ddrescue.Rescue(ctx, runner, opts, onProgress)
}

func (h *rootHolder) DiscSize(context.Context, int) (int64, error) {
	fi, err := os.Stat(h.device)
	if err != nil {
		return 0, err
	}
	return fi.Size(), nil
}
