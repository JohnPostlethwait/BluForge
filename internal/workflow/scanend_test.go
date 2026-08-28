package workflow

import (
	"encoding/json"
	"sync"
	"testing"
	"time"
)

// endScan's own comment says it "runs before the terminal event is broadcast,
// so a client that acts on 'done' immediately does not find a scan still
// running". It was registered with defer, so it ran after — the exact opposite.
//
// The page acts on "done" by fetching the titles and then resyncing the drive
// state, and that resync reads ScanStatus. Landing inside the window puts the
// scanning banner back up over a scan that has finished, and nothing takes it
// down again because the event that would have is the one already spent.
func TestScanIsNotStillRunningWhenItsOutcomeIsAnnounced(t *testing.T) {
	scanner := newProgressScanner()

	var (
		mu             sync.Mutex
		activeAtFinish []bool
	)
	var orch *Orchestrator

	orch, _, _ = setupOrchestratorWithScannerAndBroadcast(t, scanner, func(event, data string) {
		if event != "disc_scan" {
			return
		}
		var payload struct {
			Phase string `json:"phase"`
		}
		if err := json.Unmarshal([]byte(data), &payload); err != nil {
			return
		}
		switch payload.Phase {
		case "done", "failed", "recovering":
			mu.Lock()
			activeAtFinish = append(activeAtFinish, orch.ScanStatus(0).Active)
			mu.Unlock()
		}
	})

	if err := orch.StartScan(0); err != nil {
		t.Fatalf("StartScan: %v", err)
	}

	select {
	case <-scanner.started:
	case <-time.After(asyncDeadline):
		t.Fatal("timed out waiting for the scan to start")
	}
	close(scanner.release)

	deadline := time.Now().Add(asyncDeadline)
	for {
		mu.Lock()
		n := len(activeAtFinish)
		mu.Unlock()
		if n > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no terminal disc_scan event was broadcast")
		}
		time.Sleep(5 * time.Millisecond)
	}

	mu.Lock()
	defer mu.Unlock()
	for i, active := range activeAtFinish {
		if active {
			t.Errorf("terminal event %d was announced while ScanStatus still reported a running scan", i)
		}
	}
}
