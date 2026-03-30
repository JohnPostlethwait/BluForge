package drivemanager

import (
	"context"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/johnpostlethwait/bluforge/internal/makemkv"
)

// EventType describes the kind of drive event that occurred.
type EventType string

const (
	EventDiscInserted    EventType = "disc_inserted"
	EventDiscEjected     EventType = "disc_ejected"
	EventDriveDisconnect EventType = "drive_disconnect"
	EventStateChange     EventType = "state_change"
)

// DriveEvent carries information about a change detected on a drive.
type DriveEvent struct {
	Type       EventType
	DriveIndex int
	DiscName   string
	State      DriveState
}

// DriveExecutor is the interface for querying MakeMKV drive information.
type DriveExecutor interface {
	ListDrives(ctx context.Context) ([]makemkv.DriveInfo, error)
	ScanDisc(ctx context.Context, driveIndex int) (*makemkv.DiscScan, error)
}

// Manager polls drives and emits events when drive state changes.
type Manager struct {
	mu      sync.RWMutex
	exec    DriveExecutor
	drives  map[int]*DriveStateMachine
	known   map[int]string // last known disc name per drive index
	onEvent func(DriveEvent)
	ready   bool // true after the first poll completes
}

// NewManager creates a new Manager with the given executor and event callback.
func NewManager(executor DriveExecutor, onEvent func(DriveEvent)) *Manager {
	return &Manager{
		exec:    executor,
		drives:  make(map[int]*DriveStateMachine),
		known:   make(map[int]string),
		onEvent: onEvent,
	}
}

// Ready returns true after the first drive poll has completed.
func (m *Manager) Ready() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.ready
}

// discPresent returns true when a DriveInfo has a non-empty disc name and
// non-zero flags, indicating a disc is actually loaded.
func discPresent(info makemkv.DriveInfo) bool {
	return info.DiscName != "" && info.Flags > 0
}

// PollOnce lists drives, compares against previous state, and emits events.
func (m *Manager) PollOnce(ctx context.Context) {
	infos, err := m.exec.ListDrives(ctx)
	if err != nil {
		slog.Error("drive poll failed", "error", err)
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	m.ready = true

	// Track which drive indices are present in this poll.
	seen := make(map[int]bool, len(infos))

	for _, info := range infos {
		// Skip phantom drive slots with no hardware attached.
		if info.DriveName == "" {
			continue
		}

		seen[info.Index] = true

		// Ensure a state machine exists for every visible drive, even if empty.
		if _, ok := m.drives[info.Index]; !ok {
			m.drives[info.Index] = NewDriveState(info.Index, info.DriveName)
		}

		dsm := m.drives[info.Index]
		prev, hadDisc := m.known[info.Index]

		if discPresent(info) {
			// A disc is present now.
			if !hadDisc || prev != info.DiscName {
				// New disc inserted (or disc name changed — treat as new insert).
				m.known[info.Index] = info.DiscName
				dsm.SetDiscName(info.DiscName)
				// Transition to Detected if currently empty.
				if dsm.State() == StateEmpty {
					_ = dsm.TransitionTo(StateDetected)
				}
				if m.onEvent != nil {
					m.onEvent(DriveEvent{
						Type:       EventDiscInserted,
						DriveIndex: info.Index,
						DiscName:   info.DiscName,
						State:      dsm.State(),
					})
				}
			}
		} else {
			// No disc present now.
			if hadDisc {
				// Disc was ejected.
				delete(m.known, info.Index)
				dsm.ForceReset()
				if m.onEvent != nil {
					m.onEvent(DriveEvent{
						Type:       EventDiscEjected,
						DriveIndex: info.Index,
						DiscName:   prev,
						State:      dsm.State(),
					})
				}
			}
		}
	}

	// Detect drives that have disappeared entirely.
	for idx := range m.drives {
		if !seen[idx] {
			dsm := m.drives[idx]
			prev := m.known[idx]
			dsm.ForceReset()
			delete(m.known, idx)
			if m.onEvent != nil {
				m.onEvent(DriveEvent{
					Type:       EventDriveDisconnect,
					DriveIndex: idx,
					DiscName:   prev,
					State:      dsm.State(),
				})
			}
		}
	}
}

// Run performs an initial poll, then starts a ticker-based polling loop that
// calls PollOnce at the given interval. It blocks until ctx is cancelled.
func (m *Manager) Run(ctx context.Context, interval time.Duration) {
	// Poll immediately on startup so drives appear without waiting for the
	// first tick interval.
	m.PollOnce(ctx)

	// Log initial drive inventory.
	m.mu.RLock()
	if len(m.drives) == 0 {
		slog.Warn("no drives detected on initial poll")
	} else {
		for _, dsm := range m.drives {
			slog.Info("drive detected",
				"index", dsm.Index(),
				"device", dsm.DevicePath(),
				"state", dsm.State(),
				"disc", dsm.DiscName(),
			)
		}
	}
	m.mu.RUnlock()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.PollOnce(ctx)
		}
	}
}

// GetDrive returns the DriveStateMachine for the given index, or nil if unknown.
func (m *Manager) GetDrive(index int) *DriveStateMachine {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.drives[index]
}

// GetAllDrives returns all known DriveStateMachines sorted alphabetically by
// device path for deterministic display order.
func (m *Manager) GetAllDrives() []*DriveStateMachine {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]*DriveStateMachine, 0, len(m.drives))
	for _, dsm := range m.drives {
		result = append(result, dsm)
	}
	slices.SortFunc(result, func(a, b *DriveStateMachine) int {
		if a.DevicePath() < b.DevicePath() {
			return -1
		}
		if a.DevicePath() > b.DevicePath() {
			return 1
		}
		return 0
	})
	return result
}
