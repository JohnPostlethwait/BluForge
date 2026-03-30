package internal

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/johnpostlethwait/bluforge/internal/db"
	"github.com/johnpostlethwait/bluforge/internal/discdb"
	"github.com/johnpostlethwait/bluforge/internal/drivemanager"
	"github.com/johnpostlethwait/bluforge/internal/makemkv"
	"github.com/johnpostlethwait/bluforge/internal/organizer"
	"github.com/johnpostlethwait/bluforge/internal/ripper"
	"github.com/johnpostlethwait/bluforge/internal/workflow"
	"github.com/johnpostlethwait/bluforge/testutil"
)

// fullMockExecutor implements both drivemanager.DriveExecutor and ripper.RipExecutor
// using the testutil fixture data.
type fullMockExecutor struct{}

// ListDrives parses the SampleDriveListOutput fixture and returns DriveInfo entries.
func (m *fullMockExecutor) ListDrives(_ context.Context) ([]makemkv.DriveInfo, error) {
	events, err := makemkv.ParseAll(strings.NewReader(testutil.SampleDriveListOutput))
	if err != nil {
		return nil, err
	}
	var drives []makemkv.DriveInfo
	for _, ev := range events {
		if ev.Type == "DRV" && ev.Drive != nil {
			drives = append(drives, *ev.Drive)
		}
	}
	return drives, nil
}

// ScanDisc parses the SampleDiscInfoOutput fixture and returns an aggregated DiscScan.
func (m *fullMockExecutor) ScanDisc(_ context.Context, driveIndex int) (*makemkv.DiscScan, error) {
	events, err := makemkv.ParseAll(strings.NewReader(testutil.SampleDiscInfoOutput))
	if err != nil {
		return nil, err
	}

	scan := &makemkv.DiscScan{DriveIndex: driveIndex}
	discAttrs := make(map[int]string)
	titleMap := make(map[int]*makemkv.TitleInfo)

	for _, ev := range events {
		switch ev.Type {
		case "TCOUT":
			scan.TitleCount = ev.Count
		case "CINFO":
			if ev.Disc != nil {
				for k, v := range ev.Disc.Attributes {
					discAttrs[k] = v
				}
			}
		case "TINFO":
			if ev.Title == nil {
				continue
			}
			ti, ok := titleMap[ev.Title.Index]
			if !ok {
				ti = &makemkv.TitleInfo{
					Index:      ev.Title.Index,
					Attributes: make(map[int]string),
				}
				titleMap[ev.Title.Index] = ti
			}
			for k, v := range ev.Title.Attributes {
				ti.Attributes[k] = v
			}
		case "MSG":
			if ev.Message != nil {
				scan.Messages = append(scan.Messages, *ev.Message)
			}
		}
	}

	discInfo := &struct{ Attributes map[int]string }{Attributes: discAttrs}
	scan.DiscName = discInfo.Attributes[2]
	scan.DiscType = discInfo.Attributes[1]

	scan.Titles = make([]makemkv.TitleInfo, 0, len(titleMap))
	for _, ti := range titleMap {
		scan.Titles = append(scan.Titles, *ti)
	}

	return scan, nil
}

// StartRip parses the SampleProgressOutput fixture and fires onEvent for each event.
func (m *fullMockExecutor) StartRip(_ context.Context, _ int, _ int, _ string, onEvent func(makemkv.Event)) error {
	events, err := makemkv.ParseAll(strings.NewReader(testutil.SampleProgressOutput))
	if err != nil {
		return err
	}
	if onEvent != nil {
		for _, ev := range events {
			onEvent(ev)
		}
	}
	return nil
}

func TestFullRipFlow(t *testing.T) {
	ctx := context.Background()
	exec := &fullMockExecutor{}

	// -------------------------------------------------------------------------
	// Step (a): Drive manager — PollOnce, assert disc_inserted for DEADPOOL_2
	// -------------------------------------------------------------------------
	var (
		eventMu      sync.Mutex
		driveEvents  []drivemanager.DriveEvent
	)

	mgr := drivemanager.NewManager(exec, func(ev drivemanager.DriveEvent) {
		eventMu.Lock()
		driveEvents = append(driveEvents, ev)
		eventMu.Unlock()
	})
	mgr.PollOnce(ctx)

	eventMu.Lock()
	evCopy := make([]drivemanager.DriveEvent, len(driveEvents))
	copy(evCopy, driveEvents)
	eventMu.Unlock()

	var insertedEvent *drivemanager.DriveEvent
	for i := range evCopy {
		if evCopy[i].Type == drivemanager.EventDiscInserted {
			insertedEvent = &evCopy[i]
			break
		}
	}
	if insertedEvent == nil {
		t.Fatal("expected disc_inserted event, got none")
	}
	if insertedEvent.DiscName != "DEADPOOL_2" {
		t.Errorf("expected DiscName DEADPOOL_2, got %q", insertedEvent.DiscName)
	}
	if insertedEvent.DriveIndex != 0 {
		t.Errorf("expected DriveIndex 0, got %d", insertedEvent.DriveIndex)
	}

	// -------------------------------------------------------------------------
	// Step (b): Scan disc, assert 3 titles
	// -------------------------------------------------------------------------
	scan, err := exec.ScanDisc(ctx, 0)
	if err != nil {
		t.Fatalf("ScanDisc failed: %v", err)
	}
	if len(scan.Titles) != 3 {
		t.Errorf("expected 3 titles, got %d", len(scan.Titles))
	}
	if scan.DiscName != "DEADPOOL_2" {
		t.Errorf("expected disc name DEADPOOL_2, got %q", scan.DiscName)
	}

	// -------------------------------------------------------------------------
	// Step (c): MatchTitles against a mock discdb.Disc
	// Fixture titles have SourceFile from attribute 16: 00100.mpls, 00200.mpls, 00300.mpls.
	// -------------------------------------------------------------------------
	mockDisc := discdb.Disc{
		Titles: []discdb.DiscTitle{
			{SourceFile: "00100.mpls", ItemType: "movie", Item: &discdb.DiscItemReference{Title: "Deadpool 2"}},
			{SourceFile: "00200.mpls", ItemType: "movie", Item: &discdb.DiscItemReference{Title: "Deadpool 2"}},
			{SourceFile: "00300.mpls", ItemType: "movie", Item: &discdb.DiscItemReference{Title: "Deadpool 2"}},
		},
	}

	matches := discdb.MatchTitles(scan, mockDisc)
	if len(matches) != 3 {
		t.Errorf("expected 3 matches, got %d", len(matches))
	}
	for _, cm := range matches {
		if !cm.Matched {
			t.Errorf("expected title index %d to be matched, but Matched=false", cm.TitleIndex)
		}
		if cm.ContentTitle != "Deadpool 2" {
			t.Errorf("expected ContentTitle %q, got %q", "Deadpool 2", cm.ContentTitle)
		}
	}

	// -------------------------------------------------------------------------
	// Step (d): Build a movie path via organizer
	// -------------------------------------------------------------------------
	org := organizer.New(
		"Movies/{{.Title}} ({{.Year}})/{{.Title}} ({{.Year}})",
		"TV/{{.Show}}/Season {{.Season}}/{{.Show}} S{{.Season}}E{{.Episode}}",
	)
	moviePath, err := org.BuildMoviePath(organizer.MovieMeta{
		Title: "Deadpool 2",
		Year:  "2018",
	})
	if err != nil {
		t.Fatalf("BuildMoviePath failed: %v", err)
	}
	if moviePath == "" {
		t.Error("expected non-empty movie path")
	}
	t.Logf("movie path: %s", moviePath)

	// -------------------------------------------------------------------------
	// Step (e): Submit a rip job, wait for completion, assert progress == 100
	// -------------------------------------------------------------------------
	engine := ripper.NewEngine(exec)

	var (
		jobMu      sync.Mutex
		lastJob    *ripper.Job
		jobDone    = make(chan struct{})
		closedOnce sync.Once
	)

	engine.OnUpdate(func(j *ripper.Job) {
		jobMu.Lock()
		lastJob = j
		status := j.Status
		jobMu.Unlock()
		if status == ripper.StatusCompleted || status == ripper.StatusFailed {
			closedOnce.Do(func() { close(jobDone) })
		}
	})

	job := ripper.NewJob(0, 0, "DEADPOOL_2", t.TempDir())
	if err := engine.Submit(job); err != nil {
		t.Fatalf("Submit failed: %v", err)
	}

	select {
	case <-jobDone:
		// job finished
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for rip job to complete")
	}

	jobMu.Lock()
	finalJob := lastJob
	jobMu.Unlock()

	if finalJob == nil {
		t.Fatal("expected job update callbacks, got none")
	}
	if finalJob.Status != ripper.StatusCompleted {
		t.Errorf("expected status completed, got %q (error: %s)", finalJob.Status, finalJob.Error)
	}
	if finalJob.Progress != 100 {
		t.Errorf("expected progress 100, got %d", finalJob.Progress)
	}
}

// TestFullPipeline_ManualRip exercises a manual rip through the orchestrator,
// verifying that a DB job is created and a disc mapping is saved.
func TestFullPipeline_ManualRip(t *testing.T) {
	store, err := db.Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	defer store.Close()

	exec := &fullMockExecutor{}
	engine := ripper.NewEngine(exec)
	org := organizer.New(
		"Movies/{{.Title}} ({{.Year}})/{{.Title}} ({{.Year}})",
		"TV/{{.Show}}/Season {{.Season}}/{{.Show}} S{{.Season}}E{{.Episode}}",
	)

	orch := workflow.NewOrchestrator(workflow.OrchestratorDeps{
		Store:     store,
		Engine:    engine,
		Organizer: org,
		OnBroadcast: func(string, string) {},
		Scanner:   exec,
	})

	params := workflow.ManualRipParams{
		DriveIndex:  0,
		DiscName:    "DEADPOOL_2",
		DiscKey:     "test-disc-key",
		OutputDir:   t.TempDir(),
		MediaItemID: "item-123",
		ReleaseID:   "rel-456",
		MediaTitle:  "Deadpool 2",
		MediaYear:   "2018",
		MediaType:   "movie",
		Titles: []workflow.TitleSelection{
			{
				TitleIndex:   0,
				TitleName:    "main feature",
				SourceFile:   "00100.mpls",
				SizeBytes:    1024,
				ContentType:  "movie",
				ContentTitle: "Deadpool 2",
				Year:         "2018",
			},
		},
	}

	result := orch.ManualRip(params)

	if len(result.Titles) != 1 {
		t.Fatalf("expected 1 title result, got %d", len(result.Titles))
	}
	if result.Titles[0].Status != "submitted" {
		t.Fatalf("expected status 'submitted', got %q (reason: %s)", result.Titles[0].Status, result.Titles[0].Reason)
	}

	// Wait for the async rip job to complete.
	time.Sleep(500 * time.Millisecond)

	// Verify a job was created in the DB.
	jobs, err := store.ListAllJobs(10, 0)
	if err != nil {
		t.Fatalf("ListAllJobs: %v", err)
	}
	if len(jobs) == 0 {
		t.Fatal("expected at least one job in DB, got none")
	}

	// Verify disc mapping was saved.
	mapping, err := store.GetMapping("test-disc-key")
	if err != nil {
		t.Fatalf("GetMapping: %v", err)
	}
	if mapping == nil {
		t.Fatal("expected disc mapping to be saved, got nil")
	}
	if mapping.MediaTitle != "Deadpool 2" {
		t.Errorf("expected MediaTitle 'Deadpool 2', got %q", mapping.MediaTitle)
	}
}

// TestFullPipeline_Rescan verifies that rescanning a disc deletes its cached mapping.
func TestFullPipeline_Rescan(t *testing.T) {
	store, err := db.Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	defer store.Close()

	exec := &fullMockExecutor{}
	engine := ripper.NewEngine(exec)
	org := organizer.New(
		"Movies/{{.Title}} ({{.Year}})/{{.Title}} ({{.Year}})",
		"TV/{{.Show}}/Season {{.Season}}/{{.Show}} S{{.Season}}E{{.Episode}}",
	)

	orch := workflow.NewOrchestrator(workflow.OrchestratorDeps{
		Store:     store,
		Engine:    engine,
		Organizer: org,
		OnBroadcast: func(string, string) {},
		Scanner:   exec,
	})

	// First, compute the disc key the scanner will produce so we can pre-save
	// a mapping with the same key.
	scan, err := exec.ScanDisc(context.Background(), 0)
	if err != nil {
		t.Fatalf("ScanDisc: %v", err)
	}
	discKey := discdb.BuildDiscKey(scan)

	// Save a mapping that the rescan should delete.
	if err := store.SaveMapping(db.DiscMapping{
		DiscKey:     discKey,
		DiscName:    "DEADPOOL_2",
		MediaItemID: "item-123",
		MediaTitle:  "Deadpool 2",
		MediaYear:   "2018",
		MediaType:   "movie",
	}); err != nil {
		t.Fatalf("SaveMapping: %v", err)
	}

	// Confirm it was saved.
	mapping, err := store.GetMapping(discKey)
	if err != nil {
		t.Fatalf("GetMapping before rescan: %v", err)
	}
	if mapping == nil {
		t.Fatal("expected mapping to exist before rescan")
	}

	// Rescan should delete the mapping.
	if err := orch.Rescan(context.Background(), 0); err != nil {
		t.Fatalf("Rescan: %v", err)
	}

	// Verify mapping is gone.
	mapping, err = store.GetMapping(discKey)
	if err != nil {
		t.Fatalf("GetMapping after rescan: %v", err)
	}
	if mapping != nil {
		t.Fatal("expected mapping to be deleted after rescan, but it still exists")
	}
}

// TestFullPipeline_AutoRip_Unmatched verifies that auto-rip with no DiscDB client
// creates unmatched jobs in the database.
func TestFullPipeline_AutoRip_Unmatched(t *testing.T) {
	store, err := db.Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	defer store.Close()

	exec := &fullMockExecutor{}
	engine := ripper.NewEngine(exec)
	org := organizer.New(
		"Movies/{{.Title}} ({{.Year}})/{{.Title}} ({{.Year}})",
		"TV/{{.Show}}/Season {{.Season}}/{{.Show}} S{{.Season}}E{{.Episode}}",
	)

	// No DiscDB client — forces the unmatched path.
	orch := workflow.NewOrchestrator(workflow.OrchestratorDeps{
		Store:     store,
		Engine:    engine,
		Organizer: org,
		OnBroadcast: func(string, string) {},
		Scanner:   exec,
	})

	cfg := workflow.AutoRipConfig{
		OutputDir:       t.TempDir(),
		DuplicateAction: "skip",
	}

	if err := orch.AutoRip(context.Background(), 0, cfg); err != nil {
		t.Fatalf("AutoRip: %v", err)
	}

	// Wait for async rip jobs to complete.
	time.Sleep(500 * time.Millisecond)

	// Verify jobs were created in the DB (one per title in the fixture scan).
	jobs, err := store.ListAllJobs(20, 0)
	if err != nil {
		t.Fatalf("ListAllJobs: %v", err)
	}
	if len(jobs) == 0 {
		t.Fatal("expected unmatched jobs in DB, got none")
	}

	// At least one job should have been created. Some fixture titles may be
	// skipped due to disk-space checks against the local filesystem.
	t.Logf("created %d unmatched job(s)", len(jobs))
}
