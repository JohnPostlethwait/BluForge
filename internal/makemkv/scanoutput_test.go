package makemkv

import (
	"strings"
	"testing"
)

// invictusMessages is what a healthy disc reports, taken verbatim from a scan
// that BluForge announced as a disc that did not read cleanly.
func invictusMessages() []Message {
	return []Message{
		{Code: 1005, Text: "MakeMKV v1.18.3 linux(x64-release) started"},
		{Code: 1011, Text: "Using LibreDrive mode (v02.2 id=393664D791B0)"},
		{Code: 3007, Text: "Using direct disc access mode"},
		{Code: 5085, Text: "Loaded content hash table, will verify integrity of M2TS files."},
		{Code: 3344, Text: "Using Java runtime from /usr/lib/jvm/java-21-openjdk-amd64/bin/java",
			Params: []string{"/usr/lib/jvm/java-21-openjdk-amd64/bin/java"}},
		{Code: 3025, Text: "Title #00156.mpls has length of 62 seconds which is less than minimum title length of 120 seconds and was therefore skipped",
			Params: []string{"00156.mpls", "62", "120"}},
		{Code: 3025, Text: "Title #00157.mpls has length of 62 seconds which is less than minimum title length of 120 seconds and was therefore skipped",
			Params: []string{"00157.mpls", "62", "120"}},
		{Code: 3307, Text: "File 01199.mpls was added as title #0", Params: []string{"01199.mpls", "0"}},
	}
}

// This disc is fine. Every line it reports is ordinary, and saying otherwise
// sends someone looking for damage that is not there.
func TestAHealthyDiscIsNotReportedAsDamaged(t *testing.T) {
	d := Diagnose(invictusMessages())

	if len(d.Findings) != 0 {
		t.Errorf("reported %d findings for a healthy disc: %+v", len(d.Findings), d.Findings)
	}
	if d.Headline != "" || d.Summary != "" {
		t.Errorf("announced %q / %q for a disc that read fine", d.Headline, d.Summary)
	}
}

// The scan is still there to be read. Every time a scan has been questioned the
// answer was in a message somebody had decided was not worth showing.
func TestTheScanOutputKeepsEveryLine(t *testing.T) {
	out := ScanOutput(invictusMessages())

	for _, want := range []string{
		"MakeMKV v1.18.3",
		"LibreDrive",
		"Java runtime",
		"00156.mpls",
		"was added as title #0",
	} {
		var found bool
		for _, w := range out {
			if strings.Contains(w.Text, want) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("the scan output does not mention %q", want)
		}
	}
}

// A disc with unreadable sectors repeats the same read error for every retry.
// Listing it eighty times buries everything else.
func TestTheScanOutputCollapsesRepeats(t *testing.T) {
	msgs := []Message{
		{Code: 2003, Text: "Error reading data"},
		{Code: 2003, Text: "Error reading data"},
		{Code: 2003, Text: "Error reading data"},
		{Code: 1005, Text: "MakeMKV started"},
	}

	out := ScanOutput(msgs)
	if len(out) != 2 {
		t.Fatalf("got %d lines, want 2 with the repeat collapsed: %+v", len(out), out)
	}
	if out[0].Count != 3 {
		t.Errorf("Count = %d, want 3 retries of the same error", out[0].Count)
	}
}

// Ordering is the scan's own. A list reordered by severity stops matching what
// the log shows, which is what it gets compared against.
func TestTheScanOutputKeepsTheScansOrder(t *testing.T) {
	out := ScanOutput(invictusMessages())

	if len(out) == 0 {
		t.Fatal("no output at all")
	}
	if !strings.Contains(out[0].Text, "started") {
		t.Errorf("first line is %q, want the scan's own first line", out[0].Text)
	}
}
