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
	"strings"

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
	Source       makemkv.Source
	Dir          string
	Scan         *makemkv.DiscScan
	DiagnosticID int64
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
	defer cleanup()

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
		MKBVersion:       mkbVersionFromDump(req.DumpPath),
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

	scratchRoot := filepath.Join(req.OutputDir, ScratchDirName)
	if err := os.MkdirAll(scratchRoot, 0o777); err != nil {
		o.finishDiagnostic(diagID, "failed", fmt.Sprintf("create scratch root: %v", err), 0)
		return nil, fmt.Errorf("recovery: create scratch root %s: %w", scratchRoot, err)
	}

	if err := o.checkSpace(scratchRoot, needed); err != nil {
		detail := fmt.Sprintf("need %d bytes free in %s: %v", needed, scratchRoot, err)
		o.finishDiagnostic(diagID, "failed", detail, 0)
		return nil, fmt.Errorf("recovery: insufficient space for disc backup — %s", detail)
	}

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

	err = o.backupper.Backup(ctx, req.DriveIndex, backupDir, func(ev makemkv.Event) {
		if ev.Type == "PRGV" && ev.Progress != nil && ev.Progress.Max > 0 {
			pct := ev.Progress.Total * 100 / ev.Progress.Max
			if pct > 100 {
				pct = 100
			}
			progress("backing_up", pct)
		}
	})
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

	size := dirSize(backupDir)
	o.finishDiagnostic(diagID, "ok", insp.Reason, size)
	progress("done", 100)

	slog.Info("recovery: disc recovered from spurious AACS directory",
		"disc", req.DiscLabel, "backup_dir", backupDir,
		"titles", len(scan.Titles), "backup_bytes", size)

	return &RecoveredDisc{Source: src, Dir: backupDir, Scan: scan, DiagnosticID: diagID}, nil
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

	rec, err := o.RecoverSpuriousAACS(ctx, RecoveryRequest{
		DriveIndex: driveIndex,
		DevicePath: devicePath,
		DiscLabel:  se.Scan.DiscName,
		OutputDir:  outputDir,
		DumpPath:   dumpPathFromMessages(se.Messages()),
		OnProgress: func(phase string, percent int) {
			o.broadcastRecovery(driveIndex, se.Scan.DiscName, phase, percent, "")
		},
	})
	if err != nil {
		o.broadcastRecovery(driveIndex, se.Scan.DiscName, "failed", 0, err.Error())
		return nil, err
	}

	o.registerRecovered(driveIndex, rec)
	o.broadcastRecovery(driveIndex, se.Scan.DiscName, "done", 100, "")
	return rec.Scan, nil
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

// registerRecovered records a recovered disc, replacing and cleaning up any
// backup previously held for the same drive.
func (o *Orchestrator) registerRecovered(driveIndex int, rec *RecoveredDisc) {
	o.recoveredMu.Lock()
	stale := o.recovered[driveIndex]
	o.recovered[driveIndex] = &recoveredDisc{source: rec.Source, dir: rec.Dir}
	o.recoveredMu.Unlock()

	// A previous backup for this drive with no outstanding jobs is dead weight.
	if stale != nil && stale.refCount == 0 && stale.dir != rec.Dir {
		removeBackupDir(stale.dir)
	}
}

// retainRecovered claims a reference for a job about to rip from the backup.
func (o *Orchestrator) retainRecovered(driveIndex int) {
	o.recoveredMu.Lock()
	defer o.recoveredMu.Unlock()
	if rec, ok := o.recovered[driveIndex]; ok {
		rec.refCount++
	}
}

// releaseRecovered drops a job's reference and deletes the backup once the last
// job for the disc has finished — the scratch copy is up to ~100GB.
func (o *Orchestrator) releaseRecovered(driveIndex int) {
	o.recoveredMu.Lock()
	rec, ok := o.recovered[driveIndex]
	if !ok {
		o.recoveredMu.Unlock()
		return
	}
	rec.refCount--
	done := rec.refCount <= 0
	dir := rec.dir
	if done {
		delete(o.recovered, driveIndex)
	}
	o.recoveredMu.Unlock()

	if done {
		removeBackupDir(dir)
	}
}

// ReleaseRecoveredForDrive drops a drive's backup outright, used when a disc is
// ejected. A rip still in flight keeps its backup: the refcount is only zero
// when nothing is reading from it.
func (o *Orchestrator) ReleaseRecoveredForDrive(driveIndex int) {
	o.recoveredMu.Lock()
	rec, ok := o.recovered[driveIndex]
	if !ok || rec.refCount > 0 {
		o.recoveredMu.Unlock()
		return
	}
	dir := rec.dir
	delete(o.recovered, driveIndex)
	o.recoveredMu.Unlock()

	removeBackupDir(dir)
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
		return nil
	}
	return os.RemoveAll(target)
}

// SweepScratch removes disc backups left behind by a previous run. Each is up
// to ~100GB, so a crash must not leak them indefinitely.
func SweepScratch(outputDir string) error {
	scratchRoot := filepath.Join(outputDir, ScratchDirName)
	entries, err := os.ReadDir(scratchRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("sweep scratch: %w", err)
	}
	for _, e := range entries {
		path := filepath.Join(scratchRoot, e.Name())
		slog.Info("recovery: sweeping stale disc backup", "path", path)
		if err := os.RemoveAll(path); err != nil {
			slog.Warn("recovery: could not sweep stale backup", "path", path, "error", err)
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

// dirSize is treeSize with errors ignored, for reporting only.
func dirSize(root string) int64 {
	n, _ := treeSize(root)
	return n
}
