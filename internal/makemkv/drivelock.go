package makemkv

import (
	"context"
	"sync"
)

// driveLocks decides which makemkvcon commands may run at the same time.
//
// A drive does one thing at a time: two makemkvcon processes reading the same
// disc corrupt each other's output. Work on two different drives runs at once
// only when each makemkvcon is isolated to its own drive; see Executor.claim.
//
// A drive listing (info disc:9999) is different. It probes every drive, so it
// runs alone, and so does a folder source that reads a drive through a mount.
// A salvage's ddrescue holds just its own drive, through ClaimDrive. A disc
// image or whole copy reads no drive and, isolated, holds nothing.
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

// claim takes the locks a makemkvcon command on src needs and returns the
// release function and the source argument to give makemkvcon.
//
// makemkvcon opens every drive it can see, so one drive's command can run
// beside another's only when it cannot see the other drive. With isolation that
// holds: the command is addressed by its device node and runs with that drive
// alone in its /dev (see makemkvconCommand), and claims just its own drive.
// Without isolation, or without a device path to isolate to, the command would
// reach every drive, so it claims every drive and waits for the others.
//
// A detached source — a disc image or a whole copy — reads no drive. With
// isolation its makemkvcon runs with no drive in its /dev, so it claims
// nothing and runs beside work on any drive; the returned context carries that
// to makemkvconCommand. Without isolation it would still open every drive, so
// it claims every drive like any other.
func (e *Executor) claim(ctx context.Context, src Source, devicePath string) (release func(), target string, _ context.Context) {
	if src.IsDisc() && e.isolate && devicePath != "" {
		return e.locks.one(src.DriveIndex), devTargetPrefix + devicePath, ctx
	}
	if src.Detached && e.isolate {
		return func() {}, src.Arg(), withNoDrive(ctx)
	}
	return e.locks.every(), src.Arg(), ctx
}

// ClaimDrive holds one drive for work that does not go through this executor:
// a salvage's ddrescue reading the device directly. Nothing else may drive it
// meanwhile, and a drive listing, which probes every drive, waits — the poller
// contending with an unlocked rescue took it from 14 MB/s to 2.4 MB/s. Work on any
// other drive is unaffected: an isolated makemkvcon cannot see this drive, and
// an unisolated one claims every drive and so waits too.
//
// The caller must call the returned release.
func (e *Executor) ClaimDrive(driveIndex int) (release func()) {
	return e.locks.one(driveIndex)
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
