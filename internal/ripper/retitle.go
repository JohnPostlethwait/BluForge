package ripper

import (
	"context"

	"github.com/johnpostlethwait/bluforge/internal/makemkv"
)

// ripOnce runs a rip at the index the job was created for, and only that index.
//
// There used to be a retry here. When makemkvcon's enumeration did not hold the
// expected title at the expected number, the guard reported where it believed
// the title had gone, and this re-pointed the job at that index and copied it —
// recording the new number on the job so the log would agree with itself.
//
// The reasoning was that a disc with damaged sectors renumbers between passes:
// Police Story 2 enumerated seven titles on one rip and eight on the next, and
// failing there would be safe but wasteful when the title is right there under
// a different number.
//
// What that reasoning missed is where the corrected index comes from. It is a
// filename match against an enumeration that is still arriving, and a filename
// does not identify a title: a multi-angle playlist announces every angle under
// the same name. On Kiki's Delivery Service the guard misread an angle number
// as a title number, concluded the feature had moved to index 1, and this
// function copied index 1 — a different, shorter cut of the film — and filed it
// under the name of the title that was asked for. Two jobs, two wrong files, no
// failure reported. The rip was fast and the file was plausible, which is how it
// survived being looked at.
//
// So the trade is not "a wasted pass versus a saved one". It is "a rip you
// re-run versus a file you may never notice is wrong". A guess may report what
// it saw; it may not decide what gets copied. The Police Story 2 case now costs
// a re-scan, which is the price of never doing this again.
func ripOnce(ctx context.Context, exec RipExecutor, job *Job, onEvent func(makemkv.Event)) error {
	return exec.StartRip(ctx, job.RipSource(), job.TitleIndex, job.SourceFile, job.OutputDir, onEvent, job.SelectionOpts)
}
