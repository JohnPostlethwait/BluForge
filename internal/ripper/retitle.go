package ripper

import (
	"context"
	"errors"
	"log/slog"

	"github.com/johnpostlethwait/bluforge/internal/makemkv"
)

// ripWithRetry runs a rip, correcting the title number once if makemkvcon
// numbered the disc differently this pass.
//
// makemkvcon numbers titles by their position in the pass that is running, and
// leaves out any it cannot read. On a disc with damaged sectors that changes
// between invocations: Police Story 2 enumerated seven titles on the first rip
// and eight on the next, with everything after the failed title shifted down by
// one. Failing here would be safe but wasteful — the title is on the disc and
// readable, it just has a different number now.
func ripWithRetry(ctx context.Context, exec RipExecutor, job *Job, onEvent func(makemkv.Event)) error {
	err := exec.StartRip(ctx, job.RipSource(), job.TitleIndex, job.SourceFile, job.OutputDir, onEvent, job.SelectionOpts)

	var moved *makemkv.TitleMovedError
	if !errors.As(err, &moved) || moved.CorrectIndex < 0 {
		return err
	}

	slog.Warn("rip: title numbering changed, retrying at the corrected index",
		"job_id", job.ID, "source_file", job.SourceFile,
		"requested_index", moved.Requested, "corrected_index", moved.CorrectIndex,
		"index_now_holds", moved.Found)

	// Record what was actually ripped. Leaving the old number on the job would
	// make the log describe a title that no longer exists at that position.
	job.TitleIndex = moved.CorrectIndex

	// One retry only. An enumeration that keeps moving would otherwise walk the
	// disc for hours, and each pass costs a full re-read.
	return exec.StartRip(ctx, job.RipSource(), moved.CorrectIndex, job.SourceFile, job.OutputDir, onEvent, job.SelectionOpts)
}
