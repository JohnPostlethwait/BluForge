package drivemanager

import (
	"context"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/johnpostlethwait/bluforge/internal/makemkv"
)

// ejectConfirmDuration is how long a drive must report empty, continuously,
// before an eject is believed.
//
// Counting polls is not enough. The poller blocks on the executor mutex for the
// length of a scan or a rip, then several polls complete back to back the moment
// it is released — in production that turned a failed scan into an eject 1.7
// seconds later, for a disc still in the drive. Only elapsed time survives a
// polling cadence that collapses like that.
//
// A genuine eject is reported this much later than it happens, which nobody
// notices; a disc wrongly declared gone discards the user's release selection
// and the cached scan, which they do.
const ejectConfirmDuration = 30 * time.Second

// EventType describes the kind of drive event that occurred.
type EventType string

const (
	EventDiscInserted    EventType = "disc_inserted"
	EventDiscEjected     EventType = "disc_ejected"
	EventDriveDisconnect EventType = "drive_disconnect"
	EventStateChange     EventType = "state_change"
)

// DriveEvent carries information about a change detected on a drive.
// JSON tags use uppercase names to match the existing SSE contract consumed
// by Alpine.js in drive_detail.templ.
type DriveEvent struct {
	Type       EventType  `json:"Type"`
	DriveIndex int        `json:"DriveIndex"`
	DiscName   string     `json:"DiscName"`
	State      DriveState `json:"State"`
}

// DriveExecutor is the interface for querying MakeMKV drive information.
type DriveExecutor interface {
	ListDrives(ctx context.Context) ([]makemkv.DriveInfo, error)
	ScanDisc(ctx context.Context, driveIndex int) (*makemkv.DiscScan, error)
}

// Manager polls drives and emits events when drive state changes.
type Manager struct {
	mu     sync.RWMutex
	exec   DriveExecutor
	drives map[int]*DriveStateMachine
	known  map[int]string // last known disc name per drive index
	// absentSince records when a drive first reported empty, per drive. A drive
	// being read reports empty transiently, so an eject is only believed once
	// the absence has lasted ejectConfirmDuration.
	absentSince map[int]time.Time
	// now is the clock, injectable so the debounce can be tested without
	// sleeping through it.
	now     func() time.Time
	onEvent func(DriveEvent)
	ready   bool // true after the first poll completes
}

// NewManager creates a new Manager with the given executor and event callback.
func NewManager(executor DriveExecutor, onEvent func(DriveEvent)) *Manager {
	return &Manager{
		exec:        executor,
		drives:      make(map[int]*DriveStateMachine),
		known:       make(map[int]string),
		absentSince: make(map[int]time.Time),
		now:         time.Now,
		onEvent:     onEvent,
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
//
// Events are collected while holding the lock and dispatched after the lock is
// released. This prevents deadlocks when the onEvent callback reads drive
// state via GetDrive/GetAllDrives (which acquire a read lock on the same mutex).
func (m *Manager) PollOnce(ctx context.Context) {
	m.mu.RLock()
	isFirst := !m.ready
	m.mu.RUnlock()

	if isFirst {
		slog.Info("polling drives via makemkvcon…")
	}

	infos, err := m.exec.ListDrives(ctx)
	if err != nil {
		slog.Error("drive poll failed", "error", err)
		return
	}

	// Collect events under the lock; fire them after unlocking.
	var pending []DriveEvent

	m.mu.Lock()

	wasReady := m.ready
	m.ready = true

	// Track which drive indices are present in this poll.
	seen := make(map[int]bool, len(infos))

	for _, info := range infos {
		// Skip phantom drive slots with no hardware attached.
		if info.DriveName == "" {
			continue
		}

		if isFirst {
			slog.Info("makemkvcon reported drive",
				"index", info.Index,
				"drive_name", info.DriveName,
				"disc_name", info.DiscName,
				"device", info.DevicePath,
				"flags", info.Flags,
			)
		}

		seen[info.Index] = true

		// Ensure a state machine exists for every visible drive, even if empty.
		if _, ok := m.drives[info.Index]; !ok {
			m.drives[info.Index] = NewDriveState(info.Index, info.DevicePath)
			m.drives[info.Index].SetDriveName(info.DriveName)
		}

		dsm := m.drives[info.Index]
		prev, hadDisc := m.known[info.Index]

		if discPresent(info) {
			// A disc is present now; any earlier empty reading was transient.
			delete(m.absentSince, info.Index)
			if !hadDisc || prev != info.DiscName {
				// New disc inserted (or disc name changed — treat as new insert).
				m.known[info.Index] = info.DiscName
				dsm.SetDiscName(info.DiscName)
				dsm.SetState(StateDetected)
				pending = append(pending, DriveEvent{
					Type:       EventDiscInserted,
					DriveIndex: info.Index,
					DiscName:   info.DiscName,
					State:      dsm.State(),
				})
			}
		} else {
			// No disc reported. That is not the same as a disc having been
			// removed: makemkvcon reports an empty drive while it is opening the
			// disc for a long operation. Acting on one such reading fired a
			// spurious eject that cleared the user's release selection mid-backup,
			// so absence has to be sustained before it counts.
			if hadDisc {
				now := m.now()
				since, seen := m.absentSince[info.Index]
				if !seen {
					m.absentSince[info.Index] = now
					since = now
				}
				if now.Sub(since) < ejectConfirmDuration {
					slog.Debug("drive reported empty, waiting for the absence to persist",
						"drive_index", info.Index, "absent_for", now.Sub(since).String())
					continue
				}

				delete(m.known, info.Index)
				delete(m.absentSince, info.Index)
				dsm.ForceReset()
				pending = append(pending, DriveEvent{
					Type:       EventDiscEjected,
					DriveIndex: info.Index,
					DiscName:   prev,
					State:      dsm.State(),
				})
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
			pending = append(pending, DriveEvent{
				Type:       EventDriveDisconnect,
				DriveIndex: idx,
				DiscName:   prev,
				State:      dsm.State(),
			})
		}
	}

	// On the first completed poll, fire a state_change event so SSE clients
	// (e.g. the dashboard) learn that ready=true and can render drives.
	if !wasReady {
		pending = append(pending, DriveEvent{
			Type: EventStateChange,
		})
	}

	m.mu.Unlock()

	// Dispatch events outside the lock so callbacks can safely read drive state.
	if m.onEvent != nil {
		for _, ev := range pending {
			m.onEvent(ev)
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
// SetDriveState overrides a drive's state and emits a state_change event.
//
// Used for states the poller cannot infer from makemkvcon output — notably
// StateRecovering, which is driven by BluForge rather than by the drive. A poll
// that finds the same disc still present leaves the state alone, so a recovering
// drive keeps its state until recovery sets it back.
func (m *Manager) SetDriveState(index int, state DriveState) {
	m.mu.RLock()
	dsm, ok := m.drives[index]
	m.mu.RUnlock()
	if !ok {
		return
	}

	dsm.SetState(state)

	if m.onEvent != nil {
		m.onEvent(DriveEvent{
			Type:       EventStateChange,
			DriveIndex: index,
			DiscName:   dsm.DiscName(),
			State:      state,
		})
	}
}

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
