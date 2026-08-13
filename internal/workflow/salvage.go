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
)

// ErrSalvageInProgress reports that a drive is already being salvaged.
var ErrSalvageInProgress = errors.New("a salvage is already running")

// streamDir is where the video lives. Salvage compares and repairs only these
// files: they are where damage costs the user something, and the smaller files
// around them can legitimately differ between a disc and a backup of it.
const streamDir = "BDMV/STREAM"

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
	dir := filepath.Join(scratchRoot, scratchSlug(req.DiscLabel, req.DevicePath))
	if err := os.MkdirAll(dir, 0o777); err != nil {
		return nil, fmt.Errorf("salvage: create backup dir %s: %w", dir, err)
	}

	// 1. Back up what the disc will give. A backup that dies partway is not a
	// failure here — it is the starting point, and its partial tree is exactly
	// what ddrescue is about to finish.
	report("backing-up", 0, "Copying everything the disc will give")
	if err := o.backupper.Backup(ctx, req.DriveIndex, dir, func(ev makemkv.Event) {
		logSalvageEvent(ev)
	}); err != nil {
		slog.Warn("salvage: backup did not complete; continuing with what it copied",
			"drive_index", req.DriveIndex, "dir", dir, "error", err)
		report("backing-up", 0, "The copy stopped at the damage, as expected")
	}

	// 2. Read the disc as a filesystem so the damaged streams can be compared
	// against what the backup managed to take.
	root, cleanup, err := o.openDiscRoot(req.DevicePath)
	if err != nil {
		return nil, fmt.Errorf("salvage: open disc: %w", err)
	}
	defer cleanup()

	short, err := incompleteStreams(root, dir)
	if err != nil {
		return nil, err
	}
	if len(short) == 0 {
		slog.Info("salvage: the backup is already complete; nothing to rescue", "dir", dir)
	}

	// 3. Fill what the backup could not take.
	var unrecovered int64
	for i, s := range short {
		report("rescuing", percentOf(i, len(short)),
			fmt.Sprintf("Recovering %s", filepath.Base(s.name)))

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

func percentOf(i, n int) int {
	if n <= 0 {
		return 0
	}
	return i * 100 / n
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
	if o.recovering[driveIndex] || o.salvaging[driveIndex] {
		return false
	}
	if o.salvaging == nil {
		o.salvaging = make(map[int]bool)
	}
	o.salvaging[driveIndex] = true
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
	return o.salvaging[driveIndex]
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
