package ripper

import (
	"syscall"
	"testing"
)

func TestCheckDiskSpaceSufficient(t *testing.T) {
	dir := t.TempDir()
	err := CheckDiskSpace(dir, 1024) // 1KB
	if err != nil {
		t.Errorf("expected no error for small size, got: %v", err)
	}
}

func TestCheckDiskSpaceInsufficient(t *testing.T) {
	dir := t.TempDir()
	err := CheckDiskSpace(dir, 1<<62) // impossibly large
	if err == nil {
		t.Error("expected error for impossibly large size")
	}
}

// availableBytes returns the actual available bytes for the given path so
// boundary tests can be constructed relative to real free space.
func availableBytes(t *testing.T, path string) int64 {
	t.Helper()
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		t.Fatalf("Statfs: %v", err)
	}
	return int64(stat.Bavail) * int64(stat.Bsize)
}

func TestCheckDiskSpace_ExactBoundary(t *testing.T) {
	dir := t.TempDir()
	avail := availableBytes(t, dir)
	// available == required exactly: should pass.
	if err := CheckDiskSpace(dir, avail); err != nil {
		t.Errorf("expected no error when required == available, got: %v", err)
	}
}

func TestCheckDiskSpace_OneByteBelowAvailable(t *testing.T) {
	dir := t.TempDir()
	avail := availableBytes(t, dir)
	if avail == 0 {
		t.Skip("no free space available; skipping boundary test")
	}
	// available is 1 byte below required: should fail.
	if err := CheckDiskSpace(dir, avail+1); err == nil {
		t.Error("expected error when required exceeds available by 1 byte")
	}
}

func TestCheckDiskSpace_Realistic50GBInsufficient(t *testing.T) {
	dir := t.TempDir()
	const required = 50 * 1024 * 1024 * 1024 // 50 GB
	const available = 48 * 1024 * 1024 * 1024 // 48 GB
	// Simulate 48 GB available < 50 GB required by checking against an
	// impossibly large floor: if the machine actually has ≥50 GB free, we use
	// a synthetic check via the boundary helper instead.
	avail := availableBytes(t, dir)
	if avail >= required {
		// Machine has plenty of space; verify the logic using the +1 approach.
		if err := CheckDiskSpace(dir, avail+1); err == nil {
			t.Error("expected error when required > available")
		}
		return
	}
	// Real available is already < 50 GB.
	_ = available
	if err := CheckDiskSpace(dir, required); err == nil {
		t.Error("expected error: 50 GB required, but less than 50 GB available")
	}
}

func TestCheckDiskSpace_Realistic50GBSufficient(t *testing.T) {
	dir := t.TempDir()
	const required = 50 * 1024 * 1024 * 1024 // 50 GB
	avail := availableBytes(t, dir)
	if avail < required {
		t.Skipf("machine has only %d bytes free; need ≥50 GB for this test", avail)
	}
	// 55 GB scenario: available (≥50 GB) exceeds 50 GB required — should pass.
	if err := CheckDiskSpace(dir, required); err != nil {
		t.Errorf("expected no error with sufficient space, got: %v", err)
	}
}
