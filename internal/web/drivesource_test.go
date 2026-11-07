package web

import (
	"context"

	"github.com/johnpostlethwait/bluforge/internal/drives"
	"github.com/johnpostlethwait/bluforge/internal/makemkv"
)

// The web tests' executors describe drives as a listing. These adapt that
// listing to the drive manager's source: the slots are the listed drives, and
// identifying one returns its line.

func slotsOf(list []makemkv.DriveInfo) []drives.Slot {
	slots := make([]drives.Slot, 0, len(list))
	for _, d := range list {
		slots = append(slots, drives.Slot{Index: d.Index, Device: d.DevicePath})
	}
	return slots
}

func identifyIn(list []makemkv.DriveInfo, idx int) (makemkv.DriveInfo, error) {
	for _, d := range list {
		if d.Index == idx {
			return d, nil
		}
	}
	return makemkv.DriveInfo{}, drives.ErrUnknownDrive
}

func (s *stubExecutor) Slots() ([]drives.Slot, error) {
	list, _ := s.ListDrives(context.Background())
	return slotsOf(list), nil
}

func (s *stubExecutor) Identify(ctx context.Context, idx int) (makemkv.DriveInfo, error) {
	list, _ := s.ListDrives(ctx)
	return identifyIn(list, idx)
}

func (s *stubExecutor) ProbeStatus(string) drives.Status { return drives.StatusUnknown }

func (e *driveWithDiscExecutor) Slots() ([]drives.Slot, error) {
	list, _ := e.ListDrives(context.Background())
	return slotsOf(list), nil
}

func (e *driveWithDiscExecutor) Identify(ctx context.Context, idx int) (makemkv.DriveInfo, error) {
	list, _ := e.ListDrives(ctx)
	return identifyIn(list, idx)
}

func (e *driveWithDiscExecutor) ProbeStatus(string) drives.Status { return drives.StatusUnknown }
