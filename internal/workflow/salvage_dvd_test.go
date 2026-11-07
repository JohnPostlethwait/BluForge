package workflow

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/johnpostlethwait/bluforge/internal/makemkv"
)

// dvdFixture builds a mounted-DVD tree and a file standing in for the raw
// device, returning both.
func dvdFixture(t *testing.T, size int) (root, device string) {
	t.Helper()
	root = t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "VIDEO_TS"), 0o777); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "VIDEO_TS", "VTS_01_4.VOB"), make([]byte, 16), 0o644); err != nil {
		t.Fatalf("write vob: %v", err)
	}
	device = filepath.Join(t.TempDir(), "sr1")
	if err := os.WriteFile(device, make([]byte, size), 0o644); err != nil {
		t.Fatalf("write device: %v", err)
	}
	return root, device
}

// Frisky Dingo: a new DVD with an unreadable patch inside one episode. MakeMKV's
// backup cannot copy a DVD, and the Blu-ray path refused it for lacking
// BDMV/STREAM. The DVD is imaged whole from the device instead, and the image
// is what gets scanned and ripped.
func TestSalvageImagesADVDWhole(t *testing.T) {
	root, device := dvdFixture(t, 4096)
	b := &salvageBackupper{titles: 8}
	r := &fakeRescuer{size: 4096, badBytes: 168000}
	orch, outputDir := salvageOrchestrator(t, root, b, r)
	orch.holder.(*rootHolder).device = device

	rec, err := orch.Salvage(context.Background(), SalvageRequest{
		DriveIndex: 1, DevicePath: device, DiscLabel: "FRISKY_DINGO_SEASON_1", OutputDir: outputDir,
	})
	if err != nil {
		t.Fatalf("Salvage: %v", err)
	}

	image := filepath.Join(rec.Dir, dvdImageName)
	if want := makemkv.ISOSource(image); rec.Source != want {
		t.Errorf("Source = %v, want %v", rec.Source, want)
	}
	if len(r.rescued) != 1 || r.rescued[0] != image {
		t.Errorf("rescued %v, want only the disc image %s", r.rescued, image)
	}
	if len(b.scanned) != 1 || b.scanned[0] != "iso:"+image {
		t.Errorf("scanned %v, want the disc image", b.scanned)
	}
	if _, err := os.Stat(filepath.Join(rec.Dir, "BDMV")); err == nil {
		t.Error("a Blu-ray backup ran against a DVD")
	}
	if !rec.Measured || rec.Unrecovered != 168000 {
		t.Errorf("Measured=%v Unrecovered=%d, want true and 168000", rec.Measured, rec.Unrecovered)
	}
	if got := copySource(rec.Dir); got != rec.Source {
		t.Errorf("copySource(%s) = %v, want the image a restart must rip from", rec.Dir, got)
	}
}

// A Blu-ray is never mistaken for a DVD, even one that also carries VIDEO_TS.
func TestDiscIsDVD(t *testing.T) {
	orch, _, _ := setupOrchestratorWithScanner(t, &mockDriveExecutor{})

	bd := discFixture(t, 16)
	hybrid := discFixture(t, 16)
	if err := os.MkdirAll(filepath.Join(hybrid, "VIDEO_TS"), 0o777); err != nil {
		t.Fatal(err)
	}
	dvd, _ := dvdFixture(t, 16)

	for name, tc := range map[string]struct {
		root string
		want bool
	}{"blu-ray": {bd, false}, "hybrid": {hybrid, false}, "dvd": {dvd, true}} {
		orch.holder = holderFor(func(string) (string, func(), error) { return tc.root, func() {}, nil })
		if got := orch.discIsDVD(context.Background(), 0); got != tc.want {
			t.Errorf("%s: discIsDVD = %v, want %v", name, got, tc.want)
		}
	}
}

// A copy with no image is a folder, as every Blu-ray copy is.
func TestCopySourceFolder(t *testing.T) {
	dir := t.TempDir()
	if got := copySource(dir); got != makemkv.CopySource(dir) {
		t.Errorf("copySource = %v, want the folder", got)
	}
}
