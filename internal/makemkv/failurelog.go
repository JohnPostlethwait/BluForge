package makemkv

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ripJobIDKey carries a rip's job ID through the context.
type ripJobIDKey struct{}

// WithRipJobID threads a rip's job ID through the context so StartRip can name
// the saved failure log after it — lining the file up with the job on the
// activity page and in the "rip: starting" log line.
//
// It travels by context rather than as a StartRip argument on purpose: StartRip
// is behind the RipExecutor interface that a dozen tests implement, and widening
// that signature to carry a value only the failure-log path uses would spray
// edits across packages that have nothing to do with logging. Absent, the log
// falls back to a disc-and-title name, which still identifies it.
func WithRipJobID(ctx context.Context, jobID int64) context.Context {
	return context.WithValue(ctx, ripJobIDKey{}, jobID)
}

func ripJobIDFromContext(ctx context.Context) (int64, bool) {
	id, ok := ctx.Value(ripJobIDKey{}).(int64)
	return id, ok && id > 0
}

// expectedDuplicatesKey carries the playlists a scan declared equal to the
// title being ripped.
type expectedDuplicatesKey struct{}

// WithExpectedDuplicates threads the playlists a scan found equal to the title
// being ripped, so StartRip's guard accepts one of them at the requested index
// instead of killing a correct rip on a seamless-branching disc.
//
// By context for the same reason as the job ID: to avoid widening StartRip
// across its many implementers with a value only the guard uses. Unset, the
// guard keeps its filename-only behaviour — which can only cause the old
// needless kill, never a wrong-title rip, so a missed set is safe.
func WithExpectedDuplicates(ctx context.Context, playlists []string) context.Context {
	return context.WithValue(ctx, expectedDuplicatesKey{}, playlists)
}

func expectedDuplicatesFromContext(ctx context.Context) []string {
	p, _ := ctx.Value(expectedDuplicatesKey{}).([]string)
	return p
}

// failureLogName is the filename a failed rip's makemkvcon debug log is saved
// under. The job ID when the context carries it; otherwise the disc and title,
// which still tell one failure from another.
func failureLogName(ctx context.Context, target string, titleID int) string {
	ts := time.Now().UTC().Format("20060102T150405Z")
	if id, ok := ripJobIDFromContext(ctx); ok {
		return fmt.Sprintf("rip-job%d-%s.log", id, ts)
	}
	return fmt.Sprintf("rip-%s-t%d-%s.log", sanitizeForFilename(target), titleID, ts)
}

// sanitizeForFilename reduces a string to characters safe in a filename,
// replacing anything else with '-' (so "disc:1" becomes "disc-1").
func sanitizeForFilename(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	return b.String()
}

// saveDebugLog copies makemkvcon's debug log from src to dst, creating dst's
// parent directory.
//
// The temp HOME that holds src is removed the moment StartRip returns (see the
// deferred cleanup there), so a failed rip's log has to be copied out before
// then or it is gone. Unlike tailLines, this keeps the whole file — the tail is
// for showing the reason at a glance, the file is for reading the rest.
func saveDebugLog(src, dst string) error {
	if src == "" {
		return errors.New("makemkv: no debug log path to save")
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
