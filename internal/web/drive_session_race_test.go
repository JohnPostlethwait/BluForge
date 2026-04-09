package web

import (
	"fmt"
	"sync"
	"testing"
)

func TestDriveSessionStore_ConcurrentAccess(t *testing.T) {
	store := NewDriveSessionStore()

	var wg sync.WaitGroup
	const goroutines = 10
	const iterations = 50

	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				driveIdx := i % 5
				store.Set(driveIdx, &DriveSession{
					MediaTitle: fmt.Sprintf("title-%d-%d", id, i),
					ReleaseID:  fmt.Sprintf("%d", id*1000+i),
				})
				_ = store.Get(driveIdx)
				store.Clear(driveIdx)
			}
		}(g)
	}

	wg.Wait()
}
