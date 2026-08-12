// Package ddrescue recovers what can still be read from a damaged optical disc.
//
// MakeMKV aims for a perfect copy or none at all: when the drive reports an
// unrecoverable read, it abandons the title. That is the right default for an
// archival tool and useless when the disc has a scratch, because a player would
// have shown you the film with a glitch. ddrescue fills what it cannot read with
// zeros, which turns a read error into a moment of corrupt video — something
// MakeMKV will then copy without complaint.
package ddrescue

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Defaults chosen so a salvage finishes in an evening rather than a weekend.
//
// ddrescue's first pass reads everything readable at full speed and skips the
// damaged areas, returning to scrape them only at the end — so the bulk of the
// work is never subject to these limits, and the part that is has a floor on
// how much video it can cost.
const (
	// DefaultRetries is how many times ddrescue re-reads a bad sector.
	DefaultRetries = 3
	// DefaultTimeout caps the whole run. A scratch that has not yielded in this
	// long is not going to.
	DefaultTimeout = 30 * time.Minute
	// sectorSize is the optical disc block size. Reading in whole sectors keeps
	// the damaged region from spreading into neighbouring good data.
	sectorSize = 2048
)

// Runner executes ddrescue. Injected so the orchestration can be tested without
// a damaged disc to hand.
type Runner interface {
	Run(ctx context.Context, args []string, onLine func(string)) error
}

// Options describes one rescue.
type Options struct {
	// Source is the file to read, typically on a mounted disc.
	Source string
	// Dest is the file to write. Existing content is preserved: ddrescue writes
	// into it rather than truncating.
	Dest string
	// MapFile records what has been recovered so far, which is what makes a
	// rescue resumable rather than starting the disc over.
	MapFile string
	// StartOffset skips bytes already known good — the part a backup copied
	// before it hit the damage. Zero rescues the whole file.
	StartOffset int64
	// Retries and Timeout bound the scraping phase. Zero values take the
	// defaults above.
	Retries int
	Timeout time.Duration
}

// Progress is one report from a running rescue.
type Progress struct {
	// BytesRescued is how much has been recovered so far.
	BytesRescued int64
	// BytesBad is how much has been given up on and zero-filled.
	BytesBad int64
	// Line is ddrescue's own status text, for the log.
	Line string
}

// Rescue copies Source to Dest, filling unreadable regions with zeros.
//
// It returns nil when ddrescue completed, including when it had to give up on
// part of the file — that is the expected outcome on a scratched disc and the
// whole point of running it. A non-nil error means the rescue could not run.
func Rescue(ctx context.Context, r Runner, opts Options, onProgress func(Progress)) error {
	if opts.Source == "" || opts.Dest == "" {
		return fmt.Errorf("ddrescue: source and destination are both required")
	}
	if opts.MapFile == "" {
		return fmt.Errorf("ddrescue: a map file is required, or a resumed rescue would start from the beginning")
	}
	if err := os.MkdirAll(filepath.Dir(opts.Dest), 0o777); err != nil {
		return fmt.Errorf("ddrescue: create destination directory: %w", err)
	}

	retries := opts.Retries
	if retries <= 0 {
		retries = DefaultRetries
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}

	args := []string{
		"--idirect",
		fmt.Sprintf("--sector-size=%d", sectorSize),
		fmt.Sprintf("--retry-passes=%d", retries),
		fmt.Sprintf("--timeout=%ds", int(timeout.Seconds())),
		// Zero-fill what cannot be read, so the file is complete in length and
		// MakeMKV's reads succeed all the way through.
		"--fill-mode=-",
	}
	if opts.StartOffset > 0 {
		args = append(args,
			fmt.Sprintf("--input-position=%d", opts.StartOffset),
			fmt.Sprintf("--output-position=%d", opts.StartOffset),
		)
	}
	args = append(args, opts.Source, opts.Dest, opts.MapFile)

	slog.Info("ddrescue: starting", "source", opts.Source, "dest", opts.Dest,
		"start_offset", opts.StartOffset, "retries", retries, "timeout", timeout)

	err := r.Run(ctx, args, func(line string) {
		if onProgress == nil {
			return
		}
		p := parseProgress(line)
		p.Line = line
		onProgress(p)
	})
	if err != nil {
		return fmt.Errorf("ddrescue: %s: %w", filepath.Base(opts.Source), err)
	}
	return nil
}

// parseProgress reads the running totals out of ddrescue's status output.
//
// The numbers are what tell a user whether a rescue is working: "rescued: 48 GB,
// bad areas: 9 MB" is the difference between a film with a glitch and a wasted
// night.
func parseProgress(line string) Progress {
	var p Progress
	if v, ok := sizeAfter(line, "rescued:"); ok {
		p.BytesRescued = v
	}
	if v, ok := sizeAfter(line, "bad areas:"); ok {
		p.BytesBad = v
	} else if v, ok := sizeAfter(line, "bad sectors:"); ok {
		p.BytesBad = v
	}
	return p
}

// sizeAfter finds "<label> <number> <unit>" and returns it in bytes.
func sizeAfter(line, label string) (int64, bool) {
	i := strings.Index(line, label)
	if i < 0 {
		return 0, false
	}
	fields := strings.Fields(line[i+len(label):])
	if len(fields) == 0 {
		return 0, false
	}
	value, err := strconv.ParseFloat(strings.ReplaceAll(fields[0], ",", ""), 64)
	if err != nil {
		return 0, false
	}
	multiplier := float64(1)
	if len(fields) > 1 {
		// ddrescue writes "48354 MB," — the unit carries the separator with it.
		unit := strings.Trim(strings.ToLower(fields[1]), ",.;:")
		switch strings.TrimSuffix(unit, "b") {
		case "k":
			multiplier = 1000
		case "m":
			multiplier = 1000 * 1000
		case "g":
			multiplier = 1000 * 1000 * 1000
		case "t":
			multiplier = 1000 * 1000 * 1000 * 1000
		}
	}
	return int64(value * multiplier), true
}

// ExecRunner runs the real ddrescue binary.
type ExecRunner struct{}

// Run executes ddrescue, streaming its output line by line.
//
// ddrescue reports progress by rewriting the same terminal lines with carriage
// returns rather than newlines, so the output is split on both.
func (ExecRunner) Run(ctx context.Context, args []string, onLine func(string)) error {
	cmd := exec.CommandContext(ctx, "ddrescue", args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe: %w", err)
	}
	cmd.Stderr = cmd.Stdout

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start: %w", err)
	}

	scanner := bufio.NewScanner(stdout)
	scanner.Split(scanLinesOrReturns)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if onLine != nil {
			onLine(line)
		}
	}
	if err := scanner.Err(); err != nil {
		slog.Warn("ddrescue: reading output stopped early", "error", err)
	}

	return cmd.Wait()
}

// scanLinesOrReturns splits on \n or \r, because ddrescue's progress display
// updates in place.
func scanLinesOrReturns(data []byte, atEOF bool) (int, []byte, error) {
	for i, b := range data {
		if b == '\n' || b == '\r' {
			return i + 1, data[:i], nil
		}
	}
	if atEOF && len(data) > 0 {
		return len(data), data, nil
	}
	if atEOF {
		return 0, nil, io.EOF
	}
	return 0, nil, nil
}
