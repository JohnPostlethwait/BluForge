package aacs

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
)

// mkbTypeAndVersionRecord is the record type that opens a Media Key Block.
//
// Layout, per the AACS specification:
//
//	byte  0     record type (0x10)
//	bytes 1-3   record length, big endian (0x00000C)
//	bytes 4-7   MKB type, big endian
//	bytes 8-11  version number, big endian
const mkbTypeAndVersionRecord = 0x10

// mkbHeaderBytes is how much of the file is read. Only the first record is
// needed, and an MKB_RO.inf is several megabytes.
const mkbHeaderBytes = 16

// MKBInfo identifies the Media Key Block a disc carries.
//
// The MKB version is the most useful single field for spotting a pattern across
// discs — whether the ones with spurious AACS directories share a pressing
// generation. Reading it from the disc means it is recorded even when a disc
// recovers successfully, which is exactly when MakeMKV writes no diagnostic
// dump to parse it out of.
type MKBInfo struct {
	Type    uint32 `json:"type"`
	Version uint32 `json:"version"`
	// RawHeader is the hex of the first bytes of the MKB. The record layout is
	// taken from the specification rather than verified against every pressing,
	// so the raw bytes travel alongside the interpretation and a bad parse can
	// be spotted rather than believed.
	RawHeader string `json:"raw_header"`
}

// String renders the MKB the way MakeMKV names its diagnostic dumps, e.g.
// "MKB20 v82" for MKB20_v82_<title>_<hash>.tgz.
func (m MKBInfo) String() string {
	if m.Type == 0 && m.Version == 0 {
		return ""
	}
	return fmt.Sprintf("MKB%d v%d", m.Type, m.Version)
}

// ReadMKBInfo reads the Media Key Block header from root/AACS/MKB_RO.inf.
//
// On a parse failure the returned MKBInfo still carries RawHeader, so a caller
// can record what was actually there instead of discarding the evidence.
func ReadMKBInfo(root string) (MKBInfo, error) {
	path := filepath.Join(root, "AACS", "MKB_RO.inf")

	f, err := os.Open(path)
	if err != nil {
		return MKBInfo{}, fmt.Errorf("aacs: open %s: %w", path, err)
	}
	defer f.Close()

	buf := make([]byte, mkbHeaderBytes)
	n, err := f.Read(buf)
	if n > 0 {
		buf = buf[:n]
	} else if err != nil {
		return MKBInfo{}, fmt.Errorf("aacs: read %s: %w", path, err)
	}

	info := MKBInfo{RawHeader: hex.EncodeToString(buf)}

	if len(buf) < 12 {
		return info, fmt.Errorf("aacs: %s is %d bytes, too short for a Type and Version Record", path, len(buf))
	}
	if buf[0] != mkbTypeAndVersionRecord {
		return info, fmt.Errorf(
			"aacs: %s starts with record type 0x%02x, expected 0x%02x (Type and Version Record)",
			path, buf[0], mkbTypeAndVersionRecord)
	}

	info.Type = binary.BigEndian.Uint32(buf[4:8])
	info.Version = binary.BigEndian.Uint32(buf[8:12])
	return info, nil
}
