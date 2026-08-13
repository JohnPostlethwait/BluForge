package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/johnpostlethwait/bluforge/internal/ddrescue"
	"github.com/johnpostlethwait/bluforge/internal/makemkv"
	"github.com/johnpostlethwait/bluforge/internal/organizer"
)

// ErrSalvageInProgress reports that a drive is already being salvaged.
var ErrSalvageInProgress = errors.New("a salvage is already running")

// streamDir is where the video lives. Salvage compares and repairs only these
// files: they are where damage costs the user something, and the smaller files
// around them can legitimately differ between a disc and a backup of it.
const streamDir = "BDMV/STREAM"

// DriveLocker claims a drive for work that does not run through the MakeMKV
// executor. Optional: a backupper without it simply competes with the poller.
type DriveLocker interface {
	LockDrive()
	UnlockDrive()
}

// SalvageRequest describes a disc to recover from physical damage.
type SalvageRequest struct {
	DriveIndex int
	DevicePath string
	DiscLabel  string
	OutputDir  string
	// Retries and Timeout bound ddrescue's scraping phase. Zero takes the
	// package defaults.
	Retries    int
	OnProgress func(phase string, percent int, message string)
}

// Salvage recovers a physically damaged disc into a folder MakeMKV can rip.
//
// A scratch makes a title unrippable outright: MakeMKV abandons a title on an
// unrecoverable read rather than writing a file with a gap in it. Rambo's
// feature has a bad patch 48GB into a 64GB stream — under a second of video —
// and no amount of retrying gets past it, because the data is not there.
//
// The sequence is MakeMKV's own forum method. Back the disc up without
// decrypting, which captures discatt.dat and lets MakeMKV decrypt later from
// the folder; the backup gets everything it can and stops when it cannot.
// Then ddrescue whatever it left short, zero-filling the unreadable parts so
// the reads succeed. What comes out is the film with a glitch, which is what a
// player would have shown all along.
func (o *Orchestrator) Salvage(ctx context.Context, req SalvageRequest) (*RecoveredDisc, error) {
	if o.backupper == nil {
		return nil, fmt.Errorf("salvage: no backupper configured")
	}
	if req.OutputDir == "" {
		return nil, fmt.Errorf("salvage: no output directory configured")
	}

	report := func(phase string, percent int, message string) {
		if req.OnProgress != nil {
			req.OnProgress(phase, percent, message)
		}
	}

	scratchRoot := filepath.Join(req.OutputDir, ScratchDirName)
	if err := os.MkdirAll(scratchRoot, 0o777); err != nil {
		return nil, fmt.Errorf("salvage: create scratch root %s: %w", scratchRoot, err)
	}
	// Prefer a scratch this disc already has. The slug hashes the device path,
	// and optical devices renumber -- Rambo moved from sr1 to sr2 mid-session --
	// so computing a fresh one would orphan an hour of recovered data and start
	// the disc over.
	dir, resuming := FindSalvageScratch(req.OutputDir, req.DiscLabel)
	if !resuming {
		dir = filepath.Join(scratchRoot, scratchSlug(req.DiscLabel, req.DevicePath))
	} else {
		slog.Info("salvage: resuming into an existing copy", "dir", dir)
	}
	if err := os.MkdirAll(dir, 0o777); err != nil {
		return nil, fmt.Errorf("salvage: create backup dir %s: %w", dir, err)
	}

	// 1. Back up what the disc will give. A backup that dies partway is not a
	// failure here — it is the starting point, and its partial tree is exactly
	// what ddrescue is about to finish.
	report("backing-up", 0, "")
	lastPct := -1
	if err := o.backupper.Backup(ctx, req.DriveIndex, dir, func(ev makemkv.Event) {
		logSalvageEvent(ev)
		// MakeMKV reports progress throughout a copy that runs for the better
		// part of an hour. Discarding it left a spinner and a sentence, which
		// is indistinguishable from a stall.
		if ev.Type == "PRGV" && ev.Progress != nil && ev.Progress.Max > 0 {
			if pct := ev.Progress.Total * 100 / ev.Progress.Max; pct != lastPct {
				lastPct = pct
				report("backing-up", pct, "")
			}
		}
	}); err != nil {
		slog.Warn("salvage: backup did not complete; continuing with what it copied",
			"drive_index", req.DriveIndex, "dir", dir, "error", err)
		report("backing-up", lastPct, "The copy stopped at the damage, as expected")
	}

	// 2. Read the disc as a filesystem so the damaged streams can be compared
	// against what the backup managed to take.
	root, cleanup, err := o.openDiscRoot(req.DevicePath)
	if err != nil {
		return nil, fmt.Errorf("salvage: open disc: %w", err)
	}
	defer cleanup()

	// A mountpoint that exists but holds no BDMV is not the disc. The container
	// creates an empty /mnt/<dev> for every optical device it finds, and reading
	// one of those produced "no such file or directory" two steps later, naming
	// a path the user had never heard of.
	if _, err := os.Stat(filepath.Join(root, streamDir)); err != nil {
		return nil, fmt.Errorf("salvage: %s does not look like a disc: no %s in it "+
			"(is the disc in this drive?)", root, streamDir)
	}

	short, err := incompleteStreams(root, dir)
	if err != nil {
		return nil, err
	}
	if len(short) == 0 {
		slog.Info("salvage: the backup is already complete; nothing to rescue", "dir", dir)
	}

	// 3. Fill what the backup could not take.
	//
	// ddrescue is a separate process and does not go through the MakeMKV
	// executor, so nothing otherwise stops the five-second drive poller reading
	// the same drive underneath it. That contention took a rescue from 14 MB/s
	// down to 2.4 MB/s, turning an hour into nine and a half.
	if len(short) > 0 {
		if locker, ok := o.backupper.(DriveLocker); ok {
			locker.LockDrive()
			defer locker.UnlockDrive()
			slog.Info("salvage: holding the drive for the rescue", "files", len(short))
		} else {
			slog.Warn("salvage: cannot claim the drive; the poller will slow the rescue")
		}
	}

	var unrecovered int64
	for _, s := range short {
		name := filepath.Base(s.name)
		report("rescuing", 0, fmt.Sprintf("Patching %s", name))
		lastRescuePct := -1

		err := ddrescue.Rescue(ctx, o.rescuer, ddrescue.Options{
			Source:      filepath.Join(root, s.name),
			Dest:        filepath.Join(dir, s.name),
			MapFile:     filepath.Join(dir, filepath.Base(s.name)+".map"),
			StartOffset: s.have,
			Retries:     req.Retries,
		}, func(p ddrescue.Progress) {
			if p.BytesBad > 0 {
				unrecovered = p.BytesBad
			}
			if p.Line != "" {
				slog.Info("salvage: rescuing", "file", s.name, "status", p.Line)
			}
			// ddrescue reports what it has recovered; turning that into a
			// percentage of the file is the only honest measure of how far
			// through an hour-long rescue it is.
			if p.BytesRescued > 0 && s.want > 0 {
				pct := int(p.BytesRescued * 100 / s.want)
				if pct != lastRescuePct {
					lastRescuePct = pct
					report("rescuing", pct, fmt.Sprintf("Patching %s — %s of %s recovered",
						name, humanBytes(p.BytesRescued), humanBytes(s.want)))
				}
			}
		})
		if err != nil {
			return nil, fmt.Errorf("salvage: %w", err)
		}
	}

	// 4. Read the repaired tree. Reads now succeed everywhere, including across
	// the zero-filled gaps, so MakeMKV has no reason to abandon the title.
	report("verifying", 90, "Checking what can be ripped from the repaired copy")
	src := makemkv.FileSource(dir)
	scan, err := o.backupper.ScanSource(ctx, src)
	if err != nil {
		return nil, fmt.Errorf("salvage: scan the repaired copy: %w", err)
	}
	if len(scan.Titles) == 0 {
		return nil, fmt.Errorf("salvage: the repaired copy still has no readable titles")
	}

	report("done", 100, "")
	slog.Info("salvage: complete", "drive_index", req.DriveIndex, "dir", dir,
		"titles", len(scan.Titles), "rescued_files", len(short))

	return &RecoveredDisc{
		Source:      src,
		Dir:         dir,
		Scan:        scan,
		Salvaged:    true,
		Unrecovered: unrecovered,
	}, nil
}

// shortStream is a stream file the backup did not copy in full.
type shortStream struct {
	// name is relative to the disc root, e.g. "BDMV/STREAM/00000.m2ts".
	name string
	// have is how many bytes the backup already holds, and where a rescue
	// resumes from.
	have int64
	want int64
}

// incompleteStreams compares the disc's stream files against the backup's.
//
// Deriving the work from the two trees rather than from a record of which reads
// failed means a backup that died early is handled the same as one that
// finished with holes: anything short gets rescued, whatever the reason.
func incompleteStreams(discRoot, backupDir string) ([]shortStream, error) {
	streams := filepath.Join(discRoot, streamDir)
	entries, err := os.ReadDir(streams)
	if err != nil {
		return nil, fmt.Errorf("salvage: read %s: %w", streams, err)
	}

	var short []shortStream
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".m2ts") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		name := filepath.Join(streamDir, e.Name())

		var have int64
		if st, err := os.Stat(filepath.Join(backupDir, name)); err == nil {
			have = st.Size()
		}
		if have >= info.Size() {
			continue
		}
		short = append(short, shortStream{name: name, have: have, want: info.Size()})
		slog.Info("salvage: stream is short in the backup",
			"file", name, "have", have, "want", info.Size())
	}
	return short, nil
}

// logSalvageEvent records MakeMKV's own account of the backup, which is the
// only running commentary on an operation that takes the better part of an hour.
func logSalvageEvent(ev makemkv.Event) {
	if ev.Type != "MSG" || ev.Message == nil {
		return
	}
	slog.Info("salvage: makemkvcon message", "code", ev.Message.Code, "text", ev.Message.Text)
}

// salvageDeadline is the wall-clock ceiling on a salvage.
//
// ddrescue's --timeout measures time since the last successful read, not total
// runtime, so a scraping phase that trickles out a few hundred bytes a second
// never trips it — the Rambo rescue would have run for another 76 minutes to
// recover a further few kilobytes. A real limit has to come from here.
const salvageDeadline = 6 * time.Hour

// StartSalvage runs a salvage in the background, returning as soon as it is
// under way.
//
// Salvage is never automatic. It produces a file MakeMKV would refuse to make,
// containing damaged video wherever the disc is unreadable, and that is a
// decision for the person who will watch it.
func (o *Orchestrator) StartSalvage(driveIndex int) error {
	outputDir := o.currentOutputDir()
	if outputDir == "" {
		return fmt.Errorf("salvage: no output directory configured")
	}
	if o.backupper == nil {
		return fmt.Errorf("salvage: no backupper configured")
	}
	if !o.beginSalvage(driveIndex) {
		return ErrSalvageInProgress
	}

	var devicePath string
	if loc, ok := o.scanner.(DeviceLocator); ok {
		devicePath = loc.DevicePathForDrive(context.Background(), driveIndex)
	}
	discLabel := discLabelFor(context.Background(), o.scanner, driveIndex, "")

	go func() {
		defer o.endSalvage(driveIndex)

		// Detached from any request, with a ceiling of its own: a salvage runs
		// for hours and must not be killed by a browser giving up, nor run
		// until the drive gives out.
		ctx, cancel := context.WithTimeout(context.Background(), salvageDeadline)
		defer cancel()

		// Publish the cancel so a pause can reach this run.
		o.recoveredMu.Lock()
		o.salvaging[driveIndex] = cancel
		o.recoveredMu.Unlock()

		o.setDriveState(driveIndex, "salvaging")
		defer o.setDriveState(driveIndex, "detected")

		rec, err := o.Salvage(ctx, SalvageRequest{
			DriveIndex: driveIndex,
			DevicePath: devicePath,
			DiscLabel:  discLabel,
			OutputDir:  outputDir,
			OnProgress: func(phase string, percent int, message string) {
				o.broadcastSalvage(driveIndex, phase, percent, message, 0)
			},
		})
		if err != nil {
			slog.Error("salvage: failed", "drive_index", driveIndex, "error", err)
			o.broadcastSalvage(driveIndex, "failed", 0, err.Error(), 0)
			return
		}

		o.registerRecovered(driveIndex, rec)
		o.cacheScan(driveIndex, rec.Scan)
		o.broadcastSalvage(driveIndex, "done", 100, "", rec.Unrecovered)
		slog.Info("salvage: disc is ready to rip",
			"drive_index", driveIndex, "titles", len(rec.Scan.Titles),
			"unrecovered_bytes", rec.Unrecovered)
	}()

	return nil
}

// beginSalvage claims the drive. A salvage and a recovery cannot run together:
// both copy the whole disc, and two at once would race for the drive and the
// scratch directory.
func (o *Orchestrator) beginSalvage(driveIndex int) bool {
	o.recoveredMu.Lock()
	defer o.recoveredMu.Unlock()
	if o.recovering[driveIndex] || o.salvaging[driveIndex] != nil {
		return false
	}
	if o.salvaging == nil {
		o.salvaging = make(map[int]context.CancelFunc)
	}
	// Replaced with the real cancel once the salvage goroutine has a context.
	o.salvaging[driveIndex] = func() {}
	return true
}

func (o *Orchestrator) endSalvage(driveIndex int) {
	o.recoveredMu.Lock()
	defer o.recoveredMu.Unlock()
	delete(o.salvaging, driveIndex)
}

// SalvageInProgress reports whether a drive is being salvaged.
//
// Exposed because a page that reconnects has missed every event so far, and a
// salvage runs for hours.
func (o *Orchestrator) SalvageInProgress(driveIndex int) bool {
	o.recoveredMu.Lock()
	defer o.recoveredMu.Unlock()
	return o.salvaging[driveIndex] != nil
}

// broadcastSalvage pushes a salvage phase to the page.
func (o *Orchestrator) broadcastSalvage(driveIndex int, phase string, percent int, message string, unrecovered int64) {
	if o.onBroadcast == nil {
		return
	}
	data, err := json.Marshal(map[string]any{
		"drive_index": driveIndex,
		"phase":       phase,
		"percent":     percent,
		"message":     message,
		"unrecovered": unrecovered,
	})
	if err != nil {
		slog.Error("salvage: could not marshal SSE payload", "error", err)
		return
	}
	o.onBroadcast("disc_salvage", string(data))
}

// FindSalvageScratch returns a scratch directory this disc has already been
// salvaged into, if one exists.
//
// Resuming matters: ddrescue keeps a map of what it has recovered, so a rescue
// that stopped an hour in continues rather than re-reading the disc. Matching on
// the disc label rather than the full slug is deliberate — the slug hashes the
// device path, and a drive that re-enumerates would otherwise strand the copy.
func FindSalvageScratch(outputDir, discLabel string) (string, bool) {
	if discLabel == "" || outputDir == "" {
		return "", false
	}
	prefix := organizer.SanitizeFilename(discLabel)
	if prefix == "" {
		return "", false
	}
	entries, err := os.ReadDir(filepath.Join(outputDir, ScratchDirName))
	if err != nil {
		return "", false
	}
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), prefix) {
			continue
		}
		return filepath.Join(outputDir, ScratchDirName, e.Name()), true
	}
	return "", false
}

// SalvageResumable reports whether a stopped salvage of this disc can be picked
// up where it left off, which is what lets the page offer to resume rather than
// to start again.
func (o *Orchestrator) SalvageResumable(discLabel string) bool {
	dir, ok := FindSalvageScratch(o.currentOutputDir(), discLabel)
	if !ok {
		return false
	}
	// A map file is what makes it a resume rather than a restart.
	maps, _ := filepath.Glob(filepath.Join(dir, "*.map"))
	return len(maps) > 0
}

// CancelSalvage stops a running salvage, keeping everything it has recovered.
//
// Presented as a pause because that is what it is from the outside: ddrescue's
// map file survives, so starting again continues from the same place.
func (o *Orchestrator) CancelSalvage(driveIndex int) bool {
	o.recoveredMu.Lock()
	cancel := o.salvaging[driveIndex]
	o.recoveredMu.Unlock()
	if cancel == nil {
		return false
	}
	slog.Info("salvage: paused on request", "drive_index", driveIndex)
	cancel()
	return true
}
