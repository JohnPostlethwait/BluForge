package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/johnpostlethwait/bluforge/internal/contribute"
	"github.com/johnpostlethwait/bluforge/internal/db"
	"github.com/johnpostlethwait/bluforge/internal/ddrescue"
	"github.com/johnpostlethwait/bluforge/internal/discdb"
	"github.com/johnpostlethwait/bluforge/internal/fsutil"
	"github.com/johnpostlethwait/bluforge/internal/makemkv"
	"github.com/johnpostlethwait/bluforge/internal/mpls"
	"github.com/johnpostlethwait/bluforge/internal/organizer"
	"github.com/johnpostlethwait/bluforge/internal/ripper"
)

// DiscScanner abstracts disc scanning for testability.
type DiscScanner interface {
	ScanDisc(ctx context.Context, driveIndex int) (*makemkv.DiscScan, error)
}

// OrchestratorDeps holds the dependencies required to construct an Orchestrator.
type OrchestratorDeps struct {
	Store       *db.Store
	Engine      *ripper.Engine
	Organizer   *organizer.Organizer
	OnBroadcast func(event, data string)
	Scanner     DiscScanner
	DiscDB      *discdb.Client
	Cache       *discdb.Cache
	// Backupper performs the raw disc backup and folder rescan used to recover
	// a disc whose AACS directory is spurious. Optional: when nil, a disc that
	// trips the signature is reported rather than recovered.
	Backupper DiscBackupper
	// OpenDiscRoot makes a disc readable as a directory tree for inspection.
	// Defaults to mounting via the mpls package.
	OpenDiscRoot DiscRootOpener
	// OnDriveState reports a drive state the poller cannot infer on its own,
	// so a drive spending 40 minutes in recovery does not look idle. Optional.
	OnDriveState func(driveIndex int, state string)
}

// Orchestrator coordinates the end-to-end rip pipeline: disk space check,
// destination path construction, duplicate detection, DB job creation,
// engine submission, and completion handling.
type Orchestrator struct {
	store       *db.Store
	engine      *ripper.Engine
	organizer   *organizer.Organizer
	onBroadcast func(event, data string)
	scanner     DiscScanner
	discDB      *discdb.Client
	cache       *discdb.Cache

	backupper    DiscBackupper
	openDiscRoot DiscRootOpener
	onDriveState func(driveIndex int, state string)
	// checkSpace verifies the scratch volume can hold a disc backup. Injected
	// so the early-failure path can be tested without filling a disk.
	checkSpace func(path string, needBytes int64) error
	// rescuer runs ddrescue over the streams a damaged disc will not give up.
	// Injected so salvage can be tested without a scratched disc.
	rescuer ddrescue.Runner

	onDiscChanged func(driveIndex int)

	scanMu    sync.RWMutex
	scanCache map[string]*cachedScan // keyed by "driveIndex:discName"
	// driveDisc is the disc each drive currently holds, as last reported by the
	// drive itself. It is what makes a cache lookup exact: the cache is keyed by
	// drive and disc, but nearly every caller has only a drive index, and
	// answering those with "whatever is cached for this drive" is how one disc's
	// titles get shown for another. Guarded by scanMu.
	driveDisc map[int]string
	// scanning tracks in-flight scans so the page can be told what a scan that
	// takes half an hour is actually doing. Guarded by scanMu.
	scanning map[int]*scanState

	// recovered tracks discs currently being ripped from a stripped backup, so
	// the scratch copy can be deleted once the last job for the disc finishes.
	// outputDir is guarded by the same mutex: it is where scratch backups go.
	recoveredMu sync.Mutex
	recovered   map[int]*recoveredDisc
	// recovering guards against a second backup being started for a drive that
	// is already being recovered — two ~100GB copies racing each other.
	recovering map[int]bool
	// salvaging maps a drive being recovered from physical damage to the cancel
	// that pauses it. Mutually exclusive with recovering: both copy the whole
	// disc. Guarded by recoveredMu.
	salvaging map[int]context.CancelFunc
	// partialScratch holds unfinished salvage copies restored from a previous
	// run: protected from the startup sweep, not offered as rippable.
	partialScratch []string
	// salvageLabels remembers which disc each drive is salvaging, so a progress
	// broadcast can say whether there is work to resume from.
	salvageLabels map[int]string
	outputDir     string

	// scanLocks serialises scans per drive. Guarded by scanLockMu.
	scanLockMu sync.Mutex
	scanLocks  map[int]*sync.Mutex
}

// lockDriveScan blocks until this drive's scan slot is free, returning the
// release function.
func (o *Orchestrator) lockDriveScan(driveIndex int) func() {
	o.scanLockMu.Lock()
	if o.scanLocks == nil {
		o.scanLocks = make(map[int]*sync.Mutex)
	}
	mu, ok := o.scanLocks[driveIndex]
	if !ok {
		mu = &sync.Mutex{}
		o.scanLocks[driveIndex] = mu
	}
	o.scanLockMu.Unlock()

	mu.Lock()
	return mu.Unlock
}

// recoveredDisc is a live backup: the folder jobs are ripping from, plus a
// count of how many of those jobs are still outstanding.
type recoveredDisc struct {
	source makemkv.Source
	dir    string
	// discLabels are the names this copy is known by. Activity history outlives
	// the drive it was ripped on — drives are renumbered when the bus
	// re-enumerates — so anything acting on a copy from a history row has to
	// find it by disc rather than by drive index.
	//
	// Plural because a copy made by an earlier version recorded whatever
	// scanning the copied folder reported, which is not always the disc's own
	// name. The directory is named from the real label, so both are kept and
	// either one matches.
	discLabels []string
	// refCount is how many jobs are still ripping from this backup.
	refCount int
	// retired means the drive has moved on — the disc was ejected or replaced.
	// The copy is kept regardless; only a successful rip or an explicit discard
	// removes it.
	retired bool
	// ripFailed records that at least one job using this backup did not
	// succeed, which is what keeps the copy on disk for a retry.
	ripFailed bool
	// ephemeral marks a symlink tree rather than a copy: there is nothing
	// expensive to preserve, so it is always cleaned up and never persisted.
	ephemeral bool
	// salvageNote describes damage this copy carries, for the jobs ripped from
	// it. Empty for an ordinary recovery.
	salvageNote string
	// unmount releases the disc mount a symlink tree depends on.
	unmount func()
}

// NewOrchestrator creates a new Orchestrator from the provided dependencies.
func NewOrchestrator(deps OrchestratorDeps) *Orchestrator {
	openRoot := deps.OpenDiscRoot
	if openRoot == nil {
		openRoot = mpls.OpenDiscRoot
	}
	return &Orchestrator{
		store:        deps.Store,
		engine:       deps.Engine,
		organizer:    deps.Organizer,
		onBroadcast:  deps.OnBroadcast,
		scanner:      deps.Scanner,
		discDB:       deps.DiscDB,
		cache:        deps.Cache,
		backupper:    deps.Backupper,
		openDiscRoot: openRoot,
		onDriveState: deps.OnDriveState,

		checkSpace: ripper.CheckDiskSpace,
		rescuer:    ddrescue.ExecRunner{},
		scanCache:  make(map[string]*cachedScan),
		driveDisc:  make(map[int]string),
		recovered:  make(map[int]*recoveredDisc),
		recovering: make(map[int]bool),
		salvaging:  make(map[int]context.CancelFunc),
		scanLocks:  make(map[int]*sync.Mutex),
	}
}

// ManualRip processes each title in params, building destination paths,
// checking for duplicates, creating DB records, and submitting rip jobs
// to the engine. It returns a RipResult summarising the outcome of each title.
func (o *Orchestrator) ManualRip(params ManualRipParams) RipResult {
	var result RipResult

	// Create one parent temp directory for all titles in this rip session.
	// Individual per-title subdirs are created lazily when each job starts.
	parentTempDir, err := fsutil.MkdirTemp(params.OutputDir, ".rip-")
	if err != nil {
		slog.Error("failed to create parent temp dir", "error", err)
		for _, sel := range params.Titles {
			result.Titles = append(result.Titles, TitleResult{
				TitleIndex: sel.TitleIndex,
				Status:     "failed",
				Reason:     fmt.Sprintf("create parent temp dir: %v", err),
			})
		}
		return result
	}

	// Destinations are built for the batch, because a collision is only visible
	// across titles: a disc that offers its feature as both a playlist and a raw
	// stream yields the same name twice, and one rip would overwrite the other.
	destPaths := o.buildDestPaths(params)

	var wg sync.WaitGroup
	for i, sel := range params.Titles {
		tr := o.processTitle(params, sel, destPaths[i], parentTempDir, &wg)
		result.Titles = append(result.Titles, tr)
	}
	go func() {
		wg.Wait()
		os.Remove(parentTempDir)
	}()

	// Save disc mapping if we have the necessary identifiers.
	if params.DiscKey != "" && params.MediaItemID != "" {
		if err := o.store.SaveMapping(db.DiscMapping{
			DiscKey:     params.DiscKey,
			DiscName:    params.DiscName,
			MediaItemID: params.MediaItemID,
			ReleaseID:   params.ReleaseID,
			DiscID:      params.DiscID,
			MediaTitle:  params.MediaTitle,
			MediaYear:   params.MediaYear,
			MediaType:   params.MediaType,
		}); err != nil {
			slog.Error("failed to save disc mapping", "disc_key", params.DiscKey, "error", err)
		}
	}

	return result
}

// processTitle handles a single title: build path, duplicate check, disk space,
// DB creation, engine submission.
func (o *Orchestrator) processTitle(params ManualRipParams, sel TitleSelection, destPath string, parentTempDir string, wg *sync.WaitGroup) TitleResult {
	// 1. The destination is chosen for the batch so titles that would share a
	// name are told apart; see buildDestPaths.
	fullDest := filepath.Join(params.OutputDir, destPath)

	// Guard against path traversal: ensure the destination stays within OutputDir.
	// SanitizeFilename strips / and \ but not bare "..", which filepath.Join cleans
	// into a traversal (e.g. filepath.Join("/output", "..") == "/").
	absBase, err := filepath.Abs(params.OutputDir)
	if err != nil {
		return TitleResult{
			TitleIndex: sel.TitleIndex,
			Status:     "failed",
			Reason:     fmt.Sprintf("resolve output dir: %v", err),
		}
	}
	absDest, err := filepath.Abs(fullDest)
	if err != nil {
		return TitleResult{
			TitleIndex: sel.TitleIndex,
			Status:     "failed",
			Reason:     fmt.Sprintf("resolve destination path: %v", err),
		}
	}
	if !strings.HasPrefix(absDest, absBase+string(filepath.Separator)) {
		return TitleResult{
			TitleIndex: sel.TitleIndex,
			Status:     "failed",
			Reason:     "destination path escapes output directory",
		}
	}

	// 2. Check for duplicates.
	if organizer.FileExists(fullDest) {
		switch params.DuplicateAction {
		case "skip":
			return TitleResult{
				TitleIndex: sel.TitleIndex,
				Status:     "skipped",
				Reason:     fmt.Sprintf("duplicate exists: %s", destPath),
			}
		case "rename":
			// Compute a non-colliding path. fullDest is captured by the
			// OnComplete closure below, so reassigning it here is sufficient.
			fullDest = organizer.NonCollidingPath(fullDest)
		case "overwrite":
			// Intentional fall-through: AtomicMove overwrites by default.
		default:
			slog.Warn("unknown duplicate_action, defaulting to overwrite",
				"action", params.DuplicateAction, "dest", fullDest)
		}
	}

	// 3. Check disk space.
	if err := ripper.CheckDiskSpace(params.OutputDir, sel.SizeBytes); err != nil {
		return TitleResult{
			TitleIndex: sel.TitleIndex,
			Status:     "failed",
			Reason:     fmt.Sprintf("disk space: %v", err),
		}
	}

	// 4. Create DB job.
	metaJSON, _ := json.Marshal(sel.TrackMetadata)
	jobID, err := o.store.CreateJob(db.RipJob{
		DriveIndex:    params.DriveIndex,
		DiscName:      params.DiscName,
		TitleIndex:    sel.TitleIndex,
		TitleName:     sel.TitleName,
		ContentType:   sel.ContentType,
		OutputPath:    fullDest,
		Status:        "ripping",
		SizeBytes:     sel.SizeBytes,
		TrackMetadata: string(metaJSON),
		// A rip from a salvaged disc carries damage the file itself cannot
		// explain. The job record is where that explanation lives.
		SalvageNote: o.salvageNoteForDrive(params.DriveIndex),
		// The choices behind this rip, kept so a salvage can repeat it exactly
		// rather than sending the user back to choose everything again.
		SourceFile:    sel.SourceFile,
		SelectionOpts: encodeSelectionOpts(params.SelectionOpts),
	})
	if err != nil {
		return TitleResult{
			TitleIndex: sel.TitleIndex,
			Status:     "failed",
			Reason:     fmt.Sprintf("create job: %v", err),
		}
	}

	// 5. Create ripper job. The per-title temp subdirectory is created lazily
	// via OnStart when the rip actually begins (not at submission time), so
	// queued jobs don't create orphaned temp dirs up front.
	ripJob := ripper.NewJob(params.DriveIndex, sel.TitleIndex, params.DiscName, "")
	ripJob.ID = jobID
	ripJob.TitleName = sel.TitleName
	// The index alone is not enough to identify a title across makemkvcon
	// invocations; the source file is what the rip verifies against.
	ripJob.SourceFile = sel.SourceFile
	ripJob.ContentType = sel.ContentType
	ripJob.TrackMetadata = sel.TrackMetadata
	ripJob.SelectionOpts = params.SelectionOpts

	// A disc recovered from a spurious AACS directory is ripped from its
	// stripped backup: MakeMKV cannot open the drive for these discs at all.
	// Track selection and everything else is unchanged — only the source moves.
	// The claim is on the backup itself, so this job releases the copy it read
	// from even if the drive is given a different disc in the meantime.
	var backupClaim *recoveredDisc
	if src := o.RecoveredSource(params.DriveIndex); src != nil {
		ripJob.Source = *src
		backupClaim = o.retainRecovered(params.DriveIndex)
	}

	// OnStart: create the per-title subdir inside the shared parent temp dir.
	ripJob.OnStart = func(job *ripper.Job) error {
		titleDir, err := fsutil.MkdirTemp(parentTempDir, fmt.Sprintf("t%d-", sel.TitleIndex))
		if err != nil {
			return fmt.Errorf("create title temp dir: %w", err)
		}
		job.OutputDir = titleDir
		return nil
	}

	// OnComplete: move the ripped file to its final destination and clean up.
	//
	// What it returns is how the job ends. The engine holds the job at
	// Organizing for the duration and settles it from this, so a rip that read
	// the disc perfectly and then could not place the file is reported as the
	// failure it is rather than as a success the database quietly disagreed with.
	ripJob.OnComplete = func(job *ripper.Job, ripErr error) (outcome error) {
		defer wg.Done()
		// Drop this job's claim on the scratch backup. The last job to finish
		// takes the ~100GB copy with it.
		//
		// On the job's outcome, not on the rip's: a rip that read the disc
		// perfectly and then could not place the file has produced nothing, and
		// the copy is exactly what a retry needs. Keyed on ripErr, the copy was
		// deleted the moment a full destination failed the move.
		if backupClaim != nil {
			defer func() { o.releaseRecovered(backupClaim, outcome == nil) }()
		}
		if ripErr != nil {
			// Look before deleting, and say what was there. Whether makemkvcon
			// leaves its partial output behind or removes it itself was unknown
			// because this path called RemoveAll without ever opening the
			// directory. The file itself is no use -- an MKV cannot be fed back
			// into a rip or a salvage -- so it still goes, but the fact is
			// recorded rather than destroyed unseen.
			if partial, size := largestFile(job.OutputDir); partial != "" {
				slog.Warn("rip failed after writing a partial file; discarding it",
					"job_id", job.ID, "path", partial, "bytes", size)
			}
			if job.OutputDir != "" {
				if err := os.RemoveAll(job.OutputDir); err != nil {
					slog.Warn("failed to remove temp output dir", "dir", job.OutputDir, "err", err)
				}
			}
			o.setJobStatus(job.ID, "failed", job.Progress, ripErr.Error())
			return ripErr
		}

		// makemkvcon wrote the .mkv itself, with its own idea of the mode. The
		// move below preserves whatever it chose, so the film would carry it
		// into the library; normalise here, while it is still ours alone.
		if normErr := fsutil.NormalizeTree(job.OutputDir); normErr != nil {
			slog.Warn("could not normalise ripped file permissions", "job_id", job.ID, "dir", job.OutputDir, "error", normErr)
		}

		// Find the .mkv file MakeMKV wrote to the title temp dir.
		srcPath, findErr := findMKVFile(job.OutputDir)
		if findErr != nil {
			slog.Error("could not find ripped file", "job_id", job.ID, "rip_dir", job.OutputDir, "error", findErr)
			o.setJobStatus(job.ID, "failed", 100, findErr.Error())
			return findErr
		}

		// Move the ripped file to its final organized destination.
		slog.Info("organizing ripped file", "job_id", job.ID, "src", srcPath, "dest", fullDest)
		if moveErr := organizer.AtomicMove(srcPath, fullDest); moveErr != nil {
			// The rip itself succeeded. srcPath is a complete title that cost
			// however long the disc took to read, and only the move failed --
			// a full, read-only or unwritable destination, all of which the user
			// can fix and then retry. Deleting the directory here would throw
			// the film away over a problem outside it, so the file stays put and
			// the job records where it is. That record is also what keeps
			// SweepRipDirs from removing the directory on the next start.
			var size int64
			if fi, statErr := os.Stat(srcPath); statErr == nil {
				size = fi.Size()
			}
			slog.Error("rip finished but could not be moved to its destination; keeping the file",
				"job_id", job.ID, "src", srcPath, "dest", fullDest, "bytes", size, "error", moveErr)
			if dbErr := o.store.UpdateJobOutput(job.ID, srcPath, size); dbErr != nil {
				slog.Error("could not record where the rip was kept", "job_id", job.ID, "path", srcPath, "error", dbErr)
			}
			keptErr := fmt.Errorf("organize: %w; the ripped file was kept at %s", moveErr, srcPath)
			o.setJobStatus(job.ID, "failed", 100, keptErr.Error())
			return keptErr
		}

		// Clean up title temp dir. Parent is cleaned up by the WaitGroup goroutine
		// in ManualRip once all titles have completed.
		if err := os.RemoveAll(job.OutputDir); err != nil {
			slog.Warn("failed to remove temp output dir", "dir", job.OutputDir, "err", err)
		}

		// Measure what actually landed. The size shown against a finished rip
		// was MakeMKV's estimate for the title on the disc, which reported a
		// 67.4 GB success for a file that is 118 MB.
		var outputSize int64
		if fi, statErr := os.Stat(fullDest); statErr == nil {
			outputSize = fi.Size()
		} else {
			slog.Warn("could not measure the ripped file", "job_id", job.ID, "path", fullDest, "error", statErr)
		}
		if dbErr := o.store.UpdateJobOutput(job.ID, fullDest, outputSize); dbErr != nil {
			slog.Error("failed to update job output", "job_id", job.ID, "error", dbErr)
		}
		o.setJobStatus(job.ID, "completed", 100, "")
		return nil
	}

	// 7. Submit to engine.
	wg.Add(1)
	if err := o.engine.Submit(ripJob); err != nil {
		wg.Done()
		// OnComplete never runs for a job that was not accepted, so the claim
		// taken above has to be dropped here or the backup is never cleaned up.
		if backupClaim != nil {
			o.releaseRecovered(backupClaim, false)
		}
		if dbErr := o.store.UpdateJobStatus(jobID, "failed", 0, err.Error()); dbErr != nil {
			slog.Error("failed to update job status on submit failure", "job_id", jobID, "error", dbErr)
		}
		return TitleResult{
			TitleIndex: sel.TitleIndex,
			Status:     "failed",
			Reason:     fmt.Sprintf("submit to engine: %v", err),
		}
	}

	return TitleResult{
		TitleIndex: sel.TitleIndex,
		Status:     "submitted",
	}
}

// buildDestPaths builds one destination path per selected title, giving any
// that would collide a suffix naming their source.
//
// Police Story 2 offers its feature twice, as the playlist 00000.mpls and as
// the raw stream 00000.m2ts. The destination name is the source file with its
// extension stripped, so both resolved to the same .mkv and ripping both would
// have quietly overwritten 67GB with 67GB.
//
// Only the colliding titles are renamed. Suffixing every rip to prevent a
// collision that is not happening would be the worse bug.
func (o *Orchestrator) buildDestPaths(params ManualRipParams) []string {
	paths := make([]string, len(params.Titles))
	build := func(disambiguate func(TitleSelection) TitleSelection) map[string]int {
		counts := make(map[string]int, len(params.Titles))
		for i, sel := range params.Titles {
			if disambiguate != nil {
				sel = disambiguate(sel)
			}
			paths[i] = o.buildDestPath(params, sel)
			counts[paths[i]]++
		}
		return counts
	}

	counts := build(nil)
	if !hasCollision(counts) {
		return paths
	}

	// The source file is what tells two otherwise identical names apart: a
	// playlist from the stream it points at, or two titles matched to the same
	// episode. Only the colliding entries are renamed — suffixing every rip to
	// prevent a collision that is not happening would be the worse bug.
	collided := counts
	counts = build(func(sel TitleSelection) TitleSelection {
		if collided[o.buildDestPath(params, sel)] < 2 {
			return sel
		}
		return withSuffix(sel, sourceDisambiguator(sel))
	})
	if !hasCollision(counts) {
		return paths
	}

	// Nothing about the source distinguished them, so fall back to the one
	// thing that always does.
	stillCollided := counts
	build(func(sel TitleSelection) TitleSelection {
		base := o.buildDestPath(params, sel)
		if collided[base] < 2 {
			return sel
		}
		suffixed := withSuffix(sel, sourceDisambiguator(sel))
		if stillCollided[o.buildDestPath(params, suffixed)] < 2 {
			return suffixed
		}
		return withSuffix(sel, fmt.Sprintf("title %d", sel.TitleIndex))
	})
	return paths
}

func hasCollision(counts map[string]int) bool {
	for _, n := range counts {
		if n > 1 {
			return true
		}
	}
	return false
}

// sourceDisambiguator returns the part of a title's source that distinguishes
// it from another with the same destination name.
//
// A matched title is named from its episode, so the whole source file is what
// differs. An unmatched title is already named from its source file with the
// extension stripped, so the extension is the difference — that is exactly the
// 00000.mpls versus 00000.m2ts case.
func sourceDisambiguator(sel TitleSelection) string {
	if sel.SourceFile == "" {
		return fmt.Sprintf("title %d", sel.TitleIndex)
	}
	if sel.TitleName != "" {
		return sel.SourceFile
	}
	if ext := strings.TrimPrefix(filepath.Ext(sel.SourceFile), "."); ext != "" {
		return ext
	}
	return fmt.Sprintf("title %d", sel.TitleIndex)
}

// withSuffix appends a parenthesised marker to whichever field the destination
// name is built from.
func withSuffix(sel TitleSelection, suffix string) TitleSelection {
	if sel.TitleName != "" {
		sel.TitleName += " (" + suffix + ")"
		return sel
	}
	// The extension is stripped when the path is built, so the marker has to be
	// folded into the name rather than left on the end.
	sel.SourceFile = strings.TrimSuffix(sel.SourceFile, filepath.Ext(sel.SourceFile)) + " (" + suffix + ")"
	return sel
}

// buildDestPath builds the output path for a title.
// Matched titles use: <MediaTitle>/<TitleName>.mkv
// Unmatched titles use: <DiscName>/<DiscName> - <SourceFile>.mkv
func (o *Orchestrator) buildDestPath(params ManualRipParams, sel TitleSelection) string {
	if sel.TitleName != "" && params.MediaTitle != "" {
		return o.organizer.BuildPath(params.MediaTitle, sel.TitleName)
	}
	// Unmatched: use disc name as directory, prepend disc name to source file.
	dirName := params.DiscName
	if dirName == "" {
		dirName = params.MediaTitle
	}
	fileName := sel.SourceFile
	if fileName == "" {
		fileName = sel.TitleName
	}
	if dirName != "" {
		fileName = dirName + " - " + fileName
	}
	return o.organizer.BuildPath(dirName, fileName)
}

// cachedScan is a scan together with what identifies the disc it came from.
//
// The fingerprint is the point: the cache is keyed by drive index and disc
// name, and a volume label is not identity. A two-disc set sharing one label
// served the first disc's titles for the second until the entry could be
// checked against what a fresh read actually found.
type cachedScan struct {
	scan        *makemkv.DiscScan
	fingerprint string
	at          time.Time
}

// ScanResult is a cached scan and its provenance, for a page that has to say
// where a title list came from.
type ScanResult struct {
	Scan        *makemkv.DiscScan
	Fingerprint string
	CachedAt    time.Time
}

// ScanDisc returns the drive's cached scan when there is one, and otherwise
// reads the disc. It is the passive path: page loads and background callers
// that want a title list without paying for a read of the disc.
//
// Anything acting on the user pressing Scan must use RescanDisc. A cache hit
// here cannot tell that the disc was swapped for another one answering to the
// same name.
func (o *Orchestrator) ScanDisc(ctx context.Context, driveIndex int) (*makemkv.DiscScan, error) {
	return o.scanDisc(ctx, driveIndex, false)
}

// RescanDisc reads the disc in the drive, whatever is cached for it.
//
// This is what the Scan button runs. Verifying a cached scan means finding out
// what is on the disc, so the check and the read are the same operation — and
// since the page only scans when the user asks it to, re-reading costs nothing
// that was not requested. A scan that comes back describing a different disc
// than the cache held replaces it and reports the change.
func (o *Orchestrator) RescanDisc(ctx context.Context, driveIndex int) (*makemkv.DiscScan, error) {
	return o.scanDisc(ctx, driveIndex, true)
}

func (o *Orchestrator) scanDisc(ctx context.Context, driveIndex int, force bool) (*makemkv.DiscScan, error) {
	if o.scanner == nil {
		slog.Error("orchestrator: scan requested but no scanner configured")
		return nil, fmt.Errorf("no scanner configured")
	}

	// A recovered disc is served from its stripped backup. Re-reading the drive
	// would fail exactly as it did the first time and start another recovery.
	//
	// A forced rescan does not change that. The drive holds the disc MakeMKV
	// could not open, so reading it would fail on the spurious-AACS signature
	// and maybeRecover would start a second ~100GB copy of a disc already
	// sitting in scratch. What force does mean here is re-deriving the titles
	// from the copy rather than trusting the cache — a folder scan, no disc I/O.
	// Before reading a repaired copy, make sure it is a copy of the disc in the
	// drive. A copy is bound to a drive index and unbound by volume label, and a
	// two-disc set can ship both discs under one label — so the label matching
	// is not evidence that this is the same disc. Retiring it here is what makes
	// the RecoveredSource below answer nil and fall through to a real read.
	if o.RecoveredSource(driveIndex) != nil && o.copyIsForAnotherDisc(ctx, driveIndex) {
		o.retireRecoveredForDrive(driveIndex, "a different disc is in the drive")
		o.InvalidateScan(driveIndex)
	}

	if src := o.RecoveredSource(driveIndex); src != nil {
		if !force {
			if cached := o.GetCachedScanByDrive(driveIndex); cached != nil {
				slog.Info("orchestrator: serving recovered disc from its backup",
					"drive_index", driveIndex, "source", src.Arg())
				return cached, nil
			}
		}

		// No cached scan — the process restarted since the backup was made. Read
		// the folder rather than the drive: the disc is the one MakeMKV could not
		// open, and falling through would spend another ~100GB copying what is
		// already sitting in scratch.
		if o.backupper != nil {
			slog.Info("orchestrator: rescanning a restored backup",
				"drive_index", driveIndex, "source", src.Arg(), "forced", force)
			scan, err := o.backupper.ScanSource(ctx, *src)
			if err == nil && len(scan.Titles) > 0 {
				// Under the disc in the drive, not under what the copy calls
				// itself — otherwise this rescan happens again on every request.
				o.cacheScanFor(driveIndex, o.driveDiscName(driveIndex), scan)
				return scan, nil
			}
			slog.Warn("orchestrator: restored backup did not scan; falling back to the drive",
				"drive_index", driveIndex, "source", src.Arg(), "error", err)
		}

		// Nothing could re-derive the copy. Serving the cache beats reading a
		// disc that is known to fail, even on a forced rescan.
		if cached := o.GetCachedScanByDrive(driveIndex); cached != nil {
			return cached, nil
		}
	}

	// What the cache held before we queued. A forced rescan may only accept an
	// entry written while it waited — one from before is the very thing it was
	// asked to go behind.
	cachedBefore := o.cachedAtForDrive(driveIndex)

	// One scan per drive at a time. A scan that appears to hang invites another
	// click, and makemkvcon serialises on the executor mutex anyway — a second
	// process would only double the wait. Waiters re-check the cache on the way
	// in, so the second caller gets the first caller's result.
	unlock := o.lockDriveScan(driveIndex)
	defer unlock()

	if fresh := o.cachedSince(driveIndex, force, cachedBefore); fresh != nil {
		slog.Info("orchestrator: serving scan completed while waiting", "drive_index", driveIndex)
		return fresh, nil
	}

	slog.Info("orchestrator: starting disc scan", "drive_index", driveIndex)

	// Detach from the caller. A scan of a damaged disc retries every unreadable
	// sector and can run for minutes; on an HTTP request's context the browser
	// giving up killed makemkvcon mid-read, surfacing as "signal: killed". The
	// executor applies its own timeout.
	scan, err := o.scanOnce(context.WithoutCancel(ctx), driveIndex)
	if err != nil {
		recovered, recErr := o.maybeRecover(ctx, driveIndex, err)
		if recErr != nil {
			slog.Error("orchestrator: disc scan failed", "drive_index", driveIndex, "error", recErr)
			return nil, recErr
		}
		if recovered == nil {
			slog.Error("orchestrator: disc scan failed", "drive_index", driveIndex, "error", err)
			return nil, err
		}
		scan = recovered
	} else {
		o.RecordDirectScan(scan)
	}

	// Under the disc in the drive. A scan that came back from recovery is a scan
	// of the copy, and names itself after the copied BDMV rather than the disc.
	o.cacheScanFor(driveIndex, o.driveDiscName(driveIndex), scan)

	return scan, nil
}

// cacheScanFor stores a scan under the disc it belongs to, where that is not
// the name the scan reports about itself.
//
// A scan of a repaired copy is a folder scan, and a folder scan reports what
// the copied BDMV calls itself rather than the disc's volume label. Filing it
// under that name leaves it unreachable to anyone asking about the disc, which
// is everyone.
func (o *Orchestrator) cacheScanFor(driveIndex int, discLabel string, scan *makemkv.DiscScan) {
	if scan == nil {
		return
	}
	if discLabel == "" {
		discLabel = scan.DiscName
	}
	key := fmt.Sprintf("%d:%s", driveIndex, discLabel)
	fingerprint := makemkv.ScanFingerprint(scan)

	// Whatever this drive held before, under any label. The disc that was
	// swapped out answered to the label the new one now uses, so looking only
	// under the new key would compare the disc against itself.
	o.scanMu.Lock()
	previous := o.newestCachedForDrive(driveIndex)
	o.scanCache[key] = &cachedScan{scan: scan, fingerprint: fingerprint, at: time.Now()}
	o.scanMu.Unlock()

	slog.Info("orchestrator: disc scan cached",
		"drive_index", driveIndex, "cache_key", key, "fingerprint", fingerprint)

	// An empty fingerprint on either side means a scan that found no titles,
	// which describes no disc. Comparing those would report a disc change on
	// every failed scan.
	if previous == nil || previous.fingerprint == "" || fingerprint == "" {
		return
	}
	if previous.fingerprint == fingerprint {
		return
	}

	slog.Info("orchestrator: a different disc is in this drive",
		"drive_index", driveIndex,
		"was", previous.scan.DiscName, "was_fingerprint", previous.fingerprint,
		"now", scan.DiscName, "now_fingerprint", fingerprint)

	o.discChanged(driveIndex, scan.DiscName)
}

// SetOnDiscChanged registers the callback fired when a scan finds a different
// disc than the one previously cached for a drive — including one answering to
// the same volume label.
//
// The release selection, the search results and the mapping saved against them
// all belong to the disc they were chosen for. Whoever holds that state has to
// drop it, and that is the web server, which is built after this orchestrator.
func (o *Orchestrator) SetOnDiscChanged(fn func(driveIndex int)) {
	o.onDiscChanged = fn
}

// discChanged drops everything bound to the disc that just left the drive.
//
// Any repaired copy is already handled before the read that got us here — see
// copyIsForAnotherDisc — so what is left is the state outside this package: the
// release the user picked, the search results behind it, and the mapping saved
// against them, all of which describe the disc that just came out.
func (o *Orchestrator) discChanged(driveIndex int, discLabel string) {
	slog.Info("orchestrator: dropping state bound to the disc that left the drive",
		"drive_index", driveIndex, "disc", discLabel)

	if o.onDiscChanged != nil {
		o.onDiscChanged(driveIndex)
	}
}

// newestCachedForDrive returns the most recently cached scan for a drive, under
// any disc label. Callers must hold scanMu.
func (o *Orchestrator) newestCachedForDrive(driveIndex int) *cachedScan {
	prefix := fmt.Sprintf("%d:", driveIndex)
	var newest *cachedScan
	for key, entry := range o.scanCache {
		if !strings.HasPrefix(key, prefix) {
			continue
		}
		if newest == nil || entry.at.After(newest.at) {
			newest = entry
		}
	}
	return newest
}

// CachedScanInfo returns the cached scan for the disc a drive is holding, along
// with what identifies it and when it was taken, or nil when there is none.
//
// The page uses this to say a title list came from cache rather than from the
// disc in the drive, so a stale one is never mistaken for a fresh read.
func (o *Orchestrator) CachedScanInfo(driveIndex int) *ScanResult {
	o.scanMu.RLock()
	defer o.scanMu.RUnlock()

	entry := o.cachedEntryForDrive(driveIndex)
	if entry == nil {
		return nil
	}
	return &ScanResult{
		Scan:        entry.scan,
		Fingerprint: entry.fingerprint,
		CachedAt:    entry.at,
	}
}

// driveDiscName returns the disc a drive currently holds, or "" if it has not
// reported one.
func (o *Orchestrator) driveDiscName(driveIndex int) string {
	o.scanMu.RLock()
	defer o.scanMu.RUnlock()
	return o.driveDisc[driveIndex]
}

// setDriveDiscName records the disc a drive currently holds, so cache lookups
// made with only a drive index can still be exact.
func (o *Orchestrator) setDriveDiscName(driveIndex int, discLabel string) {
	if discLabel == "" {
		return
	}
	o.scanMu.Lock()
	defer o.scanMu.Unlock()
	o.driveDisc[driveIndex] = discLabel
}

// CachedScan returns a previously cached scan for the given drive and disc name,
// or nil if no cached result exists.
func (o *Orchestrator) CachedScan(driveIndex int, discName string) *makemkv.DiscScan {
	key := fmt.Sprintf("%d:%s", driveIndex, discName)
	o.scanMu.RLock()
	defer o.scanMu.RUnlock()
	if entry := o.scanCache[key]; entry != nil {
		return entry.scan
	}
	return nil
}

// InvalidateScan removes any cached scan for the given drive index.
func (o *Orchestrator) InvalidateScan(driveIndex int) {
	o.scanMu.Lock()
	defer o.scanMu.Unlock()
	prefix := fmt.Sprintf("%d:", driveIndex)
	for key := range o.scanCache {
		if strings.HasPrefix(key, prefix) {
			delete(o.scanCache, key)
		}
	}
}

// GetCachedScanByDrive returns the cached scan for the disc a drive is holding,
// or nil when there is none.
//
// Nearly every caller has a drive index and no disc name, and this used to
// answer them with whatever was cached for that drive. That is only ever
// correct because a disc change clears the cache — an event, and an event can
// be missed or not have happened yet after a restart. When the drive has told
// us what it holds, the lookup is exact instead.
func (o *Orchestrator) GetCachedScanByDrive(driveIndex int) *makemkv.DiscScan {
	o.scanMu.RLock()
	defer o.scanMu.RUnlock()

	if entry := o.cachedEntryForDrive(driveIndex); entry != nil {
		return entry.scan
	}
	return nil
}

// cachedAtForDrive returns when the drive's cached scan was taken, or the zero
// time when there is none.
func (o *Orchestrator) cachedAtForDrive(driveIndex int) time.Time {
	o.scanMu.RLock()
	defer o.scanMu.RUnlock()
	if entry := o.cachedEntryForDrive(driveIndex); entry != nil {
		return entry.at
	}
	return time.Time{}
}

// cachedSince returns the drive's cached scan, subject to a forced rescan only
// accepting one taken after the given time — the result of the scan it queued
// behind, rather than the stale entry it was asked to replace.
func (o *Orchestrator) cachedSince(driveIndex int, force bool, after time.Time) *makemkv.DiscScan {
	o.scanMu.RLock()
	defer o.scanMu.RUnlock()

	entry := o.cachedEntryForDrive(driveIndex)
	if entry == nil {
		return nil
	}
	if force && !entry.at.After(after) {
		return nil
	}
	return entry.scan
}

// cachedEntryForDrive resolves the cache entry for the disc a drive holds.
// Callers must hold scanMu.
func (o *Orchestrator) cachedEntryForDrive(driveIndex int) *cachedScan {
	if disc := o.driveDisc[driveIndex]; disc != "" {
		return o.scanCache[fmt.Sprintf("%d:%s", driveIndex, disc)]
	}

	// The drive has not reported a disc yet. Falling back to the drive's only
	// cached scan is what this always did, and it is no worse than before.
	prefix := fmt.Sprintf("%d:", driveIndex)
	for key, entry := range o.scanCache {
		if strings.HasPrefix(key, prefix) {
			return entry
		}
	}
	return nil
}

// AutoRip scans a disc, attempts to auto-match it against TheDiscDB, and
// submits all titles for ripping. If a cached disc mapping exists, it is used
// directly; otherwise the disc name is searched via the DiscDB client.
func (o *Orchestrator) AutoRip(ctx context.Context, driveIndex int, cfg AutoRipConfig) error {
	scan, err := o.ScanDisc(ctx, driveIndex)
	if err != nil {
		return fmt.Errorf("auto-rip scan: %w", err)
	}

	discKey := discdb.BuildDiscKey(scan)

	// Check for an existing disc mapping.
	mapping, err := o.store.GetMapping(discKey)
	if err != nil {
		return fmt.Errorf("auto-rip get mapping: %w", err)
	}

	var titles []TitleSelection
	var mediaItemID, releaseID, discID, mediaTitle, mediaYear, mediaType string

	if mapping != nil {
		slog.Info("auto-rip: using cached disc mapping",
			"disc_key", discKey, "media_title", mapping.MediaTitle)
		titles = o.titlesFromMapping(scan, mapping)
		mediaItemID = mapping.MediaItemID
		releaseID = mapping.ReleaseID
		discID = mapping.DiscID
		mediaTitle = mapping.MediaTitle
		mediaYear = mapping.MediaYear
		mediaType = mapping.MediaType
	} else {
		titles, mediaItemID, releaseID, discID, mediaTitle, mediaYear, mediaType = o.autoMatch(ctx, scan, cfg.SelectionOpts)
	}

	params := ManualRipParams{
		DriveIndex:      driveIndex,
		DiscName:        scan.DiscName,
		DiscKey:         discKey,
		Titles:          titles,
		OutputDir:       cfg.OutputDir,
		DuplicateAction: cfg.DuplicateAction,
		MediaItemID:     mediaItemID,
		ReleaseID:       releaseID,
		DiscID:          discID,
		MediaTitle:      mediaTitle,
		MediaYear:       mediaYear,
		MediaType:       mediaType,
		SelectionOpts:   cfg.SelectionOpts,
	}

	result := o.ManualRip(params)
	if result.HasErrors() {
		slog.Warn("auto-rip completed with errors", "summary", result.ErrorSummary())
	}

	return nil
}

// titlesFromMapping builds TitleSelections using a saved disc mapping for all
// titles in the scan.
func (o *Orchestrator) titlesFromMapping(scan *makemkv.DiscScan, mapping *db.DiscMapping) []TitleSelection {
	titles := make([]TitleSelection, 0, len(scan.Titles))
	for i := range scan.Titles {
		t := &scan.Titles[i]
		var sizeBytes int64
		if s := t.SizeBytes(); s != "" {
			fmt.Sscanf(s, "%d", &sizeBytes)
		}
		titles = append(titles, TitleSelection{
			TitleIndex:   t.Index,
			TitleName:    t.Name(),
			SourceFile:   t.SourceFile(),
			SizeBytes:    sizeBytes,
			ContentType:  mapping.MediaType,
			ContentTitle: mapping.MediaTitle,
			Year:         mapping.MediaYear,
			// Titles rebuilt from a saved mapping have no language context — include all tracks.
			TrackMetadata: buildTrackMetadata(t, nil),
		})
	}
	return titles
}

// autoMatch searches TheDiscDB for the disc name, scores matches, and returns
// title selections along with metadata. Falls back to unmatched titles if no
// confident match is found.
func (o *Orchestrator) autoMatch(ctx context.Context, scan *makemkv.DiscScan, opts *makemkv.SelectionOpts) (
	titles []TitleSelection,
	mediaItemID, releaseID, discID, mediaTitle, mediaYear, mediaType string,
) {
	if o.discDB != nil && scan.DiscName != "" {
		items, err := o.discDB.SearchByTitle(ctx, scan.DiscName)
		if err != nil {
			slog.Warn("auto-rip: discdb search failed", "error", err)
		} else if len(items) > 0 {
			best, score := discdb.BestRelease(scan, items)
			if best != nil && score >= 10 {
				slog.Info("auto-rip: matched via discdb",
					"title", best.MediaItem.Title, "score", score)
				titles = o.titlesFromSearchResult(scan, best, opts)
				mediaItemID = strconv.Itoa(best.MediaItem.ID)
				releaseID = strconv.Itoa(best.Release.ID)
				discID = strconv.Itoa(best.Disc.ID)
				mediaTitle = best.MediaItem.Title
				mediaYear = strconv.Itoa(best.MediaItem.Year)
				mediaType = best.MediaItem.Type
				// Create an update contribution so the user can correct or augment the entry.
				go o.EnsureUpdateContributionRecord(scan, best)
				return
			}
		}
	}

	slog.Info("auto-rip: no confident match, using unmatched titles",
		"disc_name", scan.DiscName)
	titles = o.unmatchedTitles(scan, opts)

	// Create a contribution record for this unmatched disc so the user
	// can contribute it to TheDiscDB later.
	o.EnsureContributionRecord(scan)

	return
}

// EnsureContributionRecord stores an unmatched disc scan for potential
// contribution to TheDiscDB. Silently skips if a contribution already exists
// for this disc key.
func (o *Orchestrator) EnsureContributionRecord(scan *makemkv.DiscScan) {
	discKey := discdb.BuildDiscKey(scan)

	// Check if a contribution already exists for this disc.
	existing, err := o.store.GetContributionByDiscKey(discKey)
	if err != nil {
		slog.Error("auto-rip: failed to check existing contribution", "disc_key", discKey, "error", err)
		return
	}
	if existing != nil {
		slog.Info("auto-rip: contribution already exists for disc", "disc_key", discKey)
		return
	}

	scanJSON, err := json.Marshal(scan)
	if err != nil {
		slog.Error("auto-rip: failed to marshal scan for contribution", "disc_key", discKey, "error", err)
		return
	}

	id, err := o.store.SaveContribution(db.Contribution{
		DiscKey:   discKey,
		DiscName:  scan.DiscName,
		RawOutput: scan.RawOutput,
		ScanJSON:  string(scanJSON),
	})
	if err != nil {
		slog.Error("auto-rip: failed to save contribution", "disc_key", discKey, "error", err)
		return
	}

	slog.Info("auto-rip: created contribution record for unmatched disc",
		"disc_key", discKey, "disc_name", scan.DiscName, "contribution_id", id)

	// Broadcast SSE event so the UI can show a notification.
	if o.onBroadcast != nil {
		data, _ := json.Marshal(map[string]any{
			"contribution_id": id,
			"disc_name":       scan.DiscName,
		})
		o.onBroadcast("contribution_available", string(data))
	}
}

// EnsureUpdateContributionRecord stores a matched disc scan for potential
// correction/augmentation of an existing TheDiscDB entry. Silently skips if
// a contribution already exists for this disc key.
func (o *Orchestrator) EnsureUpdateContributionRecord(scan *makemkv.DiscScan, best *discdb.SearchResult) {
	discKey := discdb.BuildDiscKey(scan)

	existing, err := o.store.GetContributionByDiscKey(discKey)
	if err != nil {
		slog.Error("auto-rip: failed to check existing update contribution", "disc_key", discKey, "error", err)
		return
	}
	if existing != nil {
		slog.Info("auto-rip: update contribution already exists for disc", "disc_key", discKey)
		return
	}

	// Build title labels from the match: matched titles carry TheDiscDB metadata;
	// unmatched titles get empty type (renders as Omit in the form).
	matches := discdb.MatchTitles(scan, best.Disc)
	scanByIndex := make(map[int]*makemkv.TitleInfo, len(scan.Titles))
	for i := range scan.Titles {
		scanByIndex[scan.Titles[i].Index] = &scan.Titles[i]
	}

	labels := make([]contribute.TitleLabel, 0, len(matches))
	for _, cm := range matches {
		label := contribute.TitleLabel{
			TitleIndex: cm.TitleIndex,
			Matched:    cm.Matched,
		}
		if t, ok := scanByIndex[cm.TitleIndex]; ok {
			label.FileName = t.Filename()
		}
		if cm.Matched {
			label.Type = cm.ContentType
			label.Name = cm.ContentTitle
			label.Season = cm.Season
			label.Episode = cm.Episode
		}
		labels = append(labels, label)
	}

	labelsJSON, err := json.Marshal(labels)
	if err != nil {
		slog.Error("auto-rip: failed to marshal labels for update contribution", "error", err)
		return
	}

	mi := contribute.MatchInfo{
		MediaSlug:   best.MediaItem.Slug,
		MediaType:   strings.ToLower(best.MediaItem.Type),
		MediaTitle:  best.MediaItem.Title,
		MediaYear:   best.MediaItem.Year,
		ReleaseSlug: best.Release.Slug,
		DiscIndex:   best.Disc.Index,
		ImageURL:    best.Release.ImageURL,
	}
	miJSON, err := json.Marshal(mi)
	if err != nil {
		slog.Error("auto-rip: failed to marshal match_info for update contribution", "error", err)
		return
	}

	scanJSON, err := json.Marshal(scan)
	if err != nil {
		slog.Error("auto-rip: failed to marshal scan for update contribution", "error", err)
		return
	}

	id, err := o.store.SaveContribution(db.Contribution{
		DiscKey:          discKey,
		DiscName:         scan.DiscName,
		RawOutput:        scan.RawOutput,
		ScanJSON:         string(scanJSON),
		ContributionType: "update",
		MatchInfo:        string(miJSON),
		TitleLabels:      string(labelsJSON),
	})
	if err != nil {
		slog.Error("auto-rip: failed to save update contribution", "disc_key", discKey, "error", err)
		return
	}

	slog.Info("auto-rip: created update contribution record for matched disc",
		"disc_key", discKey, "disc_name", scan.DiscName, "contribution_id", id)

	if o.onBroadcast != nil {
		data, _ := json.Marshal(map[string]any{
			"contribution_id": id,
			"disc_name":       scan.DiscName,
		})
		o.onBroadcast("contribution_available", string(data))
	}
}

// titlesFromSearchResult builds TitleSelections from a TheDiscDB match using
// MatchTitles to correlate scan titles with disc metadata.
func (o *Orchestrator) titlesFromSearchResult(scan *makemkv.DiscScan, sr *discdb.SearchResult, opts *makemkv.SelectionOpts) []TitleSelection {
	matches := discdb.MatchTitles(scan, sr.Disc)
	titles := make([]TitleSelection, 0, len(scan.Titles))

	for _, cm := range matches {
		// Find the scan title for size info and track metadata.
		var sizeBytes int64
		var titleName string
		var matchedTitle *makemkv.TitleInfo
		for i := range scan.Titles {
			if scan.Titles[i].Index == cm.TitleIndex {
				t := &scan.Titles[i]
				if s := t.SizeBytes(); s != "" {
					fmt.Sscanf(s, "%d", &sizeBytes)
				}
				titleName = t.Name()
				matchedTitle = t
				break
			}
		}

		sel := TitleSelection{
			TitleIndex: cm.TitleIndex,
			TitleName:  titleName,
			SourceFile: cm.SourceFile,
			SizeBytes:  sizeBytes,
		}

		if matchedTitle != nil {
			sel.TrackMetadata = buildTrackMetadata(matchedTitle, opts)
		}

		if cm.Matched {
			sel.ContentType = cm.ContentType
			sel.ContentTitle = cm.ContentTitle
			sel.Season = cm.Season
			sel.Episode = cm.Episode
		}

		titles = append(titles, sel)
	}

	return titles
}

// unmatchedTitles builds TitleSelections with no content metadata — the
// organizer will place them in an unmatched directory.
func (o *Orchestrator) unmatchedTitles(scan *makemkv.DiscScan, opts *makemkv.SelectionOpts) []TitleSelection {
	titles := make([]TitleSelection, 0, len(scan.Titles))
	for i := range scan.Titles {
		t := &scan.Titles[i]
		var sizeBytes int64
		if s := t.SizeBytes(); s != "" {
			fmt.Sscanf(s, "%d", &sizeBytes)
		}
		titles = append(titles, TitleSelection{
			TitleIndex:    t.Index,
			TitleName:     t.Name(),
			SourceFile:    t.SourceFile(),
			SizeBytes:     sizeBytes,
			TrackMetadata: buildTrackMetadata(t, opts),
		})
	}
	return titles
}

// InjectCachedScan is a test helper that inserts a scan into the cache.
func (o *Orchestrator) InjectCachedScan(driveIndex int, scan *makemkv.DiscScan) {
	key := fmt.Sprintf("%d:%s", driveIndex, scan.DiscName)
	o.scanMu.Lock()
	defer o.scanMu.Unlock()
	o.scanCache[key] = &cachedScan{
		scan:        scan,
		fingerprint: makemkv.ScanFingerprint(scan),
		at:          time.Now(),
	}
}

// buildTrackMetadata extracts audio and subtitle metadata from a scanned title,
// optionally filtering to the tracks permitted by opts. Pass nil for opts to
// include all tracks (auto-rip with no language filter).
func buildTrackMetadata(t *makemkv.TitleInfo, opts *makemkv.SelectionOpts) ripper.TrackMetadata {
	var meta ripper.TrackMetadata
	meta.Duration = t.Duration()
	meta.SizeHuman = t.SizeHuman()
	if s := t.SizeBytes(); s != "" {
		fmt.Sscanf(s, "%d", &meta.SizeBytes)
	}
	seen := make(map[string]bool)
	for i := range t.Streams {
		s := &t.Streams[i]
		switch s.Type() {
		case "audio":
			if opts != nil && len(opts.AudioLangs) > 0 {
				if !langInList(opts.AudioLangs, s.LangCode()) {
					continue
				}
			}
			// lossless codec check delegates to makemkv.IsLosslessAudio
			if opts != nil && !opts.KeepLossless && makemkv.IsLosslessAudio(s.CodecShort()) {
				continue
			}
			meta.AudioTracks = append(meta.AudioTracks, ripper.AudioTrack{
				Language: s.LangName(),
				Codec:    s.CodecShort(),
				Channels: s.Channels(),
			})
		case "subtitle":
			if opts != nil && len(opts.SubtitleLangs) > 0 {
				isForced := s.IsForced()
				if !langInList(opts.SubtitleLangs, s.LangCode()) && !(opts.KeepForced && isForced) {
					continue
				}
			}
			lang := s.LangName()
			if lang != "" && !seen[lang] {
				seen[lang] = true
				meta.SubtitleLanguages = append(meta.SubtitleLanguages, lang)
			}
		}
	}
	return meta
}

// langInList returns true when code is present in langs.
func langInList(langs []string, code string) bool {
	for _, l := range langs {
		if l == code {
			return true
		}
	}
	return false
}

// largestFile returns the biggest non-empty file in dir and its size, or ""
// when the directory is missing, empty, or holds nothing with any bytes in it.
//
// makemkvcon may or may not remove its own partial output when a rip fails; we
// were deleting the directory without looking, so nobody knew which. Asking is
// cheaper than assuming.
func largestFile(dir string) (string, int64) {
	if dir == "" {
		return "", 0
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", 0
	}
	var best string
	var bestSize int64
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil || info.Size() == 0 {
			continue
		}
		if info.Size() > bestSize {
			best, bestSize = filepath.Join(dir, e.Name()), info.Size()
		}
	}
	return best, bestSize
}

// salvageNoteForDrive returns the note for the copy a drive is ripping from,
// or "" when it is ripping the disc itself.
func (o *Orchestrator) salvageNoteForDrive(driveIndex int) string {
	o.recoveredMu.Lock()
	defer o.recoveredMu.Unlock()
	if rec := o.currentFor(driveIndex); rec != nil {
		return rec.salvageNote
	}
	return ""
}

// encodeSelectionOpts stores the audio and subtitle choice with the job.
func encodeSelectionOpts(opts *makemkv.SelectionOpts) string {
	if opts == nil {
		return ""
	}
	data, err := json.Marshal(opts)
	if err != nil {
		slog.Warn("could not record the language choices for this rip", "error", err)
		return ""
	}
	return string(data)
}

// humanBytes renders a size for the notes and messages users read.
func humanBytes(n int64) string {
	const unit = 1000
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "kMGTPE"[exp])
}

// findMKVFile returns the path to the first .mkv file in dir.
// MakeMKV writes exactly one .mkv per rip into the output directory.
func findMKVFile(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", fmt.Errorf("read rip dir: %w", err)
	}
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(strings.ToLower(e.Name()), ".mkv") {
			return filepath.Join(dir, e.Name()), nil
		}
	}
	return "", fmt.Errorf("no .mkv file found in %s", dir)
}

// setJobStatus updates a job's status in the DB and logs on failure.
func (o *Orchestrator) setJobStatus(jobID int64, status string, progress int, errMsg string) {
	if err := o.store.UpdateJobStatus(jobID, status, progress, errMsg); err != nil {
		slog.Error("failed to update job status", "jobID", jobID, "status", status, "err", err)
	}
}
