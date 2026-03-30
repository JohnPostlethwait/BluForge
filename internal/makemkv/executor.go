package makemkv

import (
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
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

	cmd := exec.CommandContext(ctx, "makemkvcon", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		// Return output even on error so callers can inspect messages.
		return strings.NewReader(string(out)), err
	}
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
type Executor struct {
	runner CmdRunner
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

// ListDrives detects optical drives and queries each one via makemkvcon.
//
// On Linux, optical devices are detected via sysfs (/sys/block/*/device/type)
// and each is scanned individually with `makemkvcon -r info dev:/dev/srN`.
// This is dramatically faster on systems with many non-optical drives (e.g.
// Unraid with 35+ HDDs) because makemkvcon's `disc:9999` approach probes
// every block device.
//
// On non-Linux systems (or when sysfs detection finds nothing), it falls back
// to the original `disc:9999` full scan.
func (e *Executor) ListDrives(ctx context.Context) ([]DriveInfo, error) {
	opticalDevices := DetectOpticalDevices()
	if len(opticalDevices) > 0 {
		slog.Info("detected optical devices via sysfs", "count", len(opticalDevices), "devices", opticalDevices)
		return e.listDrivesTargeted(ctx, opticalDevices)
	}
	slog.Info("sysfs detection unavailable, falling back to disc:9999 scan")
	return e.listDrivesFull(ctx)
}

// listDrivesTargeted scans specific device paths rather than all drives.
func (e *Executor) listDrivesTargeted(ctx context.Context, devices []string) ([]DriveInfo, error) {
	var allDrives []DriveInfo
	for i, dev := range devices {
		slog.Info("probing optical device", "device", dev, "index", i)
		target := fmt.Sprintf("dev:%s", dev)
		r, err := e.runner.Run(ctx, "-r", "info", target)
		slog.Info("probe complete", "device", dev, "error", err)
		events, parseErr := ParseAll(r)
		if parseErr != nil {
			if err != nil {
				slog.Warn("failed to probe optical device", "device", dev, "error", err)
			}
			continue
		}

		drives := drivesFromEvents(events)
		if len(drives) == 0 && err == nil {
			// Device exists but makemkvcon didn't return DRV lines.
			// Create a minimal DriveInfo so the device is still visible.
			allDrives = append(allDrives, DriveInfo{
				Index:      i,
				DevicePath: dev,
			})
			continue
		}

		for idx := range drives {
			// Ensure the DevicePath is set (makemkvcon may report it, but
			// if not we know it from our sysfs scan).
			if drives[idx].DevicePath == "" {
				drives[idx].DevicePath = dev
			}
		}
		allDrives = append(allDrives, drives...)
	}
	return allDrives, nil
}

// listDrivesFull runs `makemkvcon -r info disc:9999` and returns the list of
// drives reported via DRV lines (original full-scan approach).
func (e *Executor) listDrivesFull(ctx context.Context) ([]DriveInfo, error) {
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
func (e *Executor) ScanDisc(ctx context.Context, driveIndex int) (*DiscScan, error) {
	target := fmt.Sprintf("disc:%d", driveIndex)
	r, err := e.runner.Run(ctx, "-r", "info", target)
	if err != nil {
		return nil, fmt.Errorf("makemkv: scan disc %d: %w", driveIndex, err)
	}

	events, err := ParseAll(r)
	if err != nil {
		return nil, fmt.Errorf("makemkv: scan disc %d parse: %w", driveIndex, err)
	}

	scan := &DiscScan{DriveIndex: driveIndex}
	discAttrs := make(map[int]string)
	titleMap := make(map[int]*TitleInfo)      // title index -> merged TitleInfo
	streamMap := make(map[int][]StreamInfo)   // title index -> accumulated streams
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

	return scan, nil
}

// StartRip runs `makemkvcon -r mkv disc:N titleID outputDir` and calls
// onEvent for each parsed Event line. onEvent may be nil.
func (e *Executor) StartRip(ctx context.Context, driveIndex, titleID int, outputDir string, onEvent func(Event)) error {
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
