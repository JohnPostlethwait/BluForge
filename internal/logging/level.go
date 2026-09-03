// Package logging configures the application's log level.
//
// It sits outside internal/config deliberately. Configuration precedence in
// BluForge runs defaults → env → YAML, but config.Load logs while it works, so
// a level read from /config/config.yaml could not govern the lines emitted
// before the file had been read. The environment is available from the first
// instruction of main.
package logging

import (
	"io"
	"log/slog"
	"strings"
)

// LevelEnv is the environment variable that sets the log level.
const LevelEnv = "BLUFORGE_LOG_LEVEL"

// ParseLevel maps the value of LevelEnv to a level.
//
// The second return reports whether the value was understood. An empty string
// is understood: unset is how BluForge runs almost everywhere, and warning
// about it would make the default configuration complain about itself.
func ParseLevel(raw string) (slog.Level, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "":
		return slog.LevelInfo, true
	case "debug":
		return slog.LevelDebug, true
	case "info":
		return slog.LevelInfo, true
	case "warn", "warning":
		return slog.LevelWarn, true
	case "error":
		return slog.LevelError, true
	}
	return slog.LevelInfo, false
}

// Configure installs the default logger: JSON records written to w, filtered at
// the level named by raw. It returns a function restoring the previous logger,
// which the tests use and main ignores.
//
// A value that is not a level falls back to info and is reported at WARN,
// naming what was rejected. Silencing the application over a typo in a compose
// file is the one outcome this must not have.
func Configure(w io.Writer, raw string) (restore func()) {
	level, ok := ParseLevel(raw)

	// A LevelVar rather than a bare level: the handler holds a reference to it,
	// so the level stays swappable if a settings toggle ever wants to move it
	// while the process runs.
	current := new(slog.LevelVar)
	current.Set(level)

	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(w, &slog.HandlerOptions{Level: current})))

	if !ok {
		slog.Warn("log level not recognised, using info",
			"variable", LevelEnv, "value", raw,
			"accepted", []string{"debug", "info", "warn", "error"})
	}

	return func() { slog.SetDefault(prev) }
}
