package internal

import (
	"context"

	"github.com/johnpostlethwait/bluforge/internal/drives"
	"github.com/johnpostlethwait/bluforge/internal/makemkv"
)

// The mock describes drives as a listing; these adapt it to the drive
// manager's source.

func (m *fullMockExecutor) Slots() ([]drives.Slot, error) {
	list, _ := m.ListDrives(context.Background())
	slots := make([]drives.Slot, 0, len(list))
	for _, d := range list {
		slots = append(slots, drives.Slot{Index: d.Index, Device: d.DevicePath})
	}
	return slots, nil
}

func (m *fullMockExecutor) Identify(ctx context.Context, idx int) (makemkv.DriveInfo, error) {
	list, _ := m.ListDrives(ctx)
	for _, d := range list {
		if d.Index == idx {
			return d, nil
		}
	}
	return makemkv.DriveInfo{}, drives.ErrUnknownDrive
}

func (m *fullMockExecutor) ProbeStatus(string) drives.Status { return drives.StatusUnknown }
