package makemkv

import (
	"fmt"
	"strconv"
	"strings"
)

// Message codes that bound the enumeration makemkvcon performs before copying.
const (
	msgTitleAdded  = 3307 // File %1 was added as title #%2
	msgSavingTitle = 5014 // Saving %1 titles into directory %2
)

// TitleMovedError reports that the title number BluForge asked for no longer
// names the title it was chosen for.
//
// makemkvcon re-enumerates the disc on every invocation and numbers titles by
// position in that pass. A title it fails to read is left out, and everything
// after it shifts down — so an index captured during the scan can silently
// address a different title by the time the rip runs.
type TitleMovedError struct {
	Requested int
	Expected  string
	// Found is what the requested index names in this pass, empty when the
	// index is not present at all.
	Found string
	// CorrectIndex is where the expected title turned up in this pass, or -1
	// when it is not in this pass at all.
	CorrectIndex int
}

func (e *TitleMovedError) Error() string {
	if e.CorrectIndex < 0 {
		return fmt.Sprintf("makemkv: title %s is not in this pass (index %d is %s); the drive did not read it this time",
			e.Expected, e.Requested, describeFound(e.Found))
	}
	return fmt.Sprintf("makemkv: title %s moved from index %d to %d in this pass (index %d is now %s)",
		e.Expected, e.Requested, e.CorrectIndex, e.Requested, describeFound(e.Found))
}

func describeFound(found string) string {
	if found == "" {
		return "absent"
	}
	return found
}

// titleGuard watches makemkvcon's enumeration and decides whether the copy it
// is about to start would produce the title that was asked for.
//
// The check has to happen before copying begins: once makemkvcon writes the
// file there is nothing left to catch, and the wrong content lands under the
// right name. Police Story 2 produced three such files in one run.
type titleGuard struct {
	requested int
	expect    string
	seen      map[int]string
	copying   bool
}

func newTitleGuard(requested int, expect string) *titleGuard {
	return &titleGuard{requested: requested, expect: expect, seen: make(map[int]string)}
}

// observe records one event from the rip's output stream.
//
// Progress deliberately does not mark the start of copying. makemkvcon reports
// progress for its preliminary phases too — a run goes 0 to 100 and back to 0
// before the copy begins — and treating that as the enumeration boundary made
// the guard rule on an empty map within seconds of starting, failing a rip of a
// title that was about to be announced.
func (g *titleGuard) observe(ev Event) {
	if ev.Type != "MSG" || ev.Message == nil {
		return
	}
	switch ev.Message.Code {
	case msgSavingTitle:
		g.copying = true
	case msgTitleAdded:
		if source, index, ok := titleAssignment(*ev.Message); ok {
			g.seen[index] = source
		}
	}
}

// verdict returns non-nil only when drift is proven and there is nothing more
// to learn by waiting.
//
// Proving the requested index is wrong is not enough on its own: the point of
// the check is to rip the title anyway, and that needs the number it moved to.
// Titles are announced in order over several minutes, so the one we want is
// often announced after the one that proves the mismatch. Aborting on the first
// proof reported "00000.mpls is not in this pass" for a title that was about to
// be announced two lines later, and skipped the retry that would have ripped it.
func (g *titleGuard) verdict() error {
	// An expectation that cannot appear in the enumeration cannot be checked.
	// Attribute 16 is the playlist name on a UHD disc but can be a segment list
	// like "1,2,3" on standard Blu-ray, and enforcing that would fail every rip
	// on those discs rather than catch anything.
	if !checkableSource(g.expect) {
		return nil
	}

	moved := g.indexOf(g.expect)

	// Everything needed is known: the title is somewhere else in this pass, so
	// there is nothing to gain by reading the rest of the enumeration.
	if moved >= 0 && moved != g.requested {
		return g.moved(g.seen[g.requested])
	}

	// Copying is about to start, so the enumeration is complete and this is the
	// last moment to intervene. Whatever is known now is all there will be.
	if g.copying && moved != g.requested {
		return g.moved(g.seen[g.requested])
	}

	// Otherwise the enumeration is still arriving. A requested index that names
	// another title is proof of drift, but not yet of where the title went, and
	// the retry is worth more than the seconds saved by failing early.
	return nil
}

func (g *titleGuard) moved(found string) *TitleMovedError {
	return &TitleMovedError{
		Requested:    g.requested,
		Expected:     g.expect,
		Found:        found,
		CorrectIndex: g.indexOf(g.expect),
	}
}

// checkableSource reports whether an expectation names a file makemkvcon would
// announce in its enumeration.
func checkableSource(s string) bool {
	l := strings.ToLower(s)
	return strings.HasSuffix(l, ".mpls") || strings.HasSuffix(l, ".m2ts")
}

// indexOf reports where a source file landed in this pass, or -1.
func (g *titleGuard) indexOf(source string) int {
	for i, s := range g.seen {
		if s == source {
			return i
		}
	}
	return -1
}

// titleAssignment reads "File %1 was added as title #%2" from its parameters.
//
// The parameters are used rather than the text because MakeMKV localizes the
// prose; a German install reports the same enumeration with none of the same
// words, and getting this wrong means ripping the wrong title.
func titleAssignment(m Message) (string, int, bool) {
	if m.Code != msgTitleAdded || len(m.Params) < 2 {
		return "", 0, false
	}
	source := strings.TrimSpace(m.Params[0])
	index, err := strconv.Atoi(strings.TrimPrefix(strings.TrimSpace(m.Params[1]), "#"))
	if err != nil || source == "" {
		return "", 0, false
	}
	return source, index, true
}
