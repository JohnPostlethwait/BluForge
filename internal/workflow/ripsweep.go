package workflow

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// RipTempPrefix names the per-batch temp directories ManualRip creates under
// the output dir. The leading dot keeps them out of the way of media scanners.
const RipTempPrefix = ".rip-"

// SweepRipDirs removes rip temp directories left behind by a previous run.
//
// A .rip- directory lives only for the length of one batch, so anything still
// present at startup belongs to a process that died mid-rip -- a container
// stop, an OOM kill, a host reboot -- and nothing will ever come back for it.
// Left alone they accumulate, one per interrupted rip, each holding however
// much of a title had been written.
//
// The exception is a rip that finished but could not be moved to its
// destination. That file is complete and is the only copy, so its directory
// arrives in keep and is left alone.
//
// Like SweepScratch this must run before any rip can create a new directory,
// or it would delete one mid-write.
func SweepRipDirs(outputDir string, keep []string) error {
	entries, err := os.ReadDir(outputDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("sweep rip dirs: %w", err)
	}

	tracked := make(map[string]bool, len(keep))
	for _, dir := range keep {
		if abs, err := filepath.Abs(dir); err == nil {
			tracked[abs] = true
		}
	}

	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), RipTempPrefix) {
			continue
		}
		path := filepath.Join(outputDir, e.Name())

		abs, err := filepath.Abs(path)
		if err == nil && tracked[abs] {
			slog.Info("keeping rip temp dir: a failed job still points at the file inside it",
				"path", path)
			continue
		}

		// Say how much is going, for the same reason SweepScratch does: a sweep
		// that silently deletes tens of gigabytes reads as one unremarkable log
		// line until someone goes looking for what was in it.
		size := dirSize(path)
		slog.Warn("deleting rip temp dir left behind by a previous run",
			"path", path, "bytes", size, "gib", size/(1<<30),
			"reason", "no unfinished job accounts for it")
		if err := os.RemoveAll(path); err != nil {
			slog.Warn("could not sweep rip temp dir", "path", path, "error", err)
		}
	}
	return nil
}

// PreservedRipDirs returns the rip temp directories holding a finished file
// that a failed job still points at, for passing to SweepRipDirs.
//
// It returns an error rather than an empty list when the jobs cannot be read:
// an empty list tells the sweep that nothing is worth keeping, which would
// delete the very files this exists to protect.
func (o *Orchestrator) PreservedRipDirs() ([]string, error) {
	if o.store == nil {
		return nil, nil
	}
	jobs, err := o.store.ListJobsByStatus("failed")
	if err != nil {
		return nil, fmt.Errorf("list failed jobs: %w", err)
	}

	var dirs []string
	seen := make(map[string]bool)
	for _, job := range jobs {
		dir := ripDirOf(job.OutputPath)
		if dir == "" || seen[dir] {
			continue
		}
		// A record can outlive the file: the user may have moved it by hand.
		if _, statErr := os.Stat(job.OutputPath); statErr != nil {
			continue
		}
		seen[dir] = true
		dirs = append(dirs, dir)
	}
	return dirs, nil
}

// ripDirOf walks up from a file to the .rip- directory containing it, returning
// "" if the path is not inside one. It walks rather than assuming a depth so
// the per-title subdirectory layout can change without silently breaking the
// sweep's keep list.
func ripDirOf(path string) string {
	if path == "" {
		return ""
	}
	dir := filepath.Dir(path)
	for {
		if strings.HasPrefix(filepath.Base(dir), RipTempPrefix) {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}
