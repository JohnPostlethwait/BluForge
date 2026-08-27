package mpls

import (
	"os"
	"path/filepath"
	"testing"
)

// discRoot builds a disc tree with the given playlists, each written with a
// distinct size so the fingerprint has something to read.
func discRoot(t *testing.T, sub string, playlists map[string]int) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, sub)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	for name, size := range playlists {
		if err := os.WriteFile(filepath.Join(dir, name), make([]byte, size), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return root
}

func bdmv(t *testing.T, playlists map[string]int) string {
	t.Helper()
	return discRoot(t, filepath.Join("BDMV", "PLAYLIST"), playlists)
}

// The point of this fingerprint: a repaired copy stays bound to a drive, and a
// two-disc set can ship both discs under one volume label. Comparing the disc
// in the drive against the copy is what tells them apart — and unlike a
// MakeMKV scan, reading the playlist directory works on a disc whose AACS
// directory is what MakeMKV chokes on.
func TestPlaylistFingerprintDistinguishesTwoDiscs(t *testing.T) {
	main := bdmv(t, map[string]int{"00800.mpls": 512, "00801.mpls": 256})
	bonus := bdmv(t, map[string]int{"00010.mpls": 300, "00011.mpls": 128})

	if PlaylistFingerprint(main) == PlaylistFingerprint(bonus) {
		t.Error("two different discs fingerprinted the same")
	}
}

func TestPlaylistFingerprintMatchesACopyOfTheSameDisc(t *testing.T) {
	disc := bdmv(t, map[string]int{"00800.mpls": 512, "00801.mpls": 256})
	copyOfDisc := bdmv(t, map[string]int{"00800.mpls": 512, "00801.mpls": 256})

	if PlaylistFingerprint(disc) != PlaylistFingerprint(copyOfDisc) {
		t.Error("a copy of a disc fingerprinted differently from the disc")
	}
}

// Same names, different content — a main feature and its extended cut both ship
// 00800.mpls. Size is what separates them.
func TestPlaylistFingerprintNoticesDifferentContent(t *testing.T) {
	a := bdmv(t, map[string]int{"00800.mpls": 512})
	b := bdmv(t, map[string]int{"00800.mpls": 900})

	if PlaylistFingerprint(a) == PlaylistFingerprint(b) {
		t.Error("playlists of differing size fingerprinted the same")
	}
}

// A tree with no playlist directory says nothing about which disc it is.
// Returning a constant would make every unreadable disc look like the same one.
func TestPlaylistFingerprintIsEmptyWithoutPlaylists(t *testing.T) {
	if got := PlaylistFingerprint(t.TempDir()); got != "" {
		t.Errorf("fingerprint of a tree with no BDMV = %q, want \"\"", got)
	}
	if got := PlaylistFingerprint(bdmv(t, map[string]int{})); got != "" {
		t.Errorf("fingerprint of an empty PLAYLIST directory = %q, want \"\"", got)
	}
}

// UHD discs are the reason: their primary PLAYLIST directory is sometimes the
// only readable one, and sometimes only BACKUP is. Either identifies the disc.
func TestPlaylistFingerprintFallsBackToBackup(t *testing.T) {
	backupOnly := discRoot(t, filepath.Join("BDMV", "BACKUP", "PLAYLIST"),
		map[string]int{"00800.mpls": 512})

	if PlaylistFingerprint(backupOnly) == "" {
		t.Error("a disc with only a BACKUP playlist directory could not be fingerprinted")
	}
}
