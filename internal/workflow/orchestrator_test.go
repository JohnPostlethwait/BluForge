package workflow

import (
	"context"
	"strings"
	"testing"

	"github.com/johnpostlethwait/bluforge/internal/db"
	"github.com/johnpostlethwait/bluforge/internal/discdb"
	"github.com/johnpostlethwait/bluforge/internal/makemkv"
)

func TestRescan(t *testing.T) {
	scanner := &mockDriveExecutor{}
	orch, store, _ := setupOrchestratorWithScanner(t, scanner)

	scan, _ := scanner.ScanDisc(context.Background(), 0)
	discKey := discdb.BuildDiscKey(scan)

	err := store.SaveMapping(db.DiscMapping{
		DiscKey:     discKey,
		DiscName:    "DEADPOOL_2",
		MediaItemID: "item-dp2",
		ReleaseID:   "rel-dp2",
		MediaTitle:  "Deadpool 2",
		MediaYear:   "2018",
		MediaType:   "movie",
	})
	if err != nil {
		t.Fatalf("SaveMapping: %v", err)
	}

	mapping, err := store.GetMapping(discKey)
	if err != nil {
		t.Fatalf("GetMapping: %v", err)
	}
	if mapping == nil {
		t.Fatal("expected mapping to exist before rescan")
	}

	if err := orch.Rescan(context.Background(), 0); err != nil {
		t.Fatalf("Rescan: %v", err)
	}

	mapping, err = store.GetMapping(discKey)
	if err != nil {
		t.Fatalf("GetMapping after rescan: %v", err)
	}
	if mapping != nil {
		t.Error("expected mapping to be deleted after rescan")
	}
}

func TestScanDisc_CachesResult(t *testing.T) {
	orch, _, _ := setupOrchestratorWithScanner(t, &mockDriveExecutor{})

	if _, err := orch.ScanDisc(context.Background(), 0); err != nil {
		t.Fatalf("ScanDisc: %v", err)
	}

	cached := orch.CachedScan(0, "DEADPOOL_2")
	if cached == nil {
		t.Fatal("expected cached scan to be non-nil")
	}
	if cached.DiscName != "DEADPOOL_2" {
		t.Errorf("expected DiscName 'DEADPOOL_2', got %q", cached.DiscName)
	}
}

func TestCachedScan_Miss(t *testing.T) {
	orch, _, _ := setupOrchestratorWithScanner(t, &mockDriveExecutor{})

	cached := orch.CachedScan(5, "NONEXISTENT")
	if cached != nil {
		t.Errorf("expected nil for cache miss, got %+v", cached)
	}
}

func TestGetCachedScanByDrive(t *testing.T) {
	orch, _, _ := setupOrchestratorWithScanner(t, &mockDriveExecutor{})

	if _, err := orch.ScanDisc(context.Background(), 0); err != nil {
		t.Fatalf("ScanDisc: %v", err)
	}

	cached := orch.GetCachedScanByDrive(0)
	if cached == nil {
		t.Fatal("expected cached scan to be non-nil")
	}
	if cached.DiscName != "DEADPOOL_2" {
		t.Errorf("expected DiscName 'DEADPOOL_2', got %q", cached.DiscName)
	}
}

func TestGetCachedScanByDrive_NilWhenEmpty(t *testing.T) {
	orch, _, _ := setupOrchestratorWithScanner(t, &mockDriveExecutor{})

	cached := orch.GetCachedScanByDrive(0)
	if cached != nil {
		t.Errorf("expected nil when no scan cached, got %+v", cached)
	}
}

func TestInvalidateScan_ClearsCache(t *testing.T) {
	orch, _, _ := setupOrchestratorWithScanner(t, &mockDriveExecutor{})

	if _, err := orch.ScanDisc(context.Background(), 0); err != nil {
		t.Fatalf("ScanDisc: %v", err)
	}

	orch.InvalidateScan(0)

	if cached := orch.CachedScan(0, "DEADPOOL_2"); cached != nil {
		t.Errorf("expected CachedScan to return nil after invalidation, got %+v", cached)
	}
	if cached := orch.GetCachedScanByDrive(0); cached != nil {
		t.Errorf("expected GetCachedScanByDrive to return nil after invalidation, got %+v", cached)
	}
}

func TestInvalidateScan_OnlyAffectsTargetDrive(t *testing.T) {
	orch, _, _ := setupOrchestratorWithScanner(t, &mockDriveExecutor{})

	// Scan drive 0 (returns DEADPOOL_2 via mockDriveExecutor).
	if _, err := orch.ScanDisc(context.Background(), 0); err != nil {
		t.Fatalf("ScanDisc drive 0: %v", err)
	}

	// Inject a second scan for drive 1.
	orch.InjectCachedScan(1, &makemkv.DiscScan{
		DiscName:   "OTHER_DISC",
		DriveIndex: 1,
	})

	// Invalidate only drive 0.
	orch.InvalidateScan(0)

	if cached := orch.GetCachedScanByDrive(0); cached != nil {
		t.Errorf("expected drive 0 cache to be nil after invalidation, got %+v", cached)
	}

	cached := orch.GetCachedScanByDrive(1)
	if cached == nil {
		t.Fatal("expected drive 1 cache to still be present")
	}
	if cached.DiscName != "OTHER_DISC" {
		t.Errorf("expected DiscName 'OTHER_DISC', got %q", cached.DiscName)
	}
}

func TestScanDisc_NilScanner(t *testing.T) {
	orch, _, _ := setupOrchestrator(t)

	_, err := orch.ScanDisc(context.Background(), 0)
	if err == nil {
		t.Fatal("expected error when scanner is nil")
	}
	if !strings.Contains(err.Error(), "no scanner configured") {
		t.Errorf("expected error to contain 'no scanner configured', got %q", err.Error())
	}
}

func TestBuildTrackMetadata_NoFilter(t *testing.T) {
	title := makeTitleWithStreams("00800.mpls", tronStreams())
	meta := buildTrackMetadata(&title, nil)

	if len(meta.AudioTracks) != 4 {
		t.Errorf("expected 4 audio tracks (nil opts = no filter), got %d", len(meta.AudioTracks))
	}
	if len(meta.SubtitleLanguages) != 2 {
		t.Errorf("expected 2 subtitle languages, got %d: %v", len(meta.SubtitleLanguages), meta.SubtitleLanguages)
	}
}

func TestBuildTrackMetadata_AudioLangFilter(t *testing.T) {
	title := makeTitleWithStreams("00800.mpls", tronStreams())
	opts := &makemkv.SelectionOpts{
		AudioLangs:    []string{"eng"},
		SubtitleLangs: []string{"eng"},
		KeepLossless:  true,
	}
	meta := buildTrackMetadata(&title, opts)

	if len(meta.AudioTracks) != 2 {
		t.Errorf("expected 2 English audio tracks, got %d", len(meta.AudioTracks))
		for _, a := range meta.AudioTracks {
			t.Logf("  audio: %s %s %s", a.Codec, a.Channels, a.Language)
		}
	}
	for _, a := range meta.AudioTracks {
		if a.Language != "English" {
			t.Errorf("expected only English audio, got %q", a.Language)
		}
	}

	if len(meta.SubtitleLanguages) != 1 || meta.SubtitleLanguages[0] != "English" {
		t.Errorf("expected [English] subtitle, got %v", meta.SubtitleLanguages)
	}
}

func TestBuildTrackMetadata_LosslessFilter(t *testing.T) {
	title := makeTitleWithStreams("00800.mpls", tronStreams())
	opts := &makemkv.SelectionOpts{
		AudioLangs:   []string{"eng"},
		KeepLossless: false,
	}
	meta := buildTrackMetadata(&title, opts)

	// Should keep AC3 English but not TrueHD English.
	if len(meta.AudioTracks) != 1 {
		t.Errorf("expected 1 audio track (AC3 English, TrueHD filtered), got %d", len(meta.AudioTracks))
		for _, a := range meta.AudioTracks {
			t.Logf("  audio: %s %s %s", a.Codec, a.Channels, a.Language)
		}
	}
	if len(meta.AudioTracks) == 1 && meta.AudioTracks[0].Codec != "AC3" {
		t.Errorf("expected AC3, got %q", meta.AudioTracks[0].Codec)
	}

	// Subtitles should be unaffected when SubtitleLangs is empty.
	if len(meta.SubtitleLanguages) != 2 {
		t.Errorf("expected 2 subtitle languages (subtitle filter not active), got %d: %v", len(meta.SubtitleLanguages), meta.SubtitleLanguages)
	}
}
