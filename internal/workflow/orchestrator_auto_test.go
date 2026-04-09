package workflow

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/johnpostlethwait/bluforge/internal/db"
	"github.com/johnpostlethwait/bluforge/internal/discdb"
	"github.com/johnpostlethwait/bluforge/internal/organizer"
	"github.com/johnpostlethwait/bluforge/internal/ripper"
)

func TestAutoRip_WithMapping(t *testing.T) {
	scanner := &mockDriveExecutor{}
	orch, store, outputDir := setupOrchestratorWithScanner(t, scanner)

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

	cfg := AutoRipConfig{
		OutputDir:       outputDir,
		DuplicateAction: "overwrite",
	}

	if err := orch.AutoRip(context.Background(), 0, cfg); err != nil {
		t.Fatalf("AutoRip: %v", err)
	}

	waitForCompletedJob(t, store)

	jobs, err := store.ListJobsByStatus("completed")
	if err != nil {
		t.Fatalf("ListJobsByStatus: %v", err)
	}
	if len(jobs) == 0 {
		t.Fatal("no completed jobs found")
	}
	if jobs[0].DiscName != "DEADPOOL_2" {
		t.Errorf("expected disc name 'DEADPOOL_2', got %q", jobs[0].DiscName)
	}
}

func TestAutoRip_NoMatch_UsesDiscName(t *testing.T) {
	scanner := &mockDriveExecutor{}
	orch, store, outputDir := setupOrchestratorWithScanner(t, scanner)

	// No mapping saved, no discdb client — should use disc name as directory.
	cfg := AutoRipConfig{
		OutputDir:       outputDir,
		DuplicateAction: "overwrite",
	}

	if err := orch.AutoRip(context.Background(), 0, cfg); err != nil {
		t.Fatalf("AutoRip: %v", err)
	}

	waitForCompletedJob(t, store)

	jobs, err := store.ListJobsByStatus("completed")
	if err != nil {
		t.Fatalf("ListJobsByStatus: %v", err)
	}
	if len(jobs) == 0 {
		t.Fatal("no completed jobs found")
	}
	if jobs[0].DiscName != "DEADPOOL_2" {
		t.Errorf("expected disc name 'DEADPOOL_2', got %q", jobs[0].DiscName)
	}
	if jobs[0].OutputPath == "" {
		t.Error("expected output path to be set")
	}
}

func TestAutoRip_NoMatch_CreatesContributionRecord(t *testing.T) {
	scanner := &mockDriveExecutor{}
	orch, store, outputDir := setupOrchestratorWithScanner(t, scanner)

	cfg := AutoRipConfig{
		OutputDir:       outputDir,
		DuplicateAction: "overwrite",
	}

	if err := orch.AutoRip(context.Background(), 0, cfg); err != nil {
		t.Fatalf("AutoRip: %v", err)
	}

	waitForCompletedJob(t, store)

	// Verify a contribution record was created.
	contribs, err := store.ListContributions("")
	if err != nil {
		t.Fatalf("ListContributions: %v", err)
	}
	if len(contribs) != 1 {
		t.Fatalf("expected 1 contribution, got %d", len(contribs))
	}
	c := contribs[0]
	if c.DiscName != "DEADPOOL_2" {
		t.Errorf("DiscName: want %q, got %q", "DEADPOOL_2", c.DiscName)
	}
	if c.Status != "pending" {
		t.Errorf("Status: want %q, got %q", "pending", c.Status)
	}
	if c.ScanJSON == "" {
		t.Error("ScanJSON should not be empty")
	}
	if c.DiscKey == "" {
		t.Error("DiscKey should not be empty")
	}
}

func TestAutoRip_NoMatch_DuplicateContributionSkipped(t *testing.T) {
	scanner := &mockDriveExecutor{}
	orch, store, outputDir := setupOrchestratorWithScanner(t, scanner)

	cfg := AutoRipConfig{
		OutputDir:       outputDir,
		DuplicateAction: "overwrite",
	}

	// First AutoRip.
	if err := orch.AutoRip(context.Background(), 0, cfg); err != nil {
		t.Fatalf("AutoRip 1: %v", err)
	}
	waitForCompletedJob(t, store)

	// Second AutoRip — same disc, same scanner.
	if err := orch.AutoRip(context.Background(), 0, cfg); err != nil {
		t.Fatalf("AutoRip 2: %v", err)
	}
	waitForCompletedJobs(t, store, 2)

	// Should still have exactly 1 contribution (not 2).
	contribs, err := store.ListContributions("")
	if err != nil {
		t.Fatalf("ListContributions: %v", err)
	}
	if len(contribs) != 1 {
		t.Errorf("expected 1 contribution after 2 autorips, got %d", len(contribs))
	}
}

func TestAutoRip_NoMatch_BroadcastsSSE(t *testing.T) {
	scanner := &mockDriveExecutor{}

	var mu sync.Mutex
	var broadcasts []struct{ event, data string }

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	store, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	engine := ripper.NewEngine(&mockRipExecutor{})
	org := organizer.New()
	orch := NewOrchestrator(OrchestratorDeps{
		Store:     store,
		Engine:    engine,
		Organizer: org,
		OnBroadcast: func(event, data string) {
			mu.Lock()
			broadcasts = append(broadcasts, struct{ event, data string }{event, data})
			mu.Unlock()
		},
		Scanner: scanner,
	})

	outputDir := filepath.Join(tmpDir, "output")
	os.MkdirAll(outputDir, 0o755)

	cfg := AutoRipConfig{
		OutputDir:       outputDir,
		DuplicateAction: "overwrite",
	}

	if err := orch.AutoRip(context.Background(), 0, cfg); err != nil {
		t.Fatalf("AutoRip: %v", err)
	}

	// The contribution_available broadcast happens synchronously in AutoRip,
	// so it is already captured by the time AutoRip returns.
	mu.Lock()
	defer mu.Unlock()

	found := false
	for _, b := range broadcasts {
		if b.event == "contribution_available" {
			found = true
			if !strings.Contains(b.data, "contribution_id") {
				t.Errorf("broadcast data missing contribution_id: %s", b.data)
			}
			if !strings.Contains(b.data, "DEADPOOL_2") {
				t.Errorf("broadcast data missing disc name: %s", b.data)
			}
			break
		}
	}
	if !found {
		t.Error("expected contribution_available SSE broadcast, none found")
	}
}

func TestAutoRip_WithMatch_NoContributionCreated(t *testing.T) {
	scanner := &mockDriveExecutor{}
	orch, store, outputDir := setupOrchestratorWithScanner(t, scanner)

	// Save a mapping for this disc so AutoRip finds a match.
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

	cfg := AutoRipConfig{
		OutputDir:       outputDir,
		DuplicateAction: "overwrite",
	}

	if err := orch.AutoRip(context.Background(), 0, cfg); err != nil {
		t.Fatalf("AutoRip: %v", err)
	}

	waitForCompletedJob(t, store)

	// No contribution should exist.
	contribs, err := store.ListContributions("")
	if err != nil {
		t.Fatalf("ListContributions: %v", err)
	}
	if len(contribs) != 0 {
		t.Errorf("expected 0 contributions when disc is matched, got %d", len(contribs))
	}
}
