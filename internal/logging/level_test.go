package logging

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

func TestParseLevelAcceptsTheFourLevels(t *testing.T) {
	for raw, want := range map[string]slog.Level{
		"debug": slog.LevelDebug,
		"info":  slog.LevelInfo,
		"warn":  slog.LevelWarn,
		"error": slog.LevelError,
	} {
		got, ok := ParseLevel(raw)
		if !ok {
			t.Errorf("ParseLevel(%q) reported the value unrecognised", raw)
		}
		if got != want {
			t.Errorf("ParseLevel(%q) = %v, want %v", raw, got, want)
		}
	}
}

// A value is typed into a compose file by hand, where it picks up capitals and
// stray spaces on the way.
func TestParseLevelIgnoresCaseAndSurroundingSpace(t *testing.T) {
	for _, raw := range []string{"DEBUG", "Debug", " debug", "debug\n", "  DeBuG  "} {
		got, ok := ParseLevel(raw)
		if !ok || got != slog.LevelDebug {
			t.Errorf("ParseLevel(%q) = %v, %v; want debug, true", raw, got, ok)
		}
	}
}

// Unset is the ordinary case, not a mistake, and must not warn about itself.
func TestParseLevelTreatsUnsetAsInfo(t *testing.T) {
	got, ok := ParseLevel("")
	if !ok {
		t.Error("an unset level was reported as an unrecognised value")
	}
	if got != slog.LevelInfo {
		t.Errorf("ParseLevel(\"\") = %v, want info", got)
	}
}

// A typo must not silence the application. It falls back to info and says so.
func TestParseLevelRejectsAnUnknownValue(t *testing.T) {
	got, ok := ParseLevel("verbose")
	if ok {
		t.Error("ParseLevel accepted a value that is not a level")
	}
	if got != slog.LevelInfo {
		t.Errorf("ParseLevel(\"verbose\") = %v, want the info fallback", got)
	}
}

func TestConfigureAtDebugEmitsDebugRecords(t *testing.T) {
	var buf bytes.Buffer
	restore := Configure(&buf, "debug")
	defer restore()

	slog.Debug("the poll ran")

	if !strings.Contains(buf.String(), "the poll ran") {
		t.Errorf("debug record missing at debug level: %q", buf.String())
	}
}

func TestConfigureAtTheDefaultSuppressesDebugRecords(t *testing.T) {
	var buf bytes.Buffer
	restore := Configure(&buf, "")
	defer restore()

	slog.Debug("the poll ran")
	slog.Info("a disc was inserted")

	out := buf.String()
	if strings.Contains(out, "the poll ran") {
		t.Errorf("debug record printed at the default level: %q", out)
	}
	if !strings.Contains(out, "a disc was inserted") {
		t.Errorf("info record missing at the default level: %q", out)
	}
}

// The warning has to name the offending value. "Invalid log level" sends the
// reader to look for a value they have to guess at; the string they typed is
// the one thing that identifies the mistake.
func TestConfigureWarnsAboutAnUnknownValueAndNamesIt(t *testing.T) {
	var buf bytes.Buffer
	restore := Configure(&buf, "verbose")
	defer restore()

	out := buf.String()
	if !strings.Contains(out, "verbose") {
		t.Errorf("the warning does not name the value that was rejected: %q", out)
	}
	if !strings.Contains(out, "WARN") {
		t.Errorf("an unrecognised level was not reported at WARN: %q", out)
	}

	buf.Reset()
	slog.Debug("the poll ran")
	if strings.Contains(buf.String(), "the poll ran") {
		t.Errorf("an unrecognised level did not fall back to info: %q", buf.String())
	}
}
