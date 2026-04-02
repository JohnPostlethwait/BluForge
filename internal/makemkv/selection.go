package makemkv

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// SelectionOpts controls which audio and subtitle tracks MakeMKV selects when
// ripping a title.
type SelectionOpts struct {
	// AudioLangs is the list of ISO 639-2 language codes to include (e.g.
	// ["eng", "jpn"]). Empty means select all audio tracks.
	AudioLangs []string

	// SubtitleLangs is the list of ISO 639-2 language codes to include.
	// Empty means select all subtitle tracks.
	SubtitleLangs []string

	// KeepForced, when true, always includes forced subtitles regardless of the
	// SubtitleLangs filter.
	KeepForced bool

	// KeepLossless, when true, keeps lossless audio tracks even when a lossy
	// core is also present. When false, lossless tracks are dropped in favour of
	// the lossy core (saves significant disk space).
	KeepLossless bool
}

// IsEmpty returns true when opts would result in "select everything" — i.e. no
// language filtering is active and lossless audio is kept. In that case the
// HOME trick is unnecessary and can be skipped.
func (opts SelectionOpts) IsEmpty() bool {
	return len(opts.AudioLangs) == 0 && len(opts.SubtitleLangs) == 0 && opts.KeepLossless
}

// NewSelectionOpts builds a SelectionOpts from comma-separated language code
// strings (as stored in config or submitted by forms). Whitespace around each
// code is trimmed. Returns nil when no filtering would be applied (both lang
// strings empty), preserving backward-compatible "rip everything" behaviour.
func NewSelectionOpts(audioLangs, subtitleLangs string, keepForced, keepLossless bool) *SelectionOpts {
	if audioLangs == "" && subtitleLangs == "" {
		return nil
	}
	opts := &SelectionOpts{
		KeepForced:   keepForced,
		KeepLossless: keepLossless,
	}
	if audioLangs != "" {
		for _, code := range strings.Split(audioLangs, ",") {
			if c := strings.TrimSpace(code); c != "" {
				opts.AudioLangs = append(opts.AudioLangs, c)
			}
		}
	}
	if subtitleLangs != "" {
		for _, code := range strings.Split(subtitleLangs, ",") {
			if c := strings.TrimSpace(code); c != "" {
				opts.SubtitleLangs = append(opts.SubtitleLangs, c)
			}
		}
	}
	return opts
}

// BuildSelectionString constructs a MakeMKV app_DefaultSelectionString value
// from the given SelectionOpts. Rules are evaluated left-to-right by MakeMKV;
// the last matching rule wins.
//
// Example output (KeepLossless=false, AudioLangs=["eng","jpn"], SubtitleLangs=["eng"], KeepForced=true):
//
//	-sel:all,+sel:video,+sel:audio&(eng|jpn),+sel:audio&(nolang),-sel:audio&(havemulti),-sel:audio&(havelossless),+sel:subtitle&(eng),+sel:subtitle&(forced)
func BuildSelectionString(opts SelectionOpts) string {
	var parts []string

	// Start by deselecting everything; we add back what we want.
	parts = append(parts, "-sel:all")

	// Video is always kept.
	parts = append(parts, "+sel:video")

	// Audio selection.
	if len(opts.AudioLangs) > 0 {
		langExpr := strings.Join(opts.AudioLangs, "|")
		parts = append(parts, fmt.Sprintf("+sel:audio&(%s)", langExpr))
		// Also keep tracks that carry no language tag so we don't accidentally
		// drop commentary or descriptive tracks on discs with incomplete metadata.
		parts = append(parts, "+sel:audio&(nolang)")
	} else {
		// No language filter — keep everything for backward compatibility.
		parts = append(parts, "+sel:audio")
	}

	// Drop stereo/mono when a surround mix for the same language is present.
	// This avoids redundant downmix tracks.
	parts = append(parts, "-sel:audio&(havemulti)")

	// If the caller does not want lossless, drop lossless tracks when a lossy
	// core (e.g. AC3 core inside TrueHD) is available.
	if !opts.KeepLossless {
		parts = append(parts, "-sel:audio&(havelossless)")
	}

	// Subtitle selection.
	if len(opts.SubtitleLangs) > 0 {
		langExpr := strings.Join(opts.SubtitleLangs, "|")
		parts = append(parts, fmt.Sprintf("+sel:subtitle&(%s)", langExpr))
	} else {
		parts = append(parts, "+sel:subtitle")
	}

	// Forced subtitles are kept unconditionally when requested, regardless of
	// whether they match the subtitle language filter.
	if opts.KeepForced {
		parts = append(parts, "+sel:subtitle&(forced)")
	}

	return strings.Join(parts, ",")
}

// WriteTempHome creates a temporary HOME directory containing a MakeMKV
// settings.conf that encodes the given selectionString. The returned homeDir
// should be set as the HOME environment variable when invoking makemkvcon.
// cleanup removes the entire temporary directory tree and must be called when
// the rip has finished.
func WriteTempHome(selectionString string) (homeDir string, cleanup func(), err error) {
	dir, err := os.MkdirTemp("", "bluforge-makemkv-home-*")
	if err != nil {
		return "", nil, fmt.Errorf("makemkv: create temp home: %w", err)
	}

	cleanup = func() {
		// We intentionally use os.RemoveAll here because this is a controlled
		// temporary directory that we created and own.
		_ = os.RemoveAll(dir)
	}

	confDir := filepath.Join(dir, ".MakeMKV")
	if err := os.Mkdir(confDir, 0o700); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("makemkv: create .MakeMKV dir: %w", err)
	}

	confPath := filepath.Join(confDir, "settings.conf")
	content := fmt.Sprintf("app_DefaultSelectionString = %q\n", selectionString)
	if err := os.WriteFile(confPath, []byte(content), 0o600); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("makemkv: write settings.conf: %w", err)
	}

	return dir, cleanup, nil
}
