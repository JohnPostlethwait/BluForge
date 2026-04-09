package web

import (
	"path/filepath"
	"testing"

	"github.com/labstack/echo/v4"

	"github.com/johnpostlethwait/bluforge/internal/config"
	"github.com/johnpostlethwait/bluforge/internal/db"
)

// ---------------------------------------------------------------------------
// Setup helpers
// ---------------------------------------------------------------------------

func setupContribServer(t *testing.T) (*Server, *db.Store) {
	t.Helper()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	store, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	cfg := &config.AppConfig{OutputDir: tmpDir}

	srv := &Server{
		echo:          echo.New(),
		cfg:           cfg,
		store:         store,
		sseHub:        NewSSEHub(),
		driveSessions: NewDriveSessionStore(),
	}
	return srv, store
}

func seedTestContribution(t *testing.T, store *db.Store) int64 {
	t.Helper()
	c := db.Contribution{
		DiscKey:   "test-disc-key-001",
		DiscName:  "TEST_DISC",
		RawOutput: "TINFO:0,2,0,\"Test Title\"",
		ScanJSON:  `{"DiscName":"TEST_DISC","TitleCount":1,"Titles":[{"Index":0,"Attributes":{"2":"Test Title","9":"1:30:00","10":"25 GB","11":"25000000000","16":"00001.mpls"},"Streams":[]}]}`,
	}
	id, err := store.SaveContribution(c)
	if err != nil {
		t.Fatalf("SaveContribution: %v", err)
	}
	return id
}
