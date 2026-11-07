//go:build !linux

package drives

// probeStatus reports Unknown off Linux: the probe is a Linux CDROM ioctl, and
// the watcher makes no decision on Unknown.
func probeStatus(string) Status { return StatusUnknown }
