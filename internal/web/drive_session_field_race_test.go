package web

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/johnpostlethwait/bluforge/internal/discdb"
	"github.com/johnpostlethwait/bluforge/internal/drivemanager"
)

// DriveSessionStore.Get hands out the live pointer, and SetSearchResults,
// SetRawSearchResults and SetDiscLabel mutate the fields of that same object.
// A page render reading those fields off a Get is therefore racing every
// concurrent search on the same drive — one browser tab searching while another
// loads the page is enough.
//
// The existing store race test only exercises Set/Get/Clear, which swap the
// pointer under the lock. It never touches the field writes, so this went
// unseen.
//
// Fails under -race before the fix.
func TestBuildDriveStoreDoesNotRaceWithASearch(t *testing.T) {
	mgr := drivemanager.NewManager(&driveWithDiscExecutor{discName: "SESSION_DISC"}, nil)
	mgr.PollOnce(context.Background())
	srv := newTestServer(t, mgr)

	drv := mgr.GetDrive(0)
	if drv == nil {
		t.Fatal("drive 0 is not known")
	}

	srv.driveSessions.Set(0, &DriveSession{
		DiscLabel:   "SESSION_DISC",
		ReleaseID:   "rel-1",
		MediaItemID: "item-1",
	})

	stop := make(chan struct{})
	var wg sync.WaitGroup

	// A search landing repeatedly, as a second tab would produce.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for n := 0; ; n++ {
			select {
			case <-stop:
				return
			default:
			}
			srv.driveSessions.SetSearchResults(0, []SearchResultJSON{
				{MediaTitle: fmt.Sprintf("result-%d", n)},
			})
			srv.driveSessions.SetRawSearchResults(0, []discdb.MediaItem{{Title: "raw"}})
			srv.driveSessions.SetDiscLabel(0, "SESSION_DISC")
		}
	}()

	// The page render, reading the same session.
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for n := 0; n < 200; n++ {
				_ = srv.buildDriveStore(0, drv)
			}
		}()
	}

	// Let the readers finish, then stop the writer.
	go func() {
		wg.Wait()
	}()
	for n := 0; n < 200; n++ {
		_ = srv.buildDriveStore(0, drv)
	}
	close(stop)
	wg.Wait()
}
