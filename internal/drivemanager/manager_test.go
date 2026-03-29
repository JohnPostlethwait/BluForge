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

	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
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

	if len(events) != 2 {
		t.Fatalf("expected 2 events (insert + eject), got %d", len(events))
	}

	if events[0].Type != EventDiscInserted {
		t.Errorf("expected first event to be EventDiscInserted, got %q", events[0].Type)
	}

	eject := events[1]
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

	if len(events) != 2 {
		t.Fatalf("expected 2 insert events, got %d", len(events))
	}

	// Both should be inserts; collect by drive index for order-independent check.
	byIndex := make(map[int]DriveEvent)
	for _, ev := range events {
		byIndex[ev.DriveIndex] = ev
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
