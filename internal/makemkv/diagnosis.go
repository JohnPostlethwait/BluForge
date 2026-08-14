package makemkv

import (
	"fmt"
	"path"
	"sort"
	"strconv"
	"strings"
)

// Kinds of finding, in the order they matter to someone deciding whether the
// rip is worth keeping.
const (
	// FindingTitleUnreadable is a title MakeMKV gave up on. It is absent from
	// the title list entirely, which is why saying so is the whole point.
	FindingTitleUnreadable = "title-unreadable"
	// FindingTitleDamaged is a title that hit read errors but still loaded. It
	// will rip, and it may have damaged video.
	FindingTitleDamaged = "title-damaged"
	// FindingDiscReadErrors covers reads against the drive rather than a file:
	// the disc structure, not any one title.
	FindingDiscReadErrors = "disc-read-errors"
	// FindingNote is a message that did not match a known shape, kept verbatim.
	FindingNote = "note"
)

// Message codes that carry structure worth reading. Everything else falls
// through to FindingNote rather than being dropped.
const (
	msgReadError    = 2003 // Error '%1' occurred while reading '%2' at offset '%3'
	msgTitleSkipped = 3016 // Title #%1 was skipped
)

// ScanFinding is one plainly-stated problem with a disc.
type ScanFinding struct {
	Kind string `json:"kind"`
	// Text is what the user reads. No SCSI, no offsets, no drive model.
	Text string `json:"text"`
	// Target is the title the finding is about, e.g. "00008.m2ts". Empty for
	// disc-level findings and notes.
	Target string `json:"target,omitempty"`
	// Errors is how many read errors were attributed to this target.
	Errors int `json:"errors,omitempty"`
}

// ScanDiagnosis is a reading of a scan's messages in terms of what the user
// lost, rather than in terms of what the drive reported.
type ScanDiagnosis struct {
	// Headline names what kind of trouble this is. A disc that will not read
	// and a container missing a dependency are not the same problem, and one
	// heading for both told users their disc was damaged when it was not.
	Headline string `json:"headline"`
	// Summary is the one-line summary. Empty when the scan was clean.
	Summary  string        `json:"summary"`
	Findings []ScanFinding `json:"findings"`
	// TotalReadErrors is MakeMKV's own total when it reported one, otherwise
	// the number of read errors seen.
	TotalReadErrors int `json:"totalReadErrors"`
	// Details is every non-routine message verbatim. It is what MakeMKV's
	// support forum asks for, so it is kept even though nobody reads it first.
	Details []ScanWarning `json:"details"`
	// Notes are messages that are not evidence of anything wrong: which Java
	// runtime was used, a playlist skipped for duplicating another. Held apart
	// from Findings so they can be read without being read as a warning.
	Notes []ScanFinding `json:"notes,omitempty"`
}

// readErrorTarget reports what a read-error message was reading, or "" if the
// message is not a read error.
//
// The parameters are used rather than the prose because MakeMKV localizes the
// text: a German install reports the same failure with none of the same words.
func readErrorTarget(m Message) (string, bool) {
	if m.Code != msgReadError || len(m.Params) < 3 {
		return "", false
	}
	// The third parameter is a byte offset. Requiring it to be numeric keeps a
	// differently-shaped message from being read as a read error.
	if _, err := strconv.ParseUint(m.Params[2], 10, 64); err != nil {
		return "", false
	}
	return m.Params[1], true
}

// skippedTitle reports the title a skip message names, or "" if it is not one.
func skippedTitle(m Message) (string, bool) {
	if m.Code != msgTitleSkipped || len(m.Params) < 1 || m.Params[0] == "" {
		return "", false
	}
	return m.Params[0], true
}

// errorTotal reports the count from MakeMKV's own "Encountered N errors of type
// X - see <url>" summary. Matched on shape: a count, a type, and a URL.
func errorTotal(m Message) (int, bool) {
	if len(m.Params) < 3 || !strings.HasPrefix(m.Params[2], "http") {
		return 0, false
	}
	n, err := strconv.Atoi(m.Params[0])
	if err != nil {
		return 0, false
	}
	return n, true
}

// bdJavaURL appears in the message MakeMKV emits when a disc carries BD-Java
// programs and no java binary is installed. Matching the URL rather than the
// prose keeps it working on a localized install: URLs are not translated.
const bdJavaURL = "makemkv.com/bdjava"

// suppressedMessage reports a message that says nothing to the person reading
// the notice.
//
// The BD-Java warning describes BluForge's own container, which the user cannot
// install anything into. It belongs in the log, where whoever packages the image
// will see it, and nowhere else: showing it under a heading about the disc sent
// people looking for damage that was not there.
func suppressedMessage(m Message) bool {
	return strings.Contains(m.Text+" "+strings.Join(m.Params, " "), bdJavaURL)
}

// isDiscPath distinguishes a file on the disc from the drive itself. MakeMKV
// reports reads of the disc structure against the drive's model string.
func isDiscPath(target string) bool {
	return strings.HasPrefix(target, "/")
}

// Diagnose turns a scan's messages into findings a user can act on.
//
// The disc that prompted this reported eleven lines, nine of them SCSI sense
// data, and the two facts that mattered — 00007.m2ts and 00008.m2ts are not in
// your title list — were ninth and eleventh.
func Diagnose(messages []Message) ScanDiagnosis {
	d := ScanDiagnosis{
		Findings: []ScanFinding{},
		Details:  ScanWarnings(messages),
	}

	titleErrors := make(map[string]int)
	var titleOrder []string
	skipped := make(map[string]bool)
	discErrors := 0
	readErrors := 0
	reportedTotal := 0
	var notes []ScanFinding
	notesSeen := make(map[string]bool)

	for _, m := range messages {
		if target, ok := readErrorTarget(m); ok {
			readErrors++
			if !isDiscPath(target) {
				discErrors++
				continue
			}
			name := path.Base(target)
			if _, seen := titleErrors[name]; !seen {
				titleOrder = append(titleOrder, name)
			}
			titleErrors[name]++
			continue
		}
		if title, ok := skippedTitle(m); ok {
			skipped[title] = true
			if _, seen := titleErrors[title]; !seen {
				titleOrder = append(titleOrder, title)
			}
			continue
		}
		if n, ok := errorTotal(m); ok {
			reportedTotal = n
			continue
		}
		if routineScanMessages[m.Code] {
			continue
		}
		if suppressedMessage(m) {
			continue
		}
		if notesSeen[m.Text] {
			continue
		}
		notesSeen[m.Text] = true
		notes = append(notes, ScanFinding{Kind: FindingNote, Text: m.Text})
	}

	// Lost titles first, then damaged ones, each worst-first: the ordering is
	// the triage.
	sort.SliceStable(titleOrder, func(i, j int) bool {
		a, b := titleOrder[i], titleOrder[j]
		if skipped[a] != skipped[b] {
			return skipped[a]
		}
		return titleErrors[a] > titleErrors[b]
	})

	lost := 0
	damaged := 0
	for _, name := range titleOrder {
		n := titleErrors[name]
		if skipped[name] {
			lost++
			d.Findings = append(d.Findings, ScanFinding{
				Kind:   FindingTitleUnreadable,
				Target: name,
				Errors: n,
				Text: fmt.Sprintf("%s could not be read%s. MakeMKV skipped it, so it is not in the list below.",
					name, errorSuffix(n)),
			})
			continue
		}
		damaged++
		d.Findings = append(d.Findings, ScanFinding{
			Kind:   FindingTitleDamaged,
			Target: name,
			Errors: n,
			Text: fmt.Sprintf("%s loaded despite %s. It can be ripped, but may contain damaged video.",
				name, plural(n, "read error")),
		})
	}

	if discErrors > 0 {
		d.Findings = append(d.Findings, ScanFinding{
			Kind:   FindingDiscReadErrors,
			Errors: discErrors,
			Text: fmt.Sprintf("%s reading the disc's own structure, outside any single title.",
				plural(discErrors, "read error")),
		})
	}

	d.TotalReadErrors = reportedTotal
	if d.TotalReadErrors == 0 {
		d.TotalReadErrors = readErrors
	}

	// Notes never raise the alarm.
	//
	// They used to be findings, and any finding set the heading — so a healthy
	// disc that mentioned which Java runtime MakeMKV picked was announced as a
	// disc that did not read cleanly. Messages nobody has catalogued are the
	// common case, not the exceptional one, and the reader cannot tell an
	// uncatalogued message from a real fault when both arrive under that
	// heading. They stay available in the scan output, which is always there to
	// open, and say nothing about whether anything is wrong.
	if len(d.Findings) == 0 {
		d.Notes = notes
		return d
	}

	d.Findings = append(d.Findings, notes...)
	// The heading follows whatever actually cost the user something.
	d.Headline = "The disc did not read cleanly"
	d.Summary = summarize(lost, damaged, d.TotalReadErrors)
	return d
}

func summarize(lost, damaged, total int) string {
	var parts []string
	if lost > 0 {
		parts = append(parts, fmt.Sprintf("%s could not be read and %s missing below",
			plural(lost, "title"), verb(lost, "is", "are")))
	}
	if damaged > 0 {
		parts = append(parts, fmt.Sprintf("%s loaded with errors and may contain damaged video",
			plural(damaged, "title")))
	}
	if len(parts) == 0 {
		if total > 0 {
			return fmt.Sprintf("MakeMKV reported %s while reading this disc.", plural(total, "read error"))
		}
		return "MakeMKV reported problems while reading this disc."
	}
	s := strings.ToUpper(parts[0][:1]) + parts[0][1:]
	if len(parts) > 1 {
		s += "; " + parts[1]
	}
	s += "."
	if total > 0 {
		s += fmt.Sprintf(" %s in total — usually disc damage or dirt rather than a BluForge problem.",
			plural(total, "read error"))
	}
	return s
}

// errorSuffix keeps the count out of the sentence when there is nothing useful
// to report, e.g. a title skipped without any read error attributed to it.
func errorSuffix(n int) string {
	if n == 0 {
		return ""
	}
	return fmt.Sprintf(" after %s", plural(n, "read error"))
}

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

func verb(n int, singular, pl string) string {
	if n == 1 {
		return singular
	}
	return pl
}
