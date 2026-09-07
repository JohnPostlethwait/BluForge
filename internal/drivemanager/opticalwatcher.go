package drivemanager

import "time"

// DriveStatus is what a single, cheap probe of an optical device node reports.
//
// It is deliberately coarser than the truth the kernel could give: the watcher
// only needs to tell "a disc is readable" from "the slot is empty or open" from
// "ask again later". NotReady and Unknown are the last of those — a busy drive
// mid-scan reports NotReady, and treating that as an eject is exactly the
// false-positive that cancelled real work in past releases.
type DriveStatus int

const (
	// StatusUnknown means the probe could not tell — no decision is made on it.
	StatusUnknown DriveStatus = iota
	// StatusDiscOK means a disc is loaded and readable.
	StatusDiscOK
	// StatusNoDisc means the tray is closed with no media.
	StatusNoDisc
	// StatusTrayOpen means the tray is open.
	StatusTrayOpen
	// StatusNotReady means the drive is present but busy or spinning up.
	StatusNotReady
	// StatusGone means the device node itself is absent (ENODEV/ENOENT).
	StatusGone
)

// MediaTransition is a confirmed change in whether readable media is present.
type MediaTransition int

const (
	MediaNoChange MediaTransition = iota
	MediaGone
	MediaPresent
)

// opticalWatcher turns per-device DriveStatus readings into confirmed
// MediaGone/MediaPresent transitions, with a confirm window that absorbs brief
// blips. It is pure state plus an injectable clock — no I/O, no goroutine — so
// the transition logic is tested without hardware.
type opticalWatcher struct {
	now        func() time.Time
	confirmFor time.Duration
	states     map[string]*watchState
}

type watchState struct {
	// confirmed is the last reading that survived the confirm window.
	confirmed DriveStatus
	// pending is a differing reading awaiting confirmation, or StatusUnknown
	// when nothing is pending.
	pending      DriveStatus
	pendingSince time.Time
}

func newOpticalWatcher(now func() time.Time, confirmFor time.Duration) *opticalWatcher {
	return &opticalWatcher{
		now:        now,
		confirmFor: confirmFor,
		states:     make(map[string]*watchState),
	}
}

// present reports whether a status means readable media is in the drive.
func present(s DriveStatus) bool { return s == StatusDiscOK }

// decisive reports whether a reading is trusted to change the confirmed state.
// NotReady (busy drive) and Unknown (probe could not tell) carry no information
// and must never move the state — a scan keeps a drive busy, and treating that
// as an eject would cancel the very work the drive is doing.
func decisive(s DriveStatus) bool {
	switch s {
	case StatusDiscOK, StatusNoDisc, StatusTrayOpen, StatusGone:
		return true
	default:
		return false
	}
}

// observe records one reading for a device and returns any confirmed transition.
func (w *opticalWatcher) observe(devicePath string, reading DriveStatus) MediaTransition {
	if !decisive(reading) {
		return MediaNoChange
	}

	st, ok := w.states[devicePath]
	if !ok {
		// First decisive reading is the baseline; a transition needs a prior.
		w.states[devicePath] = &watchState{confirmed: reading, pending: StatusUnknown}
		return MediaNoChange
	}

	if reading == st.confirmed {
		st.pending = StatusUnknown
		return MediaNoChange
	}

	// A new candidate resets the confirm clock.
	if st.pending != reading {
		st.pending = reading
		st.pendingSince = w.now()
	}
	if w.now().Sub(st.pendingSince) < w.confirmFor {
		return MediaNoChange
	}

	from := st.confirmed
	st.confirmed = reading
	st.pending = StatusUnknown
	switch {
	case present(from) && !present(reading):
		return MediaGone
	case !present(from) && present(reading):
		return MediaPresent
	default:
		return MediaNoChange
	}
}
