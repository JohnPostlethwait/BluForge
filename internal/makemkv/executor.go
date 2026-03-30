package makemkv

import (
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// CmdRunner is the interface for running makemkvcon commands. It receives the
// arguments to pass after the binary name and returns the combined output as a
// strings.Reader along with any execution error.
type CmdRunner interface {
	Run(ctx context.Context, args ...string) (*strings.Reader, error)
}

// realRunner executes the real makemkvcon binary.
type realRunner struct{}

// commandTimeout is the maximum time a single makemkvcon invocation may run
// before being killed. Prevents indefinite hangs on unresponsive drives.
const commandTimeout = 2 * time.Minute

func (r *realRunner) Run(ctx context.Context, args ...string) (*strings.Reader, error) {
	ctx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()

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

// ListDrives runs `makemkvcon -r info disc:9999` and returns the list of
// drives reported via DRV lines.
func (e *Executor) ListDrives(ctx context.Context) ([]DriveInfo, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	r, err := e.runner.Run(ctx, "-r", "info", "disc:9999")
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
// makemkvcon often exits with a non-zero status even when it successfully
// enumerates titles (e.g. AACS warnings on Blu-ray discs). We always attempt
// to parse the output regardless of exit code, returning an error only when no
// useful disc data was produced.
func (e *Executor) ScanDisc(ctx context.Context, driveIndex int) (*DiscScan, error) {
	slog.Info("executor: starting disc scan", "drive_index", driveIndex)

	e.mu.Lock()
	defer e.mu.Unlock()

	target := fmt.Sprintf("disc:%d", driveIndex)
	r, cmdErr := e.runner.Run(ctx, "-r", "--minlength=120", "info", target)

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

	// If the command failed AND we got no useful data, return the original error.
	if cmdErr != nil && len(scan.Titles) == 0 && scan.DiscName == "" {
		slog.Error("executor: disc scan command failed with no usable output",
			"drive_index", driveIndex, "error", cmdErr)
		return nil, fmt.Errorf("makemkv: scan disc %d: %w", driveIndex, cmdErr)
	}

	slog.Info("executor: disc scan completed", "drive_index", driveIndex,
		"disc_name", scan.DiscName, "title_count", len(scan.Titles),
		"cmd_error", cmdErr != nil)
	return scan, nil
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
// onEvent for each parsed Event line. onEvent may be nil.
func (e *Executor) StartRip(ctx context.Context, driveIndex, titleID int, outputDir string, onEvent func(Event)) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	target := fmt.Sprintf("disc:%d", driveIndex)
	titleStr := fmt.Sprintf("%d", titleID)

	r, err := e.runner.Run(ctx, "-r", "mkv", target, titleStr, outputDir)
	// Parse output regardless of error so progress/messages are delivered.
	events, parseErr := ParseAll(r)
	if parseErr == nil && onEvent != nil {
		for _, ev := range events {
			onEvent(ev)
		}
	}
	if err != nil {
		return fmt.Errorf("makemkv: rip disc:%d title %d: %w", driveIndex, titleID, err)
	}
	return nil
}
