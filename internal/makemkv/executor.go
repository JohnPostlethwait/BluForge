package makemkv

import (
	"bufio"
	"context"
	"fmt"
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

// scanTimeout is the maximum time a disc scan may run. UHD discs with AACS
// negotiation and LibreDrive activation can take several minutes.
const scanTimeout = 10 * time.Minute

// driveListTimeout is the maximum time a drive listing may run. This is a
// lightweight operation that should complete quickly.
const driveListTimeout = 30 * time.Second

func (r *realRunner) Run(ctx context.Context, args ...string) (*strings.Reader, error) {
	slog.Info("makemkvcon: executing", "args", args)

	cmd := exec.CommandContext(ctx, "makemkvcon", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		slog.Error("makemkvcon: command failed", "args", args, "error", err, "output_bytes", len(out))
		// Return output even on error so callers can inspect messages.
		return strings.NewReader(string(out)), err
	}

	slog.Info("makemkvcon: command completed", "args", args, "output_bytes", len(out))
	return strings.NewReader(string(out)), nil
}

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
}

// ListDrives runs `makemkvcon -r --cache=1 info disc:9999` and returns the
// list of drives reported via DRV lines.
//
// --cache=1 minimizes memory allocation for this lightweight operation.
func (e *Executor) ListDrives(ctx context.Context) ([]DriveInfo, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	ctx, cancel := context.WithTimeout(ctx, driveListTimeout)
	defer cancel()

	r, err := e.runner.Run(ctx, "-r", "--cache=1", "info", "disc:9999")
	if err != nil {
		// makemkvcon returns non-zero when no disc is present; try to parse
		// whatever output we have before returning the error.
		events, parseErr := ParseAll(r)
		if parseErr != nil {
			return nil, fmt.Errorf("makemkv: list drives: %w", err)
		}
		drives := drivesFromEvents(events)
		if len(drives) == 0 {
			return nil, fmt.Errorf("makemkv: list drives: %w", err)
		}
		return drives, nil
	}

	events, err := ParseAll(r)
	if err != nil {
		return nil, fmt.Errorf("makemkv: list drives parse: %w", err)
	}
	return drivesFromEvents(events), nil
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
	slog.Info("executor: starting disc scan", "drive_index", driveIndex)

	// Pre-lookup: get the device path for MPLS enrichment.  This is a separate,
	// lightweight lock acquisition that completes before the main scan starts.
	devicePath := e.devicePathForDrive(ctx, driveIndex)

	e.mu.Lock()
	defer e.mu.Unlock()

	ctx, cancel := context.WithTimeout(ctx, scanTimeout)
	defer cancel()

	target := fmt.Sprintf("disc:%d", driveIndex)
	r, cmdErr := e.runner.Run(ctx, "-r", "info", target)

	// Always attempt to parse output — makemkvcon returns non-zero on AACS
	// warnings but may still have produced valid TINFO/CINFO/SINFO lines.
	events, parseErr := ParseAll(r)
	if parseErr != nil {
		slog.Error("executor: disc scan parse failed", "drive_index", driveIndex, "error", parseErr)
		if cmdErr != nil {
			return nil, fmt.Errorf("makemkv: scan disc %d: %w", driveIndex, cmdErr)
		}
		return nil, fmt.Errorf("makemkv: scan disc %d parse: %w", driveIndex, parseErr)
	}

	if cmdErr != nil {
		slog.Warn("executor: disc scan command exited non-zero, parsing output anyway",
			"drive_index", driveIndex, "error", cmdErr, "event_count", len(events))
	}

	scan := buildDiscScan(driveIndex, events)

	// If we got 0 titles, check for actionable error messages from makemkvcon.
	if len(scan.Titles) == 0 {
		var failureReason string
		for _, m := range scan.Messages {
			// MSG code 5010 = "Failed to open disc" — terminal failure.
			if m.Code == 5010 {
				failureReason = m.Text
				break
			}
		}
		if failureReason != "" {
			slog.Error("executor: disc scan failed", "drive_index", driveIndex, "reason", failureReason)
			return nil, fmt.Errorf("makemkv: scan disc %d: %s", driveIndex, failureReason)
		}
	}

	// If the command failed AND we got no useful data, return the original error.
	if cmdErr != nil && len(scan.Titles) == 0 && scan.DiscName == "" {
		slog.Error("executor: disc scan command failed with no usable output",
			"drive_index", driveIndex, "error", cmdErr)
		return nil, fmt.Errorf("makemkv: scan disc %d: %w", driveIndex, cmdErr)
	}

	slog.Info("executor: disc scan completed", "drive_index", driveIndex,
		"disc_name", scan.DiscName, "title_count", len(scan.Titles))

	// Enrich stream language codes from MPLS playlist files, which are the
	// authoritative source for language metadata on both standard BD and UHD.
	// CLPI files (what makemkvcon reads in robot info mode) often omit language
	// codes for UHD disc authorings.
	if devicePath != "" {
		enrichScanFromMPLS(scan, devicePath)
	} else {
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

// devicePathForDrive returns the device path (e.g. "/dev/sr0") for driveIndex
// by running a lightweight ListDrives call.  Returns "" on any error; callers
// treat a missing path as a non-fatal condition.
func (e *Executor) devicePathForDrive(ctx context.Context, driveIndex int) string {
	drives, err := e.ListDrives(ctx)
	if err != nil {
		return ""
	}
	for _, d := range drives {
		if d.Index == driveIndex {
			return d.DevicePath
		}
	}
	return ""
}

// enrichScanFromMPLS reads MPLS playlist files from the disc at devicePath and
// writes language codes into the streams of scan.  Each scan title's SourceFile
// attribute (TINFO attr 16) names the corresponding MPLS file (e.g.
// "00300.mpls"); streams are matched by type and position within each title.
//
// Non-fatal: any error is logged at debug level and enrichment is skipped.
func enrichScanFromMPLS(scan *DiscScan, devicePath string) {
	// Collect the unique MPLS filenames referenced by this scan's titles.
	sourceFiles := collectMPLSFilenames(scan)
	if len(sourceFiles) == 0 {
		slog.Debug("executor: no MPLS source files in scan, skipping enrichment",
			"drive_index", scan.DriveIndex)
		return
	}

	langs, err := mpls.ReadDiscLanguages(devicePath, sourceFiles)
	if err != nil {
		slog.Debug("executor: mpls enrichment unavailable",
			"drive_index", scan.DriveIndex, "error", err)
		return
	}

	applied := 0
	for i := range scan.Titles {
		srcFile := scan.Titles[i].SourceFile()
		tl, ok := langs[srcFile]
		if !ok {
			continue
		}
		applied += applyMPLSLanguages(&scan.Titles[i], tl)
	}
	slog.Debug("executor: mpls enrichment applied",
		"drive_index", scan.DriveIndex, "streams_updated", applied)
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

// applyMPLSLanguages writes language codes from tl into the audio and subtitle
// streams of title, matching by stream type and position within each type.
// Returns the number of streams updated.
func applyMPLSLanguages(title *TitleInfo, tl mpls.PlayItemLanguages) int {
	updated := 0
	audioIdx := 0
	subIdx := 0
	for j := range title.Streams {
		s := &title.Streams[j]
		switch {
		case s.IsAudio():
			if audioIdx < len(tl.Audio) && tl.Audio[audioIdx] != "" {
				if s.Attributes == nil {
					s.Attributes = make(map[int]string)
				}
				s.Attributes[AttrLangCode] = tl.Audio[audioIdx]
				updated++
			}
			audioIdx++
		case s.IsSubtitle():
			if subIdx < len(tl.Subtitle) && tl.Subtitle[subIdx] != "" {
				if s.Attributes == nil {
					s.Attributes = make(map[int]string)
				}
				s.Attributes[AttrLangCode] = tl.Subtitle[subIdx]
				updated++
			}
			subIdx++
		}
	}
	return updated
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
func (e *Executor) StartRip(ctx context.Context, driveIndex, titleID int, outputDir string, onEvent func(Event), selection *SelectionOpts) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	target := fmt.Sprintf("disc:%d", driveIndex)
	titleStr := fmt.Sprintf("%d", titleID)

	slog.Info("makemkvcon: starting rip", "drive", driveIndex, "title", titleID, "output", outputDir)

	cmd := exec.CommandContext(ctx, "makemkvcon", "-r", "--progress=-same", "mkv", target, titleStr, outputDir)

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
		return fmt.Errorf("makemkv: start rip disc:%d title %d: %w", driveIndex, titleID, err)
	}

	// Stream output line-by-line for real-time progress updates.
	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		line := strings.TrimRight(scanner.Text(), "\r")
		if line == "" {
			continue
		}
		if onEvent != nil {
			if ev, err := ParseLine(line); err == nil {
				onEvent(ev)
			}
		}
	}
	if scanErr := scanner.Err(); scanErr != nil {
		slog.Error("makemkvcon: rip scanner error", "error", scanErr)
	}

	if err := cmd.Wait(); err != nil {
		slog.Error("makemkvcon: rip command failed", "drive", driveIndex, "title", titleID, "error", err)
		return fmt.Errorf("makemkv: rip disc:%d title %d: %w", driveIndex, titleID, err)
	}
	slog.Info("makemkvcon: rip command completed successfully", "drive", driveIndex, "title", titleID)
	return nil
}
