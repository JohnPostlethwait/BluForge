package organizer

import (
	"regexp"
	"strings"
)

var (
	// controlCharsRe matches control characters: 0x00-0x1F and 0x7F.
	controlCharsRe = regexp.MustCompile(`[\x00-\x1F\x7F]`)

	// invalidCharsRe matches characters that are invalid on at least one major platform.
	// Excludes colon and double-quote which are handled separately for substitution.
	invalidCharsRe = regexp.MustCompile(`[<>/\\|?*]`)

	// multiSpaceRe matches runs of one or more whitespace characters (space or tab).
	multiSpaceRe = regexp.MustCompile(`[ \t]+`)

	// reservedNamesRe matches Windows reserved device names as exact strings (case-insensitive).
	reservedNamesRe = regexp.MustCompile(`(?i)^(CON|PRN|AUX|NUL|COM[1-9]|LPT[1-9])$`)
)

// SanitizeFilename makes a filename safe for use on Linux, macOS, and Windows.
// It applies the following transformations in order:
//  1. Replace colons with " -"
//  2. Replace double quotes with single quotes
//  3. Strip characters invalid on any platform: < > / \ | ? *
//  4. Strip control characters (0x00–0x1F, 0x7F)
//  5. Normalize whitespace (collapse tabs/multiple spaces, trim edges)
//  6. Prefix Windows reserved names with underscore (exact match only)
//  7. Truncate to 255 bytes
func SanitizeFilename(name string) string {
	// Step 1: Replace colons with " -" (e.g. "Movie: The Sequel" -> "Movie - The Sequel").
	name = strings.ReplaceAll(name, ":", " -")

	// Step 2: Replace double quotes with single quotes.
	name = strings.ReplaceAll(name, `"`, "'")

	// Step 3: Strip platform-invalid characters.
	name = invalidCharsRe.ReplaceAllString(name, "")

	// Step 4: Strip control characters.
	name = controlCharsRe.ReplaceAllString(name, "")

	// Step 5: Collapse whitespace (tabs and spaces) and trim edges.
	name = multiSpaceRe.ReplaceAllString(name, " ")
	name = strings.TrimSpace(name)

	// Step 6: Prefix Windows reserved names with underscore.
	if reservedNamesRe.MatchString(name) {
		name = "_" + name
	}

	// Step 7: Enforce 255-byte maximum component length.
	if len(name) > 255 {
		// Truncate at a valid UTF-8 boundary.
		name = truncateToBytes(name, 255)
	}

	return name
}

// truncateToBytes truncates s to at most maxBytes bytes, respecting UTF-8 rune boundaries.
func truncateToBytes(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	// Collect rune start positions so we can find the last rune that fits entirely.
	// We stop collecting once we know a rune won't fit.
	end := 0
	for i, r := range s {
		// Compute the byte length of this rune.
		runeLen := runeByteLen(r)
		if i+runeLen > maxBytes {
			break
		}
		end = i + runeLen
	}
	return s[:end]
}

// runeByteLen returns the number of bytes used to encode r in UTF-8.
func runeByteLen(r rune) int {
	switch {
	case r < 0x80:
		return 1
	case r < 0x800:
		return 2
	case r < 0x10000:
		return 3
	default:
		return 4
	}
}
