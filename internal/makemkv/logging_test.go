package makemkv

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"
)

// captureLogs redirects slog to a buffer for the duration of fn.
func captureLogs(t *testing.T, fn func()) string {
	t.Helper()
	var buf bytes.Buffer
	var mu sync.Mutex
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&syncWriter{w: &buf, mu: &mu}, &slog.HandlerOptions{Level: slog.LevelDebug})))
	defer slog.SetDefault(prev)
	fn()
	mu.Lock()
	defer mu.Unlock()
	return buf.String()
}

type syncWriter struct {
	w  *bytes.Buffer
	mu *sync.Mutex
}

func (s *syncWriter) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w.Write(p)
}

// A backup runs unattended for tens of minutes inside a container. MakeMKV's own
// messages are the only account of what it did; parsing them and keeping them to
// ourselves leaves the log with a start line, silence, and possibly a failure.
func TestBackupLogsMakeMKVMessages(t *testing.T) {
	runner := &recordingRunner{output: `MSG:5085,0,0,"Loaded content hash table, will verify integrity","%1","Loaded content hash table"
MSG:3307,0,0,"File 00800.m2ts was added as title 0","%1","File 00800.m2ts was added as title 0"
PRGV:100,100,100
`}
	ex := NewExecutor(WithRunner(runner))

	out := captureLogs(t, func() {
		if err := ex.Backup(context.Background(), 0, "/scratch/slug", nil); err != nil {
			t.Fatalf("Backup: %v", err)
		}
	})

	if !strings.Contains(out, "5085") {
		t.Errorf("backup log does not carry MakeMKV message 5085:\n%s", out)
	}
	if !strings.Contains(out, "content hash table") {
		t.Errorf("backup log does not carry the message text:\n%s", out)
	}
}

// A rip from a stripped backup folder is the least-verified part of recovery.
// If MakeMKV objects to the folder source, its complaint has to be in the log.
func TestRipLogsMakeMKVMessages(t *testing.T) {
	out := captureLogs(t, func() {
		logMakeMKVEvent(makeMsgEvent(5010, "Failed to open disc"), "rip")
	})
	if !strings.Contains(out, "Failed to open disc") {
		t.Errorf("message text missing from log:\n%s", out)
	}
	if !strings.Contains(out, "5010") {
		t.Errorf("message code missing from log:\n%s", out)
	}
}

func makeMsgEvent(code int, text string) Event {
	return Event{Type: "MSG", Message: &Message{Code: code, Text: text}}
}

// Progress is logged at deciles: often enough to show a stalled backup, rare
// enough not to bury the messages that matter under thousands of lines.
func TestProgressDecileThrottle(t *testing.T) {
	tests := []struct {
		name          string
		lastLogged    int
		current       int
		wantLog       bool
		wantNewMarker int
	}{
		{"first progress", -1, 0, true, 0},
		{"same decile", 10, 13, false, 10},
		{"next decile", 10, 20, true, 20},
		{"skips a decile", 10, 35, true, 30},
		{"completion", 90, 100, true, 100},
		{"backwards", 50, 20, false, 50},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotLog, gotMarker := progressDecile(tt.lastLogged, tt.current)
			if gotLog != tt.wantLog {
				t.Errorf("shouldLog = %v, want %v", gotLog, tt.wantLog)
			}
			if gotMarker != tt.wantNewMarker {
				t.Errorf("marker = %d, want %d", gotMarker, tt.wantNewMarker)
			}
		})
	}
}
