package drivemanager

import (
	"fmt"
	"sync"
	"testing"
)

func TestDriveStateMachine_ConcurrentAccess(t *testing.T) {
	dsm := NewDriveState(0, "/dev/sr0")

	var wg sync.WaitGroup
	const goroutines = 10
	const iterations = 100

	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				dsm.SetState(StateDetected)
				_ = dsm.State()
				dsm.SetDiscName(fmt.Sprintf("disc-%d-%d", id, i))
				_ = dsm.DiscName()
				dsm.SetDriveName(fmt.Sprintf("drive-%d-%d", id, i))
				_ = dsm.DriveName()
				dsm.ForceReset()
			}
		}(g)
	}

	wg.Wait()
}
