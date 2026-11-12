package makemkv

import (
	"strconv"
	"strings"
)

// Clips returns the clips a title plays, in play order (attribute 26), as
// MakeMKV writes them: clip numbers and ranges, e.g. "1-5,7,9-12".
//
// Some discs hide the movie among hundreds of playlists of the same length,
// each playing the same clips in a different order. The clip list is what
// tells them apart.
func (t *TitleInfo) Clips() string {
	return t.Attributes[26]
}

// ClipOrder says whether a title plays its clips in ascending order.
//
// Authoring tools usually number clips in film order, so the real playlist on a
// disc that hides it tends to be the one in order. That is a hint for which
// title to try first, not proof of which one is the movie.
type ClipOrder string

const (
	ClipOrderUnknown  ClipOrder = ""
	ClipOrderInOrder  ClipOrder = "in-order"
	ClipOrderShuffled ClipOrder = "shuffled"
)

// ClipOrderOf reads a clip list from Clips. A list it cannot parse is unknown.
func ClipOrderOf(clips string) ClipOrder {
	if strings.TrimSpace(clips) == "" {
		return ClipOrderUnknown
	}
	order := ClipOrderInOrder
	last := -1
	for _, part := range strings.Split(clips, ",") {
		first, end, ok := parseClipRange(strings.TrimSpace(part))
		if !ok {
			return ClipOrderUnknown
		}
		if first <= last || end < first {
			order = ClipOrderShuffled
		}
		last = end
	}
	return order
}

func parseClipRange(s string) (first, end int, ok bool) {
	lo, hi, isRange := strings.Cut(s, "-")
	first, err := strconv.Atoi(lo)
	if err != nil {
		return 0, 0, false
	}
	if !isRange {
		return first, first, true
	}
	end, err = strconv.Atoi(hi)
	if err != nil {
		return 0, 0, false
	}
	return first, end, true
}
