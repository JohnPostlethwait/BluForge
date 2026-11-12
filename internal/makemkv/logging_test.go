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
		if err := ex.Backup(context.Background(), discSrc(0), "/scratch/slug", nil); err != nil {
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

// Identifying a disc can take minutes while the drive's bridge resets. What
// makemkvcon said during it, timed as it said it, is what lines a reset up with
// the read that hung; parsing the listing and dropping the rest left nothing.
func TestIdentifyLogsMakeMKVMessages(t *testing.T) {
	runner := &streamingRunner{lines: []string{
		`MSG:1005,0,1,"MakeMKV started","%1","MakeMKV"`,
		`MSG:2003,0,1,"Error 'Scsi error - MEDIUM ERROR' occurred while reading '/VIDEO_TS/VTS_01_1.VOB'","%1","x"`,
		`DRV:0,2,999,1,"DVD+R-DL HL-DT-ST BD-RE BU40N","THE_WIRE_S2_D2","/dev/sr1"`,
	}}
	ex := NewExecutor(WithRunner(runner))

	var info DriveInfo
	out := captureLogs(t, func() {
		var err error
		info, err = ex.Identify(context.Background(), DeviceSource(1, "/dev/sr1"))
		if err != nil {
			t.Fatalf("Identify: %v", err)
		}
	})

	if info.DiscName != "THE_WIRE_S2_D2" || !info.HasDisc() {
		t.Errorf("identify = %+v, want THE_WIRE_S2_D2 inserted", info)
	}
	if !strings.Contains(out, "MEDIUM ERROR") || !strings.Contains(out, "phase=identify") {
		t.Errorf("identify did not log makemkvcon's error:\n%s", out)
	}
	if strings.Contains(out, "level=INFO msg=\"makemkvcon message\" phase=identify code=1005") {
		t.Errorf("routine start notice logged at INFO:\n%s", out)
	}
	// Two drives identify at once; a message that does not say whose it is
	// was read as the wrong drive's.
	if !strings.Contains(out, "device=/dev/sr1") {
		t.Errorf("identify messages do not name the drive:\n%s", out)
	}
}

// Every listing asks for disc 9999, which does not exist, and makemkvcon says
// "Failed to open disc" about it each time. During identify that is routine;
// during a rip it is the failure.
func TestFailedToOpenDiscIsRoutineOnlyDuringIdentify(t *testing.T) {
	out := captureLogs(t, func() {
		logMakeMKVEvent(makeMsgEvent(5010, "Failed to open disc"), "identify")
		logMakeMKVEvent(makeMsgEvent(5010, "Failed to open disc"), "rip")
	})
	if !strings.Contains(out, "level=DEBUG msg=\"makemkvcon message\" phase=identify code=5010") {
		t.Errorf("5010 during identify is not DEBUG:\n%s", out)
	}
	if !strings.Contains(out, "level=INFO msg=\"makemkvcon message\" phase=rip code=5010") {
		t.Errorf("5010 during a rip is not INFO:\n%s", out)
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

// The rip of Toy Story 4 dumped ~80 "title too short, skipped" lines into the
// log at INFO, twice, burying the two lines that mattered. Routine per-title
// chatter belongs at DEBUG: if it fires dozens of times in a normal run it does
// not earn INFO. Only genuinely notable messages stay at INFO.
func TestRoutineMakeMKVChatterIsDebugNotInfo(t *testing.T) {
	c := captureRecords(t, slog.LevelDebug, func() {
		logMakeMKVEvent(makeMsgEvent(3025, "Title #00019.m2ts has length of 8 seconds ... was therefore skipped"), "rip")
		logMakeMKVEvent(makeMsgEvent(3307, "File 00800.mpls was added as title #3"), "rip")
		logMakeMKVEvent(makeMsgEvent(3309, "Title 00004.mpls is equal to title 00800.mpls and was skipped"), "rip")
		logMakeMKVEvent(makeMsgEvent(5074, "Automatic checking for updates is enabled"), "rip")
	})

	if got := c.messagesAt(slog.LevelInfo); len(got) != 0 {
		t.Errorf("routine chatter reached INFO, want none: %d records", len(got))
	}
	if got := c.messagesAt(slog.LevelDebug); len(got) != 4 {
		t.Errorf("routine chatter at DEBUG = %d records, want all 4", len(got))
	}
}

// A message that is not routine — an error, a warning, an uncatalogued code —
// is what INFO is for. It must not be demoted into the DEBUG stream where a
// default-level log would never show it.
func TestNotableMakeMKVMessagesStayAtInfo(t *testing.T) {
	c := captureRecords(t, slog.LevelDebug, func() {
		logMakeMKVEvent(makeMsgEvent(5010, "Failed to open disc"), "rip")
	})

	if got := c.messagesAt(slog.LevelInfo); len(got) != 1 {
		t.Errorf("a notable message was not at INFO: %d INFO records", len(got))
	}
	if got := c.messagesAt(slog.LevelDebug); len(got) != 0 {
		t.Errorf("a notable message was demoted to DEBUG: %d DEBUG records", len(got))
	}
}
