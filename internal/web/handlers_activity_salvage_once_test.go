package web

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/johnpostlethwait/bluforge/internal/db"
	"github.com/johnpostlethwait/bluforge/internal/workflow"
)

// Frisky Dingo failed three titles in one attempt, and each card offered to
// "Salvage this disc…" — three buttons for one operation that recovers the
// whole disc and rips all three again. One card carries the offer and says how
// many titles it covers; the others say they are covered.
func TestSalvageIsOfferedOncePerDisc(t *testing.T) {
	store, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	const errMsg = "makemkv: rip dev:/dev/sr1 title 4: makemkvcon reported the copy failed and saved no titles"
	var rows []activityJobJSON
	add := func(disc string, title int) {
		id, err := store.CreateJob(db.RipJob{
			DiscName: disc, TitleIndex: title, TitleName: fmt.Sprintf("title %d", title),
			SourceFile: fmt.Sprintf("%d", title), Status: "failed", ErrorMessage: errMsg,
		})
		if err != nil {
			t.Fatalf("CreateJob: %v", err)
		}
		rows = append(rows, activityJobJSON{
			ID: id, DiscName: disc, Status: "failed", Error: errMsg,
			Salvageable: salvageable("failed", errMsg),
		})
	}
	add("FRISKY_DINGO_SEASON_1", 1)
	add("FRISKY_DINGO_SEASON_1", 0)
	add("FRISKY_DINGO_SEASON_1", 7)
	add("RAMBO_DISC2", 0)

	srv := &Server{store: store, orchestrator: workflow.NewOrchestrator(workflow.OrchestratorDeps{Store: store})}
	srv.offerSalvageOncePerDisc(rows)

	want := []struct {
		offer   bool
		titles  int
		covered bool
	}{
		{true, 3, false},
		{false, 0, true},
		{false, 0, true},
		{true, 1, false},
	}
	for i, w := range want {
		r := rows[i]
		if r.Salvageable != w.offer || r.SalvageTitles != w.titles || r.SalvageCovered != w.covered {
			t.Errorf("row %d (%s): offer=%v titles=%d covered=%v, want offer=%v titles=%d covered=%v",
				i, r.DiscName, r.Salvageable, r.SalvageTitles, r.SalvageCovered, w.offer, w.titles, w.covered)
		}
	}
}
