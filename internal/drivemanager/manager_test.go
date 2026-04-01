package drivemanager

import (
	"context"
	"testing"

	"github.com/johnpostlethwait/bluforge/internal/makemkv"
)

// mockExecutor is a test double for DriveExecutor.
type mockExecutor struct {
	drives []makemkv.DriveInfo
}

func (m *mockExecutor) ListDrives(_ context.Context) ([]makemkv.DriveInfo, error) {
	return m.drives, nil
}

func (m *mockExecutor) ScanDisc(_ context.Context, _ int) (*makemkv.DiscScan, error) {
	return &makemkv.DiscScan{}, nil
}

func TestManagerDetectsDiscInsert(t *testing.T) {
	var events []DriveEvent

	mock := &mockExecutor{
		drives: []makemkv.DriveInfo{
			{Index: 0, DriveName: "/dev/sr0", DiscName: "MOVIE_DISC", Flags: 1},
		},
	}

	mgr := NewManager(mock, func(e DriveEvent) {
		events = append(events, e)
	})

	mgr.PollOnce(context.Background())

	// First poll emits disc_inserted + state_change (ready).
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}

	ev := events[0]
	if ev.Type != EventDiscInserted {
		t.Errorf("expected EventDiscInserted, got %q", ev.Type)
	}
	if ev.DriveIndex != 0 {
		t.Errorf("expected DriveIndex 0, got %d", ev.DriveIndex)
	}
	if ev.DiscName != "MOVIE_DISC" {
		t.Errorf("expected DiscName %q, got %q", "MOVIE_DISC", ev.DiscName)
	}
	if events[1].Type != EventStateChange {
		t.Errorf("expected EventStateChange, got %q", events[1].Type)
	}
}

func TestManagerDetectsDiscEject(t *testing.T) {
	var events []DriveEvent

	mock := &mockExecutor{
		drives: []makemkv.DriveInfo{
			{Index: 0, DriveName: "/dev/sr0", DiscName: "MOVIE_DISC", Flags: 1},
		},
	}

	mgr := NewManager(mock, func(e DriveEvent) {
		events = append(events, e)
	})

	// First poll: disc is present — should emit insert.
	mgr.PollOnce(context.Background())

	// Second poll: disc is gone — should emit eject.
	mock.drives = []makemkv.DriveInfo{
		{Index: 0, DriveName: "/dev/sr0", DiscName: "", Flags: 0},
	}
	mgr.PollOnce(context.Background())

	// First poll: insert + state_change. Second poll: eject.
	if len(events) != 3 {
		t.Fatalf("expected 3 events (insert + state_change + eject), got %d", len(events))
	}

	if events[0].Type != EventDiscInserted {
		t.Errorf("expected first event to be EventDiscInserted, got %q", events[0].Type)
	}

	eject := events[2]
	if eject.Type != EventDiscEjected {
		t.Errorf("expected EventDiscEjected, got %q", eject.Type)
	}
	if eject.DriveIndex != 0 {
		t.Errorf("expected DriveIndex 0, got %d", eject.DriveIndex)
	}
	if eject.DiscName != "MOVIE_DISC" {
		t.Errorf("expected DiscName %q in eject event, got %q", "MOVIE_DISC", eject.DiscName)
	}
}

func TestManagerMultipleDrives(t *testing.T) {
	var events []DriveEvent

	mock := &mockExecutor{
		drives: []makemkv.DriveInfo{
			{Index: 0, DriveName: "/dev/sr0", DiscName: "DISC_ONE", Flags: 1},
			{Index: 1, DriveName: "/dev/sr1", DiscName: "DISC_TWO", Flags: 1},
		},
	}

	mgr := NewManager(mock, func(e DriveEvent) {
		events = append(events, e)
	})

	mgr.PollOnce(context.Background())

	// First poll: 2 inserts + 1 state_change.
	if len(events) != 3 {
		t.Fatalf("expected 3 events (2 inserts + state_change), got %d", len(events))
	}

	// Collect insert events by drive index for order-independent check.
	byIndex := make(map[int]DriveEvent)
	for _, ev := range events {
		if ev.Type == EventDiscInserted {
			byIndex[ev.DriveIndex] = ev
		}
	}

	for _, idx := range []int{0, 1} {
		ev, ok := byIndex[idx]
		if !ok {
			t.Errorf("missing insert event for drive %d", idx)
			continue
		}
		if ev.Type != EventDiscInserted {
			t.Errorf("drive %d: expected EventDiscInserted, got %q", idx, ev.Type)
		}
	}

	if byIndex[0].DiscName != "DISC_ONE" {
		t.Errorf("drive 0: expected DiscName %q, got %q", "DISC_ONE", byIndex[0].DiscName)
	}
	if byIndex[1].DiscName != "DISC_TWO" {
		t.Errorf("drive 1: expected DiscName %q, got %q", "DISC_TWO", byIndex[1].DiscName)
	}
}

func TestReady_FalseBeforePoll(t *testing.T) {
	mock := &mockExecutor{
		drives: []makemkv.DriveInfo{
			{Index: 0, DriveName: "/dev/sr0", DiscName: "DISC", Flags: 1},
		},
	}

	mgr := NewManager(mock, func(e DriveEvent) {})

	if mgr.Ready() {
		t.Error("expected Ready() to be false before PollOnce")
	}
}

func TestReady_TrueAfterPoll(t *testing.T) {
	mock := &mockExecutor{
		drives: []makemkv.DriveInfo{
			{Index: 0, DriveName: "/dev/sr0", DiscName: "DISC", Flags: 1},
		},
	}

	mgr := NewManager(mock, func(e DriveEvent) {})
	mgr.PollOnce(context.Background())

	if !mgr.Ready() {
		t.Error("expected Ready() to be true after PollOnce")
	}
}

func TestGetDrive_ValidIndex(t *testing.T) {
	mock := &mockExecutor{
		drives: []makemkv.DriveInfo{
			{Index: 0, DriveName: "/dev/sr0", DiscName: "DISC", Flags: 1},
		},
	}

	mgr := NewManager(mock, func(e DriveEvent) {})
	mgr.PollOnce(context.Background())

	drive := mgr.GetDrive(0)
	if drive == nil {
		t.Fatal("expected non-nil drive for index 0")
	}
	if drive.Index() != 0 {
		t.Errorf("expected Index() == 0, got %d", drive.Index())
	}
}

func TestGetDrive_InvalidIndex(t *testing.T) {
	mock := &mockExecutor{
		drives: []makemkv.DriveInfo{
			{Index: 0, DriveName: "/dev/sr0", DiscName: "DISC", Flags: 1},
		},
	}

	mgr := NewManager(mock, func(e DriveEvent) {})
	mgr.PollOnce(context.Background())

	drive := mgr.GetDrive(99)
	if drive != nil {
		t.Errorf("expected nil for invalid index 99, got %+v", drive)
	}
}

func TestGetAllDrives_AfterPoll(t *testing.T) {
	mock := &mockExecutor{
		drives: []makemkv.DriveInfo{
			{Index: 0, DriveName: "/dev/sr0", DiscName: "DISC_ONE", Flags: 1},
			{Index: 1, DriveName: "/dev/sr1", DiscName: "DISC_TWO", Flags: 1},
		},
	}

	mgr := NewManager(mock, func(e DriveEvent) {})
	mgr.PollOnce(context.Background())

	allDrives := mgr.GetAllDrives()
	if len(allDrives) != 2 {
		t.Errorf("expected 2 drives, got %d", len(allDrives))
	}
}

func TestGetAllDrives_NoDrives(t *testing.T) {
	mock := &mockExecutor{
		drives: []makemkv.DriveInfo{},
	}

	mgr := NewManager(mock, func(e DriveEvent) {})
	mgr.PollOnce(context.Background())

	allDrives := mgr.GetAllDrives()
	if len(allDrives) != 0 {
		t.Errorf("expected 0 drives, got %d", len(allDrives))
	}
}
