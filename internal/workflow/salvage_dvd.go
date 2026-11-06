package workflow

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/johnpostlethwait/bluforge/internal/ddrescue"
	"github.com/johnpostlethwait/bluforge/internal/fsutil"
	"github.com/johnpostlethwait/bluforge/internal/makemkv"
)

// dvdImageName is the disc image a DVD salvage writes into its scratch
// directory.
const dvdImageName = "disc.iso"

// discIsDVD reports whether the disc in the drive is a DVD.
//
// A DVD carries VIDEO_TS and no BDMV. Anything that cannot be read, or reads as
// neither, is left to the Blu-ray path, which reports what it finds.
func (o *Orchestrator) discIsDVD(devicePath string) bool {
	if o.openDiscRoot == nil {
		return false
	}
	root, cleanup, err := o.openDiscRoot(devicePath)
	if err != nil {
		return false
	}
	defer cleanup()
	if _, err := os.Stat(filepath.Join(root, "BDMV")); err == nil {
		return false
	}
	_, err = os.Stat(filepath.Join(root, "VIDEO_TS"))
	return err == nil
}

// copySource returns the source a scratch copy is read from: the disc image a
// DVD salvage wrote, or the folder any other copy is.
func copySource(dir string) makemkv.Source {
	image := filepath.Join(dir, dvdImageName)
	if _, err := os.Stat(image); err == nil {
		return makemkv.ISOSource(image)
	}
	return makemkv.CopySource(dir)
}

// salvageDVD images a damaged DVD whole, zero-filling what cannot be read.
//
// The image keeps the disc's CSS encryption; MakeMKV decrypts it from the
// image as it would from the drive. Reading the raw device also sidesteps the
// kernel's UDF reader, which MakeMKV reported it could not open VOBs through
// on the first DVD this was built for.
//
// ddrescue's map sits beside the image, so a paused or interrupted salvage
// continues from where it stopped.
func (o *Orchestrator) salvageDVD(
	ctx context.Context,
	req SalvageRequest,
	dir string,
	source makemkv.Source,
	report func(phase string, percent int, message string),
) (*RecoveredDisc, error) {
	if req.DevicePath == "" {
		return nil, fmt.Errorf("salvage: no device path for drive %d", req.DriveIndex)
	}
	size, err := deviceSize(req.DevicePath)
	if err != nil {
		return nil, fmt.Errorf("salvage: read the size of %s: %w", req.DevicePath, err)
	}

	unrecovered, err := o.imageDisc(ctx, req, dir, size, report)
	if err != nil {
		return nil, err
	}
	if err := fsutil.NormalizeTree(dir); err != nil {
		slog.Warn("salvage: could not normalise the disc image's permissions", "dir", dir, "error", err)
	}

	report("verifying", 90, "")
	scan, err := o.backupper.ScanSource(ctx, source)
	if err != nil {
		return nil, fmt.Errorf("salvage: scan the disc image: %w", err)
	}
	if len(scan.Titles) == 0 {
		return nil, fmt.Errorf("salvage: the disc image has no readable titles")
	}

	report("done", 100, "")
	slog.Info("salvage: DVD image complete", "drive_index", req.DriveIndex, "dir", dir,
		"titles", len(scan.Titles), "unrecovered_bytes", unrecovered)

	return &RecoveredDisc{
		DiscLabel: req.DiscLabel,
		Source:    source,
		Dir:       dir,
		Scan:      scan,
		Salvaged:  true,
		// ddrescue reports its totals from the map, so a resumed run still
		// knows what earlier runs lost.
		Measured:    true,
		Unrecovered: unrecovered,
	}, nil
}

// imageDisc runs ddrescue from the drive into the image, holding the drive for
// the duration. The lock is released before the scan that follows, which takes
// the same executor mutex.
func (o *Orchestrator) imageDisc(
	ctx context.Context,
	req SalvageRequest,
	dir string,
	size int64,
	report func(phase string, percent int, message string),
) (int64, error) {
	if locker, ok := o.backupper.(DriveLocker); ok {
		defer locker.ClaimDrive(req.DriveIndex)()
	} else {
		slog.Warn("salvage: cannot claim the drive; the poller will slow the rescue")
	}

	report("rescuing", 0, "Copying the disc")
	var unrecovered int64
	lastPct := -1
	err := ddrescue.Rescue(ctx, o.rescuer, ddrescue.Options{
		Source:  req.DevicePath,
		Dest:    filepath.Join(dir, dvdImageName),
		MapFile: filepath.Join(dir, salvageMapDir, dvdImageName+".map"),
		Retries: req.Retries,
	}, func(p ddrescue.Progress) {
		if p.BytesBad > 0 {
			unrecovered = p.BytesBad
		}
		if p.Line != "" {
			slog.Info("salvage: imaging", "device", req.DevicePath, "status", p.Line)
		}
		if p.BytesRescued > 0 && size > 0 {
			pct := int(p.BytesRescued * 100 / size)
			if pct != lastPct {
				lastPct = pct
				report("rescuing", pct, fmt.Sprintf("Copying the disc — %s of %s recovered",
					humanBytes(p.BytesRescued), humanBytes(size)))
			}
		}
	})
	if err != nil {
		return unrecovered, fmt.Errorf("salvage: %w", err)
	}
	return unrecovered, nil
}

// deviceSize returns the size of a block device, or of a regular file standing
// in for one.
func deviceSize(path string) (int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	return f.Seek(0, io.SeekEnd)
}
