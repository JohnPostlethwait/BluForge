//go:build !linux

package drives

// probeStatus reports Unknown off Linux: the probe is a Linux CDROM ioctl, and
// the watcher makes no decision on Unknown.
func probeStatus(string) Status { return StatusUnknown }

// probeReady reports a drive as not ready off Linux, where TEST UNIT READY is
// sent through Linux's SG_IO.
func probeReady(string) Readiness { return Readiness{Reason: "not supported on this platform"} }
