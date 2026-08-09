package db

import (
	"testing"
)

func TestSaveAndGetDiscDiagnostic(t *testing.T) {
	store := openTestDB(t)

	id, err := store.SaveDiscDiagnostic(DiscDiagnostic{
		DiscLabel:        "STARGATE_SG1_S4_D3",
		DriveIndex:       1,
		MKBVersion:       "20 v82",
		AACSDirPresent:   true,
		ScrambleVerdict:  "unencrypted",
		PacketsChecked:   2000,
		ScrambledPackets: 0,
		RipPath:          "backup_strip",
		Outcome:          "ok",
		DumpPath:         "/home/bluforge/MKB20_v82_STARGATE_a1b2c3.tgz",
		BackupBytes:      42_000_000_000,
	})
	if err != nil {
		t.Fatalf("SaveDiscDiagnostic: %v", err)
	}
	if id == 0 {
		t.Fatal("SaveDiscDiagnostic returned id 0")
	}

	got, err := store.GetDiscDiagnostic(id)
	if err != nil {
		t.Fatalf("GetDiscDiagnostic: %v", err)
	}
	if got == nil {
		t.Fatal("GetDiscDiagnostic returned nil")
	}
	if got.DiscLabel != "STARGATE_SG1_S4_D3" {
		t.Errorf("DiscLabel = %q, want STARGATE_SG1_S4_D3", got.DiscLabel)
	}
	if !got.AACSDirPresent {
		t.Error("AACSDirPresent = false, want true")
	}
	if got.ScrambleVerdict != "unencrypted" {
		t.Errorf("ScrambleVerdict = %q, want unencrypted", got.ScrambleVerdict)
	}
	if got.RipPath != "backup_strip" {
		t.Errorf("RipPath = %q, want backup_strip", got.RipPath)
	}
	if got.BackupBytes != 42_000_000_000 {
		t.Errorf("BackupBytes = %d, want 42000000000", got.BackupBytes)
	}
	if got.CreatedAt.IsZero() {
		t.Error("CreatedAt is zero")
	}
}

// A diagnostic row is opened before recovery runs and finished afterwards, so
// the outcome has to be updatable in place.
func TestUpdateDiscDiagnosticOutcome(t *testing.T) {
	store := openTestDB(t)

	id, err := store.SaveDiscDiagnostic(DiscDiagnostic{
		DiscLabel:       "SOME_DISC",
		ScrambleVerdict: "unencrypted",
		RipPath:         "backup_strip",
	})
	if err != nil {
		t.Fatalf("SaveDiscDiagnostic: %v", err)
	}

	if err := store.UpdateDiscDiagnosticOutcome(id, "failed", "backup exited non-zero", 12345); err != nil {
		t.Fatalf("UpdateDiscDiagnosticOutcome: %v", err)
	}

	got, err := store.GetDiscDiagnostic(id)
	if err != nil {
		t.Fatalf("GetDiscDiagnostic: %v", err)
	}
	if got.Outcome != "failed" {
		t.Errorf("Outcome = %q, want failed", got.Outcome)
	}
	if got.Detail != "backup exited non-zero" {
		t.Errorf("Detail = %q, want the failure detail", got.Detail)
	}
	if got.BackupBytes != 12345 {
		t.Errorf("BackupBytes = %d, want 12345", got.BackupBytes)
	}
}

// Recurrence is the thing worth diagnosing: three discs in one box set. Looking
// a label up must return its history, newest first.
func TestListDiscDiagnosticsByLabel(t *testing.T) {
	store := openTestDB(t)

	for _, verdict := range []string{"unencrypted", "scrambled"} {
		if _, err := store.SaveDiscDiagnostic(DiscDiagnostic{
			DiscLabel:       "REPEAT_DISC",
			ScrambleVerdict: verdict,
			RipPath:         "backup_strip",
		}); err != nil {
			t.Fatalf("SaveDiscDiagnostic: %v", err)
		}
	}
	if _, err := store.SaveDiscDiagnostic(DiscDiagnostic{
		DiscLabel: "OTHER_DISC",
		RipPath:   "direct",
	}); err != nil {
		t.Fatalf("SaveDiscDiagnostic: %v", err)
	}

	got, err := store.ListDiscDiagnosticsByLabel("REPEAT_DISC", 10)
	if err != nil {
		t.Fatalf("ListDiscDiagnosticsByLabel: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("returned %d rows, want 2", len(got))
	}
	for _, d := range got {
		if d.DiscLabel != "REPEAT_DISC" {
			t.Errorf("returned a row for %q, want only REPEAT_DISC", d.DiscLabel)
		}
	}
	// Newest first: the second insert has the higher id.
	if got[0].ID < got[1].ID {
		t.Errorf("rows are oldest-first (ids %d then %d), want newest-first", got[0].ID, got[1].ID)
	}
}

func TestGetDiscDiagnosticMissing(t *testing.T) {
	store := openTestDB(t)

	got, err := store.GetDiscDiagnostic(9999)
	if err != nil {
		t.Fatalf("GetDiscDiagnostic on a missing id returned error: %v", err)
	}
	if got != nil {
		t.Errorf("GetDiscDiagnostic on a missing id returned %+v, want nil", got)
	}
}
