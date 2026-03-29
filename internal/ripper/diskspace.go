//go:build !windows

package ripper

import (
	"fmt"
	"syscall"
)

// CheckDiskSpace returns an error if the output directory doesn't have enough space.
func CheckDiskSpace(path string, neededBytes int64) error {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return fmt.Errorf("check disk space: %w", err)
	}
	availableBytes := int64(stat.Bavail) * int64(stat.Bsize)
	if availableBytes < neededBytes {
		return fmt.Errorf("insufficient disk space: need %d bytes, have %d bytes available",
			neededBytes, availableBytes)
	}
	return nil
}
