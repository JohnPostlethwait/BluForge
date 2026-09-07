package makemkv

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// saveDebugLog exists because the temp HOME holding makemkvcon's log is deleted
// the moment a rip returns. It has to copy the whole file — the tail is for a
// glance, the file is for reading the rest — and create the destination dir,
// which will not exist on the first failure.
func TestSaveDebugLogCopiesTheWholeFileIntoANewDir(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "MakeMKV_log.txt")
	const body = "line one\nthe fatal reason\nline three\n"
	if err := os.WriteFile(src, []byte(body), 0o600); err != nil {
		t.Fatalf("write src: %v", err)
	}

	dst := filepath.Join(dir, "logs", "sub", "rip-job7-x.log")
	if err := saveDebugLog(src, dst); err != nil {
		t.Fatalf("saveDebugLog: %v", err)
	}

	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("read dst: %v", err)
	}
	if string(got) != body {
		t.Errorf("saved %q, want the whole source %q", got, body)
	}
}

// An empty source path is the "no announce and no HOME fallback" case; saving
// must fail rather than create an empty file that looks like a captured log.
func TestSaveDebugLogRefusesAnEmptySource(t *testing.T) {
	if err := saveDebugLog("", filepath.Join(t.TempDir(), "out.log")); err == nil {
		t.Error("saving with no source path was allowed; it should error")
	}
}

// The job ID lines the saved file up with the activity page and the "rip:
// starting" log line, so it is used whenever the context carries it.
func TestFailureLogNameUsesTheJobIDWhenPresent(t *testing.T) {
	ctx := WithRipJobID(context.Background(), 1789)
	name := failureLogName(ctx, "disc:1", 5)
	if !strings.HasPrefix(name, "rip-job1789-") || !strings.HasSuffix(name, ".log") {
		t.Errorf("name = %q, want rip-job1789-<ts>.log", name)
	}
}

// Without a job ID the disc and title still have to identify the failure, and
// the disc's colon must not leak into the filename.
func TestFailureLogNameFallsBackToDiscAndTitle(t *testing.T) {
	name := failureLogName(context.Background(), "disc:1", 5)
	if strings.Contains(name, ":") {
		t.Errorf("name %q contains a colon; it must be filename-safe", name)
	}
	if !strings.HasPrefix(name, "rip-disc-1-t5-") {
		t.Errorf("name = %q, want rip-disc-1-t5-<ts>.log", name)
	}
}
