package web

import (
	"context"
	"testing"

	"github.com/labstack/echo/v4"

	"github.com/johnpostlethwait/bluforge/internal/config"
	"github.com/johnpostlethwait/bluforge/internal/drivemanager"
	"github.com/johnpostlethwait/bluforge/internal/makemkv"
)

// stubExecutor satisfies drivemanager.DriveExecutor for tests that don't need
// any drives or disc data.
type stubExecutor struct{}

func (s *stubExecutor) ListDrives(ctx context.Context) ([]makemkv.DriveInfo, error) {
	return nil, nil
}

func (s *stubExecutor) ScanDisc(ctx context.Context, driveIndex int) (*makemkv.DiscScan, error) {
	return nil, nil
}

// driveWithDiscExecutor returns a single drive with a disc.
type driveWithDiscExecutor struct {
	discName string
}

func (e *driveWithDiscExecutor) ListDrives(ctx context.Context) ([]makemkv.DriveInfo, error) {
	return []makemkv.DriveInfo{
		{
			Index: 0,
			// A drive holding a disc. discName may be empty — that is a disc
			// with no volume label, which is a real and common case and is
			// exactly why presence is read from the state rather than the name.
			State:      makemkv.DriveStateInserted,
			Flags:      12,
			DriveName:  "Test Drive",
			DiscName:   e.discName,
			DevicePath: "/dev/sr0",
		},
	}, nil
}

func (e *driveWithDiscExecutor) ScanDisc(ctx context.Context, driveIndex int) (*makemkv.DiscScan, error) {
	return &makemkv.DiscScan{DriveIndex: driveIndex, DiscName: e.discName}, nil
}

// newTestServer creates a minimal Server with the given drive manager, suitable
// for handler unit tests.
func newTestServer(t *testing.T, mgr *drivemanager.Manager) *Server {
	t.Helper()
	cfg := config.AppConfig{OutputDir: "/tmp/test"}
	return &Server{
		echo:          echo.New(),
		cfg:           &cfg,
		driveMgr:      mgr,
		sseHub:        NewSSEHub(),
		driveSessions: NewDriveSessionStore(),
	}
}
