package workflow

import (
	"context"
	"fmt"
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
func (o *Orchestrator) discIsDVD(ctx context.Context, driveIndex int) bool {
	dvd := false
	_ = o.withDiscRoot(ctx, driveIndex, "check the disc type", func(_ context.Context, root string, _ func()) error {
		if _, err := os.Stat(filepath.Join(root, "BDMV")); err == nil {
			return nil
		}
		_, err := os.Stat(filepath.Join(root, "VIDEO_TS"))
		dvd = err == nil
		return nil
	})
	return dvd
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
	if o.holder == nil {
		return nil, errNoHolder
	}
	size, err := o.holder.DiscSize(ctx, req.DriveIndex)
	if err != nil {
		return nil, fmt.Errorf("salvage: read the size of the disc in drive %d: %w", req.DriveIndex, err)
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
// the duration. The hold ends before the image is scanned.
func (o *Orchestrator) imageDisc(
	ctx context.Context,
	req SalvageRequest,
	dir string,
	size int64,
	report func(phase string, percent int, message string),
) (int64, error) {
	report("rescuing", 0, "Copying the disc")
	var unrecovered int64
	lastPct := -1
	// The rescue holds the drive for its whole length; Source left empty is
	// the device itself.
	err := o.holder.Rescue(ctx, req.DriveIndex, o.rescuer, ddrescue.Options{
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
