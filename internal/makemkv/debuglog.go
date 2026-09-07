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

// debugAnnouncePrefix is the start of the line makemkvcon prints to say where it
// put its debug log. makemkvcon ignores the path passed to --debug and chooses
// its own ($HOME/MakeMKV_log.txt), so we take the path from this announcement
// rather than assume it.
const debugAnnouncePrefix = "Debug logging enabled, log will be saved as "

// parseDebugLogPath extracts the debug log's path from makemkvcon's announce
// line, stripping the file:// URL prefix. Returns false for any other line.
func parseDebugLogPath(line string) (string, bool) {
	if !strings.HasPrefix(line, debugAnnouncePrefix) {
		return "", false
	}
	p := strings.TrimSpace(strings.TrimPrefix(line, debugAnnouncePrefix))
	p = strings.TrimPrefix(p, "file://")
	if p == "" {
		return "", false
	}
	return p, true
}

// isDebugNoise reports lines that --debug adds to stdout but that mean nothing
// to a person: the announce line, and the obfuscated "DEBUG: Code N at <hash>"
// markers makemkvcon scrambles for its own support. The real reason lives in the
// log file, so these are dropped from the failure capture rather than shown.
func isDebugNoise(line string) bool {
	return strings.HasPrefix(line, debugAnnouncePrefix) || strings.HasPrefix(line, "DEBUG:")
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
