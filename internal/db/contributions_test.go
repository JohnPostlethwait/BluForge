package db

import (
	"testing"
)

func makeTestContribution() Contribution {
	return Contribution{
		DiscKey:   "disc-key-contrib-001",
		DiscName:  "Blade Runner 2049",
		RawOutput: "raw makemkv output here",
		ScanJSON:  `{"titles":[]}`,
	}
}

func TestSaveAndGetContribution(t *testing.T) {
	store := openTestDB(t)

	c := makeTestContribution()
	id, err := store.SaveContribution(c)
	if err != nil {
		t.Fatalf("SaveContribution: %v", err)
	}
	if id <= 0 {
		t.Fatalf("SaveContribution: expected positive ID, got %d", id)
	}

	got, err := store.GetContribution(id)
	if err != nil {
		t.Fatalf("GetContribution: %v", err)
	}
	if got == nil {
		t.Fatal("GetContribution returned nil")
	}

	if got.DiscKey != c.DiscKey {
		t.Errorf("DiscKey: want %q, got %q", c.DiscKey, got.DiscKey)
	}
	if got.DiscName != c.DiscName {
		t.Errorf("DiscName: want %q, got %q", c.DiscName, got.DiscName)
	}
	if got.RawOutput != c.RawOutput {
		t.Errorf("RawOutput: want %q, got %q", c.RawOutput, got.RawOutput)
	}
	if got.ScanJSON != c.ScanJSON {
		t.Errorf("ScanJSON: want %q, got %q", c.ScanJSON, got.ScanJSON)
	}
	if got.Status != "pending" {
		t.Errorf("Status: want %q, got %q", "pending", got.Status)
	}
	if got.PRURL != "" {
		t.Errorf("PRURL: want empty, got %q", got.PRURL)
	}
	if got.TmdbID != "" {
		t.Errorf("TmdbID: want empty, got %q", got.TmdbID)
	}
	if got.ReleaseInfo != "" {
		t.Errorf("ReleaseInfo: want empty, got %q", got.ReleaseInfo)
	}
	if got.TitleLabels != "" {
		t.Errorf("TitleLabels: want empty, got %q", got.TitleLabels)
	}
	if got.ID != id {
		t.Errorf("ID: want %d, got %d", id, got.ID)
	}
	if got.CreatedAt.IsZero() {
		t.Error("CreatedAt should not be zero")
	}
	if got.UpdatedAt.IsZero() {
		t.Error("UpdatedAt should not be zero")
	}
}

func TestGetContributionNotFound(t *testing.T) {
	store := openTestDB(t)

	got, err := store.GetContribution(99999)
	if err != nil {
		t.Fatalf("GetContribution: %v", err)
	}
	if got != nil {
		t.Errorf("GetContribution: want nil for missing ID, got %+v", got)
	}
}

func TestGetContributionByDiscKey(t *testing.T) {
	store := openTestDB(t)

	c := makeTestContribution()
	id, err := store.SaveContribution(c)
	if err != nil {
		t.Fatalf("SaveContribution: %v", err)
	}

	got, err := store.GetContributionByDiscKey(c.DiscKey)
	if err != nil {
		t.Fatalf("GetContributionByDiscKey: %v", err)
	}
	if got == nil {
		t.Fatal("GetContributionByDiscKey returned nil")
	}
	if got.ID != id {
		t.Errorf("ID: want %d, got %d", id, got.ID)
	}
	if got.DiscKey != c.DiscKey {
		t.Errorf("DiscKey: want %q, got %q", c.DiscKey, got.DiscKey)
	}
}

func TestGetContributionByDiscKey_NotFound(t *testing.T) {
	store := openTestDB(t)

	got, err := store.GetContributionByDiscKey("nonexistent-disc-key")
	if err != nil {
		t.Fatalf("GetContributionByDiscKey: %v", err)
	}
	if got != nil {
		t.Errorf("GetContributionByDiscKey: want nil for missing key, got %+v", got)
	}
}

func TestListPendingContributions(t *testing.T) {
	store := openTestDB(t)

	// Insert two pending and one submitted.
	c1 := makeTestContribution()
	c1.DiscKey = "disc-key-001"
	if _, err := store.SaveContribution(c1); err != nil {
		t.Fatalf("SaveContribution c1: %v", err)
	}

	c2 := makeTestContribution()
	c2.DiscKey = "disc-key-002"
	id2, err := store.SaveContribution(c2)
	if err != nil {
		t.Fatalf("SaveContribution c2: %v", err)
	}
	if err := store.UpdateContributionStatus(id2, "submitted", "https://github.com/pr/1"); err != nil {
		t.Fatalf("UpdateContributionStatus: %v", err)
	}

	c3 := makeTestContribution()
	c3.DiscKey = "disc-key-003"
	if _, err := store.SaveContribution(c3); err != nil {
		t.Fatalf("SaveContribution c3: %v", err)
	}

	pending, err := store.ListContributions("pending")
	if err != nil {
		t.Fatalf("ListContributions pending: %v", err)
	}
	if len(pending) != 2 {
		t.Errorf("expected 2 pending contributions, got %d", len(pending))
	}

	submitted, err := store.ListContributions("submitted")
	if err != nil {
		t.Fatalf("ListContributions submitted: %v", err)
	}
	if len(submitted) != 1 {
		t.Errorf("expected 1 submitted contribution, got %d", len(submitted))
	}

	all, err := store.ListContributions("")
	if err != nil {
		t.Fatalf("ListContributions all: %v", err)
	}
	if len(all) != 3 {
		t.Errorf("expected 3 total contributions, got %d", len(all))
	}
}

func TestUpdateContributionDraft(t *testing.T) {
	store := openTestDB(t)

	c := makeTestContribution()
	id, err := store.SaveContribution(c)
	if err != nil {
		t.Fatalf("SaveContribution: %v", err)
	}

	if err := store.UpdateContributionDraft(id, "tt1234567", `{"title":"Blade Runner 2049"}`, `{"t00":"Feature Film"}`); err != nil {
		t.Fatalf("UpdateContributionDraft: %v", err)
	}

	got, err := store.GetContribution(id)
	if err != nil {
		t.Fatalf("GetContribution: %v", err)
	}
	if got == nil {
		t.Fatal("GetContribution returned nil")
	}

	if got.TmdbID != "tt1234567" {
		t.Errorf("TmdbID: want %q, got %q", "tt1234567", got.TmdbID)
	}
	if got.ReleaseInfo != `{"title":"Blade Runner 2049"}` {
		t.Errorf("ReleaseInfo: want %q, got %q", `{"title":"Blade Runner 2049"}`, got.ReleaseInfo)
	}
	if got.TitleLabels != `{"t00":"Feature Film"}` {
		t.Errorf("TitleLabels: want %q, got %q", `{"t00":"Feature Film"}`, got.TitleLabels)
	}
	// Status should remain unchanged.
	if got.Status != "pending" {
		t.Errorf("Status: want %q, got %q", "pending", got.Status)
	}
}

func TestUpdateContributionStatus(t *testing.T) {
	store := openTestDB(t)

	c := makeTestContribution()
	id, err := store.SaveContribution(c)
	if err != nil {
		t.Fatalf("SaveContribution: %v", err)
	}

	const prURL = "https://github.com/thediscdb/db/pull/42"
	if err := store.UpdateContributionStatus(id, "submitted", prURL); err != nil {
		t.Fatalf("UpdateContributionStatus: %v", err)
	}

	got, err := store.GetContribution(id)
	if err != nil {
		t.Fatalf("GetContribution: %v", err)
	}
	if got == nil {
		t.Fatal("GetContribution returned nil")
	}

	if got.Status != "submitted" {
		t.Errorf("Status: want %q, got %q", "submitted", got.Status)
	}
	if got.PRURL != prURL {
		t.Errorf("PRURL: want %q, got %q", prURL, got.PRURL)
	}
}

func TestSaveContributionDuplicateDiscKey(t *testing.T) {
	store := openTestDB(t)

	c := makeTestContribution()
	if _, err := store.SaveContribution(c); err != nil {
		t.Fatalf("SaveContribution first: %v", err)
	}

	// Saving again with the same disc_key must fail.
	_, err := store.SaveContribution(c)
	if err == nil {
		t.Error("expected error on duplicate disc_key, got nil")
	}
}
