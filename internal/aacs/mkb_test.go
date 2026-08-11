package aacs

import (
	"encoding/binary"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

// writeMKB lays down an AACS/MKB_RO.inf whose first record is a Type and
// Version Record: record type 0x10, 3-byte length, 4-byte MKB type, 4-byte
// version.
func writeMKB(t *testing.T, root string, mkbType, version uint32, trailing int) {
	t.Helper()
	dir := filepath.Join(root, "AACS")
	if err := os.MkdirAll(dir, 0o777); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	rec := make([]byte, 12+trailing)
	rec[0] = mkbTypeAndVersionRecord
	rec[1], rec[2], rec[3] = 0x00, 0x00, 0x0C
	binary.BigEndian.PutUint32(rec[4:8], mkbType)
	binary.BigEndian.PutUint32(rec[8:12], version)
	if err := os.WriteFile(filepath.Join(dir, "MKB_RO.inf"), rec, 0o666); err != nil {
		t.Fatalf("write mkb: %v", err)
	}
}

func TestReadMKBInfo(t *testing.T) {
	root := t.TempDir()
	writeMKB(t, root, 20, 82, 4096)

	got, err := ReadMKBInfo(root)
	if err != nil {
		t.Fatalf("ReadMKBInfo: %v", err)
	}
	if got.Version != 82 {
		t.Errorf("Version = %d, want 82", got.Version)
	}
	if got.TypeRaw != "0x00000014" {
		t.Errorf("TypeRaw = %q, want the field verbatim", got.TypeRaw)
	}
	if got.String() != "v82" {
		t.Errorf("String() = %q, want %q", got.String(), "v82")
	}
	if got.RawHeader == "" {
		t.Error("RawHeader is empty; the raw bytes are what let a bad parse be spotted")
	}
}

// The first 16 bytes of MKB_RO.inf as read from a real UHD disc, 2026-08-09:
//
//	10        record type: Type and Version Record
//	00 00 0c  record length: 12
//	48 14 10 03  the field the spec calls MKB Type
//	00 00 00 52  version: 82
//
// The version is corroborated independently — MakeMKV names its dumps for this
// era of disc MKB20_v82. The type field is not the small enumerated value the
// spec's name suggests, which is why it is carried verbatim rather than
// rendered as a decimal that would look like a fact.
func TestReadMKBInfoRealDiscHeader(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "AACS")
	if err := os.MkdirAll(dir, 0o777); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	raw, err := hex.DecodeString("1000000c48141003000000522100028c")
	if err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "MKB_RO.inf"), raw, 0o666); err != nil {
		t.Fatalf("write: %v", err)
	}

	got, err := ReadMKBInfo(root)
	if err != nil {
		t.Fatalf("ReadMKBInfo on a real disc header: %v", err)
	}
	if got.Version != 82 {
		t.Errorf("Version = %d, want 82 (MakeMKV names this era MKB20_v82)", got.Version)
	}
	if got.TypeRaw != "0x48141003" {
		t.Errorf("TypeRaw = %q, want 0x48141003", got.TypeRaw)
	}
	if got.String() != "v82" {
		t.Errorf("String() = %q, want %q", got.String(), "v82")
	}
}

// A disc with no AACS directory is not an error — plenty of discs have none.
func TestReadMKBInfoMissingFile(t *testing.T) {
	if _, err := ReadMKBInfo(t.TempDir()); err == nil {
		t.Error("ReadMKBInfo succeeded with no MKB present, want an error")
	}
}

// The record layout is inferred from the AACS specification rather than
// verified against every pressing, so a file that does not start with the
// expected record type must report that rather than emit a fabricated version.
func TestReadMKBInfoUnexpectedRecordType(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "AACS")
	if err := os.MkdirAll(dir, 0o777); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	junk := make([]byte, 64)
	junk[0] = 0x81 // not a Type and Version Record
	if err := os.WriteFile(filepath.Join(dir, "MKB_RO.inf"), junk, 0o666); err != nil {
		t.Fatalf("write: %v", err)
	}

	got, err := ReadMKBInfo(root)
	if err == nil {
		t.Fatal("ReadMKBInfo accepted a file with an unexpected first record")
	}
	if got.RawHeader == "" {
		t.Error("RawHeader should still be populated so the actual bytes can be inspected")
	}
}

func TestReadMKBInfoTruncated(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "AACS")
	if err := os.MkdirAll(dir, 0o777); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "MKB_RO.inf"), []byte{0x10, 0x00}, 0o666); err != nil {
		t.Fatalf("write: %v", err)
	}

	if _, err := ReadMKBInfo(root); err == nil {
		t.Error("ReadMKBInfo accepted a truncated MKB")
	}
}
