package makemkv

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---- BuildSelectionString ----------------------------------------------------

func TestBuildSelectionString_EmptyOpts(t *testing.T) {
	// Zero-value opts: no language filtering, KeepLossless defaults to false.
	// Result should select all tracks but still drop lossless when a core exists.
	opts := SelectionOpts{}
	got := BuildSelectionString(opts)

	mustContain(t, got, "-sel:all")
	mustContain(t, got, "+sel:video")
	mustContain(t, got, "+sel:audio")
	mustContain(t, got, "+sel:subtitle")

	// When AudioLangs is empty we should get the bare "+sel:audio" rule, not a
	// language-filtered one.
	if strings.Contains(got, "+sel:audio&(") {
		t.Errorf("empty AudioLangs should not produce a language-filtered audio rule; got %q", got)
	}
	// KeepLossless=false (default) → havelossless deselect rule SHOULD be present.
	mustContain(t, got, "-sel:audio&(havelossless)")
}

func TestBuildSelectionString_SingleAudioLang(t *testing.T) {
	opts := SelectionOpts{AudioLangs: []string{"eng"}}
	got := BuildSelectionString(opts)

	mustContain(t, got, "+sel:audio&(eng)")
	mustContain(t, got, "+sel:audio&(nolang)")
}

func TestBuildSelectionString_MultipleAudioLangs(t *testing.T) {
	opts := SelectionOpts{AudioLangs: []string{"eng", "jpn", "spa"}}
	got := BuildSelectionString(opts)

	mustContain(t, got, "+sel:audio&(eng|jpn|spa)")
	mustContain(t, got, "+sel:audio&(nolang)")
}

func TestBuildSelectionString_SubtitleLangs(t *testing.T) {
	opts := SelectionOpts{SubtitleLangs: []string{"eng", "fra"}}
	got := BuildSelectionString(opts)

	mustContain(t, got, "+sel:subtitle&(eng|fra)")
	// Should not contain the bare "+sel:subtitle" rule when langs are specified.
	parts := strings.Split(got, ",")
	for _, p := range parts {
		if p == "+sel:subtitle" {
			t.Errorf("expected no bare +sel:subtitle when SubtitleLangs is set; got %q in %q", p, got)
		}
	}
}

func TestBuildSelectionString_KeepForcedTrue(t *testing.T) {
	opts := SelectionOpts{
		SubtitleLangs: []string{"eng"},
		KeepForced:    true,
	}
	got := BuildSelectionString(opts)

	mustContain(t, got, "+sel:subtitle&(forced)")
}

func TestBuildSelectionString_KeepForcedFalse(t *testing.T) {
	opts := SelectionOpts{
		SubtitleLangs: []string{"eng"},
		KeepForced:    false,
	}
	got := BuildSelectionString(opts)

	if strings.Contains(got, "+sel:subtitle&(forced)") {
		t.Errorf("expected no forced subtitle rule when KeepForced=false; got %q", got)
	}
}

func TestBuildSelectionString_KeepLosslessFalse(t *testing.T) {
	opts := SelectionOpts{KeepLossless: false}
	got := BuildSelectionString(opts)

	mustContain(t, got, "-sel:audio&(havelossless)")
}

func TestBuildSelectionString_KeepLosslessTrue(t *testing.T) {
	opts := SelectionOpts{KeepLossless: true}
	got := BuildSelectionString(opts)

	if strings.Contains(got, "havelossless") {
		t.Errorf("expected no havelossless rule when KeepLossless=true; got %q", got)
	}
}

func TestBuildSelectionString_HavemultiAlwaysPresent(t *testing.T) {
	// The havemulti deselect rule should always appear regardless of other opts.
	for _, opts := range []SelectionOpts{
		{},
		{AudioLangs: []string{"eng"}},
		{KeepLossless: true},
	} {
		got := BuildSelectionString(opts)
		mustContain(t, got, "-sel:audio&(havemulti)")
	}
}

func TestBuildSelectionString_FullCombination(t *testing.T) {
	opts := SelectionOpts{
		AudioLangs:    []string{"eng", "jpn"},
		SubtitleLangs: []string{"eng"},
		KeepForced:    true,
		KeepLossless:  false,
	}
	got := BuildSelectionString(opts)

	want := []string{
		"-sel:all",
		"+sel:video",
		"+sel:audio&(eng|jpn)",
		"+sel:audio&(nolang)",
		"-sel:audio&(havemulti)",
		"-sel:audio&(havelossless)",
		"+sel:subtitle&(eng)",
		"+sel:subtitle&(forced)",
	}
	for _, w := range want {
		mustContain(t, got, w)
	}

	// Verify left-to-right ordering matches the expected rule sequence.
	assertOrder(t, got, want)
}

func TestBuildSelectionString_FullCombinationKeepLossless(t *testing.T) {
	opts := SelectionOpts{
		AudioLangs:    []string{"eng", "jpn"},
		SubtitleLangs: []string{"eng"},
		KeepForced:    false,
		KeepLossless:  true,
	}
	got := BuildSelectionString(opts)

	if strings.Contains(got, "havelossless") {
		t.Errorf("expected no havelossless rule when KeepLossless=true; got %q", got)
	}
	mustContain(t, got, "+sel:audio&(eng|jpn)")
	mustContain(t, got, "+sel:subtitle&(eng)")
	if strings.Contains(got, "+sel:subtitle&(forced)") {
		t.Errorf("expected no forced subtitle rule when KeepForced=false; got %q", got)
	}
}

// ---- SelectionOpts.IsEmpty ---------------------------------------------------

func TestIsEmpty(t *testing.T) {
	cases := []struct {
		name string
		opts SelectionOpts
		want bool
	}{
		{
			name: "zero value (KeepLossless defaults false) → not empty",
			opts: SelectionOpts{},
			want: false,
		},
		{
			name: "keep lossless, no lang filters → empty",
			opts: SelectionOpts{KeepLossless: true},
			want: true,
		},
		{
			name: "audio lang set → not empty",
			opts: SelectionOpts{AudioLangs: []string{"eng"}, KeepLossless: true},
			want: false,
		},
		{
			name: "subtitle lang set → not empty",
			opts: SelectionOpts{SubtitleLangs: []string{"eng"}, KeepLossless: true},
			want: false,
		},
		{
			name: "audio and subtitle langs, keep lossless → not empty",
			opts: SelectionOpts{AudioLangs: []string{"eng"}, SubtitleLangs: []string{"eng"}, KeepLossless: true},
			want: false,
		},
		{
			name: "no langs but keep lossless false → not empty (filters lossless)",
			opts: SelectionOpts{KeepLossless: false},
			want: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.opts.IsEmpty()
			if got != tc.want {
				t.Errorf("IsEmpty() = %v, want %v", got, tc.want)
			}
		})
	}
}

// ---- WriteTempHome -----------------------------------------------------------

func TestWriteTempHome_CreatesDirectoryStructure(t *testing.T) {
	homeDir, cleanup, err := WriteTempHome("-sel:all,+sel:video,+sel:audio")
	if err != nil {
		t.Fatalf("WriteTempHome returned unexpected error: %v", err)
	}
	defer cleanup()

	// The returned homeDir must exist.
	if _, err := os.Stat(homeDir); os.IsNotExist(err) {
		t.Fatalf("homeDir %q does not exist", homeDir)
	}

	// .MakeMKV/ subdirectory must exist.
	confDir := filepath.Join(homeDir, ".MakeMKV")
	if _, err := os.Stat(confDir); os.IsNotExist(err) {
		t.Fatalf(".MakeMKV dir %q does not exist", confDir)
	}

	// settings.conf must exist.
	confPath := filepath.Join(confDir, "settings.conf")
	if _, err := os.Stat(confPath); os.IsNotExist(err) {
		t.Fatalf("settings.conf %q does not exist", confPath)
	}
}

func TestWriteTempHome_SettingsConfContent(t *testing.T) {
	selStr := "-sel:all,+sel:video,+sel:audio&(eng)"
	homeDir, cleanup, err := WriteTempHome(selStr)
	if err != nil {
		t.Fatalf("WriteTempHome returned unexpected error: %v", err)
	}
	defer cleanup()

	confPath := filepath.Join(homeDir, ".MakeMKV", "settings.conf")
	data, err := os.ReadFile(confPath)
	if err != nil {
		t.Fatalf("failed to read settings.conf: %v", err)
	}

	content := string(data)

	// The file must contain the key and the selection string quoted.
	if !strings.Contains(content, "app_DefaultSelectionString") {
		t.Errorf("settings.conf missing app_DefaultSelectionString key; got:\n%s", content)
	}
	if !strings.Contains(content, selStr) {
		t.Errorf("settings.conf missing selection string %q; got:\n%s", selStr, content)
	}
}

func TestWriteTempHome_CleanupRemovesDirectory(t *testing.T) {
	homeDir, cleanup, err := WriteTempHome("-sel:all,+sel:video")
	if err != nil {
		t.Fatalf("WriteTempHome returned unexpected error: %v", err)
	}

	// homeDir must exist before cleanup.
	if _, err := os.Stat(homeDir); os.IsNotExist(err) {
		t.Fatalf("homeDir %q should exist before cleanup", homeDir)
	}

	cleanup()

	// homeDir must be gone after cleanup.
	if _, err := os.Stat(homeDir); !os.IsNotExist(err) {
		t.Errorf("homeDir %q should not exist after cleanup", homeDir)
	}
}

// ---- helpers -----------------------------------------------------------------

// mustContain fails the test if substr is not found in s.
func mustContain(t *testing.T, s, substr string) {
	t.Helper()
	if !strings.Contains(s, substr) {
		t.Errorf("expected %q to contain %q", s, substr)
	}
}

// assertOrder verifies that the ordered elements in want appear in the same
// --- NewSelectionOpts tests ---

func TestNewSelectionOpts_BothEmpty(t *testing.T) {
	got := NewSelectionOpts("", "", true, true)
	if got != nil {
		t.Fatal("expected nil when both lang strings empty")
	}
}

func TestNewSelectionOpts_TrimsWhitespace(t *testing.T) {
	got := NewSelectionOpts("eng, jpn , fra", " eng , deu ", true, false)
	if got == nil {
		t.Fatal("expected non-nil")
	}
	wantAudio := []string{"eng", "jpn", "fra"}
	wantSub := []string{"eng", "deu"}
	if len(got.AudioLangs) != len(wantAudio) {
		t.Fatalf("AudioLangs: got %v, want %v", got.AudioLangs, wantAudio)
	}
	for i, code := range got.AudioLangs {
		if code != wantAudio[i] {
			t.Errorf("AudioLangs[%d]: got %q, want %q", i, code, wantAudio[i])
		}
	}
	if len(got.SubtitleLangs) != len(wantSub) {
		t.Fatalf("SubtitleLangs: got %v, want %v", got.SubtitleLangs, wantSub)
	}
	for i, code := range got.SubtitleLangs {
		if code != wantSub[i] {
			t.Errorf("SubtitleLangs[%d]: got %q, want %q", i, code, wantSub[i])
		}
	}
	if !got.KeepForced {
		t.Error("expected KeepForced=true")
	}
	if got.KeepLossless {
		t.Error("expected KeepLossless=false")
	}
}

func TestNewSelectionOpts_SkipsEmptyCodes(t *testing.T) {
	got := NewSelectionOpts("eng,,fra", "", true, true)
	if got == nil {
		t.Fatal("expected non-nil")
	}
	if len(got.AudioLangs) != 2 {
		t.Fatalf("expected 2 audio langs, got %v", got.AudioLangs)
	}
}

// left-to-right order within the comma-separated string s.
func assertOrder(t *testing.T, s string, want []string) {
	t.Helper()
	parts := strings.Split(s, ",")
	idx := 0
	for _, w := range want {
		found := false
		for idx < len(parts) {
			if parts[idx] == w {
				found = true
				idx++
				break
			}
			idx++
		}
		if !found {
			t.Errorf("rule %q not found in expected position; full string: %q", w, s)
		}
	}
}
