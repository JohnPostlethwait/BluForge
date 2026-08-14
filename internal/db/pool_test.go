package db

import (
	"path/filepath"
	"sync"
	"testing"
)

// ":memory:" is documented on Open and used by three packages' tests. Every new
// connection to it opens its own private, empty database, so a query answered
// by a second connection cannot see the migrated schema: the migrations run on
// whichever connection the pool opened first, and the next one is a different
// database entirely.
//
// Nothing capped the pool, and the pool only grows when a connection is busy,
// so the suite passed on the arithmetic of never being concurrent rather than
// on the store being sound.
func TestInMemoryDatabaseIsTheSameOnEveryConnection(t *testing.T) {
	store, err := Open(":memory:")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()

	if _, err := store.CreateJob(RipJob{DiscName: "IN_MEMORY", Status: "ripping"}); err != nil {
		t.Fatalf("CreateJob: %v", err)
	}

	var wg sync.WaitGroup
	errs := make(chan error, 16)
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := store.ListJobsByStatus("ripping"); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		t.Fatalf("a connection could not see the migrated schema: %v", err)
	}
}

// An in-memory database cannot be in WAL mode -- it reports "memory" -- so the
// journal check Open makes on a file database has to skip it, or the three
// packages that open ":memory:" all fail at startup.
func TestInMemoryDatabaseStillOpens(t *testing.T) {
	store, err := Open(":memory:")
	if err != nil {
		t.Fatalf("an in-memory database must still open: %v", err)
	}
	defer store.Close()

	if _, err := store.ListJobsByStatus("ripping"); err != nil {
		t.Errorf("in-memory store is not usable: %v", err)
	}
}

// WAL is what makes a reader and a writer coexist. It is requested in the DSN,
// but a filesystem that will not support it -- a network share without the
// shared memory WAL needs -- leaves SQLite in rollback-journal mode silently,
// and the first sign is contention nobody can account for. Open reads the mode
// back so that arrives as a startup failure instead.
func TestOpenConfirmsWALOnAFileDatabase(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "wal.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()

	var mode string
	if err := store.db.QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil {
		t.Fatalf("read journal_mode: %v", err)
	}
	if mode != "wal" {
		t.Errorf("journal_mode = %q, want wal", mode)
	}
}

// A path that cannot be opened has to be reported by Open. sql.Open does not
// connect, so without something forcing a connection the failure surfaces at
// the first query instead, wherever that happens to be.
func TestOpenReportsAnUnusablePath(t *testing.T) {
	if _, err := Open(filepath.Join(t.TempDir(), "no-such-dir", "x.db")); err == nil {
		t.Fatal("expected an error opening a database in a missing directory")
	}
}
