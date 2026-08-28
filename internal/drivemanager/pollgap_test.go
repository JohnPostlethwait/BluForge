package drivemanager

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/johnpostlethwait/bluforge/internal/makemkv"
)

// slowExecutor records when each drive listing starts and ends, and takes a
// fixed time to answer — as a real optical drive does.
type slowExecutor struct {
	mu       sync.Mutex
	duration time.Duration
	starts   []time.Time
	ends     []time.Time
}

func (s *slowExecutor) ListDrives(_ context.Context) ([]makemkv.DriveInfo, error) {
	s.mu.Lock()
	s.starts = append(s.starts, time.Now())
	s.mu.Unlock()

	time.Sleep(s.duration)

	s.mu.Lock()
	s.ends = append(s.ends, time.Now())
	s.mu.Unlock()

	return []makemkv.DriveInfo{{
		Index: 0, State: makemkv.DriveStateEmptyClosed,
		DriveName: "BD-RE", DevicePath: "/dev/sr0",
	}}, nil
}

func (s *slowExecutor) ScanDisc(_ context.Context, _ int) (*makemkv.DiscScan, error) {
	return &makemkv.DiscScan{}, nil
}

func (s *slowExecutor) gaps() []time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []time.Duration
	n := len(s.ends)
	if len(s.starts)-1 < n {
		n = len(s.starts) - 1
	}
	for i := 0; i < n; i++ {
		out = append(out, s.starts[i+1].Sub(s.ends[i]))
	}
	return out
}

// The poll interval is meant to be the rest between polls. It was a ticker
// rate, and a ticker that fires while PollOnce is still running leaves a tick
// waiting — so the moment a slow poll returns, the next one starts immediately.
//
// On real hardware a listing took 16 seconds against a 5 second interval, which
// meant the drive was being probed continuously with no gap at all, for as long
// as the process ran. Optical drives do not enjoy that.
//
// Timings are deliberately loose: the assertion is "there is a rest", not a
// precise cadence.
func TestPollsLeaveAGapWhenAPollOutlastsTheInterval(t *testing.T) {
	const interval = 40 * time.Millisecond
	const pollTime = 120 * time.Millisecond

	exec := &slowExecutor{duration: pollTime}
	mgr := NewManager(exec, func(DriveEvent) {})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		mgr.Run(ctx, interval)
		close(done)
	}()

	// Long enough for several polls.
	time.Sleep(700 * time.Millisecond)
	cancel()
	<-done

	gaps := exec.gaps()
	if len(gaps) < 2 {
		t.Fatalf("only %d gaps observed; the loop did not poll enough to judge", len(gaps))
	}

	// Allow generous slack for scheduling, but a back-to-back loop produces
	// gaps near zero and cannot pass this.
	min := interval / 2
	for i, g := range gaps {
		if g < min {
			t.Errorf("gap %d was %v, want at least %v — the drive is being polled with no rest",
				i, g, min)
		}
	}
}
