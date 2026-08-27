package mpls

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// PlaylistFingerprint identifies a Blu-ray by its playlist inventory: the names
// and sizes of the MPLS files under BDMV/PLAYLIST.
//
// It exists to tell a disc apart from a repaired copy of a different disc. A
// copy is bound to a drive index, and the check that unbinds it compares volume
// labels — which cannot separate the two discs of a set that ship under one
// label. This can: it reads what is actually on the disc.
//
// Cheap, and available where a MakeMKV scan is not. A disc gets copied in the
// first place because makemkvcon fails on its AACS directory, not because the
// filesystem is unreadable — recovery copies BDMV off that same disc. Reading a
// directory costs a mount and a stat per playlist, against the tens of minutes
// and ~100GB that re-running a recovery would.
//
// Returns "" when no playlist directory can be read. That is "cannot tell", and
// callers must treat it as such: hashing nothing would give every unreadable
// disc one fingerprint and make them all look like the same disc.
func PlaylistFingerprint(root string) string {
	if root == "" {
		return ""
	}

	// Primary first, BACKUP as fallback — the same order and the same reason as
	// readFromMountPoint: on some UHD discs only one of the two is readable.
	for _, dir := range []string{
		filepath.Join(root, "BDMV", "PLAYLIST"),
		filepath.Join(root, "BDMV", "BACKUP", "PLAYLIST"),
	} {
		if fp := fingerprintPlaylistDir(dir); fp != "" {
			return fp
		}
	}
	return ""
}

// fingerprintPlaylistDir hashes the name and size of every .mpls file in dir,
// or returns "" when the directory holds none.
func fingerprintPlaylistDir(dir string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}

	items := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.EqualFold(filepath.Ext(e.Name()), ".mpls") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		items = append(items, fmt.Sprintf("%s:%d", strings.ToUpper(e.Name()), info.Size()))
	}
	if len(items) == 0 {
		return ""
	}

	// Directory order is a filesystem detail, not a property of the disc.
	sort.Strings(items)

	sum := sha256.Sum256([]byte(strings.Join(items, ",")))
	return fmt.Sprintf("%x", sum[:16])
}
