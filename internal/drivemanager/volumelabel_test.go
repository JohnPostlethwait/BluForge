package drivemanager

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"
)

// The probe is limited to the filesystems a disc label comes from. Probing
// every type read the far end of a Blu-ray and took long enough, on a disc
// still spinning up, to be killed before it answered.
func TestBlkidProbesOnlyDiscFilesystems(t *testing.T) {
	args := blkidArgs("/dev/sr1")
	i := slices.Index(args, "-n")
	if i < 0 || i+1 >= len(args) || args[i+1] != "udf,iso9660" {
		t.Errorf("blkid args = %v, want -n udf,iso9660", args)
	}
	if args[len(args)-1] != "/dev/sr1" {
		t.Errorf("blkid args = %v, want the device last", args)
	}
}

// slowLabeler holds every read until released, counting how many started.
type slowLabeler struct {
	mu      sync.Mutex
	started int
	release chan struct{}
}

func (s *slowLabeler) Label(ctx context.Context, _ string) (string, error) {
	s.mu.Lock()
	s.started++
	s.mu.Unlock()
	select {
	case <-s.release:
	case <-ctx.Done():
	}
	return "", errors.New("no label")
}

func (s *slowLabeler) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.started
}

// The label read has no timeout, so a drive that reports arrival after arrival
// must not stack up readers behind one that is slow. A second arrival while a
// read runs on the same device starts nothing; another device still gets its
// own read.
func TestOneLabelReadPerDeviceAtATime(t *testing.T) {
	m := NewManager(&heldExecutor{listing: withoutDisc()}, nil)
	sl := &slowLabeler{release: make(chan struct{})}
	m.labeler = sl
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	m.noteArrival(ctx, "/dev/sr1")
	m.noteArrival(ctx, "/dev/sr1")
	m.noteArrival(ctx, "/dev/sr2")

	deadline := time.Now().Add(2 * time.Second)
	for sl.count() < 2 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	// Give a wrongly started third read the chance to show up.
	time.Sleep(50 * time.Millisecond)
	if got := sl.count(); got != 2 {
		t.Errorf("label reads started = %d, want 2 (one per device)", got)
	}
}
