package makemkv

import (
	"context"
	"log/slog"
	"time"
)

// logMakeMKVEvent writes MakeMKV's own messages to the application log.
//
// These are the only running account of what makemkvcon did. A backup or a rip
// runs unattended for tens of minutes inside a container; without this the log
// shows a start line, a long silence, and possibly a failure with no context —
// while the explanation was parsed and discarded.
//
// phase names the operation ("backup", "rip") so the two are separable when a
// rip follows a recovery in the same log.
func logMakeMKVEvent(ev Event, phase string) {
	if ev.Type != "MSG" || ev.Message == nil {
		return
	}
	m := ev.Message
	// Routine per-title chatter — the title-too-short skips, the enumeration,
	// the update-check notice — is DEBUG. A rip emits dozens to hundreds of
	// these in a healthy run; at INFO they bury the handful of lines that carry
	// meaning. Anything not catalogued as routine (an error, a warning, a code
	// nobody has classified) stays at INFO, where a default-level log shows it.
	level := slog.LevelInfo
	if routineScanMessages[m.Code] {
		level = slog.LevelDebug
	}
	slog.Log(context.Background(), level, "makemkvcon message", "phase", phase, "code", m.Code, "text", m.Text)
}

// progressHeartbeat is the longest a long-running operation may go without a
// log line. Deciles alone are not enough: a backup can sit inside one decile
// for many minutes, and silence there is indistinguishable from a hang.
const progressHeartbeat = 2 * time.Minute

// phaseRestartDrop is how far progress must fall to count as a new phase rather
// than jitter. makemkvcon reports separate progress for preliminary work, so a
// run legitimately goes 0 → 100 → 0 before the real copy starts.
const phaseRestartDrop = 5

// progressTracker decides when a progress percentage is worth logging.
//
// Reporting every event would bury MakeMKV's own messages under thousands of
// lines. Reporting only on rising deciles looks right until a real backup does
// what it actually does: report 100% for a preliminary phase within the first
// second, after which a monotonic tracker never logs again — silent for the
// entire copy it exists to monitor.
type progressTracker struct {
	lastDecile int
	lastAt     time.Time
	started    bool
}

func newProgressTracker() *progressTracker {
	return &progressTracker{lastDecile: -1}
}

func (p *progressTracker) shouldLog(pct int, now time.Time) bool {
	if pct < 0 {
		return false
	}

	decile := (pct / 10) * 10
	if pct >= 100 {
		decile = 100
	}

	switch {
	case !p.started:
	case decile > p.lastDecile:
	case decile <= p.lastDecile-phaseRestartDrop:
		// A new phase began; start reporting it from scratch.
	case now.Sub(p.lastAt) >= progressHeartbeat:
		// Still inside the same decile, but long enough that silence would be
		// ambiguous.
	default:
		return false
	}

	p.started = true
	p.lastDecile = decile
	p.lastAt = now
	return true
}
