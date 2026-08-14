package workflow

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/johnpostlethwait/bluforge/internal/aacs"
	"github.com/johnpostlethwait/bluforge/internal/db"
	"github.com/johnpostlethwait/bluforge/internal/discdb"
	"github.com/johnpostlethwait/bluforge/internal/makemkv"
	"github.com/johnpostlethwait/bluforge/internal/organizer"
)

// ScratchDirName is the directory under the output dir that holds disc backups
// taken during recovery.
//
// It lives under the output directory because that volume is already mapped and
// writable on every install, so recovery works without a compose change. The
// leading dot keeps it out of Plex/Jellyfin scans and matches the existing
// .rip-* convention used for per-rip temp directories.
const ScratchDirName = ".bluforge-scratch"

// ErrGenuinelyEncrypted reports a disc whose payload really is encrypted. The
// backup-and-strip workaround cannot help; the disc needs a volume key MakeMKV
// does not have.
var ErrGenuinelyEncrypted = errors.New("disc payload is encrypted: this is a genuine unknown-volume-key case, not a spurious AACS directory")

// ErrInconclusive reports that the payload could not be classified. Treated
// exactly like ErrGenuinelyEncrypted: an ambiguous read must never authorise a
// backup that could cost 100GB and 40 minutes to prove nothing.
var ErrInconclusive = errors.New("could not determine whether the disc payload is encrypted")

// ErrRecoveryInProgress reports that a disc is being recovered in the
// background and is not yet scannable.
//
// Recovery copies up to ~100GB and runs for tens of minutes, so it cannot be
// performed inside the request that triggered it: an abandoned request would
// take the backup down with it. Callers should surface this as "working on it"
// rather than as a failure, and wait for the disc_recovery SSE event.
var ErrRecoveryInProgress = errors.New("disc recovery in progress")

// DiscBackupper is the subset of the MakeMKV executor recovery needs.
type DiscBackupper interface {
	Backup(ctx context.Context, driveIndex int, destDir string, onEvent func(makemkv.Event)) error
	ScanSource(ctx context.Context, src makemkv.Source) (*makemkv.DiscScan, error)
}

// DiscRootOpener makes a disc readable as a directory tree, returning the root
// and a cleanup function. Injected so tests can supply a fixture tree.
type DiscRootOpener func(devicePath string) (root string, cleanup func(), err error)

// RecoveryRequest describes the disc to recover.
type RecoveryRequest struct {
	DriveIndex int
	DevicePath string
	DiscLabel  string
	OutputDir  string
	// DumpPath, when known, is the .tgz MakeMKV wrote about this disc. Recorded
	// for diagnostics and quoted back to the user on a genuine key failure.
	DumpPath string
	// OnProgress reports recovery phases so the UI is not dark for 40 minutes.
	OnProgress func(phase string, percent int)
}

// RecoveredDisc is a disc that has been backed up and stripped of its AACS
// directory, ready to rip as a folder source.
type RecoveredDisc struct {
	// DiscLabel is the disc this copy was made from, taken from the request
	// that made it rather than from rescanning the copy.
	//
	// A folder scan reports whatever the copied BDMV names itself, which is not
	// the drive's volume label and is sometimes nothing at all. Anything that
	// has to match a copy against a disc — a history entry offering to delete
	// it — needs the name the rest of BluForge uses for that disc.
	DiscLabel    string
	Source       makemkv.Source
	Dir          string
	Scan         *makemkv.DiscScan
	DiagnosticID int64
	// Salvaged marks a copy repaired from a physically damaged disc, whose
	// content is knowingly imperfect.
	Salvaged bool
	// Measured reports that this run actually rescued something, and so knows
	// what it lost. A resume that found nothing short has no figure to give.
	Measured bool
	// Unrecovered is how many bytes a salvage could not read and left blank.
	// Zero for an ordinary recovery. It is what explains a glitch to someone
	// looking at the file a year later.
	Unrecovered int64
	// Ephemeral marks a recovery that only holds while the disc stays mounted —
	// a symlink tree rather than a copy. It is never persisted across a restart,
	// because the links would dangle and rebuilding takes seconds.
	Ephemeral bool
	// unmount releases the disc mount the links depend on. Held until the last
	// job finishes, since unmounting mid-rip would break it.
	unmount func()
}

// RecoverSpuriousAACS determines whether a disc that failed to open carries a
// spurious AACS directory, and if so recovers it.
//
// The disc is mounted and its largest stream sampled first: reading a few
// hundred kilobytes is what separates this case from a genuine missing volume
// key, and doing it before the backup means an encrypted disc costs seconds
// rather than a ~100GB copy. Only a confirmed-unencrypted payload proceeds to
// the backup, the AACS removal, and the folder rescan.
func (o *Orchestrator) RecoverSpuriousAACS(ctx context.Context, req RecoveryRequest) (*RecoveredDisc, error) {
	progress := req.OnProgress
	if progress == nil {
		progress = func(string, int) {}
	}

	progress("detecting", 0)

	root, cleanup, err := o.openDiscRoot(req.DevicePath)
	if err != nil {
		// Without a readable disc tree there is no way to tell the two cases
		// apart. Guessing here would mean a 100GB backup on a hunch.
		o.recordDiagnostic(db.DiscDiagnostic{
			DiscLabel:       req.DiscLabel,
			DriveIndex:      req.DriveIndex,
			ScrambleVerdict: string(aacs.VerdictUnknown),
			RipPath:         "blocked",
			Outcome:         "failed",
			Detail:          fmt.Sprintf("disc could not be mounted for inspection: %v", err),
			DumpPath:        req.DumpPath,
		})
		return nil, fmt.Errorf("%w: disc could not be mounted for inspection: %v", ErrInconclusive, err)
	}
	// The symlink path hands this mount to the recovered disc, which holds it
	// until the last rip finishes — unmounting on return would break the links
	// the rips read through.
	keepMounted := false
	defer func() {
		if !keepMounted {
			cleanup()
		}
	}()

	aacsPresent := aacs.HasAACSDir(root)
	insp, err := aacs.InspectStreams(root)
	if err != nil {
		o.recordDiagnostic(db.DiscDiagnostic{
			DiscLabel:       req.DiscLabel,
			DriveIndex:      req.DriveIndex,
			AACSDirPresent:  aacsPresent,
			ScrambleVerdict: string(aacs.VerdictUnknown),
			RipPath:         "blocked",
			Outcome:         "failed",
			Detail:          fmt.Sprintf("stream inspection failed: %v", err),
			DumpPath:        req.DumpPath,
		})
		return nil, fmt.Errorf("%w: %v", ErrInconclusive, err)
	}

	slog.Info("recovery: payload inspection complete",
		"disc", req.DiscLabel, "aacs_dir_present", aacsPresent,
		"verdict", insp.Verdict, "stride", insp.Stride,
		"packets_checked", insp.PacketsChecked, "scrambled", insp.ScrambledPackets,
		"reason", insp.Reason)

	diag := db.DiscDiagnostic{
		DiscLabel:        req.DiscLabel,
		DriveIndex:       req.DriveIndex,
		MKBVersion:       mkbVersion(root, req.DumpPath),
		AACSDirPresent:   aacsPresent,
		ScrambleVerdict:  string(insp.Verdict),
		PacketsChecked:   insp.PacketsChecked,
		ScrambledPackets: insp.ScrambledPackets,
		DumpPath:         req.DumpPath,
	}

	if insp.Verdict != aacs.VerdictUnencrypted {
		diag.RipPath = "blocked"
		diag.Outcome = "failed"
		diag.Detail = insp.Reason
		o.recordDiagnostic(diag)

		base := ErrGenuinelyEncrypted
		if insp.Verdict != aacs.VerdictScrambled {
			base = ErrInconclusive
		}
		return nil, fmt.Errorf("%w (%s)%s", base, insp.Reason, dumpHint(req.DumpPath))
	}

	scratchRoot := filepath.Join(req.OutputDir, ScratchDirName)
	if err := os.MkdirAll(scratchRoot, 0o777); err != nil {
		diag.RipPath = "blocked"
		diag.Outcome = "failed"
		diag.Detail = fmt.Sprintf("create scratch root: %v", err)
		o.recordDiagnostic(diag)
		return nil, fmt.Errorf("recovery: create scratch root %s: %w", scratchRoot, err)
	}

	// Try the cheap path first: a tree of symlinks to the mounted disc with AACS
	// left out. MakeMKV then reads the disc directly and only the selected
	// titles, instead of copying ~100GB to read all of it twice.
	if rec, err := o.recoverViaSymlinks(ctx, req, root, scratchRoot, diag, cleanup); err == nil {
		keepMounted = true
		progress("done", 100)
		return rec, nil
	} else {
		slog.Warn("recovery: symlink path unavailable, falling back to a full backup",
			"disc", req.DiscLabel, "error", err)
	}

	diag.RipPath = "backup_strip"
	diagID := o.recordDiagnostic(diag)

	// Size the backup from the disc itself rather than assuming a worst case —
	// a single-layer BD needs a quarter of what a dual-layer UHD does.
	needed, err := treeSize(root)
	if err != nil {
		slog.Warn("recovery: could not size disc content, assuming a full UHD",
			"disc", req.DiscLabel, "error", err)
		needed = 100 << 30
	}
	needed += needed / 20 // 5% headroom

	if err := o.checkSpace(scratchRoot, needed); err != nil {
		detail := fmt.Sprintf("need %d bytes free in %s: %v", needed, scratchRoot, err)
		o.finishDiagnostic(diagID, "failed", detail, 0)
		return nil, fmt.Errorf("recovery: insufficient space for disc backup — %s", detail)
	}
	slog.Info("recovery: scratch space check passed",
		"scratch_root", scratchRoot, "required_bytes", needed,
		"required_gib", needed/(1<<30))

	backupDir := filepath.Join(scratchRoot, scratchSlug(req.DiscLabel, req.DevicePath))
	// makemkvcon wants a destination that does not already hold a disc tree.
	if err := os.RemoveAll(backupDir); err != nil {
		o.finishDiagnostic(diagID, "failed", fmt.Sprintf("clear stale backup dir: %v", err), 0)
		return nil, fmt.Errorf("recovery: clear stale backup dir %s: %w", backupDir, err)
	}
	if err := os.MkdirAll(backupDir, 0o777); err != nil {
		o.finishDiagnostic(diagID, "failed", fmt.Sprintf("create backup dir: %v", err), 0)
		return nil, fmt.Errorf("recovery: create backup dir %s: %w", backupDir, err)
	}

	slog.Info("recovery: starting raw disc backup",
		"disc", req.DiscLabel, "dest", backupDir, "estimated_bytes", needed)
	progress("backing_up", 0)

	// Drive the progress bar from bytes on disk rather than makemkvcon's own
	// numbers. On a real backup it reported 0% then 100% within 100ms — for a
	// preliminary phase, before copying 95GB — which leaves a progress bar
	// pinned and useless. Bytes written against the size measured from the disc
	// are monotonic and mean what a user expects them to mean.
	stopTicker := make(chan struct{})
	tickerDone := make(chan struct{})
	go func() {
		defer close(tickerDone)
		t := time.NewTicker(backupProgressInterval)
		defer t.Stop()
		for {
			select {
			case <-stopTicker:
				return
			case <-t.C:
				written := dirSize(backupDir)
				pct := backupPercent(written, needed)
				progress("backing_up", pct)
				slog.Info("recovery: backup progress",
					"disc", req.DiscLabel, "percent", pct,
					"written_bytes", written, "estimated_bytes", needed)
			}
		}
	}()

	err = o.backupper.Backup(ctx, req.DriveIndex, backupDir, nil)
	close(stopTicker)
	<-tickerDone
	if err != nil {
		// Retained deliberately: a partial backup is evidence, and re-copying
		// 100GB to look at it again is not reasonable.
		detail := fmt.Sprintf("backup failed: %v", err)
		o.finishDiagnostic(diagID, "failed", detail, dirSize(backupDir))
		return nil, fmt.Errorf("recovery: %s — backup retained at %s for inspection", detail, backupDir)
	}

	progress("stripping", 0)
	if err := removeAACSDir(scratchRoot, backupDir); err != nil {
		detail := fmt.Sprintf("could not remove AACS directory: %v", err)
		o.finishDiagnostic(diagID, "failed", detail, dirSize(backupDir))
		return nil, fmt.Errorf("recovery: %s — backup retained at %s", detail, backupDir)
	}

	// Re-verify on the copy. This should always agree with the disc; if it does
	// not, something is wrong with the backup and continuing would waste a long
	// rip on bad data.
	if verify, err := aacs.InspectStreams(backupDir); err != nil || verify.Verdict != aacs.VerdictUnencrypted {
		detail := fmt.Sprintf("backup re-verification disagreed with the disc (verdict %q, err %v)", verify.Verdict, err)
		o.finishDiagnostic(diagID, "failed", detail, dirSize(backupDir))
		return nil, fmt.Errorf("recovery: %s — backup retained at %s", detail, backupDir)
	}

	progress("rescanning", 0)
	src := makemkv.FileSource(backupDir)
	scan, err := o.backupper.ScanSource(ctx, src)
	if err != nil {
		detail := fmt.Sprintf("stripped backup still failed to scan: %v", err)
		o.finishDiagnostic(diagID, "failed", detail, dirSize(backupDir))
		return nil, fmt.Errorf("recovery: %s — backup retained at %s", detail, backupDir)
	}
	if len(scan.Titles) == 0 {
		detail := "stripped backup scanned but produced no titles"
		o.finishDiagnostic(diagID, "failed", detail, dirSize(backupDir))
		return nil, fmt.Errorf("recovery: %s — backup retained at %s", detail, backupDir)
	}

	// The disc key hashes the title list, so it only becomes meaningful now.
	if key := discdb.BuildDiscKey(scan); key != "" && o.store != nil {
		if err := o.store.SetDiscDiagnosticKey(diagID, key); err != nil {
			slog.Warn("recovery: could not record disc key", "error", err)
		}
	}

	// The rescan of the stripped backup names the disc even when the original
	// scan could not — a disc that fails to open reports no CINFO disc name at
	// all. Backfill it, since the label is what the per-disc record is keyed on.
	if scan.DiscName != "" && scan.DiscName != req.DiscLabel && o.store != nil {
		if err := o.store.SetDiscDiagnosticLabel(diagID, scan.DiscName); err != nil {
			slog.Warn("recovery: could not record disc label", "error", err)
		}
		slog.Info("recovery: disc identified from the stripped backup",
			"disc", scan.DiscName, "drive_index", req.DriveIndex)
	}

	size := dirSize(backupDir)
	o.finishDiagnostic(diagID, "ok", insp.Reason, size)
	progress("done", 100)

	slog.Info("recovery: disc recovered from spurious AACS directory",
		"disc", req.DiscLabel, "backup_dir", backupDir,
		"titles", len(scan.Titles), "backup_bytes", size)

	return &RecoveredDisc{DiscLabel: req.DiscLabel, Source: src, Dir: backupDir, Scan: scan, DiagnosticID: diagID}, nil
}

// maybeRecover inspects a scan failure and, when it carries the spurious-AACS
// signature, attempts recovery.
//
// Three outcomes: (nil, nil) means this was an ordinary failure the caller
// should report as-is; (scan, nil) means the disc was recovered and the returned
// scan replaces the failed one; (nil, err) means recovery was attempted and
// could not proceed, and its error is more informative than the original —
// naming a genuine missing volume key, for instance, instead of a bare "failed
// to open disc" that sends users hunting for key databases.
func (o *Orchestrator) maybeRecover(ctx context.Context, driveIndex int, scanErr error) (*makemkv.DiscScan, error) {
	var se *makemkv.ScanError
	if !errors.As(scanErr, &se) || se.Scan == nil {
		return nil, nil
	}
	if !makemkv.IsSpuriousAACSSignature(se.Messages(), len(se.Scan.Titles)) {
		return nil, nil
	}

	// Recovery already running for this drive: report that rather than starting
	// a second ~100GB copy of the same disc.
	if o.recoveryRunning(driveIndex) {
		return nil, fmt.Errorf("%w for drive %d", ErrRecoveryInProgress, driveIndex)
	}

	slog.Warn("orchestrator: disc failed with the spurious-AACS signature, inspecting payload",
		"drive_index", driveIndex, "disc", se.Scan.DiscName,
		"message_codes", makemkv.MessageCodes(se.Messages()))

	if o.backupper == nil {
		slog.Warn("orchestrator: no backupper configured, cannot attempt recovery", "drive_index", driveIndex)
		return nil, nil
	}

	outputDir := o.currentOutputDir()
	if outputDir == "" {
		slog.Warn("orchestrator: no output directory configured, cannot attempt recovery", "drive_index", driveIndex)
		return nil, nil
	}

	var devicePath string
	if loc, ok := o.scanner.(DeviceLocator); ok {
		devicePath = loc.DevicePathForDrive(ctx, driveIndex)
	}

	if !o.beginRecovery(driveIndex) {
		return nil, fmt.Errorf("%w for drive %d", ErrRecoveryInProgress, driveIndex)
	}

	discName := discLabelFor(ctx, o.scanner, driveIndex, se.Scan.DiscName)
	dumpPath := dumpPathFromMessages(se.Messages())
	if discName == "" {
		slog.Warn("recovery: disc reports no label; diagnostics will be harder to correlate",
			"drive_index", driveIndex)
	}

	// Detach from the caller's context. Recovery copies up to ~100GB and runs
	// for tens of minutes; inheriting an HTTP request's context meant the
	// backup was SIGKILLed the moment the browser stopped waiting, which is
	// exactly how this failed in practice. Values are preserved, cancellation
	// is not — the backup carries its own timeout.
	bgCtx := context.WithoutCancel(ctx)

	go func() {
		defer o.endRecovery(driveIndex)

		// Mark the drive so it does not read as idle for the length of the backup.
		o.setDriveState(driveIndex, "recovering")
		defer o.setDriveState(driveIndex, "detected")

		rec, err := o.RecoverSpuriousAACS(bgCtx, RecoveryRequest{
			DriveIndex: driveIndex,
			DevicePath: devicePath,
			DiscLabel:  discName,
			OutputDir:  outputDir,
			DumpPath:   dumpPath,
			OnProgress: func(phase string, percent int) {
				o.broadcastRecovery(driveIndex, discName, phase, percent, "")
			},
		})
		if err != nil {
			slog.Error("recovery: failed", "drive_index", driveIndex, "disc", discName, "error", err)
			o.broadcastRecovery(driveIndex, discName, "failed", 0, err.Error())
			return
		}

		o.registerRecovered(driveIndex, rec)
		// Cache the recovered scan so the next scan request is served instantly
		// rather than re-reading the disc that could not be read in the first
		// place.
		o.cacheScan(driveIndex, rec.Scan)
		o.broadcastRecovery(driveIndex, discName, "done", 100, "")
		slog.Info("recovery: complete, disc is ready to rip",
			"drive_index", driveIndex, "disc", discName, "titles", len(rec.Scan.Titles))
	}()

	return nil, fmt.Errorf("%w for drive %d", ErrRecoveryInProgress, driveIndex)
}

// beginRecovery claims the recovery slot for a drive, returning false when one
// is already running.
func (o *Orchestrator) beginRecovery(driveIndex int) bool {
	o.recoveredMu.Lock()
	defer o.recoveredMu.Unlock()
	if o.recovering[driveIndex] {
		return false
	}
	if o.recovering == nil {
		o.recovering = make(map[int]bool)
	}
	o.recovering[driveIndex] = true
	return true
}

func (o *Orchestrator) endRecovery(driveIndex int) {
	o.recoveredMu.Lock()
	defer o.recoveredMu.Unlock()
	delete(o.recovering, driveIndex)
}

// RecoveryInProgress reports whether a drive is currently being recovered.
//
// Exposed because a client that lost its event stream cannot tell: the "done"
// phase is delivered once and never replayed, so the page needs to be able to
// ask rather than wait forever for an event that already happened.
func (o *Orchestrator) RecoveryInProgress(driveIndex int) bool {
	return o.recoveryRunning(driveIndex)
}

// recoveryRunning reports whether a recovery is in flight for a drive.
func (o *Orchestrator) recoveryRunning(driveIndex int) bool {
	o.recoveredMu.Lock()
	defer o.recoveredMu.Unlock()
	return o.recovering[driveIndex]
}

// SetDriveStateReporter installs the callback used to publish drive states the
// poller cannot infer. Set after construction because the drive manager and the
// orchestrator each need the other.
func (o *Orchestrator) SetDriveStateReporter(fn func(driveIndex int, state string)) {
	o.recoveredMu.Lock()
	defer o.recoveredMu.Unlock()
	o.onDriveState = fn
}

func (o *Orchestrator) setDriveState(driveIndex int, state string) {
	o.recoveredMu.Lock()
	fn := o.onDriveState
	o.recoveredMu.Unlock()
	if fn != nil {
		fn(driveIndex, state)
	}
}

// broadcastRecovery pushes a recovery phase to the UI. Recovery runs
// automatically and can take 40 minutes, so the user is told what is happening
// rather than left watching an idle-looking drive.
func (o *Orchestrator) broadcastRecovery(driveIndex int, discName, phase string, percent int, message string) {
	if o.onBroadcast == nil {
		return
	}
	data, err := json.Marshal(map[string]any{
		"drive_index": driveIndex,
		"disc_name":   discName,
		"phase":       phase,
		"percent":     percent,
		"message":     message,
	})
	if err != nil {
		slog.Error("recovery: could not marshal SSE payload", "error", err)
		return
	}
	o.onBroadcast("disc_recovery", string(data))
}

// tgzPattern finds the diagnostic dump MakeMKV writes when it cannot decrypt a
// disc. The path appears in the message text; it is what the user sends to
// svq@makemkv.com for a genuine missing key.
var tgzPattern = regexp.MustCompile(`(\S*MKB\d+_v\d+_\S*\.tgz)`)

func dumpPathFromMessages(messages []makemkv.Message) string {
	for _, m := range messages {
		if match := tgzPattern.FindString(m.Text); match != "" {
			return match
		}
	}
	return ""
}

// DeviceLocator resolves a drive index to its device path. Implemented by the
// MakeMKV executor; recovery needs the device to mount the disc for inspection.
type DeviceLocator interface {
	DevicePathForDrive(ctx context.Context, driveIndex int) string
}

// DiscLabeller reports the disc label a drive is currently reporting.
//
// A disc that fails to open produces no CINFO disc name, so the scan cannot
// supply one — yet the label is the field the whole per-disc record is keyed on,
// and it is right there in the drive listing.
type DiscLabeller interface {
	DiscLabelForDrive(ctx context.Context, driveIndex int) string
}

// discLabelFor resolves the best available label for a disc.
//
// The scan's own name wins when it has one. Otherwise the drive listing is
// asked, which is what makes a disc that never opened still identifiable in the
// diagnostics record and in its scratch directory name.
func discLabelFor(ctx context.Context, scanner DiscScanner, driveIndex int, fromScan string) string {
	if fromScan != "" {
		return fromScan
	}
	if labeller, ok := scanner.(DiscLabeller); ok {
		return labeller.DiscLabelForDrive(ctx, driveIndex)
	}
	return ""
}

// SetOutputDir tells the orchestrator where scratch backups may be written.
// Recovery is triggered from the scan path, which has no output directory of
// its own, so it is supplied once at startup and refreshed when settings change.
func (o *Orchestrator) SetOutputDir(dir string) {
	o.recoveredMu.Lock()
	defer o.recoveredMu.Unlock()
	o.outputDir = dir
}

func (o *Orchestrator) currentOutputDir() string {
	o.recoveredMu.Lock()
	defer o.recoveredMu.Unlock()
	return o.outputDir
}

// RecoveredSource returns the folder source registered for a drive, or nil when
// the drive's disc was read normally.
func (o *Orchestrator) RecoveredSource(driveIndex int) *makemkv.Source {
	o.recoveredMu.Lock()
	defer o.recoveredMu.Unlock()
	rec, ok := o.recovered[driveIndex]
	if !ok {
		return nil
	}
	src := rec.source
	return &src
}

// RecoveredDir returns the scratch backup directory for a drive, or "".
func (o *Orchestrator) RecoveredDir(driveIndex int) string {
	o.recoveredMu.Lock()
	defer o.recoveredMu.Unlock()
	if rec, ok := o.recovered[driveIndex]; ok {
		return rec.dir
	}
	return ""
}

// registerRecovered records a recovered disc for a drive.
//
// Any backup previously registered for that drive is retired rather than
// discarded: if jobs are still ripping from it — a disc swapped mid-rip — it
// survives until they finish, and only its map entry is replaced.
func (o *Orchestrator) registerRecovered(driveIndex int, rec *RecoveredDisc) {
	discLabel := discLabelOf(rec)

	o.recoveredMu.Lock()
	stale := o.recovered[driveIndex]
	o.recovered[driveIndex] = &recoveredDisc{
		source:      rec.Source,
		dir:         rec.Dir,
		discLabels:  discLabelsFor(discLabel, rec.Dir),
		ephemeral:   rec.Ephemeral,
		unmount:     rec.unmount,
		salvageNote: salvageNoteFor(rec),
	}
	if stale != nil && stale.dir != rec.Dir {
		// A previous backup for this drive is no longer the current disc, but it
		// is not deleted: only a successful rip or an explicit discard removes a
		// copy that cost tens of minutes to make.
		stale.retired = true
	}
	o.recoveredMu.Unlock()

	// A link tree is not worth persisting: its links dangle once the disc is
	// unmounted, and rebuilding takes seconds. Only real copies are recorded.
	if rec.Ephemeral {
		return
	}

	// Persist so the copy survives a restart. Without the record the startup
	// sweep cannot tell it from crash debris, and would delete it.
	if o.store != nil {
		if _, err := o.store.SaveDiscBackup(db.DiscBackup{
			DriveIndex: driveIndex,
			DiscLabel:  discLabel,
			BackupDir:  rec.Dir,
			SourceArg:  rec.Source.Arg(),
		}); err != nil {
			slog.Warn("recovery: could not record disc backup", "dir", rec.Dir, "error", err)
		}
	}
}

// RestoreBackups reloads recovered discs recorded by a previous run.
//
// A backup is worth up to ~100GB and tens of minutes, so it has to outlive a
// restart: otherwise the copy is orphaned on disk and the next scan goes back
// to the drive MakeMKV could not read in the first place. Records whose
// directory has since disappeared are dropped.
func (o *Orchestrator) RestoreBackups() error {
	if o.store == nil {
		return nil
	}

	backups, err := o.store.ListDiscBackups()
	if err != nil {
		return err
	}

	for _, b := range backups {
		if _, statErr := os.Stat(b.BackupDir); statErr != nil {
			slog.Info("recovery: forgetting a backup whose directory is gone",
				"dir", b.BackupDir, "drive_index", b.DriveIndex)
			if delErr := o.store.DeleteDiscBackup(b.BackupDir); delErr != nil {
				slog.Warn("recovery: could not drop stale backup record", "error", delErr)
			}
			continue
		}

		if b.Partial {
			// A salvage that was interrupted. Worth protecting from the sweep
			// and worth resuming, but not a disc that can be ripped yet.
			o.recoveredMu.Lock()
			o.partialScratch = append(o.partialScratch, b.BackupDir)
			o.recoveredMu.Unlock()
			slog.Info("recovery: keeping an unfinished salvage copy for resuming",
				"disc", b.DiscLabel, "dir", b.BackupDir)
			continue
		}

		o.recoveredMu.Lock()
		o.recovered[b.DriveIndex] = &recoveredDisc{
			source:     makemkv.FileSource(b.BackupDir),
			dir:        b.BackupDir,
			discLabels: discLabelsFor(b.DiscLabel, b.BackupDir),
		}
		o.recoveredMu.Unlock()

		slog.Info("recovery: restored disc backup from a previous run",
			"drive_index", b.DriveIndex, "disc", b.DiscLabel, "dir", b.BackupDir)
	}
	return nil
}

// TrackedBackupDirs lists the backup directories currently accounted for, so a
// sweep can distinguish them from debris.
func (o *Orchestrator) TrackedBackupDirs() []string {
	o.recoveredMu.Lock()
	defer o.recoveredMu.Unlock()

	dirs := make([]string, 0, len(o.recovered)+len(o.partialScratch))
	for _, rec := range o.recovered {
		dirs = append(dirs, rec.dir)
	}
	// Unfinished salvages count as tracked: they are hours of work, and the
	// sweep exists to remove debris rather than work in progress.
	dirs = append(dirs, o.partialScratch...)
	return dirs
}

// DiscardBackup deletes a drive's scratch copy on request.
//
// This is the manual escape hatch: a copy is kept whenever a rip did not
// succeed, so reclaiming the space has to be something the user can ask for.
func (o *Orchestrator) DiscardBackup(driveIndex int) error {
	o.recoveredMu.Lock()
	rec, ok := o.recovered[driveIndex]
	if !ok {
		o.recoveredMu.Unlock()
		return fmt.Errorf("no disc backup for drive %d", driveIndex)
	}
	dir := rec.dir
	unmount := rec.unmount
	delete(o.recovered, driveIndex)
	o.recoveredMu.Unlock()

	slog.Info("recovery: discarding disc backup on request", "drive_index", driveIndex, "dir", dir)
	o.forgetBackup(dir)
	if unmount != nil {
		unmount()
	}
	return nil
}

// DiscsWithBackup lists the discs that currently have a copy on disk.
//
// Activity history shows every rip that ever came off a repaired copy, but only
// some of those copies still exist. Offering to discard one that is already
// gone is a button that does nothing, so the page is told which discs are real.
func (o *Orchestrator) DiscsWithBackup() []string {
	o.recoveredMu.Lock()
	defer o.recoveredMu.Unlock()

	var discs []string
	seen := make(map[string]bool, len(o.recovered))
	for _, rec := range o.recovered {
		// A link tree costs kilobytes and disappears with the disc. There is no
		// space to reclaim, so there is nothing to offer.
		if rec.ephemeral {
			continue
		}
		for _, label := range rec.discLabels {
			if seen[label] {
				continue
			}
			seen[label] = true
			discs = append(discs, label)
		}
	}
	return discs
}

// DiscardBackupForDisc deletes the copy made from a named disc.
//
// The drive-page discard works by drive index, which is right there and right
// then. History is neither: a rip listed in Activity may have come off a drive
// that has since been renumbered or unplugged, and the disc name is the only
// identifier that still means the same thing.
func (o *Orchestrator) DiscardBackupForDisc(discLabel string) error {
	if discLabel == "" {
		return fmt.Errorf("no disc named")
	}

	o.recoveredMu.Lock()
	var dirs []string
	var unmounts []func()
	for idx, rec := range o.recovered {
		if !slices.Contains(rec.discLabels, discLabel) {
			continue
		}
		dirs = append(dirs, rec.dir)
		if rec.unmount != nil {
			unmounts = append(unmounts, rec.unmount)
		}
		delete(o.recovered, idx)
	}
	o.recoveredMu.Unlock()

	if len(dirs) == 0 {
		return fmt.Errorf("no disc copy for %q", discLabel)
	}

	for _, dir := range dirs {
		slog.Info("recovery: discarding disc copy on request", "disc", discLabel, "dir", dir)
		o.forgetBackup(dir)
	}
	for _, unmount := range unmounts {
		unmount()
	}
	return nil
}

// forgetBackup removes a backup from disk and from the record.
func (o *Orchestrator) forgetBackup(dir string) {
	removeBackupDir(dir)
	if o.store != nil && dir != "" {
		if err := o.store.DeleteDiscBackup(dir); err != nil {
			slog.Warn("recovery: could not drop backup record", "dir", dir, "error", err)
		}
	}
}

// retainRecovered claims the drive's current backup for a job about to rip from
// it, returning the claim to release later.
//
// The claim is the backup itself, not the drive index: a job must release the
// copy it actually read from, even if the drive has since been given a new disc.
// Returns nil when the drive has no recovered backup.
func (o *Orchestrator) retainRecovered(driveIndex int) *recoveredDisc {
	o.recoveredMu.Lock()
	defer o.recoveredMu.Unlock()

	rec, ok := o.recovered[driveIndex]
	if !ok {
		return nil
	}
	rec.refCount++
	return rec
}

// releaseRecovered drops a job's claim on a backup.
//
// The copy is deleted only when every job that used it succeeded. A rip that
// failed — or never produced anything — leaves it in place: it cost tens of
// minutes and up to ~100GB, and the user will want to retry without re-reading
// a disc that MakeMKV cannot open. Anything left behind is removed explicitly
// via DiscardBackup.
func (o *Orchestrator) releaseRecovered(claim *recoveredDisc, ripSucceeded bool) {
	if claim == nil {
		return
	}

	o.recoveredMu.Lock()
	claim.refCount--
	if !ripSucceeded {
		claim.ripFailed = true
	}
	done := claim.refCount <= 0
	// A link tree costs kilobytes and seconds to rebuild, so there is nothing
	// worth keeping for a retry — only a real copy is retained after a failure.
	discard := done && (!claim.ripFailed || claim.ephemeral)
	dir := claim.dir
	unmount := claim.unmount
	ephemeral := claim.ephemeral

	if discard {
		for idx, rec := range o.recovered {
			if rec == claim {
				delete(o.recovered, idx)
				break
			}
		}
	}
	o.recoveredMu.Unlock()

	if discard {
		if ephemeral {
			slog.Info("recovery: releasing the disc link tree", "dir", dir)
		} else {
			slog.Info("recovery: rip succeeded, discarding the scratch backup", "dir", dir)
		}
		o.forgetBackup(dir)
		if unmount != nil {
			unmount()
		}
		return
	}
	if done {
		slog.Warn("recovery: keeping the scratch backup because a rip did not succeed",
			"dir", dir, "hint", "discard it from the drive page when you no longer need it")
	}
}

// ReleaseRecoveredForDrive marks a drive's backup as no longer current, without
// deleting it.
//
// Ejecting a disc used to throw the copy away. It no longer does: a copy that
// never produced a successful rip is exactly the one worth keeping, and
// removing it is an explicit action.
func (o *Orchestrator) ReleaseRecoveredForDrive(driveIndex int) {
	o.recoveredMu.Lock()
	defer o.recoveredMu.Unlock()

	if rec, ok := o.recovered[driveIndex]; ok {
		rec.retired = true
	}
}

func removeBackupDir(dir string) {
	if dir == "" {
		return
	}
	slog.Info("recovery: removing disc backup", "dir", dir)
	if err := os.RemoveAll(dir); err != nil {
		slog.Warn("recovery: could not remove disc backup", "dir", dir, "error", err)
	}
}

// RecordDirectScan notes a disc that scanned normally.
//
// Recording the ordinary discs is what makes the odd ones legible: three discs
// in a twenty-five disc box set failed, and knowing the other twenty-two took
// the direct path is half the diagnosis.
func (o *Orchestrator) RecordDirectScan(scan *makemkv.DiscScan) {
	if scan == nil {
		return
	}
	o.recordDiagnostic(db.DiscDiagnostic{
		DiscLabel:       scan.DiscName,
		DiscKey:         discdb.BuildDiscKey(scan),
		DriveIndex:      scan.DriveIndex,
		ScrambleVerdict: string(aacs.VerdictNotApplicable),
		RipPath:         "direct",
		Outcome:         "ok",
	})
}

// buildSymlinkTree mirrors a disc's top level as symlinks, omitting AACS.
//
// MakeMKV decides whether to demand a volume key from the tree it is pointed
// at, not from the physical disc, so a tree of links with AACS left out is
// enough: it reads the disc directly and only the titles that get selected.
// Confirmed on real hardware — MakeMKV logged "AACS directory not present,
// assuming unencrypted disc" and both scanned and ripped through the links.
//
// This costs kilobytes and milliseconds where a backup costs ~100GB and an
// hour, so it is tried first; the copy remains the fallback.
func buildSymlinkTree(discRoot, linkDir string) error {
	entries, err := os.ReadDir(discRoot)
	if err != nil {
		return fmt.Errorf("read disc root %s: %w", discRoot, err)
	}

	if err := os.MkdirAll(linkDir, 0o777); err != nil {
		return fmt.Errorf("create link dir %s: %w", linkDir, err)
	}

	linked := 0
	for _, e := range entries {
		if strings.EqualFold(e.Name(), "AACS") {
			// The entire point: MakeMKV must not find it.
			continue
		}
		target := filepath.Join(discRoot, e.Name())
		link := filepath.Join(linkDir, e.Name())

		// Replace any existing entry so a repeat attempt is not an error.
		if err := os.Remove(link); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("clear %s: %w", link, err)
		}
		if err := os.Symlink(target, link); err != nil {
			return fmt.Errorf("link %s -> %s: %w", link, target, err)
		}
		linked++
	}

	if linked == 0 {
		return fmt.Errorf("disc root %s contained nothing to link", discRoot)
	}
	return nil
}

// recoverViaSymlinks recovers a disc without copying it.
//
// The mount is handed to the caller's recovered-disc record rather than
// released here: the links point at the mounted disc, so it has to stay mounted
// for as long as anything might rip from them.
func (o *Orchestrator) recoverViaSymlinks(
	ctx context.Context,
	req RecoveryRequest,
	discRoot, scratchRoot string,
	diag db.DiscDiagnostic,
	unmount func(),
) (*RecoveredDisc, error) {
	linkDir := filepath.Join(scratchRoot, scratchSlug(req.DiscLabel, req.DevicePath)+"-link")

	if err := buildSymlinkTree(discRoot, linkDir); err != nil {
		return nil, err
	}

	src := makemkv.FileSource(linkDir)
	scan, err := o.backupper.ScanSource(ctx, src)
	if err != nil {
		removeBackupDir(linkDir)
		return nil, fmt.Errorf("scan of symlinked disc failed: %w", err)
	}
	if len(scan.Titles) == 0 {
		removeBackupDir(linkDir)
		return nil, fmt.Errorf("symlinked disc scanned but produced no titles")
	}

	diag.RipPath = "symlink"
	diag.Outcome = "ok"
	diag.Detail = fmt.Sprintf("read directly from the disc through a link tree; no copy taken (%s)", diag.Detail)
	diag.DiscKey = discdb.BuildDiscKey(scan)
	if scan.DiscName != "" {
		diag.DiscLabel = scan.DiscName
	}
	diagID := o.recordDiagnostic(diag)

	slog.Info("recovery: disc recovered without copying",
		"disc", req.DiscLabel, "link_dir", linkDir, "titles", len(scan.Titles))

	return &RecoveredDisc{
		Source:       src,
		Dir:          linkDir,
		Scan:         scan,
		DiagnosticID: diagID,
		unmount:      unmount,
		// A link tree is meaningless once the mount is gone, so it is never
		// restored after a restart — it is seconds to rebuild anyway.
		Ephemeral: true,
	}, nil
}

// removeAACSDir deletes the AACS directory from a backup.
//
// This is the only destructive step in the feature, so it is constrained rather
// than trusted: the target must resolve to a path inside the scratch root, which
// makes deleting anything the user cares about structurally impossible even if a
// caller passes a bad directory. The disc itself is never touched.
func removeAACSDir(scratchRoot, backupDir string) error {
	absRoot, err := filepath.Abs(scratchRoot)
	if err != nil {
		return fmt.Errorf("resolve scratch root: %w", err)
	}
	absBackup, err := filepath.Abs(backupDir)
	if err != nil {
		return fmt.Errorf("resolve backup dir: %w", err)
	}
	// Resolve symlinks so a link inside the scratch root cannot redirect the
	// deletion somewhere else.
	if resolved, err := filepath.EvalSymlinks(absRoot); err == nil {
		absRoot = resolved
	}
	if resolved, err := filepath.EvalSymlinks(absBackup); err == nil {
		absBackup = resolved
	}

	if absBackup != absRoot && !strings.HasPrefix(absBackup, absRoot+string(filepath.Separator)) {
		return fmt.Errorf("refusing to modify %s: outside the scratch root %s", absBackup, absRoot)
	}

	target := filepath.Join(absBackup, "AACS")
	if _, err := os.Stat(target); os.IsNotExist(err) {
		// Already absent — MakeMKV will treat the folder as unencrypted, which
		// is the desired end state.
		slog.Info("recovery: no AACS directory in the backup, nothing to strip", "backup_dir", absBackup)
		return nil
	}

	// This is the one destructive step in the feature; the log records exactly
	// what was removed, so a surprising outcome can be traced to it.
	entries, _ := os.ReadDir(target)
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	slog.Info("recovery: removing AACS directory from backup",
		"path", target, "entry_count", len(names), "entries", strings.Join(names, ","))

	if err := os.RemoveAll(target); err != nil {
		return err
	}
	slog.Info("recovery: AACS directory removed", "path", target)
	return nil
}

// SweepScratch removes disc backups left behind by a previous run. Each is up
// to ~100GB, so a crash must not leak them indefinitely.
func SweepScratch(outputDir string, keep []string) error {
	scratchRoot := filepath.Join(outputDir, ScratchDirName)
	entries, err := os.ReadDir(scratchRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("sweep scratch: %w", err)
	}

	tracked := make(map[string]bool, len(keep))
	for _, dir := range keep {
		if abs, err := filepath.Abs(dir); err == nil {
			tracked[abs] = true
		}
	}

	for _, e := range entries {
		path := filepath.Join(scratchRoot, e.Name())
		abs, err := filepath.Abs(path)
		if err == nil && tracked[abs] {
			// A live backup, restored from the database. Deleting it here used
			// to throw away a copy that cost ~100GB and tens of minutes.
			slog.Info("recovery: keeping tracked disc backup", "path", path)
			continue
		}
		// Say how much is going. A sweep that silently deleted ~100GB read as a
		// single unremarkable log line, which is how a copy worth 2.5 hours
		// disappeared without anyone noticing until it was needed.
		size := dirSize(path)
		slog.Warn("recovery: deleting untracked disc backup",
			"path", path, "bytes", size, "gib", size/(1<<30),
			"reason", "no database record accounts for it")
		if err := os.RemoveAll(path); err != nil {
			slog.Warn("recovery: could not sweep untracked backup", "path", path, "error", err)
		}
	}
	return nil
}

// recordDiagnostic writes a diagnostic row, returning its id. Diagnostics are
// observational: a failure to record must never fail a rip.
func (o *Orchestrator) recordDiagnostic(d db.DiscDiagnostic) int64 {
	if o.store == nil {
		return 0
	}
	id, err := o.store.SaveDiscDiagnostic(d)
	if err != nil {
		slog.Warn("recovery: could not record disc diagnostic", "disc", d.DiscLabel, "error", err)
		return 0
	}
	return id
}

func (o *Orchestrator) finishDiagnostic(id int64, outcome, detail string, backupBytes int64) {
	if id == 0 || o.store == nil {
		return
	}
	if err := o.store.UpdateDiscDiagnosticOutcome(id, outcome, detail, backupBytes); err != nil {
		slog.Warn("recovery: could not finish disc diagnostic", "id", id, "error", err)
	}
}

// scratchSlug builds a stable, filesystem-safe directory name for a disc.
// discLabelsFor lists every name a copy can be matched by, best first.
//
// Both the recorded label and the one encoded in the directory are kept. They
// normally agree; where they do not, the copy was made by a version that
// recorded whatever scanning the copied folder reported, and only the directory
// still carries the disc's own name. Matching on either is what makes those
// copies actionable without a migration.
func discLabelsFor(recorded, dir string) []string {
	var labels []string
	for _, l := range []string{recorded, discLabelFromDir(dir)} {
		if l == "" || slices.Contains(labels, l) {
			continue
		}
		labels = append(labels, l)
	}
	return labels
}

// discLabelOf resolves the disc a copy was made from.
//
// The request that made the copy is authoritative: it is the same label the
// drive, the jobs and the history rows all use. Scanning the copy is only a
// fallback, and a poor one — a folder scan reports what the copied BDMV calls
// itself, which is often not the volume label and sometimes nothing.
//
// Failing both, the directory name still carries it: scratchSlug builds every
// copy's name as "<sanitized label>-<hash>". That is what gives copies made by
// earlier versions, which recorded no label at all, a name to match on.
func discLabelOf(rec *RecoveredDisc) string {
	if rec == nil {
		return ""
	}
	if rec.DiscLabel != "" {
		return rec.DiscLabel
	}
	if rec.Scan != nil && rec.Scan.DiscName != "" {
		return rec.Scan.DiscName
	}
	return discLabelFromDir(rec.Dir)
}

// discLabelFromDir recovers a disc label from the scratch directory named after
// it, undoing scratchSlug's "<label>-<hash>" and the "-link" suffix a symlink
// tree adds.
func discLabelFromDir(dir string) string {
	name := strings.TrimSuffix(filepath.Base(dir), "-link")
	cut := strings.LastIndex(name, "-")
	if cut <= 0 {
		return ""
	}
	// Only the 8 hex characters scratchSlug appends. Anything else is part of a
	// disc name that happens to contain a hyphen.
	if suffix := name[cut+1:]; len(suffix) != 8 || strings.Trim(suffix, "0123456789abcdef") != "" {
		return ""
	}
	if label := name[:cut]; label != "disc" {
		return label
	}
	// scratchSlug's placeholder for a disc that had no name to begin with.
	return ""
}

func scratchSlug(discLabel, devicePath string) string {
	name := organizer.SanitizeFilename(discLabel)
	if name == "" {
		name = "disc"
	}
	if len(name) > 60 {
		name = name[:60]
	}
	sum := sha256.Sum256([]byte(discLabel + "|" + devicePath))
	return fmt.Sprintf("%s-%x", name, sum[:4])
}

// mkbDumpPattern matches the dump MakeMKV writes when it cannot decrypt a disc,
// e.g. MKB20_v82_SOME_TITLE_a1b2c3d4.tgz. The MKB version it encodes is the
// single most useful field for spotting a pattern across discs.
var mkbDumpPattern = regexp.MustCompile(`MKB(\d+)_v(\d+)`)

// mkbVersion identifies the disc's Media Key Block.
//
// The disc itself is the better source: AACS/MKB_RO.inf is present on every
// protected disc, whereas MakeMKV only writes the .tgz dump when it fails — so
// a disc that recovers successfully would otherwise record no version at all,
// which is precisely the case worth tracking across a box set.
func mkbVersion(discRoot, dumpPath string) string {
	if info, err := aacs.ReadMKBInfo(discRoot); err == nil {
		if s := info.String(); s != "" {
			return s
		}
	} else if info.RawHeader != "" {
		slog.Warn("recovery: could not parse MKB header, recording raw bytes",
			"error", err, "raw_header", info.RawHeader)
	}
	return mkbVersionFromDump(dumpPath)
}

func mkbVersionFromDump(dumpPath string) string {
	m := mkbDumpPattern.FindStringSubmatch(filepath.Base(dumpPath))
	if m == nil {
		return ""
	}
	return fmt.Sprintf("%s v%s", m[1], m[2])
}

func dumpHint(dumpPath string) string {
	if dumpPath == "" {
		return ""
	}
	return fmt.Sprintf(" — MakeMKV wrote a dump at %s; send it to svq@makemkv.com to have the key added", dumpPath)
}

// treeSize sums the size of every file under root.
func treeSize(root string) (int64, error) {
	var total int64
	err := filepath.WalkDir(root, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		total += info.Size()
		return nil
	})
	return total, err
}

// backupProgressInterval is how often the growing backup is measured. Frequent
// enough that the bar visibly moves, rare enough that walking the tree costs
// nothing against a copy measured in tens of minutes.
const backupProgressInterval = 5 * time.Second

// backupPercent reports how far a backup has got, from bytes written against
// the size measured on the disc.
//
// It never returns 100: the estimate carries headroom, and a bar sitting at
// 100% while the copy is still running is worse than one that stops just short.
// Completion is signalled by the phase changing, not by the number.
func backupPercent(written, needed int64) int {
	if needed <= 0 || written <= 0 {
		return 0
	}
	pct := int(written * 100 / needed)
	if pct > 99 {
		return 99
	}
	return pct
}

// dirSize is treeSize with errors ignored, for reporting only.
func dirSize(root string) int64 {
	n, _ := treeSize(root)
	return n
}

// salvageNoteFor describes what a salvaged copy cost, for the record kept
// against every rip made from it.
//
// An ordinary recovery gets nothing: it produces a faithful copy, and a note
// there would be noise. A salvage always gets one, including when it recovered
// everything, because "salvaged" is itself worth knowing about a file.
func salvageNoteFor(rec *RecoveredDisc) string {
	if !rec.Salvaged {
		return ""
	}
	if !rec.Measured {
		return "Salvaged from a damaged disc"
	}
	if rec.Unrecovered <= 0 {
		return "Salvaged from a damaged disc; everything was recovered"
	}
	return fmt.Sprintf("Salvaged from a damaged disc; %s could not be read and is blank in this file",
		humanBytes(rec.Unrecovered))
}
