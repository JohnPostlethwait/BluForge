package organizer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestFileExists_Symlink(t *testing.T) {
	dir := t.TempDir()

	// Create a real file.
	realFile := filepath.Join(dir, "real.txt")
	if err := os.WriteFile(realFile, []byte("content"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Create a symlink pointing to the real file.
	symlink := filepath.Join(dir, "link.txt")
	if err := os.Symlink(realFile, symlink); err != nil {
		t.Fatalf("failed to create symlink: %v", err)
	}

	// The real file should exist.
	if !FileExists(realFile) {
		t.Error("FileExists should return true for a regular file")
	}

	// The symlink should be rejected.
	if FileExists(symlink) {
		t.Error("FileExists should return false for a symlink")
	}
}

func TestFileExists_NonExistent(t *testing.T) {
	if FileExists(filepath.Join(t.TempDir(), "no-such-file")) {
		t.Error("FileExists should return false for a non-existent path")
	}
}

func TestSanitizeFilename_UTF8Truncation(t *testing.T) {
	// Each CJK character is 3 bytes in UTF-8. Repeat enough to exceed 255 bytes.
	// 86 characters * 3 bytes = 258 bytes, which exceeds 255.
	base := strings.Repeat("日本語テスト", 15) // 6 chars * 3 bytes * 15 = 270 bytes
	got := SanitizeFilename(base)

	if len(got) > 255 {
		t.Errorf("result is %d bytes, want <= 255", len(got))
	}

	// Verify the result is valid UTF-8 (no split multi-byte characters).
	if !utf8.ValidString(got) {
		t.Error("result is not valid UTF-8; truncation split a multi-byte character")
	}

	// Verify we used as much space as possible (next rune wouldn't fit).
	// 255 / 3 = 85 runes, 85 * 3 = 255 bytes exactly.
	if len(got) != 255 {
		t.Errorf("result is %d bytes, expected 255 (should fit exactly 85 3-byte runes)", len(got))
	}
}

func TestSanitizeFilename_UTF8Truncation_4Byte(t *testing.T) {
	// Use 4-byte emoji characters. 64 emojis = 256 bytes, exceeds 255.
	base := strings.Repeat("\U0001F600", 64) // each is 4 bytes
	got := SanitizeFilename(base)

	if len(got) > 255 {
		t.Errorf("result is %d bytes, want <= 255", len(got))
	}

	if !utf8.ValidString(got) {
		t.Error("result is not valid UTF-8; truncation split a multi-byte character")
	}

	// 255 / 4 = 63 runes, 63 * 4 = 252 bytes (next rune wouldn't fit).
	if len(got) != 252 {
		t.Errorf("result is %d bytes, expected 252 (63 4-byte runes)", len(got))
	}
}

func TestSanitizeFilename_ColonReplacement(t *testing.T) {
	got := SanitizeFilename("Movie: The Sequel")
	want := "Movie - The Sequel"
	if got != want {
		t.Errorf("SanitizeFilename(%q) = %q, want %q", "Movie: The Sequel", got, want)
	}
}

func TestSanitizeFilename_QuoteReplacement(t *testing.T) {
	got := SanitizeFilename(`He said "hello"`)
	want := "He said 'hello'"
	if got != want {
		t.Errorf("SanitizeFilename(%q) = %q, want %q", `He said "hello"`, got, want)
	}
}
