//go:build linux

package drives

import (
	"testing"
	"unsafe"
)

// sgIOHdr must match struct sg_io_hdr byte for byte, or the kernel reads the
// command and writes the status into the wrong fields.
func TestSgIOHdrMatchesTheKernelLayout(t *testing.T) {
	var h sgIOHdr
	want := map[string][2]uintptr{
		// field: {offset on 64-bit, offset on 32-bit}
		"dxferp":       {16, 16},
		"timeout":      {40, 28},
		"usrPtr":       {56, 40},
		"status":       {64, 44},
		"hostStatus":   {68, 48},
		"driverStatus": {70, 50},
		"info":         {80, 60},
	}
	got := map[string]uintptr{
		"dxferp":       unsafe.Offsetof(h.dxferp),
		"timeout":      unsafe.Offsetof(h.timeout),
		"usrPtr":       unsafe.Offsetof(h.usrPtr),
		"status":       unsafe.Offsetof(h.status),
		"hostStatus":   unsafe.Offsetof(h.hostStatus),
		"driverStatus": unsafe.Offsetof(h.driverStatus),
		"info":         unsafe.Offsetof(h.info),
	}
	arch := 0
	if unsafe.Sizeof(uintptr(0)) == 4 {
		arch = 1
	}
	for f, offs := range want {
		if got[f] != offs[arch] {
			t.Errorf("offset of %s = %d, want %d", f, got[f], offs[arch])
		}
	}
	if size, wantSize := unsafe.Sizeof(h), [2]uintptr{88, 64}[arch]; size != wantSize {
		t.Errorf("size = %d, want %d", size, wantSize)
	}
}
