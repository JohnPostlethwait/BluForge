package contribute

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	ghpkg "github.com/johnpostlethwait/bluforge/internal/github"
	"github.com/johnpostlethwait/bluforge/internal/db"
	"github.com/johnpostlethwait/bluforge/internal/makemkv"
)

// mockGitHub implements GitHubClient for testing.
type mockGitHub struct {
	user          string
	userErr       error
	forkName      string
	forkErr       error
	defaultBranch string
	defaultSHA    string
	defaultErr    error
	createErr     error
	commitFiles   [][]ghpkg.FileEntry
	commitErr     error
	prURL         string
	prErr         error
}

func (m *mockGitHub) GetUser(ctx context.Context) (string, error) {
	return m.user, m.userErr
}

func (m *mockGitHub) EnsureFork(ctx context.Context, owner, repo string) (string, error) {
	return m.forkName, m.forkErr
}

func (m *mockGitHub) GetDefaultBranchSHA(ctx context.Context, owner, repo string) (string, string, error) {
	return m.defaultBranch, m.defaultSHA, m.defaultErr
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
		user:          "testuser",
		forkName:      "testuser/data",
		defaultBranch: "master",
		defaultSHA:    "abc123sha",
		prURL:         "https://github.com/TheDiscDb/data/pull/42",
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
		user:          "testuser",
		forkName:      "testuser/data",
		defaultBranch: "master",
		defaultSHA:    "abc123sha",
		prURL:         "https://github.com/TheDiscDb/data/pull/99",
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
		user:          "testuser",
		forkName:      "testuser/data",
		defaultBranch: "master",
		defaultSHA:    "abc123sha",
		prURL:         "https://github.com/TheDiscDb/data/pull/99",
	}

	svc := NewService(store, gh)

	_, err := svc.Submit(context.Background(), 9999, "The Matrix", 1999, "movie")
	if err == nil {
		t.Fatal("expected error for missing contribution, got nil")
	}
}

// --- GitHub error propagation tests ---

func TestSubmitFailsGetUser(t *testing.T) {
	store := openTestStore(t)
	id, _, _ := seedContribution(t, store, nil)

	gh := &mockGitHub{
		userErr: fmt.Errorf("github: unauthorized"),
	}

	svc := NewService(store, gh)
	_, err := svc.Submit(context.Background(), id, "The Matrix", 1999, "movie")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "get github user") {
		t.Errorf("error %q should contain %q", err.Error(), "get github user")
	}
}

func TestSubmitFailsEnsureFork(t *testing.T) {
	store := openTestStore(t)
	id, _, _ := seedContribution(t, store, nil)

	gh := &mockGitHub{
		user:    "testuser",
		forkErr: fmt.Errorf("github: permission denied"),
	}

	svc := NewService(store, gh)
	_, err := svc.Submit(context.Background(), id, "The Matrix", 1999, "movie")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "ensure fork") {
		t.Errorf("error %q should contain %q", err.Error(), "ensure fork")
	}
}

func TestSubmitFailsGetDefaultBranch(t *testing.T) {
	store := openTestStore(t)
	id, _, _ := seedContribution(t, store, nil)

	gh := &mockGitHub{
		user:       "testuser",
		forkName:   "testuser/data",
		defaultErr: fmt.Errorf("github: repo not found"),
	}

	svc := NewService(store, gh)
	_, err := svc.Submit(context.Background(), id, "The Matrix", 1999, "movie")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "get default branch SHA") {
		t.Errorf("error %q should contain %q", err.Error(), "get default branch SHA")
	}
}

func TestSubmitFailsCreateBranch(t *testing.T) {
	store := openTestStore(t)
	id, _, _ := seedContribution(t, store, nil)

	gh := &mockGitHub{
		user:          "testuser",
		forkName:      "testuser/data",
		defaultBranch: "master",
		defaultSHA:    "abc123sha",
		createErr:     fmt.Errorf("github: forbidden"),
	}

	svc := NewService(store, gh)
	_, err := svc.Submit(context.Background(), id, "The Matrix", 1999, "movie")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "create branch") {
		t.Errorf("error %q should contain %q", err.Error(), "create branch")
	}
}

func TestSubmitFailsCommitFiles(t *testing.T) {
	store := openTestStore(t)
	id, _, _ := seedContribution(t, store, nil)

	gh := &mockGitHub{
		user:          "testuser",
		forkName:      "testuser/data",
		defaultBranch: "master",
		defaultSHA:    "abc123sha",
		commitErr:     fmt.Errorf("github: tree too large"),
	}

	svc := NewService(store, gh)
	_, err := svc.Submit(context.Background(), id, "The Matrix", 1999, "movie")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "commit files") {
		t.Errorf("error %q should contain %q", err.Error(), "commit files")
	}
}

func TestSubmitFailsCreatePR(t *testing.T) {
	store := openTestStore(t)
	id, _, _ := seedContribution(t, store, nil)

	gh := &mockGitHub{
		user:          "testuser",
		forkName:      "testuser/data",
		defaultBranch: "master",
		defaultSHA:    "abc123sha",
		prErr:         fmt.Errorf("github: PR already exists"),
	}

	svc := NewService(store, gh)
	_, err := svc.Submit(context.Background(), id, "The Matrix", 1999, "movie")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "create PR") {
		t.Errorf("error %q should contain %q", err.Error(), "create PR")
	}
}

// --- Idempotency tests ---

func TestSubmitAlreadySubmittedReturnsExistingPR(t *testing.T) {
	store := openTestStore(t)
	id, _, _ := seedContribution(t, store, nil)

	// Manually mark as already submitted with a PR URL.
	if err := store.UpdateContributionStatus(id, "submitted", "https://github.com/existing/pr"); err != nil {
		t.Fatalf("UpdateContributionStatus: %v", err)
	}

	gh := &mockGitHub{
		user:          "testuser",
		forkName:      "testuser/data",
		defaultBranch: "master",
		defaultSHA:    "abc123sha",
		prURL:         "https://github.com/TheDiscDb/data/pull/99",
	}

	svc := NewService(store, gh)
	prURL, err := svc.Submit(context.Background(), id, "The Matrix", 1999, "movie")
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if prURL != "https://github.com/existing/pr" {
		t.Errorf("prURL: want %q, got %q", "https://github.com/existing/pr", prURL)
	}
	// No GitHub API calls should have been made.
	if len(gh.commitFiles) != 0 {
		t.Errorf("expected 0 CommitFiles calls, got %d", len(gh.commitFiles))
	}
}

func TestSubmitBranchAlreadyExistsContinues(t *testing.T) {
	store := openTestStore(t)
	id, _, _ := seedContribution(t, store, nil)

	gh := &mockGitHub{
		user:          "testuser",
		forkName:      "testuser/data",
		defaultBranch: "master",
		defaultSHA:    "abc123sha",
		createErr:     fmt.Errorf("github: create branch: Reference already exists"),
		prURL:         "https://github.com/TheDiscDb/data/pull/77",
	}

	svc := NewService(store, gh)
	prURL, err := svc.Submit(context.Background(), id, "The Matrix", 1999, "movie")
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if prURL != "https://github.com/TheDiscDb/data/pull/77" {
		t.Errorf("prURL: want %q, got %q", "https://github.com/TheDiscDb/data/pull/77", prURL)
	}
	// CommitFiles should have been called despite the branch-exists error.
	if len(gh.commitFiles) != 1 {
		t.Errorf("expected 1 CommitFiles call, got %d", len(gh.commitFiles))
	}
}

// --- Malformed JSON tests ---

func TestSubmitFailsMalformedReleaseInfo(t *testing.T) {
	store := openTestStore(t)

	scan := testScan()
	scanData, err := json.Marshal(scan)
	if err != nil {
		t.Fatalf("marshal scan: %v", err)
	}

	labels := []TitleLabel{
		{TitleIndex: 0, Type: "MainMovie", Name: "The Matrix", FileName: "The Matrix (1999).mkv"},
	}
	labelsJSON, err := json.Marshal(labels)
	if err != nil {
		t.Fatalf("marshal labels: %v", err)
	}

	c := db.Contribution{
		DiscKey:   "malformed-release-info",
		DiscName:  "THE_MATRIX",
		RawOutput: scan.RawOutput,
		ScanJSON:  string(scanData),
	}
	id, err := store.SaveContribution(c)
	if err != nil {
		t.Fatalf("SaveContribution: %v", err)
	}

	// Set release_info to invalid JSON.
	if err := store.UpdateContributionDraft(id, "tt0133093", "not valid json", string(labelsJSON)); err != nil {
		t.Fatalf("UpdateContributionDraft: %v", err)
	}

	gh := &mockGitHub{user: "testuser", forkName: "testuser/data"}
	svc := NewService(store, gh)
	_, err = svc.Submit(context.Background(), id, "The Matrix", 1999, "movie")
	if err == nil {
		t.Fatal("expected error for malformed release_info, got nil")
	}
	if !strings.Contains(err.Error(), "parse release_info") {
		t.Errorf("error %q should contain %q", err.Error(), "parse release_info")
	}
}

func TestSubmitFailsMalformedTitleLabels(t *testing.T) {
	store := openTestStore(t)

	scan := testScan()
	scanData, err := json.Marshal(scan)
	if err != nil {
		t.Fatalf("marshal scan: %v", err)
	}

	ri := ReleaseInfo{UPC: "883929543236", RegionCode: "A", Year: 1999, Format: "Blu-ray", Slug: "1999-blu-ray"}
	riJSON, err := json.Marshal(ri)
	if err != nil {
		t.Fatalf("marshal release info: %v", err)
	}

	c := db.Contribution{
		DiscKey:   "malformed-title-labels",
		DiscName:  "THE_MATRIX",
		RawOutput: scan.RawOutput,
		ScanJSON:  string(scanData),
	}
	id, err := store.SaveContribution(c)
	if err != nil {
		t.Fatalf("SaveContribution: %v", err)
	}

	// Set title_labels to invalid JSON.
	if err := store.UpdateContributionDraft(id, "tt0133093", string(riJSON), "{{bad json"); err != nil {
		t.Fatalf("UpdateContributionDraft: %v", err)
	}

	gh := &mockGitHub{user: "testuser", forkName: "testuser/data"}
	svc := NewService(store, gh)
	_, err = svc.Submit(context.Background(), id, "The Matrix", 1999, "movie")
	if err == nil {
		t.Fatal("expected error for malformed title_labels, got nil")
	}
	if !strings.Contains(err.Error(), "parse title_labels") {
		t.Errorf("error %q should contain %q", err.Error(), "parse title_labels")
	}
}

func TestSubmitFailsMalformedScanJSON(t *testing.T) {
	store := openTestStore(t)

	ri := ReleaseInfo{UPC: "883929543236", RegionCode: "A", Year: 1999, Format: "Blu-ray", Slug: "1999-blu-ray"}
	riJSON, err := json.Marshal(ri)
	if err != nil {
		t.Fatalf("marshal release info: %v", err)
	}

	labels := []TitleLabel{
		{TitleIndex: 0, Type: "MainMovie", Name: "The Matrix", FileName: "The Matrix (1999).mkv"},
	}
	labelsJSON, err := json.Marshal(labels)
	if err != nil {
		t.Fatalf("marshal labels: %v", err)
	}

	// Create contribution with invalid ScanJSON.
	c := db.Contribution{
		DiscKey:   "malformed-scan-json",
		DiscName:  "THE_MATRIX",
		RawOutput: "raw output data",
		ScanJSON:  "not json",
	}
	id, err := store.SaveContribution(c)
	if err != nil {
		t.Fatalf("SaveContribution: %v", err)
	}

	if err := store.UpdateContributionDraft(id, "tt0133093", string(riJSON), string(labelsJSON)); err != nil {
		t.Fatalf("UpdateContributionDraft: %v", err)
	}

	gh := &mockGitHub{user: "testuser", forkName: "testuser/data"}
	svc := NewService(store, gh)
	_, err = svc.Submit(context.Background(), id, "The Matrix", 1999, "movie")
	if err == nil {
		t.Fatal("expected error for malformed scan_json, got nil")
	}
	if !strings.Contains(err.Error(), "parse scan_json") {
		t.Errorf("error %q should contain %q", err.Error(), "parse scan_json")
	}
}

// --- Missing required fields tests ---

func TestSubmitFailsMissingReleaseInfo(t *testing.T) {
	store := openTestStore(t)

	scan := testScan()
	scanData, err := json.Marshal(scan)
	if err != nil {
		t.Fatalf("marshal scan: %v", err)
	}

	labels := []TitleLabel{
		{TitleIndex: 0, Type: "MainMovie", Name: "The Matrix", FileName: "The Matrix (1999).mkv"},
	}
	labelsJSON, err := json.Marshal(labels)
	if err != nil {
		t.Fatalf("marshal labels: %v", err)
	}

	c := db.Contribution{
		DiscKey:   "missing-release-info",
		DiscName:  "THE_MATRIX",
		RawOutput: scan.RawOutput,
		ScanJSON:  string(scanData),
	}
	id, err := store.SaveContribution(c)
	if err != nil {
		t.Fatalf("SaveContribution: %v", err)
	}

	// Set tmdb_id and title_labels but leave release_info empty.
	if err := store.UpdateContributionDraft(id, "tt0133093", "", string(labelsJSON)); err != nil {
		t.Fatalf("UpdateContributionDraft: %v", err)
	}

	gh := &mockGitHub{user: "testuser", forkName: "testuser/data"}
	svc := NewService(store, gh)
	_, err = svc.Submit(context.Background(), id, "The Matrix", 1999, "movie")
	if err == nil {
		t.Fatal("expected error for missing release_info, got nil")
	}
	if !strings.Contains(err.Error(), "has no release_info") {
		t.Errorf("error %q should contain %q", err.Error(), "has no release_info")
	}
}

func TestSubmitFailsMissingTitleLabels(t *testing.T) {
	store := openTestStore(t)

	scan := testScan()
	scanData, err := json.Marshal(scan)
	if err != nil {
		t.Fatalf("marshal scan: %v", err)
	}

	ri := ReleaseInfo{UPC: "883929543236", RegionCode: "A", Year: 1999, Format: "Blu-ray", Slug: "1999-blu-ray"}
	riJSON, err := json.Marshal(ri)
	if err != nil {
		t.Fatalf("marshal release info: %v", err)
	}

	c := db.Contribution{
		DiscKey:   "missing-title-labels",
		DiscName:  "THE_MATRIX",
		RawOutput: scan.RawOutput,
		ScanJSON:  string(scanData),
	}
	id, err := store.SaveContribution(c)
	if err != nil {
		t.Fatalf("SaveContribution: %v", err)
	}

	// Set tmdb_id and release_info but leave title_labels empty.
	if err := store.UpdateContributionDraft(id, "tt0133093", string(riJSON), ""); err != nil {
		t.Fatalf("UpdateContributionDraft: %v", err)
	}

	gh := &mockGitHub{user: "testuser", forkName: "testuser/data"}
	svc := NewService(store, gh)
	_, err = svc.Submit(context.Background(), id, "The Matrix", 1999, "movie")
	if err == nil {
		t.Fatal("expected error for missing title_labels, got nil")
	}
	if !strings.Contains(err.Error(), "has no title_labels") {
		t.Errorf("error %q should contain %q", err.Error(), "has no title_labels")
	}
}

// --- Slugify tests ---

func TestSlugify(t *testing.T) {
	tests := []struct {
		title string
		year  int
		want  string
	}{
		{"The Matrix", 1999, "the-matrix-1999"},
		{"Alien: Romulus", 2024, "alien-romulus-2024"},
		{"Spider-Man: No Way Home", 2021, "spider-man-no-way-home-2021"},
		{"   Leading Spaces  ", 2020, "leading-spaces-2020"},
		{"ALL CAPS TITLE", 2023, "all-caps-title-2023"},
		{"Title with 'Quotes' & Symbols!", 2022, "title-with-quotes-symbols-2022"},
		{"Hello---World", 2000, "hello-world-2000"},
		{"  --Dashes--  ", 2001, "dashes-2001"},
		{"Simple", 2025, "simple-2025"},
	}

	for _, tc := range tests {
		t.Run(fmt.Sprintf("%s_%d", tc.title, tc.year), func(t *testing.T) {
			got := slugify(tc.title, tc.year)
			if got != tc.want {
				t.Errorf("slugify(%q, %d): want %q, got %q", tc.title, tc.year, tc.want, got)
			}
		})
	}
}
