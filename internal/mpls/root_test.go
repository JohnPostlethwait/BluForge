package mpls

import (
	"os"
	"path/filepath"
	"testing"
)

// ReadFrom reads playlists from a disc root that is already accessible as a
// directory. A backup folder produced by `makemkvcon backup` is exactly that,
// so a recovered disc gets language enrichment with no mount involved.
func TestReadFromDiscRoot(t *testing.T) {
	root := t.TempDir()
	playlistDir := filepath.Join(root, "BDMV", "PLAYLIST")
	if err := os.MkdirAll(playlistDir, 0o777); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Outer slice is per PlayItem; inner is the languages within it. ReadFrom
	// reports the first PlayItem, so both audio tracks belong in one entry.
	data := buildMPLS(t, [][]string{{"eng", "fra"}}, [][]string{{"eng"}})
	if err := os.WriteFile(filepath.Join(playlistDir, "00800.mpls"), data, 0o666); err != nil {
		t.Fatalf("write mpls: %v", err)
	}

	got, err := ReadFrom(root, nil)
	if err != nil {
		t.Fatalf("ReadFrom: %v", err)
	}

	pl, ok := got["00800.mpls"]
	if !ok {
		t.Fatalf("playlist 00800.mpls missing from result (got %d entries)", len(got))
	}
	if len(pl.Audio) != 2 {
		t.Errorf("audio streams = %d, want 2", len(pl.Audio))
	}
	if len(pl.Subtitle) != 1 {
		t.Errorf("subtitle streams = %d, want 1", len(pl.Subtitle))
	}
}

func TestReadFromHonoursSourceFileFilter(t *testing.T) {
	root := t.TempDir()
	playlistDir := filepath.Join(root, "BDMV", "PLAYLIST")
	if err := os.MkdirAll(playlistDir, 0o777); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	wanted := buildMPLS(t, [][]string{{"eng"}}, [][]string{{"eng"}})
	other := buildMPLS(t, [][]string{{"jpn"}}, nil)
	if err := os.WriteFile(filepath.Join(playlistDir, "00100.mpls"), wanted, 0o666); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.WriteFile(filepath.Join(playlistDir, "00200.mpls"), other, 0o666); err != nil {
		t.Fatalf("write: %v", err)
	}

	got, err := ReadFrom(root, []string{"00100.mpls"})
	if err != nil {
		t.Fatalf("ReadFrom: %v", err)
	}

	if _, ok := got["00200.mpls"]; ok {
		t.Error("ReadFrom returned a playlist that was not requested")
	}
	if _, ok := got["00100.mpls"]; !ok {
		t.Error("ReadFrom did not return the requested playlist")
	}
}

func TestReadFromMissingPlaylistDir(t *testing.T) {
	if _, err := ReadFrom(t.TempDir(), nil); err == nil {
		t.Error("ReadFrom succeeded on a root with no PLAYLIST directory, want error")
	}
}
