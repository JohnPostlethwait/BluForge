package makemkv

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/johnpostlethwait/bluforge/internal/mpls"
)

// CmdRunner is the interface for running makemkvcon commands. It receives the
// arguments to pass after the binary name and returns the combined output as a
// strings.Reader along with any execution error.
type CmdRunner interface {
	Run(ctx context.Context, args ...string) (*strings.Reader, error)
}

// realRunner executes the real makemkvcon binary.
type realRunner struct{}

// scanTimeout is the maximum time a disc scan may run.
//
// This exists to stop a wedged process holding the executor mutex forever, not
// to bound how long a difficult disc may legitimately take. A disc with
// unreadable sectors makes the drive retry each one, and a real scan was killed
// at 10m07s under the previous ten-minute value — a disc that scanned to
// completion when run by hand without a ceiling.
const scanTimeout = 60 * time.Minute

// driveListTimeout is the maximum time a drive listing may run. This is a
// lightweight operation that should complete quickly.
const driveListTimeout = 30 * time.Second

// The runner records its invocations at DEBUG, not INFO.
//
// It is plumbing shared by every caller, so it cannot tell a disc scan from the
// drive poll — and the poll runs `info disc:9999` every five seconds, which put
// roughly 17,000 lines a day into the log to report that nothing had happened.
// Deciding what is worth announcing belongs to the operation, which knows what
// it is doing: ScanDisc, StartBackup and StartRip each log their own start and
// finish at INFO.
func (r *realRunner) Run(ctx context.Context, args ...string) (*strings.Reader, error) {
	slog.Debug("makemkvcon: executing", "args", args)

	cmd := exec.CommandContext(ctx, "makemkvcon", args...)
	configureTeardown(cmd)
	out, err := cmd.CombinedOutput()
	if err != nil {
		slog.Error("makemkvcon: command failed", "args", args, "error", err, "output_bytes", len(out))
		// Return output even on error so callers can inspect messages.
		return strings.NewReader(string(out)), err
	}

	slog.Debug("makemkvcon: command completed", "args", args, "output_bytes", len(out))
	return strings.NewReader(string(out)), nil
}

// StreamRunner is an optional capability of a CmdRunner: delivering output
// line-by-line as it is produced rather than in one buffer at exit. Operations
// that run for tens of minutes — rips and backups — need it so progress reaches
// the UI while they are still running.
//
// Runners that do not implement it still work; their output is parsed once the
// command finishes.
type StreamRunner interface {
	RunStream(ctx context.Context, onLine func(string), args ...string) error
}

// RunStream executes makemkvcon and invokes onLine for each output line.
func (r *realRunner) RunStream(ctx context.Context, onLine func(string), args ...string) error {
	slog.Debug("makemkvcon: streaming", "args", args)

	cmd := exec.CommandContext(ctx, "makemkvcon", args...)
	configureTeardown(cmd)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("makemkv: stdout pipe: %w", err)
	}
	cmd.Stderr = cmd.Stdout

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("makemkv: start: %w", err)
	}

	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		line := strings.TrimRight(scanner.Text(), "\r")
		if line == "" {
			continue
		}
		onLine(line)
	}
	if scanErr := scanner.Err(); scanErr != nil {
		slog.Error("makemkvcon: scanner error", "error", scanErr)
	}

	return cmd.Wait()
}

// ErrNoOpticalDrives reports that makemkvcon could not see any optical drive.
//
// Under Docker this is almost always a permissions problem rather than missing
// hardware: makemkvcon enumerates drives through /dev/sg* nodes, which are
// mode 0660 and owned by root:disk. A container process running as a non-root
// user that is not in that group sees no drives at all, and the underlying
// operation fails with a message that says nothing about groups.
var ErrNoOpticalDrives = errors.New(
	"makemkvcon reports no usable optical drives: the process must belong to the group owning /dev/sg* " +
		"(commonly 'disk', GID 6) — add `group_add: [6]` to the container, or check the entrypoint's group detection")

// Option is a functional option for configuring an Executor.
type Option func(*Executor)

// WithRunner overrides the CmdRunner used by the Executor. Primarily intended
// for testing.
func WithRunner(r CmdRunner) Option {
	return func(e *Executor) {
		e.runner = r
	}
}

// Executor wraps makemkvcon and exposes high-level operations.
// All commands are serialized via mu because makemkvcon does not support
// concurrent execution — running multiple instances simultaneously produces
// corrupted output.
type Executor struct {
	runner CmdRunner
	mu     sync.Mutex
}

// NewExecutor creates an Executor. By default it uses the real makemkvcon
// binary; pass WithRunner to inject a mock for testing.
func NewExecutor(opts ...Option) *Executor {
	e := &Executor{runner: &realRunner{}}
	for _, o := range opts {
		o(e)
	}
	return e
}

// DiscScan holds the aggregated result of scanning a single disc.
type DiscScan struct {
	DriveIndex int
	DiscName   string
	DiscType   string
	TitleCount int
	Titles     []TitleInfo
	Messages   []Message
	RawOutput  string // Full makemkvcon robot-mode output, preserved for TheDiscDB contributions.
}

// ListDrives runs `makemkvcon -r --cache=1 info disc:9999` and returns the
// list of drives reported via DRV lines.
//
// --cache=1 minimizes memory allocation for this lightweight operation.
// LockDrive claims the drive for work that does not go through this executor.
//
// Everything makemkvcon does is serialised on one mutex because makemkvcon
// cannot share a drive. A salvage also runs ddrescue, which is a separate
// process outside that lock — and the drive poller, running every five seconds,
// contends with it for the same drive. Left unlocked, a rescue that should read
// at 14 MB/s managed 2.4 MB/s and reported nine and a half hours remaining.
//
// The caller must always call UnlockDrive.
func (e *Executor) LockDrive() {
	e.mu.Lock()
}

// UnlockDrive releases the claim taken by LockDrive.
func (e *Executor) UnlockDrive() {
	e.mu.Unlock()
}

func (e *Executor) ListDrives(ctx context.Context) ([]DriveInfo, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.listDrives(ctx)
}

// TryListDrives lists drives only if nothing else is using them, reporting
// false when it declined.
//
// The poller must not talk to a drive that a rip or a ddrescue rescue is
// reading — an unlocked poll every five seconds took a rescue from 14 MB/s to
// 2.4 MB/s, which is why every makemkvcon call shares one mutex.
//
// But waiting on that mutex is not the same as staying out of the way. A poll
// queued behind a three-hour rip learns nothing that a skipped poll would not,
// and it is worse in two ways: the poller goroutine is parked for the duration,
// and the moment the lock frees, the poll fires instantly and the next one
// right behind it — the burst the eject debounce was written to survive.
//
// Declining costs a stale drive list for the length of the operation, which is
// what blocking produced anyway.
func (e *Executor) TryListDrives(ctx context.Context) ([]DriveInfo, bool, error) {
	if !e.mu.TryLock() {
		return nil, false, nil
	}
	defer e.mu.Unlock()

	drives, err := e.listDrives(ctx)
	return drives, true, err
}

// listDrives is the body shared by ListDrives and TryListDrives. Callers must
// already hold e.mu.
func (e *Executor) listDrives(ctx context.Context) ([]DriveInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, driveListTimeout)
	defer cancel()

	r, err := e.runner.Run(ctx, "-r", "--cache=1", "info", "disc:9999")
	if err != nil {
		// A listing we killed ourselves is not a listing. makemkvcon was still
		// enumerating when the timeout fired, so the DRV lines it emitted are a
		// prefix rather than a list, and the drives it had not reached yet are
		// missing for want of time — which the poller would read as unplugged.
		//
		// Only makemkvcon's own non-zero exits are salvageable, and those are
		// ordinary: an empty drive is one, and it still names every drive.
		if ctx.Err() != nil {
			return nil, fmt.Errorf("makemkv: list drives: %w", err)
		}

		// makemkvcon returns non-zero when no disc is present; try to parse
		// whatever output we have before returning the error.
		events, parseErr := ParseAll(r)
		if parseErr != nil {
			return nil, fmt.Errorf("makemkv: list drives: %w", err)
		}
		drives := drivesFromEvents(events)
		if len(drives) == 0 {
			return nil, noDrivesError(events, err)
		}
		return drives, nil
	}

	events, err := ParseAll(r)
	if err != nil {
		return nil, fmt.Errorf("makemkv: list drives parse: %w", err)
	}
	drives := drivesFromEvents(events)
	if len(drives) == 0 {
		if msgs := messagesFromEvents(events); HasNoDrivesMessage(msgs) {
			return nil, fmt.Errorf("makemkv: list drives: %w", ErrNoOpticalDrives)
		}
	}
	return drives, nil
}

// runScan executes an info scan, returning the raw output, the parsed events,
// and the command error.
//
// When the runner supports streaming, events reach onEvent as makemkvcon
// produces them. A scan of a disc with unreadable sectors runs for many minutes
// while the drive retries, and buffering until exit left nothing able to tell
// that apart from a hang.
func (e *Executor) runScan(ctx context.Context, target string, onEvent func(Event)) (string, []Event, error) {
	args := []string{"-r", "info", target}

	if sr, ok := e.runner.(StreamRunner); ok {
		var raw strings.Builder
		var events []Event
		err := sr.RunStream(ctx, func(line string) {
			raw.WriteString(line)
			raw.WriteByte('\n')
			ev, parseErr := ParseLine(line)
			if parseErr != nil {
				return
			}
			events = append(events, ev)
			logMakeMKVEvent(ev, "scan")
			if onEvent != nil {
				onEvent(ev)
			}
		}, args...)
		return raw.String(), events, err
	}

	// Buffered fallback: parse once the command exits.
	r, cmdErr := e.runner.Run(ctx, args...)
	if r == nil {
		return "", nil, cmdErr
	}
	rawBytes, readErr := io.ReadAll(r)
	if readErr != nil && cmdErr == nil {
		cmdErr = readErr
	}
	raw := string(rawBytes)

	events, parseErr := ParseAll(strings.NewReader(raw))
	if parseErr != nil && cmdErr == nil {
		cmdErr = parseErr
	}
	for _, ev := range events {
		logMakeMKVEvent(ev, "scan")
		if onEvent != nil {
			onEvent(ev)
		}
	}
	return raw, events, cmdErr
}

// noDrivesError distinguishes "makemkvcon cannot see any drive" from a generic
// listing failure. On a container the former is a group-membership problem with
// a concrete fix, and reporting it as such saves the user from investigating
// the drive or the disc instead.
func noDrivesError(events []Event, cmdErr error) error {
	if HasNoDrivesMessage(messagesFromEvents(events)) {
		return fmt.Errorf("makemkv: list drives: %w", ErrNoOpticalDrives)
	}
	return fmt.Errorf("makemkv: list drives: %w", cmdErr)
}

func messagesFromEvents(events []Event) []Message {
	var msgs []Message
	for _, ev := range events {
		if ev.Type == "MSG" && ev.Message != nil {
			msgs = append(msgs, *ev.Message)
		}
	}
	return msgs
}

func drivesFromEvents(events []Event) []DriveInfo {
	var drives []DriveInfo
	for _, ev := range events {
		if ev.Type == "DRV" && ev.Drive != nil {
			drives = append(drives, *ev.Drive)
		}
	}
	return drives
}

// ScanDisc runs `makemkvcon -r info disc:N` for the given driveIndex and
// returns an aggregated DiscScan. CINFO attributes are merged into disc
// metadata, TINFO attributes are merged per title index, and SINFO streams are
// attached to their respective titles.
//
// Title minimum length filtering is controlled by dvd_MinimumTitleLength in
// MakeMKV's settings.conf, NOT via --minlength here. Using --minlength with
// info renumbers title IDs, causing mismatches when those IDs are later passed
// to mkv for ripping.
//
// makemkvcon often exits with a non-zero status even when it successfully
// enumerates titles (e.g. AACS warnings on Blu-ray discs). We always attempt
// to parse the output regardless of exit code, returning an error only when no
// useful disc data was produced.
func (e *Executor) ScanDisc(ctx context.Context, driveIndex int) (*DiscScan, error) {
	return e.ScanSource(ctx, DiscSource(driveIndex))
}

// ScanDiscWithProgress is ScanDisc with live reporting. A scan of a damaged
// disc can run for the better part of an hour; onEvent is what lets the caller
// say so rather than appear to hang.
func (e *Executor) ScanDiscWithProgress(ctx context.Context, driveIndex int, onEvent func(Event)) (*DiscScan, error) {
	return e.ScanSourceWithProgress(ctx, DiscSource(driveIndex), onEvent)
}

// ScanSource scans either a physical drive or a disc folder on disk, returning
// the same aggregated DiscScan for both. A folder source is what allows a
// backup with its AACS directory removed to stand in for a disc that MakeMKV
// refuses to open — everything downstream is unable to tell the difference.
//
// On failure the returned error is a *ScanError carrying the partially parsed
// scan, so callers can inspect the message codes makemkvcon emitted.
func (e *Executor) ScanSource(ctx context.Context, src Source) (*DiscScan, error) {
	return e.ScanSourceWithProgress(ctx, src, nil)
}

// ScanSourceWithProgress is ScanSource with live reporting.
//
// onEvent receives each parsed line as makemkvcon produces it. A scan of a disc
// with unreadable sectors runs for many minutes while the drive retries, and
// buffering its output until exit meant nothing could distinguish that from a
// hung process — not the UI, not the log. onEvent may be nil.
func (e *Executor) ScanSourceWithProgress(ctx context.Context, src Source, onEvent func(Event)) (*DiscScan, error) {
	slog.Info("executor: starting scan", "source", src.Arg())

	driveIndex := src.DriveIndex

	// Pre-lookup: get the device path for MPLS enrichment.  This is a separate,
	// lightweight lock acquisition that completes before the main scan starts.
	// Folder sources are read directly and need no device.
	var devicePath string
	if src.IsDisc() {
		devicePath = e.DevicePathForDrive(ctx, driveIndex)
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	ctx, cancel := context.WithTimeout(ctx, scanTimeout)
	defer cancel()

	target := src.Arg()
	rawOutput, events, cmdErr := e.runScan(ctx, target, onEvent)

	// A scan killed by the ceiling reports whatever the kernel did — "signal:
	// killed" — which describes the mechanism and hides the cause. Say what
	// actually happened, since the two look identical in a log otherwise.
	if cmdErr != nil && errors.Is(ctx.Err(), context.DeadlineExceeded) {
		slog.Error("executor: scan exceeded its time limit",
			"source", src.Arg(), "limit", scanTimeout.String())
		return nil, &ScanError{
			Source: src,
			Reason: fmt.Sprintf("timed out after %s — the drive may be retrying unreadable sectors", scanTimeout),
			Err:    cmdErr,
		}
	}

	// Read the full output so we can preserve it for contributions and still parse events.
	if cmdErr != nil {
		slog.Warn("executor: scan command exited non-zero, parsing output anyway",
			"source", src.Arg(), "error", cmdErr, "event_count", len(events))
	}

	scan := buildDiscScan(driveIndex, events)
	scan.RawOutput = rawOutput

	// If we got 0 titles, check for actionable error messages from makemkvcon.
	if len(scan.Titles) == 0 {
		// Log every message code seen. A disc whose AACS directory is spurious
		// produces a specific combination (3303 + 5010 + no titles); logging the
		// full set means a variant of that signature stays diagnosable from logs
		// alone, without repeating the original investigation.
		slog.Info("executor: scan produced no titles",
			"source", src.Arg(), "message_codes", MessageCodes(scan.Messages),
			"spurious_aacs_signature", IsSpuriousAACSSignature(scan.Messages, len(scan.Titles)))

		var failureReason string
		for _, m := range scan.Messages {
			// MSG code 5010 = "Failed to open disc" — terminal failure.
			if m.Code == MsgFailedToOpenDisc {
				failureReason = m.Text
				break
			}
		}
		if failureReason != "" {
			slog.Error("executor: scan failed", "source", src.Arg(), "reason", failureReason)
			return nil, &ScanError{Scan: scan, Source: src, Reason: failureReason, Err: cmdErr}
		}
	}

	// If the command failed AND we got no useful data, return the original error.
	if cmdErr != nil && len(scan.Titles) == 0 && scan.DiscName == "" {
		slog.Error("executor: scan command failed with no usable output",
			"source", src.Arg(), "error", cmdErr)
		return nil, &ScanError{Scan: scan, Source: src, Err: cmdErr}
	}

	slog.Info("executor: scan completed", "source", src.Arg(),
		"disc_name", scan.DiscName, "title_count", len(scan.Titles))

	// Enrich stream language codes from MPLS playlist files, which are the
	// authoritative source for language metadata on both standard BD and UHD.
	// CLPI files (what makemkvcon reads in robot info mode) often omit language
	// codes for UHD disc authorings.
	switch {
	case !src.IsDisc():
		// A folder source is already a readable disc tree, so playlists are
		// read straight out of it — no mount required.
		enrichScanFromMPLS(scan, mplsReaderForDir(src.Path))
	case devicePath != "":
		enrichScanFromMPLS(scan, mplsReaderForDevice(devicePath))
	default:
		slog.Warn("executor: no device path for drive, skipping MPLS enrichment",
			"drive_index", driveIndex)
	}

	// Log stream language status to aid debugging when track selection shows
	// "Language information not available" in the UI.
	audioLangs, subLangs := 0, 0
	for i := range scan.Titles {
		for j := range scan.Titles[i].Streams {
			s := &scan.Titles[i].Streams[j]
			if s.IsAudio() && s.LangCode() != "" {
				audioLangs++
			}
			if s.IsSubtitle() && s.LangCode() != "" {
				subLangs++
			}
		}
	}
	slog.Info("executor: stream language summary",
		"drive_index", driveIndex,
		"audio_streams_with_lang", audioLangs,
		"subtitle_streams_with_lang", subLangs)

	return scan, nil
}

// DevicePathForDrive returns the device path (e.g. "/dev/sr0") for driveIndex
// by running a lightweight ListDrives call.  Returns "" on any error; callers
// treat a missing path as a non-fatal condition.
func (e *Executor) DevicePathForDrive(ctx context.Context, driveIndex int) string {
	info, ok := e.driveInfo(ctx, driveIndex)
	if !ok {
		return ""
	}
	return info.DevicePath
}

// DiscLabelForDrive returns the disc label the drive is reporting.
//
// A disc that MakeMKV cannot open produces no disc name in its scan, but the
// drive listing still carries the volume label. That label is what identifies
// the disc in diagnostics and names its scratch directory, so it is worth
// asking for separately.
func (e *Executor) DiscLabelForDrive(ctx context.Context, driveIndex int) string {
	info, ok := e.driveInfo(ctx, driveIndex)
	if !ok {
		return ""
	}
	return info.DiscName
}

func (e *Executor) driveInfo(ctx context.Context, driveIndex int) (DriveInfo, bool) {
	drives, err := e.ListDrives(ctx)
	if err != nil {
		return DriveInfo{}, false
	}
	for _, d := range drives {
		if d.Index == driveIndex {
			return d, true
		}
	}
	return DriveInfo{}, false
}

// enrichScanFromMPLS reads MPLS playlist files from the disc at devicePath and
// writes language codes into the streams of scan.  Each scan title's SourceFile
// attribute (TINFO attr 16) names the corresponding MPLS file (e.g.
// "00300.mpls"); streams are matched by type and position within each title.
//
// Non-fatal: any error is logged at debug level and enrichment is skipped.
// mplsReader fetches playlist languages for a scan, given the playlist
// filenames the scan referenced. Abstracting over the two ways a disc tree can
// be reached — a mounted device or a plain directory — keeps enrichment
// identical for a real disc and for a backup folder.
type mplsReader func(sourceFiles []string) (map[string]mpls.PlayItemLanguages, error)

func mplsReaderForDevice(devicePath string) mplsReader {
	return func(sourceFiles []string) (map[string]mpls.PlayItemLanguages, error) {
		return mpls.ReadDiscLanguages(devicePath, sourceFiles)
	}
}

func mplsReaderForDir(root string) mplsReader {
	return func(sourceFiles []string) (map[string]mpls.PlayItemLanguages, error) {
		return mpls.ReadFrom(root, sourceFiles)
	}
}

func enrichScanFromMPLS(scan *DiscScan, read mplsReader) {
	// Collect the unique MPLS filenames referenced by this scan's titles.
	// For some UHD discs, makemkvcon omits TINFO attr 16 (source file), so
	// sourceFiles may be empty. ReadDiscLanguages handles this by reading
	// ALL .mpls files from the disc when given an empty list.
	sourceFiles := collectMPLSFilenames(scan)

	langs, err := read(sourceFiles)
	if err != nil {
		slog.Warn("executor: mpls enrichment unavailable",
			"drive_index", scan.DriveIndex, "error", err,
			"source_files_count", len(sourceFiles))
		return
	}

	// Log what MPLS returned so we can diagnose empty-stream issues.
	for fn, pl := range langs {
		slog.Info("executor: mpls file loaded",
			"file", fn, "audio_streams", len(pl.Audio), "subtitle_streams", len(pl.Subtitle))
	}

	applied := 0
	if len(sourceFiles) > 0 {
		// Direct matching: each title's SourceFile names its MPLS playlist.
		slog.Info("executor: mpls direct match path",
			"drive_index", scan.DriveIndex,
			"source_files", sourceFiles, "langs_keys_count", len(langs))
		for i := range scan.Titles {
			srcFile := scan.Titles[i].SourceFile()
			tl, ok := langs[srcFile]
			if !ok {
				slog.Info("executor: mpls no match for title source file",
					"title_index", scan.Titles[i].Index, "source_file", srcFile)
				continue
			}
			n := applyMPLSLanguages(&scan.Titles[i], tl)
			slog.Info("executor: mpls applied to title",
				"title_index", scan.Titles[i].Index, "source_file", srcFile,
				"existing_streams", len(scan.Titles[i].Streams)-n, "applied", n)
			applied += n
		}
	} else {
		// Fallback: no source file info available (common on UHD discs).
		// Pick the richest MPLS playlist and apply its languages to all
		// titles that have no streams. All titles on a disc share the same
		// set of audio/subtitle languages, so this is safe.
		best := pickRichestMPLS(langs)
		slog.Info("executor: mpls fallback — no source file info, using richest playlist",
			"drive_index", scan.DriveIndex, "mpls_files_found", len(langs),
			"best_audio", len(best.Audio), "best_subtitle", len(best.Subtitle))
		for i := range scan.Titles {
			applied += applyMPLSLanguages(&scan.Titles[i], best)
		}
	}
	slog.Info("executor: mpls enrichment completed",
		"drive_index", scan.DriveIndex, "streams_updated_or_created", applied)
}

// collectMPLSFilenames returns the deduplicated list of MPLS filenames
// (e.g. "00300.mpls") referenced by scan titles via their SourceFile attribute.
// Titles whose SourceFile does not end in ".mpls" are skipped — those are
// standard BD segment maps, not playlist filenames.
func collectMPLSFilenames(scan *DiscScan) []string {
	seen := make(map[string]bool)
	var out []string
	for i := range scan.Titles {
		sf := scan.Titles[i].SourceFile()
		if sf == "" || !strings.EqualFold(filepath.Ext(sf), ".mpls") {
			continue
		}
		if !seen[sf] {
			seen[sf] = true
			out = append(out, sf)
		}
	}
	return out
}

// pickRichestMPLS returns the PlayItemLanguages with the most combined
// audio + subtitle streams. This is used as a fallback when makemkvcon doesn't
// report source file names, so we can't directly map titles to MPLS playlists.
func pickRichestMPLS(langs map[string]mpls.PlayItemLanguages) mpls.PlayItemLanguages {
	var best mpls.PlayItemLanguages
	bestCount := 0
	for _, tl := range langs {
		count := len(tl.Audio) + len(tl.Subtitle)
		if count > bestCount {
			best = tl
			bestCount = count
		}
	}
	return best
}

// applyMPLSLanguages appends MPLS-derived streams to the title. MPLS is the
// authoritative source for language metadata on Blu-ray discs, so we always
// create streams from it rather than trying to enrich existing SINFO streams
// (which requires fragile stream-type classification that fails on UHD discs).
// Any existing SINFO streams are left in place for codec/bitrate display.
// Returns the number of streams created.
func applyMPLSLanguages(title *TitleInfo, tl mpls.PlayItemLanguages) int {
	return createStreamsFromMPLS(title, tl)
}

// createStreamsFromMPLS creates StreamInfo objects from MPLS data. These carry
// correct Matroska-style type prefixes (A_, S_) and language codes that the
// frontend needs for track selection.
func createStreamsFromMPLS(title *TitleInfo, tl mpls.PlayItemLanguages) int {
	created := 0
	streamIdx := 0

	for _, entry := range tl.Audio {
		attrs := map[int]string{}
		codecID := mpls.CodingTypeToCodecID(entry.CodingType)
		if codecID == "" {
			codecID = "A_UNKNOWN"
		}
		attrs[AttrType] = codecID
		if short := mpls.CodingTypeToCodecShort(entry.CodingType); short != "" {
			attrs[AttrCodecShort] = short
		}
		if entry.LangCode != "" {
			attrs[AttrLangCode] = entry.LangCode
			attrs[AttrLangName] = LangCodeToName(entry.LangCode)
		}
		title.Streams = append(title.Streams, StreamInfo{
			TitleIndex:  title.Index,
			StreamIndex: streamIdx,
			Attributes:  attrs,
		})
		streamIdx++
		created++
	}

	for _, entry := range tl.Subtitle {
		attrs := map[int]string{}
		codecID := mpls.CodingTypeToCodecID(entry.CodingType)
		if codecID == "" {
			codecID = "S_UNKNOWN"
		}
		attrs[AttrType] = codecID
		if short := mpls.CodingTypeToCodecShort(entry.CodingType); short != "" {
			attrs[AttrCodecShort] = short
		}
		if entry.LangCode != "" {
			attrs[AttrLangCode] = entry.LangCode
			attrs[AttrLangName] = LangCodeToName(entry.LangCode)
		}
		title.Streams = append(title.Streams, StreamInfo{
			TitleIndex:  title.Index,
			StreamIndex: streamIdx,
			Attributes:  attrs,
		})
		streamIdx++
		created++
	}

	return created
}

// buildDiscScan aggregates parsed events into a DiscScan result.
func buildDiscScan(driveIndex int, events []Event) *DiscScan {
	scan := &DiscScan{DriveIndex: driveIndex}
	discAttrs := make(map[int]string)
	titleMap := make(map[int]*TitleInfo)    // title index -> merged TitleInfo
	streamMap := make(map[int][]StreamInfo) // title index -> accumulated streams
	// Track per-title, per-stream accumulated attributes.
	type streamKey struct{ title, stream int }
	streamAttrMap := make(map[streamKey]map[int]string)

	for _, ev := range events {
		switch ev.Type {
		case "TCOUT":
			scan.TitleCount = ev.Count

		case "CINFO":
			if ev.Disc != nil {
				for k, v := range ev.Disc.Attributes {
					discAttrs[k] = v
				}
			}

		case "TINFO":
			if ev.Title == nil {
				continue
			}
			ti, ok := titleMap[ev.Title.Index]
			if !ok {
				ti = &TitleInfo{
					Index:      ev.Title.Index,
					Attributes: make(map[int]string),
				}
				titleMap[ev.Title.Index] = ti
			}
			for k, v := range ev.Title.Attributes {
				ti.Attributes[k] = v
			}

		case "SINFO":
			if ev.Stream == nil {
				continue
			}
			sk := streamKey{ev.Stream.TitleIndex, ev.Stream.StreamIndex}
			if streamAttrMap[sk] == nil {
				streamAttrMap[sk] = make(map[int]string)
			}
			for k, v := range ev.Stream.Attributes {
				streamAttrMap[sk][k] = v
			}

		case "MSG":
			if ev.Message != nil {
				scan.Messages = append(scan.Messages, *ev.Message)
			}
		}
	}

	// Build the merged disc metadata.
	discInfo := &DiscInfo{Attributes: discAttrs}
	scan.DiscName = discInfo.Name()
	scan.DiscType = discInfo.Type()

	// Flatten streamAttrMap into per-title stream slices.
	for sk, attrs := range streamAttrMap {
		si := StreamInfo{
			TitleIndex:  sk.title,
			StreamIndex: sk.stream,
			Attributes:  attrs,
		}
		streamMap[sk.title] = append(streamMap[sk.title], si)
	}

	// Build ordered title list.
	scan.Titles = make([]TitleInfo, 0, len(titleMap))
	for idx, ti := range titleMap {
		ti.Streams = streamMap[idx]
		scan.Titles = append(scan.Titles, *ti)
	}

	return scan
}

// backupTimeout bounds a full-disc backup. A dual-layer UHD is ~100GB and a
// drive that is re-reading marginal sectors can crawl, so this is generous.
const backupTimeout = 4 * time.Hour

// Backup copies the disc in driveIndex to destDir as a raw disc folder,
// deliberately WITHOUT --decrypt.
//
// Decryption is precisely what fails on a disc whose AACS directory is spurious:
// MakeMKV sees the directory, demands a volume key, and no such key exists
// because the payload was never encrypted. Taking a raw copy sidesteps that; the
// AACS directory is then removed from the copy so MakeMKV treats it as an
// unencrypted disc.
//
// onEvent receives parsed events (including PRGV progress) and may be nil.
func (e *Executor) Backup(ctx context.Context, driveIndex int, destDir string, onEvent func(Event)) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	ctx, cancel := context.WithTimeout(ctx, backupTimeout)
	defer cancel()

	src := DiscSource(driveIndex)
	slog.Info("makemkvcon: starting backup", "source", src.Arg(), "dest", destDir)

	var messages []Message
	progress := newProgressTracker()
	collect := func(ev Event) {
		if ev.Type == "MSG" && ev.Message != nil {
			messages = append(messages, *ev.Message)
		}
		logMakeMKVEvent(ev, "backup")

		// A UHD backup runs for tens of minutes. Without a heartbeat there is no
		// way to distinguish slow progress from a stalled process.
		if ev.Type == "PRGV" && ev.Progress != nil && ev.Progress.Max > 0 {
			pct := ev.Progress.Total * 100 / ev.Progress.Max
			if progress.shouldLog(pct, time.Now()) {
				slog.Info("makemkvcon: backup progress",
					"source", src.Arg(), "percent", pct, "dest", destDir)
			}
		}

		if onEvent != nil {
			onEvent(ev)
		}
	}

	// No --decrypt: see the doc comment above.
	err := e.runEvents(ctx, collect, "-r", "--progress=-same", "backup", src.Arg(), destDir)
	if err != nil {
		// A backup that failed because the container cannot see the drive at all
		// must say so. Left as a bare "backup failed" it sends the user looking
		// at the disc instead of at group membership.
		if HasNoDrivesMessage(messages) {
			slog.Error("makemkvcon: backup failed with no usable optical drives",
				"source", src.Arg(), "error", err)
			return fmt.Errorf("makemkv: backup %s: %w", src.Arg(), ErrNoOpticalDrives)
		}
		slog.Error("makemkvcon: backup failed", "source", src.Arg(), "dest", destDir, "error", err)
		return fmt.Errorf("makemkv: backup %s to %s: %w", src.Arg(), destDir, err)
	}

	slog.Info("makemkvcon: backup completed", "source", src.Arg(), "dest", destDir)
	return nil
}

// runEvents executes makemkvcon and delivers parsed events to onEvent.
//
// When the runner supports streaming, events arrive while the command is still
// running — necessary for operations measured in tens of minutes. Otherwise the
// output is parsed once the command exits.
func (e *Executor) runEvents(ctx context.Context, onEvent func(Event), args ...string) error {
	emit := func(line string) {
		if ev, err := ParseLine(line); err == nil {
			onEvent(ev)
		}
	}

	if sr, ok := e.runner.(StreamRunner); ok {
		return sr.RunStream(ctx, emit, args...)
	}

	r, runErr := e.runner.Run(ctx, args...)
	if r != nil {
		scanner := bufio.NewScanner(r)
		for scanner.Scan() {
			line := strings.TrimRight(scanner.Text(), "\r")
			if line != "" {
				emit(line)
			}
		}
		if err := scanner.Err(); err != nil {
			slog.Error("makemkvcon: output scanner error", "error", err)
		}
	}
	return runErr
}

// StartRip runs `makemkvcon -r mkv disc:N titleID outputDir` and calls
// onEvent for each parsed Event line in real time. onEvent may be nil.
//
// When selection is non-nil and not empty (see SelectionOpts.IsEmpty), a
// temporary HOME directory is created containing a MakeMKV settings.conf that
// encodes the desired track selection string. HOME is overridden in the child
// process environment so that makemkvcon reads the generated config.
//
// Unlike scan operations, rips use the caller's context directly — no
// additional timeout is applied because disc rips can take 30+ minutes
// depending on title size and drive speed.
func (e *Executor) StartRip(ctx context.Context, src Source, titleID int, expectSource string, outputDir string, onEvent func(Event), selection *SelectionOpts) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	target := src.Arg()
	titleStr := fmt.Sprintf("%d", titleID)

	// expect names the playlist this title number is supposed to be, so the line
	// can be read on its own. Without it, "title 1" is a number whose meaning
	// lives in another line somewhere above.
	// DEBUG: the ripper already announces the rip at INFO with fuller context
	// ("rip: starting"). This is the plumbing echo, useful when tracing the
	// makemkvcon invocation itself, not a second headline.
	slog.Debug("makemkvcon: starting rip",
		"source", target, "title", titleID, "expect", expectSource, "output", outputDir)

	// The guard below stops a rip that is about to copy the wrong title. It does
	// that by cancelling this context rather than signalling the process itself,
	// so the stop goes through exactly the machinery a timeout uses: ask the
	// process group first, kill only what will not go. Signalling directly would
	// send SIGTERM with nothing behind it, and a makemkvcon that ignored it would
	// hang Wait — and the executor mutex with it — for good.
	runCtx, stopRip := context.WithCancel(ctx)
	defer stopRip()

	cmd := exec.CommandContext(runCtx, "makemkvcon", "-r", "--progress=-same", "mkv", target, titleStr, outputDir)
	configureTeardown(cmd)

	// Apply track selection via a temporary HOME directory when requested.
	if selection != nil && !selection.IsEmpty() {
		selStr := BuildSelectionString(*selection)
		homeDir, cleanup, err := WriteTempHome(selStr)
		if err != nil {
			return fmt.Errorf("makemkv: prepare selection home: %w", err)
		}
		defer cleanup()
		cmd.Env = append(os.Environ(), "HOME="+homeDir)
		slog.Info("makemkvcon: using track selection", "selection_string", selStr, "temp_home", homeDir)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("makemkv: stdout pipe: %w", err)
	}
	cmd.Stderr = cmd.Stdout

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("makemkv: start rip %s title %d: %w", target, titleID, err)
	}

	// Verify makemkvcon still numbers this title the way the scan did. It
	// re-enumerates the disc every run and leaves out titles it cannot read, so
	// an index captured at scan time can address a different title now.
	kill := func() { stopRip() }
	guardErr, copyFailed := streamRip(stdout, titleID, expectSource, kill, onEvent, target)

	if err := ripOutcome(guardErr, cmd.Wait(), copyFailed, target, titleID); err != nil {
		return err
	}
	// DEBUG: the ripper reports the job's completion as a state event; this is
	// the plumbing echo of the makemkvcon command returning cleanly.
	slog.Debug("makemkvcon: rip command completed successfully",
		"source", target, "title", titleID, "expect", expectSource)
	return nil
}

// streamRip reads a rip's output, deciding as it goes whether the copy about to
// start is the title that was asked for.
//
// Separated from process management so the decision can be tested: the guard's
// own logic was covered while nothing checked that StartRip consulted it, which
// is the same kind of unverified assumption that caused the bug it guards.
//
// Returns the guard's objection, if any, and whether makemkvcon reported that
// it saved nothing.
func streamRip(out io.Reader, titleID int, expectSource string, kill func(), onEvent func(Event), target string) (error, bool) {
	guard := newTitleGuard(titleID, expectSource)
	var guardErr error
	copyFailed := false

	progress := newProgressTracker()
	scanner := bufio.NewScanner(out)
	for scanner.Scan() {
		line := strings.TrimRight(scanner.Text(), "\r")
		if line == "" {
			continue
		}
		ev, err := ParseLine(line)
		if err != nil {
			// makemkvcon returns a nonzero exit only on a fatal error, and it
			// prints that error as a plain line rather than robot format — so
			// ParseLine rejects it. Dropping it here is how a fatal rip came to
			// "report no reason": the reason was on the line we threw away. Keep
			// it, as its own event, so it reaches the log and the failure
			// capture the activity page shows. It is not fed to the guard or the
			// progress tracker — it is not a title event.
			raw := Event{Type: "MSG", Message: &Message{Text: line}}
			logMakeMKVEvent(raw, "rip")
			if onEvent != nil {
				onEvent(raw)
			}
			continue
		}
		// Ripping from a stripped backup folder is the least-exercised path in
		// the whole pipeline; if MakeMKV objects to the folder source, its
		// complaint has to reach the log rather than only the progress bar.
		logMakeMKVEvent(ev, "rip")

		if ev.Type == "MSG" && ev.Message != nil && ev.Message.Code == MsgCopyFailed {
			copyFailed = true
		}

		// Stop before the copy rather than after: once makemkvcon has written
		// the file, the wrong title is already on disk under the right name.
		if guardErr == nil {
			guard.observe(ev)
			if verr := guard.verdict(); verr != nil {
				guardErr = verr
				slog.Error("makemkvcon: aborting rip, the title moved",
					"source", target, "requested_index", titleID,
					"expected", expectSource, "error", verr)
				kill()
			}
		}

		if ev.Type == "PRGV" && ev.Progress != nil && ev.Progress.Max > 0 {
			pct := ev.Progress.Total * 100 / ev.Progress.Max
			if progress.shouldLog(pct, time.Now()) {
				slog.Info("makemkvcon: rip progress",
					"source", target, "title", titleID, "expect", expectSource, "percent", pct)
			}
		}
		if onEvent != nil {
			onEvent(ev)
		}
	}
	if scanErr := scanner.Err(); scanErr != nil {
		slog.Error("makemkvcon: rip scanner error", "error", scanErr)
	}
	return guardErr, copyFailed
}

// ripOutcome decides which of a rip's several failure signals to report.
//
// A killed process reports "signal: killed", which describes what the kernel
// did rather than why. Twice in this project that sent an investigation after
// cancellation bugs when the real answer was elsewhere.
func ripOutcome(guardErr, waitErr error, copyFailed bool, target string, titleID int) error {
	// The guard outranks the wait error: the process was killed on purpose.
	if guardErr != nil {
		return guardErr
	}
	if waitErr != nil {
		slog.Error("makemkvcon: rip command failed", "source", target, "title", titleID, "error", waitErr)
		// A clean nonzero exit reports as "exit status N" — a number that says
		// nothing on its own. When makemkvcon quits with a code, say so in words
		// and name the code, without inventing a meaning for it. A stop by
		// signal (a cancelled context, a timeout) is a different thing and keeps
		// its own wording.
		var exitErr *exec.ExitError
		if errors.As(waitErr, &exitErr) && exitErr.ExitCode() >= 0 {
			// makemkvcon returns zero even when it fails to save a title; a
			// nonzero code means a fatal error, and it prints the reason as it
			// dies. That reason is captured with the rip's other output (see
			// streamRip) and shown on the activity page — point there rather
			// than guessing at a cause.
			return fmt.Errorf("makemkv: rip %s title %d: makemkvcon exited with status %d — a fatal error; see the captured MakeMKV output for the reason",
				target, titleID, exitErr.ExitCode())
		}
		return fmt.Errorf("makemkv: rip %s title %d: %w", target, titleID, waitErr)
	}
	// makemkvcon exits zero after saving nothing, so without this the run reads
	// as a success that merely left no file behind.
	if copyFailed {
		slog.Error("makemkvcon: rip saved no titles", "source", target, "title", titleID)
		// "saved no titles" is what MakeMKV reported and is all this knows: a
		// rip also saves nothing when the guard stops it and when the track
		// selection matches nothing. The phrase is load-bearing beyond its
		// reading — salvageable() in internal/web keys the salvage button off
		// it — so it stays exactly as written.
		return fmt.Errorf("makemkv: rip %s title %d: makemkvcon reported the copy failed and saved no titles", target, titleID)
	}
	return nil
}
