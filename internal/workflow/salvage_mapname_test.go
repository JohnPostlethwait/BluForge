package workflow

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// mapRecordingRescuer notes which map file each rescue was given.
type mapRecordingRescuer struct {
	maps []string
}

func (m *mapRecordingRescuer) Run(_ context.Context, args []string, _ func(string)) error {
	// The last three arguments are source, destination and map file.
	mapFile := args[len(args)-1]
	dest := args[len(args)-2]
	m.maps = append(m.maps, mapFile)
	_ = os.MkdirAll(filepath.Dir(dest), 0o777)
	return os.WriteFile(dest, []byte("x"), 0o644)
}

// Every structural file on a Blu-ray has a duplicate under BACKUP/. Naming map
// files by basename gave both the same one: the second read the first's map,
// concluded it had already been rescued, copied nothing, and left a zero-byte
// file. It wiped every playlist, every BDJO and index.bdmv on a real disc, and
// MakeMKV then failed to read any of them.
func TestEachRescuedFileGetsItsOwnMap(t *testing.T) {
	root := t.TempDir()
	for _, rel := range []string{
		"BDMV/PLAYLIST/00800.mpls",
		"BDMV/BACKUP/PLAYLIST/00800.mpls",
		"BDMV/index.bdmv",
		"BDMV/BACKUP/index.bdmv",
	} {
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o777); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(full, make([]byte, 512), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}

	orch, _, outputDir := setupOrchestratorWithScanner(t, &mockDriveExecutor{})
	r := &mapRecordingRescuer{}
	orch.rescuer = r
	orch.outputDir = outputDir

	short, err := incompleteFiles(root, t.TempDir())
	if err != nil {
		t.Fatalf("incompleteFiles: %v", err)
	}
	if len(short) != 4 {
		t.Fatalf("found %d files short, want 4", len(short))
	}

	if _, err := orch.rescueStreams(context.Background(), SalvageRequest{},
		root, filepath.Join(outputDir, "scratch"), short,
		func(string, int, string) {}); err != nil {
		t.Fatalf("rescueStreams: %v", err)
	}

	seen := make(map[string]string)
	for i, m := range r.maps {
		if prev, dup := seen[m]; dup {
			t.Errorf("%s and %s share the map %s; the second would copy nothing",
				prev, short[i].name, m)
		}
		seen[m] = short[i].name
	}
	if len(seen) != 4 {
		t.Errorf("got %d distinct maps for 4 files: %v", len(seen), r.maps)
	}
}

// The maps live inside the scratch, so they must not sit among the disc's own
// files where a later comparison could mistake them for content.
func TestRescueMapsAreKeptApartFromDiscContent(t *testing.T) {
	if !isSalvageMap(filepath.Join(salvageMapDir, "BDMV", "index.bdmv.map")) {
		t.Error("a map file was not recognised as ours")
	}
	if isSalvageMap("BDMV/index.bdmv") {
		t.Error("disc content was mistaken for a map file")
	}
	if !strings.HasPrefix(salvageMapDir, ".") {
		t.Errorf("salvageMapDir = %q; a hidden directory keeps it out of media scans", salvageMapDir)
	}
}
