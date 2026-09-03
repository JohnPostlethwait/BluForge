package workflow

import (
	"fmt"
	"path/filepath"
	"strings"
)

// PathBuilder turns a directory and a file name into a sanitised relative path.
// *organizer.Organizer satisfies it; naming depends only on this much of it.
type PathBuilder interface {
	BuildPath(dirName, fileName string) string
}

// OutputPaths is the one authority for what a title's file is called.
//
// It exists because there were three: this algorithm, a copy in the web layer
// for matched titles, and a third reimplementation in JavaScript on the review
// page. The three disagreed, and the review page showed names the rip did not
// produce — including two angles of one playlist under a single name, which is
// how a 4GB cut of Kiki's Delivery Service was written over the name of the
// 27GB feature. A name BluForge generates may never collide with another name
// BluForge generates, and the page must show exactly what the rip will write.
//
// It returns one relative path per title, keyed by title index. Callers pass
// the full scanned title set and take the entries they need: the review page to
// display, the rip to write. The displayed name is also carried through the
// form into the rip, so the value shown and the value written are the same
// string, not two computations that happen to agree.
func OutputPaths(pb PathBuilder, discName, mediaTitle string, titles []TitleSelection) map[int]string {
	// A title's base name before any collision handling.
	base := func(sel TitleSelection) string {
		return buildDestName(pb, discName, mediaTitle, sel)
	}

	paths := make(map[int]string, len(titles))
	counts := make(map[string]int, len(titles))
	for _, sel := range titles {
		p := base(sel)
		paths[sel.TitleIndex] = p
		counts[p]++
	}
	if !hasCollision(counts) {
		return paths
	}

	// Two titles want the same name. Rename only those, by their source — a
	// playlist told from the raw stream it points at, or two titles matched to
	// one episode.
	collided := counts
	counts = make(map[string]int, len(titles))
	for _, sel := range titles {
		p := base(sel)
		if collided[p] >= 2 {
			p = buildDestName(pb, discName, mediaTitle, withMarker(sel, mediaTitle, sourceMarker(sel, mediaTitle)))
		}
		paths[sel.TitleIndex] = p
		counts[p]++
	}
	if !hasCollision(counts) {
		return paths
	}

	// The source did not tell them apart — both angles are the same playlist
	// name. Fall back to the one thing that always differs: the title index.
	stillCollided := counts
	for _, sel := range titles {
		p := base(sel)
		if collided[p] < 2 {
			continue
		}
		bySource := buildDestName(pb, discName, mediaTitle, withMarker(sel, mediaTitle, sourceMarker(sel, mediaTitle)))
		if stillCollided[bySource] < 2 {
			paths[sel.TitleIndex] = bySource
			continue
		}
		paths[sel.TitleIndex] = buildDestName(pb, discName, mediaTitle,
			withMarker(sel, mediaTitle, fmt.Sprintf("title %d", sel.TitleIndex)))
	}
	return paths
}

// isMatched reports whether a title is named from its media match rather than
// its source file. Both branches of naming, and the suffixing that disambiguates
// them, have to agree on this: the original bug was that the name was built from
// SourceFile while the suffix was appended to TitleName, so the suffix landed on
// a field the name never read and two angles kept one name.
func isMatched(sel TitleSelection, mediaTitle string) bool {
	return sel.TitleName != "" && mediaTitle != ""
}

func hasCollision(counts map[string]int) bool {
	for _, n := range counts {
		if n > 1 {
			return true
		}
	}
	return false
}

// sourceMarker returns the text that tells two titles with the same base name
// apart by their source.
//
// A matched title is named from its episode, so the whole source file is what
// differs (two titles matched to one episode). An unmatched title is named from
// its source file, so the extension is the difference — the 00000.mpls versus
// 00000.m2ts case. When the extension is the same too (both angles of one
// playlist), this returns nothing useful and the caller falls back to the index.
func sourceMarker(sel TitleSelection, mediaTitle string) string {
	if isMatched(sel, mediaTitle) {
		if sel.SourceFile != "" {
			return sel.SourceFile
		}
		return fmt.Sprintf("title %d", sel.TitleIndex)
	}
	if ext := strings.TrimPrefix(filepath.Ext(sel.SourceFile), "."); ext != "" {
		return ext
	}
	return fmt.Sprintf("title %d", sel.TitleIndex)
}

// withMarker appends a parenthesised marker to the field the destination name is
// actually built from — TitleName for a matched title, SourceFile otherwise.
// Appending to the wrong field is the bug this whole file exists to close: a
// marker on TitleName does nothing when the name is read from SourceFile.
func withMarker(sel TitleSelection, mediaTitle, marker string) TitleSelection {
	if isMatched(sel, mediaTitle) {
		sel.TitleName += " (" + marker + ")"
		return sel
	}
	// The extension is stripped when the path is built, so the marker is folded
	// into the name rather than left on the end.
	if sel.SourceFile != "" {
		sel.SourceFile = strings.TrimSuffix(sel.SourceFile, filepath.Ext(sel.SourceFile)) + " (" + marker + ")"
		return sel
	}
	sel.TitleName = strings.TrimSuffix(sel.TitleName, filepath.Ext(sel.TitleName)) + " (" + marker + ")"
	return sel
}

// buildDestName builds the relative output path for a single title, before any
// collision handling.
//
// Matched titles use: <MediaTitle>/<TitleName>.mkv
// Unmatched titles use: <DiscName>/<DiscName> - <SourceFile>.mkv
func buildDestName(pb PathBuilder, discName, mediaTitle string, sel TitleSelection) string {
	if sel.TitleName != "" && mediaTitle != "" {
		return pb.BuildPath(mediaTitle, sel.TitleName)
	}
	dirName := discName
	if dirName == "" {
		dirName = mediaTitle
	}
	fileName := sel.SourceFile
	if fileName == "" {
		fileName = sel.TitleName
	}
	if dirName != "" {
		fileName = dirName + " - " + fileName
	}
	return pb.BuildPath(dirName, fileName)
}
