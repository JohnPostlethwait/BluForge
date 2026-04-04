package contribute

import (
	"context"
	"encoding/json"
	"testing"

	ghpkg "github.com/johnpostlethwait/bluforge/internal/github"
	"github.com/johnpostlethwait/bluforge/internal/db"
	"github.com/johnpostlethwait/bluforge/internal/makemkv"
)

// mockGitHub implements GitHubClient for testing.
type mockGitHub struct {
	user        string
	userErr     error
	forkName    string
	forkErr     error
	defaultSHA  string
	defaultErr  error
	createErr   error
	commitFiles [][]ghpkg.FileEntry
	commitErr   error
	prURL       string
	prErr       error
}

func (m *mockGitHub) GetUser(ctx context.Context) (string, error) {
	return m.user, m.userErr
}

func (m *mockGitHub) EnsureFork(ctx context.Context, owner, repo string) (string, error) {
	return m.forkName, m.forkErr
}

func (m *mockGitHub) GetDefaultBranchSHA(ctx context.Context, owner, repo string) (string, error) {
	return m.defaultSHA, m.defaultErr
}

func (m *mockGitHub) CreateBranch(ctx context.Context, owner, repo, branchName, baseSHA string) error {
	return m.createErr
}

func (m *mockGitHub) CommitFiles(ctx context.Context, owner, repo, branch string, files []ghpkg.FileEntry, message string) error {
	m.commitFiles = append(m.commitFiles, files)
	return m.commitErr
}

func (m *mockGitHub) CreatePR(ctx context.Context, upstreamOwner, upstreamRepo, head, baseBranch, title, body string) (string, error) {
	return m.prURL, m.prErr
}

func openTestStore(t *testing.T) *db.Store {
	t.Helper()
	store, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func seedContribution(t *testing.T, store *db.Store, scanOverride *makemkv.DiscScan) (int64, ReleaseInfo, []TitleLabel) {
	t.Helper()

	scan := scanOverride
	if scan == nil {
		scan = testScan()
	}

	scanData, err := json.Marshal(scan)
	if err != nil {
		t.Fatalf("marshal scan: %v", err)
	}

	ri := ReleaseInfo{
		UPC:        "883929543236",
		RegionCode: "A",
		Year:       1999,
		Format:     "Blu-ray",
		Slug:       "1999-blu-ray",
	}
	riJSON, err := json.Marshal(ri)
	if err != nil {
		t.Fatalf("marshal release info: %v", err)
	}

	labels := []TitleLabel{
		{TitleIndex: 0, Type: "MainMovie", Name: "The Matrix", FileName: "The Matrix (1999).mkv"},
		{TitleIndex: 1, Type: "Extra", Name: "Behind the Scenes", FileName: "Behind the Scenes.mkv"},
	}
	labelsJSON, err := json.Marshal(labels)
	if err != nil {
		t.Fatalf("marshal labels: %v", err)
	}

	c := db.Contribution{
		DiscKey:  "matrix-disc-key",
		DiscName: "THE_MATRIX",
		RawOutput: scan.RawOutput,
		ScanJSON: string(scanData),
	}
	id, err := store.SaveContribution(c)
	if err != nil {
		t.Fatalf("SaveContribution: %v", err)
	}

	if err := store.UpdateContributionDraft(id, "tt0133093", string(riJSON), string(labelsJSON)); err != nil {
		t.Fatalf("UpdateContributionDraft: %v", err)
	}

	return id, ri, labels
}

func TestSubmitCreatesGitHubPR(t *testing.T) {
	store := openTestStore(t)
	id, _, _ := seedContribution(t, store, nil)

	gh := &mockGitHub{
		user:       "testuser",
		forkName:   "testuser/data",
		defaultSHA: "abc123sha",
		prURL:      "https://github.com/TheDiscDb/data/pull/42",
	}

	svc := NewService(store, gh)

	prURL, err := svc.Submit(context.Background(), id, "The Matrix", 1999, "movie")
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if prURL != "https://github.com/TheDiscDb/data/pull/42" {
		t.Errorf("prURL: want %q, got %q", "https://github.com/TheDiscDb/data/pull/42", prURL)
	}

	// Should have committed exactly one batch of files.
	if len(gh.commitFiles) != 1 {
		t.Fatalf("expected 1 CommitFiles call, got %d", len(gh.commitFiles))
	}
	// Expect 4 files: release.json, disc01.json, disc01-summary.txt, raw output.
	files := gh.commitFiles[0]
	if len(files) != 4 {
		t.Errorf("expected 4 files in commit, got %d", len(files))
		for _, f := range files {
			t.Logf("  file: %s", f.Path)
		}
	}

	// Verify status updated to "submitted" with PR URL.
	got, err := store.GetContribution(id)
	if err != nil {
		t.Fatalf("GetContribution: %v", err)
	}
	if got.Status != "submitted" {
		t.Errorf("Status: want %q, got %q", "submitted", got.Status)
	}
	if got.PRURL != prURL {
		t.Errorf("PRURL: want %q, got %q", prURL, got.PRURL)
	}
}

func TestSubmitFailsMissingTmdbID(t *testing.T) {
	store := openTestStore(t)

	scan := testScan()
	scanData, err := json.Marshal(scan)
	if err != nil {
		t.Fatalf("marshal scan: %v", err)
	}

	// Save contribution without setting tmdb_id / release_info / title_labels.
	c := db.Contribution{
		DiscKey:  "matrix-no-tmdb",
		DiscName: "THE_MATRIX",
		RawOutput: scan.RawOutput,
		ScanJSON: string(scanData),
	}
	id, err := store.SaveContribution(c)
	if err != nil {
		t.Fatalf("SaveContribution: %v", err)
	}

	gh := &mockGitHub{
		user:       "testuser",
		forkName:   "testuser/data",
		defaultSHA: "abc123sha",
		prURL:      "https://github.com/TheDiscDb/data/pull/99",
	}

	svc := NewService(store, gh)

	_, err = svc.Submit(context.Background(), id, "The Matrix", 1999, "movie")
	if err == nil {
		t.Fatal("expected error for missing tmdb_id, got nil")
	}
}

func TestSubmitNotFound(t *testing.T) {
	store := openTestStore(t)

	gh := &mockGitHub{
		user:       "testuser",
		forkName:   "testuser/data",
		defaultSHA: "abc123sha",
		prURL:      "https://github.com/TheDiscDb/data/pull/99",
	}

	svc := NewService(store, gh)

	_, err := svc.Submit(context.Background(), 9999, "The Matrix", 1999, "movie")
	if err == nil {
		t.Fatal("expected error for missing contribution, got nil")
	}
}
