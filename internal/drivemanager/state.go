package drivemanager

import (
	"fmt"
	"sync"
)

// DriveState represents the current state of an optical drive.
type DriveState string

const (
	StateEmpty      DriveState = "empty"
	StateDetected   DriveState = "detected"
	StateScanning   DriveState = "scanning"
	StateIdentified DriveState = "identified"
	StateNotFound   DriveState = "not_found"
	StateReady      DriveState = "ready"
	StateRipping    DriveState = "ripping"
	StateOrganizing DriveState = "organizing"
	StateComplete   DriveState = "complete"
	StateEjecting   DriveState = "ejecting"
)

// validTransitions defines the allowed state transitions for a drive.
var validTransitions = map[DriveState][]DriveState{
	StateEmpty:      {StateDetected},
	StateDetected:   {StateScanning},
	StateScanning:   {StateIdentified, StateNotFound},
	StateIdentified: {StateReady},
	StateNotFound:   {StateRipping},
	StateReady:      {StateRipping},
	StateRipping:    {StateOrganizing},
	StateOrganizing: {StateComplete},
	StateComplete:   {StateEjecting},
	StateEjecting:   {StateEmpty},
}

// IsValidTransition returns true if transitioning from -> to is permitted.
func IsValidTransition(from, to DriveState) bool {
	targets, ok := validTransitions[from]
	if !ok {
		return false
	}
	for _, t := range targets {
		if t == to {
			return true
		}
	}
	return false
}

// DriveStateMachine is a thread-safe state machine for a single optical drive.
type DriveStateMachine struct {
	mu         sync.RWMutex
	index      int
	devicePath string
	driveName  string // human-readable drive name, e.g. "BD-RE ASUS BW-16D1HT"
	state      DriveState
	discName   string
}

// NewDriveState creates a new DriveStateMachine starting in StateEmpty.
func NewDriveState(index int, devicePath string) *DriveStateMachine {
	return &DriveStateMachine{
		index:      index,
		devicePath: devicePath,
		state:      StateEmpty,
	}
}

// State returns the current state of the drive (read-locked).
func (d *DriveStateMachine) State() DriveState {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.state
}

// Index returns the drive index.
func (d *DriveStateMachine) Index() int {
	return d.index
}

// DevicePath returns the device path for the drive (e.g. "/dev/sr0").
func (d *DriveStateMachine) DevicePath() string {
	return d.devicePath
}

// DriveName returns the human-readable drive name (e.g. "BD-RE ASUS BW-16D1HT").
func (d *DriveStateMachine) DriveName() string {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.driveName
}

// SetDriveName sets the human-readable drive name.
func (d *DriveStateMachine) SetDriveName(name string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.driveName = name
}

// DiscName returns the disc name (read-locked).
func (d *DriveStateMachine) DiscName() string {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.discName
}

// SetDiscName sets the disc name (write-locked).
func (d *DriveStateMachine) SetDiscName(name string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.discName = name
}

// TransitionTo attempts to move the drive to newState, returning an error if
// the transition is not valid from the current state.
func (d *DriveStateMachine) TransitionTo(newState DriveState) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !IsValidTransition(d.state, newState) {
		return fmt.Errorf("invalid transition from %q to %q", d.state, newState)
	}
	d.state = newState
	return nil
}

// ForceReset resets the drive to StateEmpty and clears the disc name.
// Intended for error recovery only.
func (d *DriveStateMachine) ForceReset() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.state = StateEmpty
	d.discName = ""
}
