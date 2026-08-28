package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"
)

// Recovery has the same ordering fault the scan path had, for the same reason:
// endRecovery was registered with defer, so the "done" phase went out while
// RecoveryInProgress still answered true.
//
// The drive page reacts to "done" by kicking off a scan, and the scan's own
// completion resyncs the drive state — which reads RecoveryInProgress and puts
// the "Recovering disc" banner back up over a recovery that has finished.
// Nothing takes it down, because the event that would have is already spent.
func TestRecoveryIsNotStillRunningWhenItsOutcomeIsAnnounced(t *testing.T) {
	var (
		mu             sync.Mutex
		activeAtFinish []bool
	)
	var orch *Orchestrator

	orch, backupper, _ := setupAsyncWithBroadcast(t, func(event, data string) {
		if event != "disc_recovery" {
			return
		}
		var payload struct {
			Phase string `json:"phase"`
		}
		if err := json.Unmarshal([]byte(data), &payload); err != nil {
			return
		}
		if payload.Phase != "done" && payload.Phase != "failed" {
			return
		}
		mu.Lock()
		activeAtFinish = append(activeAtFinish, orch.RecoveryInProgress(0))
		mu.Unlock()
	})

	if _, err := orch.ScanDisc(context.Background(), 0); !errors.Is(err, ErrRecoveryInProgress) {
		t.Fatalf("ScanDisc returned %v, want ErrRecoveryInProgress", err)
	}

	select {
	case <-backupper.started:
	case <-time.After(asyncDeadline):
		t.Fatal("timed out waiting for the backup to start")
	}
	close(backupper.release)

	deadline := time.Now().Add(asyncDeadline)
	for {
		mu.Lock()
		n := len(activeAtFinish)
		mu.Unlock()
		if n > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no terminal disc_recovery event was broadcast")
		}
		time.Sleep(5 * time.Millisecond)
	}

	mu.Lock()
	defer mu.Unlock()
	for i, active := range activeAtFinish {
		if active {
			t.Errorf("terminal event %d was announced while RecoveryInProgress still reported a running recovery", i)
		}
	}
}
