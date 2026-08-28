package web

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/johnpostlethwait/bluforge/internal/drivemanager"
	"github.com/johnpostlethwait/bluforge/internal/ripper"
)

// The dashboard and the drive-update SSE event describe the same drives, and
// they used to build that description separately. The SSE copy set only four
// fields, leaving RipProgress and WorkflowStep at their zero values — and zero
// means "ripping, at 0%" and "no disc" respectively. Every disc insert or eject
// therefore rewrote every card on the dashboard as a drive at 0% with no step.
//
// One builder, used by both, is what makes that impossible to reintroduce.
func TestDriveListJSON_IdleDriveIsNotReportedAsRipping(t *testing.T) {
	mgr := drivemanager.NewManager(&driveWithDiscExecutor{discName: "TEST_DISC"}, nil)
	mgr.PollOnce(context.Background())

	srv, _ := setupDashboardServer(t, mgr)

	drives := srv.DriveListJSON()
	if len(drives) != 1 {
		t.Fatalf("expected 1 drive, got %d", len(drives))
	}

	if drives[0].RipProgress != -1 {
		t.Errorf("RipProgress = %d, want -1: a drive with no rip must not read as ripping at 0%%",
			drives[0].RipProgress)
	}
	if drives[0].WorkflowStep < 1 {
		t.Errorf("WorkflowStep = %d, want at least 1: the drive holds a disc",
			drives[0].WorkflowStep)
	}
	if drives[0].DiscName != "TEST_DISC" {
		t.Errorf("DiscName = %q, want %q", drives[0].DiscName, "TEST_DISC")
	}
}

func TestDriveListJSON_ReportsProgressOfAnActiveRip(t *testing.T) {
	mgr := drivemanager.NewManager(&driveWithDiscExecutor{discName: "TEST_DISC"}, nil)
	mgr.PollOnce(context.Background())

	blocker := &blockingRipExecutor{block: make(chan struct{})}
	engine := ripper.NewEngine(blocker)

	srv, _ := setupDashboardServer(t, mgr)
	srv.ripEngine = engine

	tmpDir := t.TempDir()
	job := ripper.NewJob(0, 0, "TEST_DISC", filepath.Join(tmpDir, "out"))
	jobDone := make(chan struct{})
	job.OnComplete = func(_ *ripper.Job, _ error) error { close(jobDone); return nil }
	if err := os.MkdirAll(job.OutputDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := engine.Submit(job); err != nil {
		t.Fatalf("Submit: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for atomic.LoadInt32(&blocker.started) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for the rip to start")
		}
		time.Sleep(5 * time.Millisecond)
	}

	drives := srv.DriveListJSON()
	if len(drives) != 1 {
		t.Fatalf("expected 1 drive, got %d", len(drives))
	}
	if drives[0].RipProgress < 0 {
		t.Errorf("RipProgress = %d, want >= 0 while a rip is running", drives[0].RipProgress)
	}
	if drives[0].WorkflowStep != 5 {
		t.Errorf("WorkflowStep = %d, want 5 while a rip is running", drives[0].WorkflowStep)
	}

	close(blocker.block)
	<-jobDone
}
