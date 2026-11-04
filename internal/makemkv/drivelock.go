package makemkv

import (
	"context"
	"sync"
)

// driveLocks decides which makemkvcon commands may run at the same time.
//
// A drive does one thing at a time: two makemkvcon processes reading the same
// disc corrupt each other's output. Two different drives are two different
// devices, so work on one does not wait for work on another.
//
// A drive listing (info disc:9999) is different. It probes every drive, so it
// runs alone, and so does anything not tied to one drive here: a folder source,
// or a salvage's ddrescue held through LockDrive. An unlocked poll against a
// drive ddrescue was reading took a rescue from 14 MB/s to 2.4 MB/s.
//
// all is held for reading by work on one drive and for writing by work that
// needs every drive. A drive's lane is taken before all, so work queued for a
// busy drive does not sit on a read lock while it waits, delaying a listing
// that could run between the two.
type driveLocks struct {
	all sync.RWMutex

	lanesMu sync.Mutex
	lanes   map[int]*sync.Mutex

	// last is the most recent completed drive listing. Guarded by lastMu.
	lastMu sync.Mutex
	last   []DriveInfo
}

func (l *driveLocks) lane(driveIndex int) *sync.Mutex {
	l.lanesMu.Lock()
	defer l.lanesMu.Unlock()
	if l.lanes == nil {
		l.lanes = make(map[int]*sync.Mutex)
	}
	mu, ok := l.lanes[driveIndex]
	if !ok {
		mu = &sync.Mutex{}
		l.lanes[driveIndex] = mu
	}
	return mu
}

// one claims a single drive and returns the release function.
func (l *driveLocks) one(driveIndex int) func() {
	lane := l.lane(driveIndex)
	lane.Lock()
	l.all.RLock()
	return func() {
		l.all.RUnlock()
		lane.Unlock()
	}
}

// every claims all drives and returns the release function.
func (l *driveLocks) every() func() {
	l.all.Lock()
	return l.all.Unlock
}

// source claims what a command on src needs: its drive for a disc, every
// drive for a folder, which is not tied to a drive here.
func (l *driveLocks) source(src Source) func() {
	if src.IsDisc() {
		return l.one(src.DriveIndex)
	}
	return l.every()
}

func (l *driveLocks) remember(drives []DriveInfo) {
	l.lastMu.Lock()
	defer l.lastMu.Unlock()
	l.last = append([]DriveInfo(nil), drives...)
}

func (l *driveLocks) remembered() []DriveInfo {
	l.lastMu.Lock()
	defer l.lastMu.Unlock()
	return append([]DriveInfo(nil), l.last...)
}

// devicePathListing returns a drive listing to read a device path from.
//
// It lists afresh when no drive is busy. When one is, a fresh listing would
// wait for that drive's work to end — a rip of half an hour or more — so the
// last completed listing is used instead. A drive's device node does not move
// while it stays connected.
func (e *Executor) devicePathListing(ctx context.Context) ([]DriveInfo, error) {
	drives, listed, err := e.TryListDrives(ctx)
	if listed {
		return drives, err
	}
	if last := e.locks.remembered(); len(last) > 0 {
		return last, nil
	}
	return e.ListDrives(ctx)
}
