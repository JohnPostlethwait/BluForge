package drivemanager

import (
	"context"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/johnpostlethwait/bluforge/internal/drives"
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
// JSON tags use uppercase names to match the existing SSE contract consumed
// by Alpine.js in drive_detail.templ.
type DriveEvent struct {
	Type       EventType  `json:"Type"`
	DriveIndex int        `json:"DriveIndex"`
	DiscName   string     `json:"DiscName"`
	State      DriveState `json:"State"`
	// DevicePath is the node the event happened to, e.g. "/dev/sr1".
	//
	// Carried on the event rather than looked up by index, because on a
	// disconnect the state machine holding it has already been removed.
	DevicePath string `json:"DevicePath"`
}

// DriveSource is what the manager learns drives from: internal/drives.
//
// None of it touches a drive outside that drive's hold. Slots reads sysfs.
// Identify runs makemkvcon on one drive under its hold. ProbeStatus is the
// eject watcher's status ioctl, the one sanctioned exception.
type DriveSource interface {
	Slots() ([]drives.Slot, error)
	Identify(ctx context.Context, idx int) (makemkv.DriveInfo, error)
	ProbeStatus(device string) drives.Status
}

// Manager tracks drives and emits events when their state changes.
//
// It no longer polls makemkvcon. A listing (`info disc:9999`) opened every
// drive, including ones mid-rip, every five seconds — the global reach that
// the drive-ownership design removes. Instead:
//
//   - which drives exist comes from sysfs, which reads no drive;
//   - a disc arriving or leaving comes from the physical watcher;
//   - what a drive and its disc are called comes from makemkvcon on that one
//     drive, under its hold, when the drive appears and when a disc arrives.
type Manager struct {
	src     DriveSource
	onEvent func(DriveEvent)

	mu     sync.RWMutex
	drives map[int]*DriveStateMachine
	known  map[int]string // disc name per drive index
	ready  bool
	// ignored holds the makemkv.DriveIdentity of every drive the user has told
	// BluForge to leave alone. See SetIgnoredDrives.
	ignored map[string]bool
	// names is each attached drive's makemkvcon name, ignored ones included,
	// learned by identifying it. Keyed by index.
	names map[int]makemkv.DriveInfo
	// ignoredIdx marks attached drives that are ignored.
	ignoredIdx map[int]bool
	// identifying holds the identify running on each drive. An identify the
	// eject watcher cancels is removed at once, so the next disc's identify is
	// not refused while the cancelled one winds down.
	identifying map[int]*identifyRun

	watcher *opticalWatcher
	now     func() time.Time
	// identifyRetry is the rest before asking makemkvcon again about a disc it
	// reported as still loading.
	identifyRetry time.Duration
}

// identifyRetryInterval is how often makemkvcon is asked again about a disc it
// reported as loading. The watcher's own cadence: the question is the same one
// the watcher just answered, asked of the program that names the disc.
const identifyRetryInterval = watchInterval

// physicalConfirmDuration is how long a physical status change must persist
// before the watcher acts on it. Short, because CDROM_DRIVE_STATUS is a
// trustworthy signal, but non-zero so a single stray reading while a drive
// settles does not fire.
const physicalConfirmDuration = 2 * time.Second

// watchInterval is the rest between physical-status probes.
const watchInterval = 1 * time.Second

// NewManager creates a Manager learning drives from src.
func NewManager(src DriveSource, onEvent func(DriveEvent)) *Manager {
	m := &Manager{
		src:           src,
		onEvent:       onEvent,
		drives:        make(map[int]*DriveStateMachine),
		known:         make(map[int]string),
		ignored:       make(map[string]bool),
		names:         make(map[int]makemkv.DriveInfo),
		ignoredIdx:    make(map[int]bool),
		identifying:   make(map[int]*identifyRun),
		now:           time.Now,
		identifyRetry: identifyRetryInterval,
	}
	m.watcher = newOpticalWatcher(func() time.Time { return m.now() }, physicalConfirmDuration)
	return m
}

// SetIgnoredDrives sets the drives BluForge leaves alone, by the drive names
// makemkvcon reports. Each is matched by makemkv.DriveIdentity, so a drive
// stays ignored when it is renumbered or its firmware is updated.
//
// It takes effect at once for drives already identified.
func (m *Manager) SetIgnoredDrives(driveNames []string) {
	set := make(map[string]bool, len(driveNames))
	for _, name := range driveNames {
		if id := makemkv.DriveIdentity(name); id != "" {
			set[id] = true
		}
	}
	m.mu.Lock()
	m.ignored = set
	var pending []DriveEvent
	var unignored []int
	for idx, info := range m.names {
		now := set[info.Identity()]
		switch {
		case now && !m.ignoredIdx[idx]:
			m.ignoredIdx[idx] = true
			delete(m.drives, idx)
			delete(m.known, idx)
		case !now && m.ignoredIdx[idx]:
			delete(m.ignoredIdx, idx)
			unignored = append(unignored, idx)
		}
	}
	m.mu.Unlock()
	m.dispatch(pending)
	for _, idx := range unignored {
		m.applyIdentity(m.names[idx])
	}
}

// ListedDrives returns every attached drive that has been identified,
// including ignored ones, for the settings page.
func (m *Manager) ListedDrives() []makemkv.DriveInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]makemkv.DriveInfo, 0, len(m.names))
	for _, info := range m.names {
		out = append(out, info)
	}
	slices.SortFunc(out, func(a, b makemkv.DriveInfo) int { return a.Index - b.Index })
	return out
}

// Ready returns true once every drive attached at startup has been identified.
func (m *Manager) Ready() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.ready
}

// Refresh finds the attached drives and identifies any not yet known, waiting
// for the identifications. Run calls it at startup.
func (m *Manager) Refresh(ctx context.Context) {
	m.refreshSlots(ctx, true)
	m.mu.Lock()
	m.ready = true
	m.mu.Unlock()
}

// Run identifies the drives attached now, then follows them until ctx is
// cancelled: sysfs for drives coming and going every interval, the watcher for
// discs every watchInterval.
func (m *Manager) Run(ctx context.Context, interval time.Duration) {
	slog.Info("finding drives…")
	m.Refresh(ctx)

	m.mu.RLock()
	n := len(m.drives)
	m.mu.RUnlock()
	if n == 0 {
		slog.Warn("no drives detected")
	}
	for _, dsm := range m.GetAllDrives() {
		slog.Info("drive detected", "index", dsm.Index(), "device", dsm.DevicePath(),
			"state", dsm.State(), "disc", dsm.DiscName(), "drive_name", dsm.DriveName())
	}
	m.dispatch([]DriveEvent{{Type: EventStateChange}})

	slotTimer := time.NewTimer(interval)
	watchTimer := time.NewTimer(watchInterval)
	defer slotTimer.Stop()
	defer watchTimer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-slotTimer.C:
			m.refreshSlots(ctx, false)
			slotTimer.Reset(interval)
		case <-watchTimer.C:
			m.watchOnce(ctx)
			watchTimer.Reset(watchInterval)
		}
	}
}

// refreshSlots compares the attached drives against those tracked. A drive
// that has appeared is identified; one that has gone is reported disconnected.
// sysfs is the kernel's own record, so an absence there is believed at once.
//
// On the first pass the identifications are waited for, so Run reports the
// drives as they are.
func (m *Manager) refreshSlots(ctx context.Context, wait bool) {
	slots, err := m.src.Slots()
	if err != nil {
		slog.Error("could not list drives from sysfs", "error", err)
		return
	}

	present := make(map[int]drives.Slot, len(slots))
	for _, s := range slots {
		present[s.Index] = s
	}

	var pending []DriveEvent
	var fresh []drives.Slot
	m.mu.Lock()
	for idx, dsm := range m.drives {
		if _, ok := present[idx]; ok {
			continue
		}
		pending = append(pending, DriveEvent{
			Type: EventDriveDisconnect, DriveIndex: idx, DiscName: m.known[idx],
			State: StateEmpty, DevicePath: dsm.DevicePath(),
		})
		m.forgetLocked(idx)
	}
	for idx := range m.names {
		if _, ok := present[idx]; !ok {
			m.forgetLocked(idx)
		}
	}
	for idx, s := range present {
		if _, tracked := m.drives[idx]; tracked {
			continue
		}
		if _, known := m.names[idx]; known {
			continue // identified and ignored
		}
		if _, running := m.identifying[idx]; running {
			continue
		}
		fresh = append(fresh, s)
	}
	m.mu.Unlock()
	m.dispatch(pending)

	var wg sync.WaitGroup
	for _, s := range fresh {
		wg.Add(1)
		go func(s drives.Slot) {
			defer wg.Done()
			m.identify(ctx, s.Index)
		}(s)
	}
	if wait {
		wg.Wait()
	}
}

func (m *Manager) forgetLocked(idx int) {
	m.cancelIdentifyLocked(idx)
	if dsm, ok := m.drives[idx]; ok {
		m.watcher.forget(dsm.DevicePath())
	}
	delete(m.drives, idx)
	delete(m.known, idx)
	delete(m.names, idx)
	delete(m.ignoredIdx, idx)
}

// identify asks drive idx what it and its disc are called, under its hold,
// and records the answer. One identify per drive at a time.
func (m *Manager) identify(ctx context.Context, idx int) {
	m.mu.Lock()
	if _, running := m.identifying[idx]; running {
		m.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(ctx)
	run := &identifyRun{cancel: cancel}
	m.identifying[idx] = run
	m.mu.Unlock()

	defer func() {
		cancel()
		m.mu.Lock()
		current := m.identifying[idx] == run
		if current {
			delete(m.identifying, idx)
		}
		m.mu.Unlock()
		// A cancelled identify was already replaced, or its disc already
		// left; the drive's state is no longer its to settle.
		if current {
			m.endReading(idx)
		}
	}()

	// makemkvcon answers "loading" while a disc spins up, and the status ioctl
	// that started this identify can report the disc before makemkvcon is done
	// with it. Loading is makemkvcon saying it has no answer yet, so ask again.
	// Recording it as an empty drive lost the disc for good: the watcher had
	// already confirmed the arrival and would not ask a second time. The loop
	// ends when makemkvcon answers, or when the disc leaves and ctx is cancelled.
	saidLoading := false
	for {
		info, err := m.src.Identify(ctx, idx)
		if err != nil {
			if ctx.Err() == nil {
				slog.Error("could not identify the drive", "drive_index", idx, "error", err)
			}
			return
		}
		// Cancelled while makemkvcon answered: the disc it described has left.
		if ctx.Err() != nil {
			return
		}
		if info.State != makemkv.DriveStateLoading {
			m.logIdentity(info)
			m.applyIdentity(info)
			return
		}
		if !saidLoading {
			slog.Info("drive: the disc is still loading; asking again",
				"drive_index", idx, "device", info.DevicePath)
			saidLoading = true
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(m.identifyRetry):
		}
	}
}

// identifyRun is one identify on one drive.
type identifyRun struct {
	cancel context.CancelFunc
}

// cancelIdentifyLocked stops drive idx's identify and forgets it at once, so a
// new identify can start while the cancelled one winds down. m.mu must be held.
func (m *Manager) cancelIdentifyLocked(idx int) {
	if run, ok := m.identifying[idx]; ok {
		run.cancel()
		delete(m.identifying, idx)
	}
}

// endReading takes a drive out of StateReading when its identify ends without
// an answer: it failed, or was cancelled. The drive is then empty as far as
// BluForge knows, and says so rather than reading forever.
func (m *Manager) endReading(idx int) {
	m.mu.RLock()
	dsm, ok := m.drives[idx]
	m.mu.RUnlock()
	if ok && dsm.State() == StateReading {
		m.SetDriveState(idx, StateEmpty)
	}
}

// logIdentity records what makemkvcon said a drive holds. When it reports no
// disc while the drive itself says one is loaded, the two disagree, and BluForge
// will show an empty drive with a disc in it: that is a warning, with both
// readings.
func (m *Manager) logIdentity(info makemkv.DriveInfo) {
	if info.HasDisc() {
		slog.Info("drive identified", "drive_index", info.Index, "device", info.DevicePath,
			"makemkv_state", info.State, "disc_name", info.DiscName)
		return
	}
	if info.DevicePath != "" && m.src.ProbeStatus(info.DevicePath) == StatusDiscOK {
		slog.Warn("drive: makemkvcon reports no disc, but the drive reports one loaded",
			"drive_index", info.Index, "device", info.DevicePath, "makemkv_state", info.State)
		return
	}
	slog.Info("drive identified with no disc", "drive_index", info.Index, "device", info.DevicePath,
		"makemkv_state", info.State)
}

// applyIdentity records what makemkvcon said drive info.Index and its disc
// are, and reports a disc that is new.
func (m *Manager) applyIdentity(info makemkv.DriveInfo) {
	idx := info.Index
	var pending []DriveEvent

	m.mu.Lock()
	m.names[idx] = info
	if m.ignored[info.Identity()] {
		if !m.ignoredIdx[idx] {
			slog.Info("ignoring drive", "drive_name", info.DriveName, "index", idx, "device", info.DevicePath)
		}
		m.ignoredIdx[idx] = true
		delete(m.drives, idx)
		delete(m.known, idx)
		m.mu.Unlock()
		return
	}

	dsm, ok := m.drives[idx]
	if !ok {
		dsm = NewDriveState(idx, info.DevicePath)
		m.drives[idx] = dsm
	}
	dsm.SetDriveName(info.DriveName)

	prev, hadDisc := m.known[idx]
	switch {
	case info.HasDisc() && (!hadDisc || prev != info.DiscName):
		m.known[idx] = info.DiscName
		dsm.SetDiscName(info.DiscName)
		dsm.SetState(StateDetected)
		pending = append(pending, DriveEvent{
			Type: EventDiscInserted, DriveIndex: idx, DiscName: info.DiscName,
			State: dsm.State(), DevicePath: dsm.DevicePath(),
		})
	case info.HasDisc() && dsm.State() == StateReading:
		// The disc already known, read again: back to what it was.
		dsm.SetState(StateDetected)
		pending = append(pending, DriveEvent{
			Type: EventStateChange, DriveIndex: idx, DiscName: dsm.DiscName(),
			State: StateDetected, DevicePath: dsm.DevicePath(),
		})
	case !info.HasDisc() && !hadDisc:
		if dsm.State() != StateEmpty {
			dsm.SetState(StateEmpty)
			pending = append(pending, DriveEvent{
				Type: EventStateChange, DriveIndex: idx, State: StateEmpty, DevicePath: dsm.DevicePath(),
			})
		}
	}
	m.mu.Unlock()

	if len(pending) > 0 {
		slog.Info("disc arrived", "drive_index", idx, "device", info.DevicePath, "disc_name", info.DiscName)
	}
	m.dispatch(pending)
}

// watchOnce probes every tracked drive once and acts on a confirmed change.
// It runs beside whatever holds a drive, which is the point: a disc pulled
// mid-rip is seen, and the rip stopped. Ignored drives are not probed.
func (m *Manager) watchOnce(ctx context.Context) {
	m.mu.RLock()
	type target struct {
		idx    int
		device string
	}
	targets := make([]target, 0, len(m.drives))
	for idx, d := range m.drives {
		if p := d.DevicePath(); p != "" {
			targets = append(targets, target{idx, p})
		}
	}
	m.mu.RUnlock()

	for _, t := range targets {
		reading := m.src.ProbeStatus(t.device)
		switch m.watcher.observe(t.device, reading) {
		case MediaGone:
			m.notePhysicalGone(t.device, reading == StatusGone)
		case MediaPresent:
			slog.Info("drive: disc detected, reading it", "drive_index", t.idx, "device", t.device)
			m.SetDriveState(t.idx, StateReading)
			go m.identify(ctx, t.idx)
		}
	}
}

// SetDriveState overrides a drive's state and emits a state_change event.
//
// Used for states nothing physical reports — notably StateRecovering, which is
// driven by BluForge rather than by the drive.
func (m *Manager) SetDriveState(index int, state DriveState) {
	m.mu.RLock()
	dsm, ok := m.drives[index]
	m.mu.RUnlock()
	if !ok {
		return
	}
	dsm.SetState(state)
	m.dispatch([]DriveEvent{{
		Type: EventStateChange, DriveIndex: index, DiscName: dsm.DiscName(),
		State: state, DevicePath: dsm.DevicePath(),
	}})
}

// notePhysicalGone records a removal the watcher confirmed: the disc left, or
// the drive's node vanished. It resets the drive and fires the event that
// releases mounts and cancels work on it.
func (m *Manager) notePhysicalGone(devicePath string, disconnected bool) {
	m.mu.Lock()
	var (
		idx   int
		dsm   *DriveStateMachine
		found bool
		prev  string
	)
	for i, d := range m.drives {
		if d.DevicePath() == devicePath {
			idx, dsm, found, prev = i, d, true, m.known[i]
			break
		}
	}
	if !found {
		m.mu.Unlock()
		return
	}
	m.cancelIdentifyLocked(idx)
	dsm.ForceReset()
	delete(m.known, idx)
	if disconnected {
		m.forgetLocked(idx)
	}
	m.mu.Unlock()

	evType := EventDiscEjected
	if disconnected {
		evType = EventDriveDisconnect
	}
	m.dispatch([]DriveEvent{{
		Type: evType, DriveIndex: idx, DiscName: prev, State: dsm.State(), DevicePath: devicePath,
	}})
}

func (m *Manager) dispatch(events []DriveEvent) {
	if m.onEvent == nil {
		return
	}
	for _, ev := range events {
		m.onEvent(ev)
	}
}

// GetDrive returns the DriveStateMachine for the given index, or nil.
func (m *Manager) GetDrive(index int) *DriveStateMachine {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.drives[index]
}

// GetAllDrives returns all tracked drives sorted by device path.
func (m *Manager) GetAllDrives() []*DriveStateMachine {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]*DriveStateMachine, 0, len(m.drives))
	for _, dsm := range m.drives {
		result = append(result, dsm)
	}
	slices.SortFunc(result, func(a, b *DriveStateMachine) int {
		switch {
		case a.DevicePath() < b.DevicePath():
			return -1
		case a.DevicePath() > b.DevicePath():
			return 1
		}
		return 0
	})
	return result
}
