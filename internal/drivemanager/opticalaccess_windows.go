//go:build windows

package drivemanager

// fileGroup has no meaning on Windows, which does not use POSIX group
// ownership for device access. The /dev/sg* diagnosis this supports is
// Linux-container-specific and simply does not apply here.
func fileGroup(string) (uint32, bool) {
	return 0, false
}
