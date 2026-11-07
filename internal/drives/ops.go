package drives

import (
	"context"
	"fmt"

	"github.com/johnpostlethwait/bluforge/internal/ddrescue"
	"github.com/johnpostlethwait/bluforge/internal/makemkv"
)

// MountError reports a disc that could not be mounted.
type MountError struct{ Err error }

func (e *MountError) Error() string { return "mount the disc: " + e.Err.Error() }
func (e *MountError) Unwrap() error { return e.Err }

// WithDiscRoot holds drive idx, mounts its disc, and runs fn with its root.
// unmount takes the mount down early; it comes down when fn returns anyway.
func (b *Broker) WithDiscRoot(ctx context.Context, idx int, purpose string, fn func(ctx context.Context, root string, unmount func()) error) error {
	return b.Do(ctx, idx, purpose, func(ctx context.Context, d *Drive) error {
		root, unmount, err := d.OpenRoot(ctx)
		if err != nil {
			return &MountError{Err: err}
		}
		return fn(ctx, root, unmount)
	})
}

// Rescue runs ddrescue from drive idx; see Drive.Rescue.
func (b *Broker) Rescue(ctx context.Context, idx int, runner ddrescue.Runner, opts ddrescue.Options, onProgress func(ddrescue.Progress)) error {
	return b.Do(ctx, idx, "rescue", func(ctx context.Context, d *Drive) error {
		return d.Rescue(ctx, runner, opts, onProgress)
	})
}

// DiscSize returns the size of the disc in drive idx.
func (b *Broker) DiscSize(ctx context.Context, idx int) (int64, error) {
	var size int64
	err := b.Do(ctx, idx, "measure the disc", func(_ context.Context, d *Drive) error {
		var err error
		size, err = d.Size()
		return err
	})
	return size, err
}

// The operations below are the broker's answers to the questions the rest of
// BluForge asks of a drive. Each takes the hold it needs for exactly as long
// as it runs.

// ScanDisc scans the disc in drive idx.
func (b *Broker) ScanDisc(ctx context.Context, idx int) (*makemkv.DiscScan, error) {
	return b.ScanDiscWithProgress(ctx, idx, nil)
}

// ScanDiscWithProgress scans the disc in drive idx, reporting as it goes.
func (b *Broker) ScanDiscWithProgress(ctx context.Context, idx int, onEvent func(makemkv.Event)) (*makemkv.DiscScan, error) {
	var scan *makemkv.DiscScan
	err := b.Do(ctx, idx, "scan", func(ctx context.Context, d *Drive) error {
		var err error
		scan, err = d.Scan(ctx, onEvent)
		return err
	})
	return scan, err
}

// ScanSource scans a drive or a copy of a disc.
func (b *Broker) ScanSource(ctx context.Context, src makemkv.Source) (*makemkv.DiscScan, error) {
	return b.ScanSourceWithProgress(ctx, src, nil)
}

// ScanSourceWithProgress scans a drive or a copy, reporting as it goes.
//
// A copy on disk reads no drive. A link tree reads its drive through a mount,
// so it holds that drive and mounts the disc for the length of the scan; the
// links resolve because the mount point does not move.
func (b *Broker) ScanSourceWithProgress(ctx context.Context, src makemkv.Source, onEvent func(makemkv.Event)) (*makemkv.DiscScan, error) {
	var scan *makemkv.DiscScan
	err := b.route(ctx, src, "scan", func(ctx context.Context, d *Drive, src makemkv.Source) error {
		var err error
		if d != nil && src.IsDisc() {
			scan, err = d.Scan(ctx, onEvent)
			return err
		}
		scan, err = b.eng.ScanSourceWithProgress(ctx, src, onEvent)
		return err
	})
	return scan, err
}

// StartRip rips one title from a drive or a copy.
func (b *Broker) StartRip(ctx context.Context, src makemkv.Source, titleID int, expectSource, outputDir string, onEvent func(makemkv.Event), sel *makemkv.SelectionOpts) error {
	return b.route(ctx, src, fmt.Sprintf("rip title %d", titleID), func(ctx context.Context, d *Drive, src makemkv.Source) error {
		if d != nil && src.IsDisc() {
			return d.Rip(ctx, titleID, expectSource, outputDir, onEvent, sel)
		}
		return b.eng.StartRip(ctx, src, titleID, expectSource, outputDir, onEvent, sel)
	})
}

// Backup copies the disc in drive idx to destDir.
func (b *Broker) Backup(ctx context.Context, idx int, destDir string, onEvent func(makemkv.Event)) error {
	return b.Do(ctx, idx, "backup", func(ctx context.Context, d *Drive) error {
		return d.Backup(ctx, destDir, onEvent)
	})
}

// Identify reports drive idx and the disc in it.
func (b *Broker) Identify(ctx context.Context, idx int) (makemkv.DriveInfo, error) {
	var info makemkv.DriveInfo
	err := b.Do(ctx, idx, "identify", func(ctx context.Context, d *Drive) error {
		var err error
		info, err = d.Identify(ctx)
		return err
	})
	return info, err
}

// DiscLabelForDrive returns the disc name drive idx reports, or "".
func (b *Broker) DiscLabelForDrive(ctx context.Context, idx int) string {
	info, err := b.Identify(ctx, idx)
	if err != nil {
		return ""
	}
	return info.DiscName
}

// DevicePathForDrive returns drive idx's device node, for display and records.
// It reads no drive.
func (b *Broker) DevicePathForDrive(_ context.Context, idx int) string {
	return b.DevicePath(idx)
}

// route runs fn under the hold src needs: the drive for a disc, the drive and a
// mount for a link tree, and for a copy on disk only what detached requires.
func (b *Broker) route(ctx context.Context, src makemkv.Source, purpose string, fn func(ctx context.Context, d *Drive, src makemkv.Source) error) error {
	switch {
	case src.IsDisc():
		return b.Do(ctx, src.DriveIndex, purpose, func(ctx context.Context, d *Drive) error {
			return fn(ctx, d, src)
		})
	case src.Linked:
		return b.Do(ctx, src.DriveIndex, purpose+" through links", func(ctx context.Context, d *Drive) error {
			if _, _, err := d.OpenRoot(ctx); err != nil {
				return fmt.Errorf("drives: mount the disc the links point into: %w", err)
			}
			return fn(ctx, d, src)
		})
	default:
		return b.detached(ctx, func(ctx context.Context) error {
			return fn(ctx, nil, src)
		})
	}
}
