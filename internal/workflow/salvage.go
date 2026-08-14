package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/johnpostlethwait/bluforge/internal/db"
	"github.com/johnpostlethwait/bluforge/internal/ddrescue"
	"github.com/johnpostlethwait/bluforge/internal/makemkv"
	"github.com/johnpostlethwait/bluforge/internal/organizer"
	"github.com/johnpostlethwait/bluforge/internal/ripper"
)

// salvageRuns numbers salvage runs so their events can be told apart. A run
// that has been cancelled keeps reporting while its processes die, and those
// events must not be mistaken for the run that replaced it.
var salvageRuns int64

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

	// Record it now, not on success. The startup sweep deletes any scratch the
	// database does not know about, and a salvage runs for hours: a restart
	// partway through deleted a backup and 11.8GB of rescued data on the first
	// real run, because nothing was written down until the very end.
	if o.store != nil {
		if _, err := o.store.SaveDiscBackup(db.DiscBackup{
			DriveIndex: req.DriveIndex,
			DiscLabel:  req.DiscLabel,
			BackupDir:  dir,
			SourceArg:  makemkv.FileSource(dir).Arg(),
			Partial:    true,
		}); err != nil {
			slog.Warn("salvage: could not record the scratch copy; a restart could delete it",
				"dir", dir, "error", err)
		}
	}

	// 1. Back up what the disc will give, unless a copy is already here. A
	// resumed salvage must never re-run this: the backup rewrites the stream
	// files, including the one ddrescue spent three hours patching, and would
	// fail on it again and take the recovery with it.
	//
	// A partial tree is not a reason to copy again either. Whatever it is
	// missing is exactly what the rescue below fills in.
	if resuming {
		slog.Info("salvage: a copy of this disc is already here; continuing rather than copying again",
			"dir", dir)
		report("rescuing", 0, "")
	} else {
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
			report("backing-up", lastPct, "")
		}
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

	short, err := incompleteFiles(root, dir)
	if err != nil {
		return nil, err
	}
	if len(short) == 0 {
		slog.Info("salvage: the backup is already complete; nothing to rescue", "dir", dir)
	}

	// 3. Fill what the backup could not take.
	unrecovered, err := o.rescueStreams(ctx, req, root, dir, short, report)
	if err != nil {
		return nil, err
	}

	// 4. Read the repaired tree. Reads now succeed everywhere, including across
	// the zero-filled gaps, so MakeMKV has no reason to abandon the title.
	report("verifying", 90, "")
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
		Source: src,
		Dir:    dir,
		Scan:   scan,
		// Measured is what makes Unrecovered meaningful. A resume that rescues
		// nothing has measured nothing, and reporting zero read as "everything
		// was recovered" when an earlier run had lost bytes it never saw.
		Salvaged:    true,
		Measured:    len(short) > 0,
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

// incompleteFiles compares every file on the disc against the backup's copy.
//
// It used to look only at BDMV/STREAM, because that is where the damage was on
// the disc that prompted all this. A backup that stopped early leaves the small
// structural files short too — a playlist, a clip info file — and MakeMKV then
// cannot parse the disc at all: it opens the folder and fails immediately,
// enumerating nothing, however perfect the streams are.
func incompleteFiles(discRoot, backupDir string) ([]shortStream, error) {
	var short []shortStream

	err := filepath.WalkDir(discRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// A file the disc will not even list is not one we can rescue.
			slog.Warn("salvage: skipping an unreadable entry", "path", path, "error", err)
			return nil
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		rel, err := filepath.Rel(discRoot, path)
		if err != nil {
			return nil
		}
		if !rescuableFile(rel) {
			return nil
		}

		var have int64
		if st, statErr := os.Stat(filepath.Join(backupDir, rel)); statErr == nil {
			have = st.Size()
		}
		if have >= info.Size() {
			return nil
		}
		short = append(short, shortStream{name: rel, have: have, want: info.Size()})
		slog.Info("salvage: file is short in the backup",
			"file", rel, "have", have, "want", info.Size())
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("salvage: read %s: %w", discRoot, err)
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
	// The context is made here rather than inside the goroutine so the cancel
	// stored with the claim is the real one. A placeholder left a window where
	// Pause found a no-op, reported success, and the salvage carried on — a
	// silent lie, and the button is on screen for that whole window.
	ctx, cancel := context.WithTimeout(context.Background(), salvageDeadline)

	if !o.beginSalvage(driveIndex, cancel) {
		cancel()
		return ErrSalvageInProgress
	}
	runID := atomic.AddInt64(&salvageRuns, 1)

	var devicePath string
	if loc, ok := o.scanner.(DeviceLocator); ok {
		devicePath = loc.DevicePathForDrive(context.Background(), driveIndex)
	}
	discLabel := discLabelFor(context.Background(), o.scanner, driveIndex, "")
	o.recoveredMu.Lock()
	if o.salvageLabels == nil {
		o.salvageLabels = make(map[int]string)
	}
	o.salvageLabels[driveIndex] = discLabel
	o.recoveredMu.Unlock()

	// Detached from any request: a salvage runs for hours and must not be killed
	// by a browser giving up, nor run until the drive gives out.
	go func() {
		defer o.endSalvage(driveIndex)
		defer cancel()

		o.setDriveState(driveIndex, "salvaging")
		defer o.setDriveState(driveIndex, "detected")

		rec, err := o.Salvage(ctx, SalvageRequest{
			DriveIndex: driveIndex,
			DevicePath: devicePath,
			DiscLabel:  discLabel,
			OutputDir:  outputDir,
			OnProgress: func(phase string, percent int, message string) {
				// Once cancelled, this run has nothing left to say. Without this
				// the dying backup kept reporting progress and the page showed
				// a salvage running that the user had just stopped.
				if ctx.Err() != nil {
					return
				}
				o.broadcastSalvage(runID, driveIndex, phase, percent, message, 0)
			},
		})
		if errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
			// A pause, not a failure. Everything recovered stays on disk and the
			// map file means the next run continues from here.
			slog.Info("salvage: paused", "drive_index", driveIndex)
			o.broadcastSalvage(runID, driveIndex, "paused", 0,
				"Paused. Everything recovered so far is kept.", 0)
			return
		}
		if err != nil {
			slog.Error("salvage: failed", "drive_index", driveIndex, "error", err)
			o.broadcastSalvage(runID, driveIndex, "failed", 0, err.Error(), 0)
			return
		}

		o.registerRecovered(driveIndex, rec)
		o.cacheScan(driveIndex, rec.Scan)

		// Rip what the user already chose. They matched the disc, picked titles,
		// chose languages and named the files before the rip failed; sending
		// them back to do all of it again is not a recovery, it is starting
		// over with extra steps.
		ripped := o.ripAfterSalvage(driveIndex, discLabel, outputDir)
		msg := ""
		if ripped > 0 {
			noun := "titles"
			if ripped == 1 {
				noun = "title"
			}
			msg = fmt.Sprintf("Ripping %d %s again with the same choices.", ripped, noun)
		}
		o.broadcastSalvage(runID, driveIndex, "done", 100, msg, rec.Unrecovered)
		slog.Info("salvage: disc is ready to rip",
			"drive_index", driveIndex, "titles", len(rec.Scan.Titles),
			"unrecovered_bytes", rec.Unrecovered)
	}()

	return nil
}

// beginSalvage claims the drive. A salvage and a recovery cannot run together:
// both copy the whole disc, and two at once would race for the drive and the
// scratch directory.
func (o *Orchestrator) beginSalvage(driveIndex int, cancel context.CancelFunc) bool {
	o.recoveredMu.Lock()
	defer o.recoveredMu.Unlock()
	if o.recovering[driveIndex] || o.salvaging[driveIndex] != nil {
		return false
	}
	if o.salvaging == nil {
		o.salvaging = make(map[int]context.CancelFunc)
	}
	o.salvaging[driveIndex] = cancel
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
func (o *Orchestrator) broadcastSalvage(runID int64, driveIndex int, phase string, percent int, message string, unrecovered int64) {
	if o.onBroadcast == nil {
		return
	}
	data, err := json.Marshal(map[string]any{
		// A cancelled run keeps reporting for a moment while its processes die,
		// and those events used to land on top of the run that replaced it: the
		// spinner returned seconds after a pause, and a resumed salvage paused
		// itself. The page ignores anything from a run it has moved on from.
		"run":         runID,
		"drive_index": driveIndex,
		"phase":       phase,
		"percent":     percent,
		"message":     message,
		"unrecovered": unrecovered,
		// Whether a stopped salvage can be picked up, so the page can offer to
		// resume without waiting for a reload to recompute it.
		"resumable": o.salvageResumableFor(driveIndex),
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

// salvageResumableFor reports whether the disc being salvaged on this drive has
// work on disk to resume from.
//
// It takes the lock itself: it is called from the broadcast path, which does
// not hold one, and it reads state the salvage goroutine writes. Assuming a
// caller's lock here was a data race that tests never hit, because they always
// happened to write before the reader started.
func (o *Orchestrator) salvageResumableFor(driveIndex int) bool {
	o.recoveredMu.Lock()
	outputDir, label := o.outputDir, o.salvageLabels[driveIndex]
	o.recoveredMu.Unlock()

	dir, ok := FindSalvageScratch(outputDir, label)
	if !ok {
		return false
	}
	maps, _ := filepath.Glob(filepath.Join(dir, "*.map"))
	return len(maps) > 0
}

// SalvageState is what a page needs to draw the salvage panel without having
// seen any events.
//
// A reload or a dropped connection during a salvage left the panel blank until
// the next event, which during a quiet phase can be minutes.
type SalvageState struct {
	Active bool `json:"active"`
	// Paused reports a salvage that was stopped and has work waiting on disk.
	// It is derived from what is recorded rather than remembered in a browser:
	// a reload used to lose the paused state entirely, leaving no way back to
	// hours of recovered data.
	Paused     bool   `json:"paused"`
	DriveIndex int    `json:"driveIndex"`
	DiscLabel  string `json:"discLabel"`
	Resumable  bool   `json:"resumable"`
}

// CurrentSalvage reports a salvage in progress, or one that is paused with work
// waiting to be resumed.
//
// Both come from what is recorded rather than from what a page happens to
// remember. A reload during a pause used to show nothing at all, stranding
// hours of recovered data behind a button that offered to start over.
func (o *Orchestrator) CurrentSalvage() SalvageState {
	o.recoveredMu.Lock()
	driveIndex := -1
	for idx := range o.salvaging {
		driveIndex = idx
		break
	}
	label := o.salvageLabels[driveIndex]
	o.recoveredMu.Unlock()

	if driveIndex >= 0 {
		return SalvageState{
			Active:     true,
			DriveIndex: driveIndex,
			DiscLabel:  label,
			Resumable:  o.salvageResumableFor(driveIndex),
		}
	}

	// Nothing running. An unfinished copy on disk means a salvage was stopped
	// and can be picked up.
	if paused, ok := o.pausedSalvage(); ok {
		return paused
	}
	return SalvageState{}
}

// pausedSalvage finds a salvage that was stopped with work left on disk.
//
// The record survives a restart, which the browser's idea of "paused" does not:
// this is what lets the page offer to resume after a reload, a reconnect, or a
// redeploy.
func (o *Orchestrator) pausedSalvage() (SalvageState, bool) {
	if o.store == nil {
		return SalvageState{}, false
	}
	backups, err := o.store.ListDiscBackups()
	if err != nil {
		slog.Warn("salvage: could not look for a paused salvage", "error", err)
		return SalvageState{}, false
	}
	for _, b := range backups {
		if !b.Partial {
			continue
		}
		if _, statErr := os.Stat(b.BackupDir); statErr != nil {
			continue
		}
		// Resumable either way: with a map file the rescue continues from where
		// it stopped, and without one the backup already on disk is still worth
		// continuing from rather than re-reading.
		return SalvageState{
			Paused:     true,
			DriveIndex: b.DriveIndex,
			DiscLabel:  b.DiscLabel,
			Resumable:  true,
		}, true
	}
	return SalvageState{}, false
}

// rescueStreams runs ddrescue over the streams the backup left short, holding
// the drive for the duration.
//
// Separated from Salvage so the drive lock is released when the rescue ends
// rather than when the whole salvage does. Held across the folder scan that
// follows, it deadlocked: the scan takes the same executor mutex, Go mutexes do
// not nest, and a salvage sat with its rescue complete and nothing running for
// as long as anyone left it.
func (o *Orchestrator) rescueStreams(
	ctx context.Context,
	req SalvageRequest,
	root, dir string,
	short []shortStream,
	report func(phase string, percent int, message string),
) (int64, error) {
	if len(short) == 0 {
		return 0, nil
	}

	// ddrescue is a separate process and does not go through the MakeMKV
	// executor, so nothing otherwise stops the drive poller reading the same
	// drive underneath it. That contention took a rescue from 14 MB/s to
	// 2.4 MB/s.
	if locker, ok := o.backupper.(DriveLocker); ok {
		locker.LockDrive()
		defer locker.UnlockDrive()
		slog.Info("salvage: holding the drive for the rescue", "files", len(short))
	} else {
		slog.Warn("salvage: cannot claim the drive; the poller will slow the rescue")
	}

	var unrecovered int64
	for _, s := range short {
		name := filepath.Base(s.name)
		report("rescuing", 0, fmt.Sprintf("Patching %s", name))
		lastRescuePct := -1

		err := ddrescue.Rescue(ctx, o.rescuer, ddrescue.Options{
			Source: filepath.Join(root, s.name),
			Dest:   filepath.Join(dir, s.name),
			// Mirror the file's own path. Naming maps by basename collided:
			// BDMV/PLAYLIST/00800.mpls and BDMV/BACKUP/PLAYLIST/00800.mpls
			// shared one, so the second read the first's map, concluded it was
			// already rescued, copied nothing, and left a zero-byte file. Every
			// structural file on a Blu-ray has a duplicate under BACKUP/, so
			// every one of them was wiped.
			MapFile:     filepath.Join(dir, salvageMapDir, s.name+".map"),
			StartOffset: s.have,
			Retries:     req.Retries,
		}, func(p ddrescue.Progress) {
			if p.BytesBad > 0 {
				unrecovered = p.BytesBad
			}
			if p.Line != "" {
				slog.Info("salvage: rescuing", "file", s.name, "status", p.Line)
			}
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
			return unrecovered, fmt.Errorf("salvage: %w", err)
		}
	}
	return unrecovered, nil
}

// salvageMapDir holds ddrescue's maps, mirroring the disc's own layout so two
// files with the same name never share one.
const salvageMapDir = ".salvage-maps"

// rescuableFile reports whether a file may be copied from the disc into a
// backup.
//
// Only the video tree. MakeMKV's backup writes the protection-related files —
// AACS, CERTIFICATE, discatt.dat — in the form it wants them, which is not the
// raw form they take on the disc. Copying the disc's versions over them fed
// makemkvcon a 134MB MKB_RO.inf where it expected a few kilobytes, and it
// crashed reading the result.
func rescuableFile(rel string) bool {
	return strings.HasPrefix(filepath.ToSlash(rel), "BDMV/")
}

// FindSalvageScratch and the sweep must not mistake the map directory for disc
// content; it lives inside the scratch and is ours.
func isSalvageMap(rel string) bool {
	return strings.HasPrefix(filepath.ToSlash(rel), salvageMapDir+"/")
}

// ripAfterSalvage repeats the rips that failed on this disc, using the choices
// already made for them, and returns how many were submitted.
//
// A salvage exists because a rip failed. The user had already matched the disc,
// selected titles, chosen audio and subtitle languages and settled on names —
// all of it recorded against those failed jobs. Finishing the salvage and
// stopping there sent them back to the beginning.
func (o *Orchestrator) ripAfterSalvage(driveIndex int, discLabel, outputDir string) int {
	if o.store == nil || discLabel == "" {
		return 0
	}
	failed, err := o.store.ListJobsByStatus("failed")
	if err != nil {
		slog.Error("salvage: could not look up what to rip again", "error", err)
		return 0
	}

	var titles []TitleSelection
	var mediaTitle string
	var opts *makemkv.SelectionOpts

	for _, j := range failed {
		if j.DiscName != discLabel {
			continue
		}
		sel := TitleSelection{
			TitleIndex:    j.TitleIndex,
			TitleName:     j.TitleName,
			SourceFile:    j.SourceFile,
			SizeBytes:     j.SizeBytes,
			ContentType:   j.ContentType,
			TrackMetadata: parseTrackMeta(j.TrackMetadata),
		}
		titles = append(titles, sel)

		// The names were settled before the failure: <output>/<media>/<title>.mkv.
		// Taking the media directory back off the path reproduces them exactly.
		if mediaTitle == "" && j.OutputPath != "" {
			mediaTitle = filepath.Base(filepath.Dir(j.OutputPath))
		}
		if opts == nil && j.SelectionOpts != "" {
			var parsed makemkv.SelectionOpts
			if err := json.Unmarshal([]byte(j.SelectionOpts), &parsed); err == nil {
				opts = &parsed
			} else {
				slog.Warn("salvage: could not read the language choices for this rip",
					"job_id", j.ID, "error", err)
			}
		}
	}
	if len(titles) == 0 {
		slog.Info("salvage: nothing was waiting to be ripped for this disc", "disc", discLabel)
		return 0
	}

	slog.Info("salvage: ripping again with the choices already made",
		"disc", discLabel, "titles", len(titles), "media_title", mediaTitle,
		"languages_kept", opts != nil)

	o.ManualRip(ManualRipParams{
		DriveIndex:      driveIndex,
		DiscName:        discLabel,
		MediaTitle:      mediaTitle,
		OutputDir:       outputDir,
		DuplicateAction: "overwrite",
		SelectionOpts:   opts,
		Titles:          titles,
	})
	return len(titles)
}

func parseTrackMeta(raw string) ripper.TrackMetadata {
	var m ripper.TrackMetadata
	if raw != "" {
		_ = json.Unmarshal([]byte(raw), &m)
	}
	return m
}
