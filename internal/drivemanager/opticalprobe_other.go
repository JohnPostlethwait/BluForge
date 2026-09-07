//go:build !linux

package drivemanager

// sysProbe on non-Linux platforms reports Unknown for every device: physical
// status monitoring uses Linux CDROM ioctls. Because the watcher ignores
// Unknown readings, this disables the watcher entirely and drive state falls
// back to makemkvcon polling — which is what non-Linux dev machines already do.
type sysProbe struct{}

func newSysProbe() opticalProbe { return sysProbe{} }

func (sysProbe) Probe(string) DriveStatus { return StatusUnknown }
