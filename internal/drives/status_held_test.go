package drives

import (
	"context"
	"sync/atomic"
	"testing"
)

// countingProbe records which devices the status probe reached.
type countingProbe struct{ sr0, sr1 int32 }

func (c *countingProbe) probe(device string) Status {
	switch device {
	case "/dev/sr0":
		atomic.AddInt32(&c.sr0, 1)
	case "/dev/sr1":
		atomic.AddInt32(&c.sr1, 1)
	}
	return StatusDiscOK
}

// The BU40N reset twice under a rip on 2026-11-20 while the watcher sent it
// CDROM_DRIVE_STATUS every second beside makemkvcon. The status probe is a
// command to the drive like any other, so it waits for the hold to end.
func TestTheStatusProbeLeavesAHeldDriveAlone(t *testing.T) {
	eng := &fakeEngine{isolated: true}
	b, _ := newTestBroker(t, eng)
	cp := &countingProbe{}
	b.probe = cp.probe

	held := make(chan struct{})
	letGo := make(chan struct{})
	done := make(chan struct{})
	go func() {
		_ = b.Do(context.Background(), 0, "rip", func(context.Context, *Drive) error {
			close(held)
			<-letGo
			return nil
		})
		close(done)
	}()
	<-held

	if got := b.ProbeStatus("/dev/sr0"); got != StatusUnknown {
		t.Errorf("probe of a held drive = %v, want StatusUnknown", got)
	}
	if n := atomic.LoadInt32(&cp.sr0); n != 0 {
		t.Errorf("the probe reached the held drive %d times", n)
	}
	// Isolated: the other drive is free and is probed.
	if got := b.ProbeStatus("/dev/sr1"); got != StatusDiscOK {
		t.Errorf("probe of a free drive = %v, want its reading", got)
	}

	close(letGo)
	<-done
	if got := b.ProbeStatus("/dev/sr0"); got != StatusDiscOK {
		t.Errorf("probe after the hold ended = %v, want its reading", got)
	}
}

// Without isolation every makemkvcon opens every drive, so a hold on one is a
// hold on all of them.
func TestTheStatusProbeLeavesEveryDriveAloneWithoutIsolation(t *testing.T) {
	eng := &fakeEngine{isolated: false}
	b, _ := newTestBroker(t, eng)
	cp := &countingProbe{}
	b.probe = cp.probe

	held := make(chan struct{})
	letGo := make(chan struct{})
	done := make(chan struct{})
	go func() {
		_ = b.Do(context.Background(), 0, "rip", func(context.Context, *Drive) error {
			close(held)
			<-letGo
			return nil
		})
		close(done)
	}()
	<-held

	if got := b.ProbeStatus("/dev/sr1"); got != StatusUnknown {
		t.Errorf("probe of another drive under an unisolated hold = %v, want StatusUnknown", got)
	}
	if n := atomic.LoadInt32(&cp.sr1); n != 0 {
		t.Errorf("the probe reached a drive makemkvcon had open %d times", n)
	}
	close(letGo)
	<-done
}
