//go:build windows

package ripper

// CheckDiskSpace is a no-op stub on Windows.
func CheckDiskSpace(path string, neededBytes int64) error {
	return nil
}
