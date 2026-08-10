package makemkv

import "log/slog"

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
	// 5042 is emitted on nearly every invocation and means nothing on its own;
	// it stays at debug so it cannot bury the messages that matter.
	if m.Code == MsgNoUsableDrives {
		slog.Debug("makemkvcon message", "phase", phase, "code", m.Code, "text", m.Text)
		return
	}
	slog.Info("makemkvcon message", "phase", phase, "code", m.Code, "text", m.Text)
}

// progressDecile decides whether a progress percentage is worth logging.
//
// Reporting every event would bury MakeMKV's messages under thousands of lines;
// reporting nothing leaves no way to tell a slow backup from a stalled one.
// Deciles give roughly one line every few minutes on a UHD backup.
//
// Returns whether to log, and the marker to carry forward.
func progressDecile(lastLogged, current int) (bool, int) {
	if current < 0 {
		return false, lastLogged
	}
	decile := (current / 10) * 10
	if current >= 100 {
		decile = 100
	}
	if decile <= lastLogged {
		return false, lastLogged
	}
	if lastLogged < 0 {
		return true, decile
	}
	return true, decile
}
