package makemkv

import (
	"crypto/sha256"
	"fmt"
	"sort"
	"strings"
)

// ScanFingerprint identifies the disc a scan was taken from, by what the scan
// found rather than by what the disc calls itself.
//
// A volume label is not identity. A two-disc set ships a main disc and a bonus
// disc under one label, and keying anything on the label alone serves the first
// disc's titles for the second. The title inventory — how many, which
// playlists, how long, how large — is what actually differs.
//
// Returns "" for a scan with no titles. A titleless scan describes no disc, and
// hashing one would give every empty scan the same fingerprint, making them all
// look like a single disc that keeps coming back. Callers must read "" as
// "cannot tell", never as a value to compare.
//
// This is deliberately not discdb.BuildDiscKey. That key is persisted in
// disc_mappings.disc_key and contributions.disc_key, so changing what it hashes
// would orphan every stored row.
func ScanFingerprint(scan *DiscScan) string {
	if scan == nil || len(scan.Titles) == 0 {
		return ""
	}

	// Sorted, because makemkvcon's title order is an artefact of the scan and
	// not a property of the disc.
	titles := make([]string, 0, len(scan.Titles))
	for i := range scan.Titles {
		t := &scan.Titles[i]
		titles = append(titles, strings.Join([]string{t.SourceFile(), t.Duration(), t.SizeBytes()}, "|"))
	}
	sort.Strings(titles)

	input := fmt.Sprintf("%s|%d|%s", scan.DiscName, len(scan.Titles), strings.Join(titles, ","))
	sum := sha256.Sum256([]byte(input))
	return fmt.Sprintf("%x", sum[:16])
}
