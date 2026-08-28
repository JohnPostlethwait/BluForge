package workflow

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/johnpostlethwait/bluforge/internal/makemkv"
)

// Auto-rip reaches the disc through ScanDisc, which does not claim the scan
// slot — so scanProgressFn finds no scanState, returns early, and not one
// disc_scan event is ever emitted.
//
// The result is a drive page that sits completely blank for the length of the
// scan. On a disc that retries unreadable sectors that is the better part of an
// hour with no banner, no elapsed time and no sign the machine is doing
// anything, which is indistinguishable from a hang. A scan the user pressed for
// narrates itself; one the machine started should too.
func TestAutoRipNarratesItsScan(t *testing.T) {
	scanner := newProgressScanner(makemkv.Event{
		Type:      "PRGT",
		Operation: "Scanning seamless segments",
	})
	close(scanner.release) // no need to hold it open

	var (
		mu     sync.Mutex
		phases []string
	)
	orch, _, outputDir := setupOrchestratorWithScannerAndBroadcast(t, scanner, func(event, data string) {
		if event != "disc_scan" {
			return
		}
		var payload struct {
			Phase string `json:"phase"`
		}
		if err := json.Unmarshal([]byte(data), &payload); err != nil {
			return
		}
		mu.Lock()
		phases = append(phases, payload.Phase)
		mu.Unlock()
	})

	if err := orch.AutoRip(context.Background(), 0, AutoRipConfig{
		OutputDir:       outputDir,
		DuplicateAction: "skip",
	}); err != nil {
		t.Fatalf("AutoRip: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(phases) == 0 {
		t.Fatal("auto-rip emitted no disc_scan events at all; the page shows nothing for the whole scan")
	}

	var sawScanning, sawTerminal bool
	for _, p := range phases {
		switch p {
		case "scanning":
			sawScanning = true
		case "done", "failed", "recovering":
			sawTerminal = true
		}
	}
	if !sawScanning {
		t.Errorf("no scanning phase; phases were %v", phases)
	}
	if !sawTerminal {
		t.Errorf("no terminal phase, so the banner would never come down; phases were %v", phases)
	}
}
