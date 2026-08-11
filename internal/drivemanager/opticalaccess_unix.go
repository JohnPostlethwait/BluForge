//go:build !windows

package drivemanager

import (
	"os"
	"syscall"
)

// fileGroup returns the GID owning path.
func fileGroup(path string) (uint32, bool) {
	fi, err := os.Stat(path)
	if err != nil {
		return 0, false
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return st.Gid, true
}
