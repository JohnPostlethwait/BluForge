package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/johnpostlethwait/bluforge/internal/db"
	"github.com/johnpostlethwait/bluforge/internal/drivemanager"
)

// The activity page carried two overlapping lists of the same jobs: History,
// paginated to 50, and Completed — every completed and every failed job in the
// database, unpaginated, serialized into the page on every load.
//
// Nothing rendered Completed. The template writes to it in the rip-update
// handler and no x-for ever reads it. So it was pure growth: a library of a few
// hundred rips put a few hundred job records into every page load, for nothing.
func TestActivityListsAJobOnlyOnce(t *testing.T) {
	mgr := drivemanager.NewManager(&stubExecutor{}, nil)
	srv, store := setupDashboardServer(t, mgr)
	srv.echo.GET("/activity/state", srv.handleActivityState)

	const jobs = 60
	for i := 0; i < jobs; i++ {
		if _, err := store.CreateJob(db.RipJob{
			DriveIndex: 0,
			DiscName:   fmt.Sprintf("DISC_%02d", i),
			TitleName:  fmt.Sprintf("Title %02d", i),
			Status:     "completed",
			OutputPath: "/output/x.mkv",
		}); err != nil {
			t.Fatalf("CreateJob: %v", err)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/activity/state", nil)
	req.Header.Set("Accept", "application/json")
	rec := httptest.NewRecorder()
	srv.echo.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	// Decode loosely so the test does not depend on which fields survive.
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}

	seen := map[int64]string{}
	for _, list := range []string{"active", "pending", "history", "completed"} {
		raw, ok := payload[list]
		if !ok {
			continue
		}
		var entries []struct {
			ID int64 `json:"id"`
		}
		if err := json.Unmarshal(raw, &entries); err != nil {
			t.Fatalf("decode %s: %v", list, err)
		}
		for _, e := range entries {
			if prev, dup := seen[e.ID]; dup {
				t.Fatalf("job %d appears in both %q and %q; the page carries the same rip twice",
					e.ID, prev, list)
			}
			seen[e.ID] = list
		}
	}

	// And the payload must stay bounded by the page size rather than growing
	// with the size of the library.
	if len(seen) > activityHistoryPageSize {
		t.Errorf("the page carries %d job records, want at most %d",
			len(seen), activityHistoryPageSize)
	}
}
