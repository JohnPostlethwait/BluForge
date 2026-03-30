package makemkv

import (
	"context"
	"strings"
	"testing"
)

// mockCmdRunner returns fixed canned output for every Run call.
type mockCmdRunner struct {
	output string
	err    error
}

func (m *mockCmdRunner) Run(_ context.Context, _ ...string) (*strings.Reader, error) {
	return strings.NewReader(m.output), m.err
}

// ---- TestExecutorListDrives ------------------------------------------------

const twoDriverOutput = `DRV:0,2,999,1,"BD-RE HL-DT-ST BD-RE  WH16NS40","DEADPOOL_2","/dev/sr0"
DRV:1,2,999,0,"BD-RE ASUS BW-16D1HT","AVENGERS_ENDGAME","/dev/sr1"
`

func TestExecutorListDrives(t *testing.T) {
	mock := &mockCmdRunner{output: twoDriverOutput}
	ex := NewExecutor(WithRunner(mock))

	drives, err := ex.ListDrives(context.Background())
	if err != nil {
		t.Fatalf("ListDrives returned unexpected error: %v", err)
	}
	if len(drives) != 2 {
		t.Fatalf("expected 2 drives, got %d", len(drives))
	}

	// First drive
	if drives[0].Index != 0 {
		t.Errorf("drive[0].Index: expected 0, got %d", drives[0].Index)
	}
	if drives[0].DiscName != "DEADPOOL_2" {
		t.Errorf("drive[0].DiscName: expected DEADPOOL_2, got %q", drives[0].DiscName)
	}

	// Second drive
	if drives[1].Index != 1 {
		t.Errorf("drive[1].Index: expected 1, got %d", drives[1].Index)
	}
	if drives[1].DiscName != "AVENGERS_ENDGAME" {
		t.Errorf("drive[1].DiscName: expected AVENGERS_ENDGAME, got %q", drives[1].DiscName)
	}
}

// ---- TestExecutorScanDisc --------------------------------------------------

// Attribute IDs used by makemkvcon:
//
//	1  = disc type
//	2  = disc name
//	9  = duration
//	27 = output filename
//	33 = source file
const scanDiscOutput = `TCOUT:2
CINFO:2,0,"DEADPOOL_2"
CINFO:1,0,"Blu-ray disc"
TINFO:0,2,0,"Deadpool 2"
TINFO:0,9,0,"1:59:45"
TINFO:0,27,0,"title_t00.mkv"
TINFO:0,33,0,"/path/to/source.m2ts"
TINFO:1,2,0,"Special Features"
TINFO:1,9,0,"0:05:30"
TINFO:1,27,0,"title_t01.mkv"
TINFO:1,33,0,"/path/to/source2.m2ts"
SINFO:0,0,1,0,"V_MPEG4/ISO/AVC"
SINFO:0,1,1,0,"A_AC3"
MSG:1005,0,1,"Operation successfully completed","",""
`

func TestExecutorScanDisc(t *testing.T) {
	mock := &mockCmdRunner{output: scanDiscOutput}
	ex := NewExecutor(WithRunner(mock))

	scan, err := ex.ScanDisc(context.Background(), 0)
	if err != nil {
		t.Fatalf("ScanDisc returned unexpected error: %v", err)
	}

	// Disc-level metadata.
	if scan.DiscName != "DEADPOOL_2" {
		t.Errorf("DiscName: expected DEADPOOL_2, got %q", scan.DiscName)
	}
	if scan.DiscType != "Blu-ray disc" {
		t.Errorf("DiscType: expected \"Blu-ray disc\", got %q", scan.DiscType)
	}
	if scan.TitleCount != 2 {
		t.Errorf("TitleCount: expected 2, got %d", scan.TitleCount)
	}
	if len(scan.Titles) != 2 {
		t.Fatalf("len(Titles): expected 2, got %d", len(scan.Titles))
	}

	// Find title 0 by index (order in slice is not guaranteed because we
	// iterate a map).
	var t0, t1 *TitleInfo
	for i := range scan.Titles {
		switch scan.Titles[i].Index {
		case 0:
			t0 = &scan.Titles[i]
		case 1:
			t1 = &scan.Titles[i]
		}
	}
	if t0 == nil {
		t.Fatal("title index 0 not found in scan")
	}
	if t1 == nil {
		t.Fatal("title index 1 not found in scan")
	}

	// Title 0 attributes.
	if t0.Name() != "Deadpool 2" {
		t.Errorf("t0.Name: expected \"Deadpool 2\", got %q", t0.Name())
	}
	if t0.Duration() != "1:59:45" {
		t.Errorf("t0.Duration: expected \"1:59:45\", got %q", t0.Duration())
	}
	if t0.Filename() != "title_t00.mkv" {
		t.Errorf("t0.Filename: expected \"title_t00.mkv\", got %q", t0.Filename())
	}
	if t0.SourceFile() != "/path/to/source.m2ts" {
		t.Errorf("t0.SourceFile (attr 33): expected \"/path/to/source.m2ts\", got %q", t0.SourceFile())
	}

	// Title 0 should have 2 streams attached.
	if len(t0.Streams) != 2 {
		t.Errorf("t0 stream count: expected 2, got %d", len(t0.Streams))
	}

	// Title 1 attributes.
	if t1.Name() != "Special Features" {
		t.Errorf("t1.Name: expected \"Special Features\", got %q", t1.Name())
	}
	if t1.SourceFile() != "/path/to/source2.m2ts" {
		t.Errorf("t1.SourceFile (attr 33): expected \"/path/to/source2.m2ts\", got %q", t1.SourceFile())
	}

	// One message should be captured.
	if len(scan.Messages) != 1 {
		t.Errorf("Messages count: expected 1, got %d", len(scan.Messages))
	}
}
