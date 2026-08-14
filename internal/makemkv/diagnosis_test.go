package makemkv

import (
	"strings"
	"testing"
)

// The real messages from a damaged Police Story 2 UHD, verbatim. The user saw
// eleven lines of SCSI jargon and could not tell which titles were lost.
func policeStory2Messages() []Message {
	const drive = "BD-RE PIONEER BD-RW BDR-XD08U 1.04 DCDL008313UC"
	readErr := func(target, offset string) Message {
		return Message{
			Code:   2003,
			Text:   "Error 'Scsi error - MEDIUM ERROR:L-EC UNCORRECTABLE ERROR' occurred while reading '" + target + "' at offset '" + offset + "'",
			Format: "Error '%1' occurred while reading '%2' at offset '%3'",
			Params: []string{"Scsi error - MEDIUM ERROR:L-EC UNCORRECTABLE ERROR", target, offset},
		}
	}

	msgs := []Message{
		{Code: 1005, Text: "MakeMKV v1.18.3 linux(x64-release) started"},
		readErr(drive, "98323398656"),
		readErr(drive, "98323398656"),
		readErr(drive, "98327330816"),
		readErr(drive, "98327369728"),
		{Code: 5084, Text: "Failed to load content hash table, unable to verify integrity of M2TS files."},
	}
	// Three error classes against the same title — POSITIONING ×4, HARDWARE ×2,
	// L-EC ×2 — which must add up to one finding of 8, not three findings.
	for i := 0; i < 8; i++ {
		msgs = append(msgs, readErr("/BDMV/STREAM/00006.m2ts", "0"))
	}
	for i := 0; i < 31; i++ {
		msgs = append(msgs, readErr("/BDMV/STREAM/00008.m2ts", "0"))
	}
	msgs = append(msgs, Message{
		Code:   3016,
		Text:   "Title #00008.m2ts was skipped",
		Format: "Title #%1 was skipped",
		Params: []string{"00008.m2ts"},
	})
	for i := 0; i < 31; i++ {
		msgs = append(msgs, readErr("/BDMV/STREAM/00007.m2ts", "0"))
	}
	msgs = append(msgs,
		Message{
			Code:   3016,
			Text:   "Title #00007.m2ts was skipped",
			Format: "Title #%1 was skipped",
			Params: []string{"00007.m2ts"},
		},
		Message{
			Code:   5063,
			Text:   "Encountered 86 errors of type 'Read Error' - see http://www.makemkv.com/errors/read/",
			Format: "Encountered %1 errors of type '%2' - see %3",
			Params: []string{"86", "Read Error", "http://www.makemkv.com/errors/read/"},
		},
	)
	return msgs
}

func findingFor(d ScanDiagnosis, target string) *ScanFinding {
	for i := range d.Findings {
		if d.Findings[i].Target == target {
			return &d.Findings[i]
		}
	}
	return nil
}

// A title MakeMKV gave up on is missing from the list entirely. That is the one
// thing the user must be told, and it was buried in the ninth SCSI line.
func TestDiagnoseNamesTheTitlesThatWereLost(t *testing.T) {
	d := Diagnose(policeStory2Messages())

	for _, want := range []string{"00007.m2ts", "00008.m2ts"} {
		f := findingFor(d, want)
		if f == nil {
			t.Fatalf("no finding for the skipped title %s", want)
		}
		if f.Kind != FindingTitleUnreadable {
			t.Errorf("%s Kind = %q, want %q", want, f.Kind, FindingTitleUnreadable)
		}
		if f.Errors != 31 {
			t.Errorf("%s Errors = %d, want 31", want, f.Errors)
		}
		if strings.Contains(f.Text, "Scsi") || strings.Contains(f.Text, "L-EC") {
			t.Errorf("%s still reads like a SCSI dump: %q", want, f.Text)
		}
		if !strings.Contains(f.Text, want) {
			t.Errorf("%s text does not name the title: %q", want, f.Text)
		}
	}
}

// A title that hit errors but still loaded is a different problem: it is in the
// list, it will rip, and it may have damaged video. Saying "lost" would be wrong
// and saying nothing would hide it.
func TestDiagnoseSeparatesDamagedTitlesFromLostOnes(t *testing.T) {
	d := Diagnose(policeStory2Messages())

	f := findingFor(d, "00006.m2ts")
	if f == nil {
		t.Fatal("no finding for the damaged-but-present title 00006.m2ts")
	}
	if f.Kind != FindingTitleDamaged {
		t.Errorf("Kind = %q, want %q", f.Kind, FindingTitleDamaged)
	}
	if f.Errors != 8 {
		t.Errorf("Errors = %d, want 8", f.Errors)
	}
}

// Reads against the drive rather than a path are the disc structure, not a
// title. Attributing them to a title would name the wrong victim.
func TestDiagnoseReportsDiscLevelReadErrorsSeparately(t *testing.T) {
	d := Diagnose(policeStory2Messages())

	var found *ScanFinding
	for i := range d.Findings {
		if d.Findings[i].Kind == FindingDiscReadErrors {
			found = &d.Findings[i]
		}
	}
	if found == nil {
		t.Fatal("the four reads against the drive itself were not reported")
	}
	if found.Errors != 4 {
		t.Errorf("Errors = %d, want 4", found.Errors)
	}
	if strings.Contains(found.Text, "PIONEER") {
		t.Errorf("the drive model leaked into the summary: %q", found.Text)
	}
}

// The summary is what the user reads first; it has to carry the count.
func TestDiagnoseSummarizesTheDamage(t *testing.T) {
	d := Diagnose(policeStory2Messages())

	if d.Summary == "" {
		t.Fatal("no summary")
	}
	if !strings.Contains(d.Summary, "2") {
		t.Errorf("summary does not say two titles were lost: %q", d.Summary)
	}
	if d.TotalReadErrors != 86 {
		t.Errorf("TotalReadErrors = %d, want 86 (MakeMKV's own total)", d.TotalReadErrors)
	}
}

// Everything MakeMKV said is still available, because it is what the user needs
// when they take the disc to MakeMKV's forum.
func TestTheRawMessagesAreKept(t *testing.T) {
	out := ScanOutput(policeStory2Messages())

	if len(out) == 0 {
		t.Fatal("the raw messages were dropped")
	}
	var sawRaw bool
	for _, w := range out {
		if strings.Contains(w.Text, "L-EC UNCORRECTABLE") {
			sawRaw = true
		}
	}
	if !sawRaw {
		t.Error("the SCSI detail is gone; it is what MakeMKV support asks for")
	}
}

// A message nobody has catalogued must survive verbatim rather than vanish
// because it did not match a pattern.
func TestDiagnoseKeepsUnrecognizedProblemsVerbatim(t *testing.T) {
	d := Diagnose([]Message{
		{Code: 1005, Text: "MakeMKV started"},
		{Code: 9999, Text: "Something nobody has seen before"},
	})

	var found bool
	for _, n := range d.Notes {
		if strings.Contains(n.Text, "Something nobody has seen before") {
			found = true
		}
	}
	if !found {
		t.Errorf("an unrecognized message was swallowed: %+v", d.Notes)
	}
}

// Surviving is not the same as raising an alarm. Uncatalogued messages are the
// common case — a healthy disc naming its Java runtime was announced as a disc
// that did not read cleanly — and the reader cannot tell one from a real fault
// when both arrive under that heading.
func TestAnUnrecognizedMessageIsNotReportedAsDamage(t *testing.T) {
	d := Diagnose([]Message{
		{Code: 1005, Text: "MakeMKV started"},
		{Code: 3344, Text: "Using Java runtime from /usr/lib/jvm/java-21-openjdk-amd64/bin/java"},
		{Code: 9999, Text: "Something nobody has seen before"},
	})

	if len(d.Findings) != 0 {
		t.Errorf("a scan with nothing wrong reported %d findings: %+v", len(d.Findings), d.Findings)
	}
	if d.Headline != "" {
		t.Errorf("Headline = %q, want nothing said about a disc that read fine", d.Headline)
	}
	if d.Summary != "" {
		t.Errorf("Summary = %q, want nothing said about a disc that read fine", d.Summary)
	}
}

// A real fault still says so, and still carries the uncatalogued messages with
// it: when something did go wrong, the unexplained line beside it is context.
func TestARealFaultStillRaisesTheAlarm(t *testing.T) {
	d := Diagnose([]Message{
		{Code: 3344, Text: "Using Java runtime from /usr/lib/jvm/java-21-openjdk-amd64/bin/java"},
		{Code: msgTitleSkipped, Text: "Title 00008.m2ts was skipped", Params: []string{"00008.m2ts"}},
	})

	if d.Headline == "" {
		t.Error("a title that could not be read said nothing")
	}
	var sawNote bool
	for _, f := range d.Findings {
		if strings.Contains(f.Text, "Java runtime") {
			sawNote = true
		}
	}
	if !sawNote {
		t.Error("the uncatalogued message was dropped from a report that had room for it")
	}
}

// The whole point is that no finding reads like a drive log. This asserts the
// rendered output for the real disc, and prints it so a human can judge it.
func TestDiagnoseReadsAsPlainLanguage(t *testing.T) {
	d := Diagnose(policeStory2Messages())

	jargon := []string{"Scsi", "SCSI", "L-EC", "offset", "0x", "PIONEER", "BD-RW"}
	t.Logf("summary: %s", d.Summary)
	for _, f := range d.Findings {
		t.Logf("  - [%s] %s", f.Kind, f.Text)
		for _, j := range jargon {
			if strings.Contains(f.Text, j) {
				t.Errorf("finding leaks %q: %s", j, f.Text)
			}
		}
	}
	for _, j := range jargon {
		if strings.Contains(d.Summary, j) {
			t.Errorf("summary leaks %q: %s", j, d.Summary)
		}
	}
}

// A clean scan must produce no notice at all.
func TestDiagnoseIsSilentOnAHealthyScan(t *testing.T) {
	d := Diagnose([]Message{
		{Code: 1005, Text: "MakeMKV started"},
		{Code: 1011, Text: "Using LibreDrive mode"},
		{Code: 3307, Text: "File 00000.mpls was added as title #4"},
		{Code: 5085, Text: "Loaded content hash table"},
		{Code: 5011, Text: "Operation successfully completed"},
	})

	if len(d.Findings) != 0 {
		t.Errorf("a clean scan produced findings: %+v", d.Findings)
	}
	if d.Summary != "" {
		t.Errorf("a clean scan produced a summary: %q", d.Summary)
	}
}

// The parse must not depend on the English text: MakeMKV localizes it, and the
// parameters are the stable part of the line.
func TestDiagnoseUsesMessageParametersNotProse(t *testing.T) {
	d := Diagnose([]Message{
		{
			Code:   2003,
			Text:   "Fehler 'Lesefehler' beim Lesen von '/BDMV/STREAM/00042.m2ts' an Position '0'",
			Format: "Error '%1' occurred while reading '%2' at offset '%3'",
			Params: []string{"Lesefehler", "/BDMV/STREAM/00042.m2ts", "0"},
		},
		{
			Code:   3016,
			Text:   "Titel #00042.m2ts wurde übersprungen",
			Format: "Title #%1 was skipped",
			Params: []string{"00042.m2ts"},
		},
	})

	f := findingFor(d, "00042.m2ts")
	if f == nil {
		t.Fatalf("localized messages were not understood: %+v", d.Findings)
	}
	if f.Kind != FindingTitleUnreadable {
		t.Errorf("Kind = %q, want %q", f.Kind, FindingTitleUnreadable)
	}
}

// A missing Java runtime describes BluForge's container, not the disc, and the
// person reading the notice cannot install anything into it. Showing it under
// "the disc did not read cleanly" sent someone looking for damage that was not
// there. It is logged and never displayed.
func TestDiagnoseSaysNothingAboutAMissingJavaRuntime(t *testing.T) {
	d := Diagnose([]Message{
		{Code: 1005, Text: "MakeMKV started"},
		{
			Code:   5081,
			Text:   "This disc requires Java runtime (JRE), but none was found. Certain functions will fail, please install Java. See http://www.makemkv.com/bdjava/ for details.",
			Format: "This disc requires Java runtime (JRE), but none was found. Certain functions will fail, please install Java. See %1 for details.",
			Params: []string{"http://www.makemkv.com/bdjava/"},
		},
	})

	if len(d.Findings) != 0 {
		t.Errorf("the notice appeared for a missing runtime: %+v", d.Findings)
	}
	if d.Summary != "" {
		t.Errorf("summary = %q, want nothing said at all", d.Summary)
	}
	for _, n := range d.Notes {
		if strings.Contains(n.Text, "bdjava") {
			t.Errorf("the message survived into the notes: %q", n.Text)
		}
	}
}

// It is still in the scan output, which is a record of what MakeMKV said rather
// than a list of problems. Leaving it out would mean the one person who can act
// on it — whoever builds the image — cannot see it without a container log.
func TestTheMissingRuntimeMessageIsStillInTheScanOutput(t *testing.T) {
	out := ScanOutput([]Message{
		{
			Code:   3344,
			Text:   "This disc requires Java runtime (JRE), but none was found. See http://www.makemkv.com/bdjava/ for details.",
			Params: []string{"http://www.makemkv.com/bdjava/"},
		},
	})

	if len(out) != 1 || !strings.Contains(out[0].Text, "bdjava") {
		t.Errorf("the scan output dropped a message MakeMKV emitted: %+v", out)
	}
}

// Real damage still reads as damage.
func TestDiagnoseStillBlamesTheDiscForReadErrors(t *testing.T) {
	d := Diagnose(policeStory2Messages())

	if !strings.Contains(strings.ToLower(d.Headline), "did not read") {
		t.Errorf("headline = %q, want it to name the disc", d.Headline)
	}
}

// A disc carrying both real damage and a suppressed message still reports the
// damage, which is the part that costs content.
func TestDiagnoseStillReportsDamageAlongsideASuppressedMessage(t *testing.T) {
	msgs := append(policeStory2Messages(), Message{
		Code:   5081,
		Text:   "This disc requires Java runtime (JRE), but none was found. See http://www.makemkv.com/bdjava/ for details.",
		Params: []string{"http://www.makemkv.com/bdjava/"},
	})

	d := Diagnose(msgs)
	if !strings.Contains(strings.ToLower(d.Headline), "did not read") {
		t.Errorf("headline = %q, want the disc damage reported", d.Headline)
	}
	for _, f := range d.Findings {
		if strings.Contains(f.Text, "Java") {
			t.Errorf("the suppressed message reappeared: %q", f.Text)
		}
	}
}
