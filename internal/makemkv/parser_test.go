package makemkv

import (
	"strings"
	"testing"

	"github.com/johnpostlethwait/bluforge/testutil"
)

func TestParseDRVLine(t *testing.T) {
	line := `DRV:0,2,999,1,"BD-RE HL-DT-ST BD-RE  WH16NS40","DEADPOOL_2","/dev/sr0"`
	event, err := ParseLine(line)
	if err != nil {
		t.Fatalf("ParseLine returned error: %v", err)
	}
	if event.Type != "DRV" {
		t.Errorf("expected type DRV, got %q", event.Type)
	}
	if event.Drive == nil {
		t.Fatal("expected Drive to be non-nil")
	}
	if event.Drive.Index != 0 {
		t.Errorf("expected Index=0, got %d", event.Drive.Index)
	}
	if event.Drive.DriveName != "BD-RE HL-DT-ST BD-RE  WH16NS40" {
		t.Errorf("unexpected DriveName: %q", event.Drive.DriveName)
	}
	if event.Drive.DiscName != "DEADPOOL_2" {
		t.Errorf("unexpected DiscName: %q", event.Drive.DiscName)
	}
}

func TestParseTINFOLine(t *testing.T) {
	lines := []string{
		`TINFO:0,9,0,"1:59:45"`,
		`TINFO:0,11,0,"57344761856"`,
		`TINFO:0,27,0,"title_t00.mkv"`,
	}

	// Gather all events for title 0 by parsing all three lines.
	titles := make(map[int]*TitleInfo)
	for _, line := range lines {
		event, err := ParseLine(line)
		if err != nil {
			t.Fatalf("ParseLine(%q) error: %v", line, err)
		}
		if event.Type != "TINFO" {
			t.Errorf("expected TINFO, got %q", event.Type)
		}
		if event.Title == nil {
			t.Fatal("expected Title to be non-nil")
		}
		ti := titles[event.Title.Index]
		if ti == nil {
			ti = &TitleInfo{
				Index:      event.Title.Index,
				Attributes: make(map[int]string),
			}
			titles[event.Title.Index] = ti
		}
		for k, v := range event.Title.Attributes {
			ti.Attributes[k] = v
		}
	}

	ti := titles[0]
	if ti == nil {
		t.Fatal("no title 0 found")
	}
	if ti.Duration() != "1:59:45" {
		t.Errorf("expected duration 1:59:45, got %q", ti.Duration())
	}
	if ti.SizeBytes() != "57344761856" {
		t.Errorf("expected size bytes 57344761856, got %q", ti.SizeBytes())
	}
	if ti.Filename() != "title_t00.mkv" {
		t.Errorf("expected filename title_t00.mkv, got %q", ti.Filename())
	}
}

func TestParsePRGVLine(t *testing.T) {
	line := "PRGV:125,1000,65536"
	event, err := ParseLine(line)
	if err != nil {
		t.Fatalf("ParseLine error: %v", err)
	}
	if event.Type != "PRGV" {
		t.Errorf("expected type PRGV, got %q", event.Type)
	}
	if event.Progress == nil {
		t.Fatal("expected Progress to be non-nil")
	}
	if event.Progress.Current != 125 {
		t.Errorf("expected Current=125, got %d", event.Progress.Current)
	}
	if event.Progress.Total != 1000 {
		t.Errorf("expected Total=1000, got %d", event.Progress.Total)
	}
	if event.Progress.Max != 65536 {
		t.Errorf("expected Max=65536, got %d", event.Progress.Max)
	}
}

func TestParseMSGLine(t *testing.T) {
	line := `MSG:1005,0,1,"Operation successfully completed","",""`
	event, err := ParseLine(line)
	if err != nil {
		t.Fatalf("ParseLine error: %v", err)
	}
	if event.Type != "MSG" {
		t.Errorf("expected type MSG, got %q", event.Type)
	}
	if event.Message == nil {
		t.Fatal("expected Message to be non-nil")
	}
	if event.Message.Code != 1005 {
		t.Errorf("expected Code=1005, got %d", event.Message.Code)
	}
	if event.Message.Text != "Operation successfully completed" {
		t.Errorf("unexpected Text: %q", event.Message.Text)
	}
}

func TestParseMultiLineOutput(t *testing.T) {
	input := strings.Join([]string{
		"TCOUT:3",
		`CINFO:1,0,"Blu-ray disc"`,
		`TINFO:0,9,0,"1:59:45"`,
		`TINFO:0,27,0,"title_t00.mkv"`,
		`SINFO:0,0,1,0,"V_MPEG4/ISO/AVC"`,
		`MSG:1005,0,1,"Operation successfully completed","",""`,
	}, "\n")

	events, err := ParseAll(strings.NewReader(input))
	if err != nil {
		t.Fatalf("ParseAll error: %v", err)
	}
	if len(events) != 6 {
		t.Errorf("expected 6 events, got %d", len(events))
	}

	typeOrder := []string{"TCOUT", "CINFO", "TINFO", "TINFO", "SINFO", "MSG"}
	for i, ev := range events {
		if ev.Type != typeOrder[i] {
			t.Errorf("event[%d]: expected type %q, got %q", i, typeOrder[i], ev.Type)
		}
	}

	// Verify TCOUT count
	if events[0].Count != 3 {
		t.Errorf("expected TCOUT count=3, got %d", events[0].Count)
	}
}

func TestParseSampleDriveListOutput(t *testing.T) {
	events, err := ParseAll(strings.NewReader(testutil.SampleDriveListOutput))
	if err != nil {
		t.Fatalf("ParseAll error: %v", err)
	}
	if len(events) != 3 {
		t.Errorf("expected 3 events, got %d", len(events))
	}
	for _, ev := range events {
		if ev.Type != "DRV" {
			t.Errorf("expected DRV, got %q", ev.Type)
		}
	}
	if events[0].Drive.DiscName != "DEADPOOL_2" {
		t.Errorf("expected DEADPOOL_2, got %q", events[0].Drive.DiscName)
	}
}

func TestParseSampleDiscInfoOutput(t *testing.T) {
	events, err := ParseAll(strings.NewReader(testutil.SampleDiscInfoOutput))
	if err != nil {
		t.Fatalf("ParseAll error: %v", err)
	}
	if len(events) == 0 {
		t.Fatal("expected events, got none")
	}
	// Count TINFO events
	tinfoCount := 0
	for _, ev := range events {
		if ev.Type == "TINFO" {
			tinfoCount++
		}
	}
	if tinfoCount == 0 {
		t.Error("expected at least one TINFO event")
	}
}

func TestParseSampleProgressOutput(t *testing.T) {
	events, err := ParseAll(strings.NewReader(testutil.SampleProgressOutput))
	if err != nil {
		t.Fatalf("ParseAll error: %v", err)
	}
	prgvCount := 0
	for _, ev := range events {
		if ev.Type == "PRGV" {
			prgvCount++
		}
	}
	if prgvCount != 4 {
		t.Errorf("expected 4 PRGV events, got %d", prgvCount)
	}
}
