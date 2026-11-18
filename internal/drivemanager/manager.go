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
// eject watcher's status ioctl, the one sanctioned exception, and ProbeReady
// sends the TEST UNIT READY that the status ioctl already starts with.
type DriveSource interface {
	Slots() ([]drives.Slot, error)
	Identify(ctx context.Context, idx int) (makemkv.DriveInfo, error)
	ProbeStatus(device string) drives.Status
	ProbeReady(device string) drives.Readiness
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
	// awaiting holds each drive whose disc has arrived and that has not yet
	// said it is ready. See awaitReady.
	awaiting map[int]*readyWait
	// probing marks the devices whose watcher probe is still running. A probe
	// of a hung drive blocks in the kernel; it must not hold up the others, and
	// a second one must not pile onto it.
	probing map[string]bool

	watcher *opticalWatcher
	now     func() time.Time
}

// readyWait is one arrived disc waiting for its drive to be ready.
type readyWait struct {
	since time.Time
	// reason is the drive's last not-ready answer, logged when it changes.
	reason string
}

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
		src:         src,
		onEvent:     onEvent,
		drives:      make(map[int]*DriveStateMachine),
		known:       make(map[int]string),
		ignored:     make(map[string]bool),
		names:       make(map[int]makemkv.DriveInfo),
		ignoredIdx:  make(map[int]bool),
		identifying: make(map[int]*identifyRun),
		awaiting:    make(map[int]*readyWait),
		probing:     make(map[string]bool),
		now:         time.Now,
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
	delete(m.awaiting, idx)
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
//
// makemkvcon is asked once. A disc that arrived is identified only after its
// drive answered TEST UNIT READY (see awaitReady), so makemkvcon is never
// pointed at a drive still loading a disc. Asking again was how BluForge used
// to cope with asking too early: it ran makemkvcon over and over against a
// drive spinning a disc up, and a drive that dies under that stays dead.
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
	switch {
	case info.State == makemkv.DriveStateLoading:
		slog.Warn("drive: makemkvcon says the disc is still loading; not reading it",
			"drive_index", idx, "device", info.DevicePath)
		return
	case !info.HasDisc() && m.driveReportsDisc(info):
		slog.Warn("drive: makemkvcon reports no disc, but the drive reports one loaded",
			"drive_index", idx, "device", info.DevicePath, "makemkv_state", info.State)
	default:
		m.logIdentity(info)
	}
	m.applyIdentity(info)
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

// driveReportsDisc reports whether the drive itself, through its status ioctl,
// says a disc is loaded.
func (m *Manager) driveReportsDisc(info makemkv.DriveInfo) bool {
	return info.DevicePath != "" && m.src.ProbeStatus(info.DevicePath) == StatusDiscOK
}

// logIdentity records what makemkvcon said a drive holds.
func (m *Manager) logIdentity(info makemkv.DriveInfo) {
	if info.HasDisc() {
		slog.Info("drive identified", "drive_index", info.Index, "device", info.DevicePath,
			"makemkv_state", info.State, "disc_name", info.DiscName)
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

	if slices.ContainsFunc(pending, func(ev DriveEvent) bool { return ev.Type == EventDiscInserted }) {
		slog.Info("disc arrived", "drive_index", idx, "device", info.DevicePath, "disc_name", info.DiscName)
	}
	m.dispatch(pending)
}

// watchOnce probes every tracked drive once and acts on a confirmed change.
// Ignored drives are not probed. A held drive is not probed either: the broker
// answers StatusUnknown for it, so a disc pulled mid-rip shows up as the rip
// failing, and the watcher sees the drive again when the hold ends.
//
// Each drive is probed on its own goroutine. A probe of a hung drive blocks in
// the kernel until the drive is reset, and one drive's hang must not stop the
// watcher seeing an eject on another. A drive whose last probe has not
// returned is skipped. The returned WaitGroup covers the probes started.
func (m *Manager) watchOnce(ctx context.Context) *sync.WaitGroup {
	var wg sync.WaitGroup
	m.mu.Lock()
	for idx, d := range m.drives {
		device := d.DevicePath()
		if device == "" || m.probing[device] {
			continue
		}
		m.probing[device] = true
		wg.Add(1)
		go func(idx int, device string) {
			defer wg.Done()
			defer func() {
				m.mu.Lock()
				delete(m.probing, device)
				m.mu.Unlock()
			}()
			m.watchDrive(ctx, idx, device)
		}(idx, device)
	}
	m.mu.Unlock()
	return &wg
}

// watchDrive probes one drive and acts on what it says.
func (m *Manager) watchDrive(ctx context.Context, idx int, device string) {
	reading := m.src.ProbeStatus(device)
	switch m.watcher.observe(device, reading) {
	case MediaGone:
		m.notePhysicalGone(device, reading)
		return
	case MediaPresent:
		slog.Info("drive: disc detected; waiting for the drive to be ready", "drive_index", idx, "device", device)
		m.mu.Lock()
		m.awaiting[idx] = &readyWait{since: m.now()}
		m.mu.Unlock()
		m.SetDriveState(idx, StateReading)
	}
	if reading == StatusDiscOK {
		m.awaitReady(ctx, idx, device)
	}
}

// awaitReady identifies an arrived disc once its drive is ready for it.
//
// CDROM_DRIVE_STATUS reports a disc as soon as the drive says one is in the
// tray, while it is still spinning it up and reading its structure. A drive
// on Jotunheim stopped answering for good when makemkvcon was started on a
// reinserted disc a second after the status said it was there. So an arrived
// disc waits, shown as reading, until the drive answers TEST UNIT READY, asked
// once per watcher tick. There is no deadline: the drive's own answer is the
// signal, and the wait ends early only when the disc leaves.
func (m *Manager) awaitReady(ctx context.Context, idx int, device string) {
	m.mu.RLock()
	w := m.awaiting[idx]
	m.mu.RUnlock()
	if w == nil {
		return
	}

	r := m.src.ProbeReady(device)

	m.mu.Lock()
	if m.awaiting[idx] != w {
		// The disc left, or another arrived, while the drive was asked.
		m.mu.Unlock()
		return
	}
	if !r.Ready {
		changed := r.Reason != w.reason
		w.reason = r.Reason
		m.mu.Unlock()
		if changed {
			slog.Info("drive: not ready yet", "drive_index", idx, "device", device, "reason", r.Reason)
		}
		return
	}
	delete(m.awaiting, idx)
	m.mu.Unlock()

	slog.Info("drive: ready; identifying the disc", "drive_index", idx, "device", device,
		"after", m.now().Sub(w.since).Round(time.Second).String())
	go m.identify(ctx, idx)
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
// releases mounts and cancels work on it. reading is what confirmed it.
func (m *Manager) notePhysicalGone(devicePath string, reading DriveStatus) {
	disconnected := reading == StatusGone
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
	delete(m.awaiting, idx)
	dsm.ForceReset()
	delete(m.known, idx)
	if disconnected {
		m.forgetLocked(idx)
	}
	m.mu.Unlock()

	evType := EventDiscEjected
	if disconnected {
		evType = EventDriveDisconnect
		slog.Info("drive: disconnected", "drive_index", idx, "device", devicePath)
	} else {
		slog.Info("drive: disc removed; stopping work on it", "drive_index", idx, "device", devicePath,
			"disc_name", prev, "status", reading.String())
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
