package ripper

import "testing"

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
