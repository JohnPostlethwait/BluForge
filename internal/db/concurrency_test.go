package db

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// Open configures the connection, but database/sql hands out a pool of them.
// A PRAGMA is per-connection state, so running one through the pool sets it on
// whichever connection happened to answer and leaves every other connection on
// the SQLite defaults: busy_timeout 0, foreign_keys off.
//
// journal_mode is the exception that hid this for so long -- WAL is recorded in
// the database file header, so it applies to every connection however it was
// set, and the one setting that visibly worked was taken as evidence the others
// had too.
//
// TestEveryConnectionGetsTheBusyTimeout in dsn_test.go covers the same fix by
// asserting on the DSN string. This is the behavioural half: it opens real
// connections and reads the pragmas back off each one, so a change that keeps
// the DSN intact but stops it reaching the pool still fails.
func TestEveryConnectionGetsThePragmas(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "pragmas.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()

	ctx := context.Background()

	// Hold the connections open at the same time, or the pool just hands the
	// same configured one back and the test proves nothing.
	const conns = 4
	held := make([]*sql.Conn, 0, conns)
	defer func() {
		for _, c := range held {
			c.Close()
		}
	}()

	for i := range conns {
		c, err := store.db.Conn(ctx)
		if err != nil {
			t.Fatalf("open connection %d: %v", i, err)
		}
		held = append(held, c)

		var busyTimeout int
		if err := c.QueryRowContext(ctx, "PRAGMA busy_timeout").Scan(&busyTimeout); err != nil {
			t.Fatalf("read busy_timeout on connection %d: %v", i, err)
		}
		if busyTimeout != 5000 {
			t.Errorf("connection %d has busy_timeout=%d, want 5000: it will fail a contended write instead of waiting",
				i, busyTimeout)
		}

		var foreignKeys int
		if err := c.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&foreignKeys); err != nil {
			t.Fatalf("read foreign_keys on connection %d: %v", i, err)
		}
		if foreignKeys != 1 {
			t.Errorf("connection %d has foreign_keys=%d, want 1: constraints are not enforced on it",
				i, foreignKeys)
		}
	}
}

// The symptom that started this: concurrent rips updating their own job rows
// got "database is locked (5) (SQLITE_BUSY)" and the update was simply lost --
// a job stayed at "ripping" forever because the write that would have completed
// it was dropped.
func TestConcurrentWritersDoNotGetSQLiteBusy(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "writers.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()

	const writers = 8
	const updates = 25

	ids := make([]int64, writers)
	for i := range ids {
		id, err := store.CreateJob(RipJob{
			DiscName: fmt.Sprintf("DISC_%d", i),
			Status:   "ripping",
		})
		if err != nil {
			t.Fatalf("CreateJob %d: %v", i, err)
		}
		ids[i] = id
	}

	var wg sync.WaitGroup
	errs := make(chan error, writers*updates)
	for w := range writers {
		wg.Add(1)
		go func(id int64) {
			defer wg.Done()
			for u := range updates {
				if err := store.UpdateJobStatus(id, "ripping", u*4, ""); err != nil {
					errs <- err
				}
			}
		}(ids[w])
	}
	wg.Wait()
	close(errs)

	var busy, other int
	var sample error
	for err := range errs {
		if strings.Contains(err.Error(), "SQLITE_BUSY") || strings.Contains(err.Error(), "database is locked") {
			busy++
		} else {
			other++
		}
		if sample == nil {
			sample = err
		}
	}
	if busy > 0 || other > 0 {
		t.Errorf("%d writes failed with SQLITE_BUSY and %d for other reasons out of %d; first: %v",
			busy, other, writers*updates, sample)
	}
}
