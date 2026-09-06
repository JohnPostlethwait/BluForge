package makemkv

import (
	"io"
	"os"
	"strings"
)

// makemkvcon writes the reason for a fatal exit to a debug log, not to the
// robot message stream (--robot structures stdout but omits debug output;
// --debug sends it to a file). So a rip that dies with a nonzero code leaves
// nothing on stdout to explain it — the explanation is in the debug file, which
// is why BluForge now asks for one and reads its tail on failure.

// debugTailLines is how many trailing debug lines a failure surfaces. The fatal
// reason sits at the end of the log; enough to carry it and a little context,
// not the whole verbose trace.
const debugTailLines = 40

// debugTailBytes bounds how much of the log's end is read. Debug logs can run to
// many megabytes on a long rip; the reason is at the very end, so the tail is
// all that is needed and reading the whole file is avoided.
const debugTailBytes = 64 * 1024

// newDebugLog creates an empty file for makemkvcon's --debug output and returns
// its path plus a cleanup. cleanup is always safe to call, including when
// creation failed (path is then empty).
func newDebugLog() (path string, cleanup func()) {
	f, err := os.CreateTemp("", "bluforge-makemkv-debug-*.log")
	if err != nil {
		return "", func() {}
	}
	p := f.Name()
	_ = f.Close()
	return p, func() { _ = os.Remove(p) }
}

// tailLines returns up to maxLines non-blank lines from the end of the file at
// path, or nil when there is nothing to read. Only the last debugTailBytes are
// examined, so a huge log costs a small bounded read.
func tailLines(path string, maxLines int) []string {
	if path == "" {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		return nil
	}

	off := int64(0)
	if fi.Size() > debugTailBytes {
		off = fi.Size() - debugTailBytes
	}
	buf := make([]byte, fi.Size()-off)
	if _, err := f.ReadAt(buf, off); err != nil && err != io.EOF {
		return nil
	}

	raw := strings.Split(string(buf), "\n")
	// A mid-line start (we seeked into the file) leaves a partial first line;
	// drop it so we never show half a line.
	if off > 0 && len(raw) > 0 {
		raw = raw[1:]
	}

	var lines []string
	for _, l := range raw {
		if strings.TrimSpace(l) != "" {
			lines = append(lines, l)
		}
	}
	if len(lines) > maxLines {
		lines = lines[len(lines)-maxLines:]
	}
	return lines
}
