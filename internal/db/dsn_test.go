package db

import (
	"strings"
	"testing"
)

// A PRAGMA set with db.Exec applies to whichever pooled connection served the
// call. The pool opens more on demand, and those had a busy timeout of zero: a
// rip writing its status while the activity page queried was enough to produce
// "database is locked" and lose the update outright. CI caught it and local
// runs did not, because the timing only loses under load.
func TestEveryConnectionGetsTheBusyTimeout(t *testing.T) {
	got := dsn("/config/bluforge.db")
	for _, want := range []string{"busy_timeout", "journal_mode(WAL)", "foreign_keys"} {
		if !strings.Contains(got, want) {
			t.Errorf("dsn is missing %s: %s", want, got)
		}
	}
}

// A path that already carries parameters must keep them.
func TestDSNPreservesExistingParameters(t *testing.T) {
	got := dsn("file:/config/bluforge.db?mode=rwc")
	if !strings.Contains(got, "mode=rwc") {
		t.Errorf("dsn dropped an existing parameter: %s", got)
	}
	if strings.Count(got, "?") != 1 {
		t.Errorf("dsn produced a malformed query string: %s", got)
	}
}
