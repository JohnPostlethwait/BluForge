package drivemanager

import (
	"sync"
)

// DriveState represents the current state of an optical drive.
type DriveState string

const (
	StateEmpty    DriveState = "empty"
	StateDetected DriveState = "detected"
)

// DriveStateMachine is a thread-safe state holder for a single optical drive.
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

// SetState sets the drive state.
func (d *DriveStateMachine) SetState(state DriveState) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.state = state
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

// ForceReset resets the drive to StateEmpty and clears the disc name.
func (d *DriveStateMachine) ForceReset() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.state = StateEmpty
	d.discName = ""
}
