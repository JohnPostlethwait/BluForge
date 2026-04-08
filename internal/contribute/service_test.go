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
	"github.com/johnpostlethwait/bluforge/internal/tmdb"
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
	commitErrList []error // consumed in order; falls back to commitErr when empty
	prURL         string
	prErr         error
	prCalled      bool
	waitErr       error
	callOrder     []string
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
	m.callOrder = append(m.callOrder, "CreateBranch")
	return m.createErr
}

func (m *mockGitHub) CommitFiles(ctx context.Context, owner, repo, branch string, files []ghpkg.FileEntry, message string) error {
	m.commitFiles = append(m.commitFiles, files)
	if len(m.commitErrList) > 0 {
		err := m.commitErrList[0]
		m.commitErrList = m.commitErrList[1:]
		return err
	}
	return m.commitErr
}

func (m *mockGitHub) CreatePR(ctx context.Context, upstreamOwner, upstreamRepo, head, baseBranch, title, body string) (string, error) {
	m.prCalled = true
	return m.prURL, m.prErr
}

func (m *mockGitHub) ReopenPR(_ context.Context, _, _ string, _ int) error {
	return nil
}

func (m *mockGitHub) WaitForRepo(ctx context.Context, owner, repo string) error {
	m.callOrder = append(m.callOrder, "WaitForRepo")
	return m.waitErr
}

func (m *mockGitHub) GetFileContent(ctx context.Context, owner, repo, path string) (string, error) {
	return "", nil
}

func (m *mockGitHub) FileExists(ctx context.Context, owner, repo, path string) (bool, error) {
	return false, nil
}

// mockTMDB implements TMDBFetcher for testing.
type mockTMDB struct {
	raw     json.RawMessage
	details *tmdb.MediaDetails
	getErr  error
	imgData []byte
	imgErr  error
}

func (m *mockTMDB) GetDetails(_ context.Context, _ int, _ string) (json.RawMessage, *tmdb.MediaDetails, error) {
	return m.raw, m.details, m.getErr
}

func (m *mockTMDB) DownloadImage(_ context.Context, _ string, _ string) ([]byte, error) {
	return m.imgData, m.imgErr
}

// defaultMockTMDB returns a mockTMDB that simulates a successful TMDB fetch with poster.
func defaultMockTMDB() *mockTMDB {
	return &mockTMDB{
		raw: json.RawMessage(`{"id":603,"title":"The Matrix","overview":"test plot","poster_path":"/test.jpg"}`),
		details: &tmdb.MediaDetails{
			ID:             603,
			Title:          "The Matrix",
			Overview:       "test plot",
			PosterPath:     "/test.jpg",
			ImdbID:         "tt0133093",
			ReleaseDate:    "1999-03-31",
			RuntimeMinutes: 136,
		},
		imgData: []byte("fakejpegdata"),
	}
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

	if err := store.UpdateContributionDraft(id, "603", string(riJSON), string(labelsJSON)); err != nil {
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

	svc := NewService(store, gh, defaultMockTMDB())

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
	// Expect 8 files: 4 disc files + metadata.json + tmdb.json + cover.jpg + front.jpg.
	files := gh.commitFiles[0]
	if len(files) != 8 {
		t.Errorf("expected 8 files in commit, got %d", len(files))
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

	svc := NewService(store, gh, defaultMockTMDB())

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

	svc := NewService(store, gh, defaultMockTMDB())

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

	svc := NewService(store, gh, defaultMockTMDB())
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

	svc := NewService(store, gh, defaultMockTMDB())
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

	svc := NewService(store, gh, defaultMockTMDB())
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

	svc := NewService(store, gh, defaultMockTMDB())
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

	svc := NewService(store, gh, defaultMockTMDB())
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

	svc := NewService(store, gh, defaultMockTMDB())
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

	svc := NewService(store, gh, defaultMockTMDB())
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

	svc := NewService(store, gh, defaultMockTMDB())
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

func TestSubmitFailsWaitForFork(t *testing.T) {
	store := openTestStore(t)
	id, _, _ := seedContribution(t, store, nil)

	gh := &mockGitHub{
		user:     "testuser",
		forkName: "testuser/data",
		waitErr:  fmt.Errorf("github: wait for repo testuser/data: timed out"),
	}

	svc := NewService(store, gh, defaultMockTMDB())
	_, err := svc.Submit(context.Background(), id, "The Matrix", 1999, "movie")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "wait for fork") {
		t.Errorf("error %q should contain %q", err.Error(), "wait for fork")
	}
}

func TestSubmitWaitsForForkBeforeCreatingBranch(t *testing.T) {
	store := openTestStore(t)
	id, _, _ := seedContribution(t, store, nil)

	gh := &mockGitHub{
		user:          "testuser",
		forkName:      "testuser/data",
		defaultBranch: "master",
		defaultSHA:    "abc123sha",
		prURL:         "https://github.com/TheDiscDb/data/pull/42",
	}

	svc := NewService(store, gh, defaultMockTMDB())
	if _, err := svc.Submit(context.Background(), id, "The Matrix", 1999, "movie"); err != nil {
		t.Fatalf("Submit: %v", err)
	}

	// Find positions of WaitForRepo and CreateBranch in the call order.
	waitIdx, createIdx := -1, -1
	for i, call := range gh.callOrder {
		switch call {
		case "WaitForRepo":
			waitIdx = i
		case "CreateBranch":
			createIdx = i
		}
	}
	if waitIdx == -1 {
		t.Fatal("WaitForRepo was never called")
	}
	if createIdx == -1 {
		t.Fatal("CreateBranch was never called")
	}
	if waitIdx >= createIdx {
		t.Errorf("WaitForRepo (pos %d) must be called before CreateBranch (pos %d); call order: %v",
			waitIdx, createIdx, gh.callOrder)
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
	if err := store.UpdateContributionDraft(id, "603", "not valid json", string(labelsJSON)); err != nil {
		t.Fatalf("UpdateContributionDraft: %v", err)
	}

	gh := &mockGitHub{user: "testuser", forkName: "testuser/data"}
	svc := NewService(store, gh, defaultMockTMDB())
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
	if err := store.UpdateContributionDraft(id, "603", string(riJSON), "{{bad json"); err != nil {
		t.Fatalf("UpdateContributionDraft: %v", err)
	}

	gh := &mockGitHub{user: "testuser", forkName: "testuser/data"}
	svc := NewService(store, gh, defaultMockTMDB())
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

	if err := store.UpdateContributionDraft(id, "603", string(riJSON), string(labelsJSON)); err != nil {
		t.Fatalf("UpdateContributionDraft: %v", err)
	}

	gh := &mockGitHub{user: "testuser", forkName: "testuser/data"}
	svc := NewService(store, gh, defaultMockTMDB())
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
	if err := store.UpdateContributionDraft(id, "603", "", string(labelsJSON)); err != nil {
		t.Fatalf("UpdateContributionDraft: %v", err)
	}

	gh := &mockGitHub{user: "testuser", forkName: "testuser/data"}
	svc := NewService(store, gh, defaultMockTMDB())
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
	if err := store.UpdateContributionDraft(id, "603", string(riJSON), ""); err != nil {
		t.Fatalf("UpdateContributionDraft: %v", err)
	}

	gh := &mockGitHub{user: "testuser", forkName: "testuser/data"}
	svc := NewService(store, gh, defaultMockTMDB())
	_, err = svc.Submit(context.Background(), id, "The Matrix", 1999, "movie")
	if err == nil {
		t.Fatal("expected error for missing title_labels, got nil")
	}
	if !strings.Contains(err.Error(), "has no title_labels") {
		t.Errorf("error %q should contain %q", err.Error(), "has no title_labels")
	}
}

// --- Resubmit tests ---

func TestResubmitPushesCorrectiveCommit(t *testing.T) {
	store := openTestStore(t)
	id, _, _ := seedContribution(t, store, nil)

	// First submit to put the contribution in "submitted" state.
	gh := &mockGitHub{
		user:          "testuser",
		forkName:      "testuser/data",
		defaultBranch: "master",
		defaultSHA:    "abc123sha",
		prURL:         "https://github.com/TheDiscDb/data/pull/42",
	}
	svc := NewService(store, gh, defaultMockTMDB())
	if _, err := svc.Submit(context.Background(), id, "The Matrix", 1999, "movie"); err != nil {
		t.Fatalf("Submit: %v", err)
	}

	// Reset commitFiles to only capture the Resubmit call.
	gh.commitFiles = nil

	if err := svc.Resubmit(context.Background(), id, "The Matrix", 1999, "movie"); err != nil {
		t.Fatalf("Resubmit: %v", err)
	}

	// Must commit exactly one batch of 4 files.
	if len(gh.commitFiles) != 1 {
		t.Fatalf("expected 1 CommitFiles call, got %d", len(gh.commitFiles))
	}
	if len(gh.commitFiles[0]) != 8 {
		t.Errorf("expected 8 files in resubmit commit, got %d", len(gh.commitFiles[0]))
	}

	// Status must remain "submitted" — Resubmit does not change it.
	got, err := store.GetContribution(id)
	if err != nil {
		t.Fatalf("GetContribution: %v", err)
	}
	if got.Status != "submitted" {
		t.Errorf("Status: want %q, got %q after Resubmit", "submitted", got.Status)
	}
}

func TestResubmitRecreatesBranchAndPRWhenBranchDeleted(t *testing.T) {
	store := openTestStore(t)
	id, _, _ := seedContribution(t, store, nil)

	// First submit to put the contribution in "submitted" state.
	gh := &mockGitHub{
		user:          "testuser",
		forkName:      "testuser/data",
		defaultBranch: "master",
		defaultSHA:    "abc123sha",
		prURL:         "https://github.com/TheDiscDb/data/pull/42",
	}
	svc := NewService(store, gh, defaultMockTMDB())
	if _, err := svc.Submit(context.Background(), id, "The Matrix", 1999, "movie"); err != nil {
		t.Fatalf("Submit: %v", err)
	}

	// Simulate the branch being deleted: first CommitFiles call returns ErrBranchNotFound,
	// second (after branch recreation) succeeds.
	newPRURL := "https://github.com/TheDiscDb/data/pull/99"
	gh.commitFiles = nil
	gh.commitErrList = []error{ghpkg.ErrBranchNotFound}
	gh.prURL = newPRURL
	gh.prCalled = false
	gh.callOrder = nil

	if err := svc.Resubmit(context.Background(), id, "The Matrix", 1999, "movie"); err != nil {
		t.Fatalf("Resubmit: %v", err)
	}

	// CommitFiles must have been called twice: first returning ErrBranchNotFound, then
	// succeeding after branch recreation.
	if len(gh.commitFiles) != 2 {
		t.Fatalf("expected 2 CommitFiles calls, got %d", len(gh.commitFiles))
	}

	// A new PR must have been opened.
	if !gh.prCalled {
		t.Error("expected CreatePR to be called for branch recreation, but it was not")
	}

	// CreateBranch and WaitForRepo must have been called.
	if !contains(gh.callOrder, "CreateBranch") {
		t.Error("expected CreateBranch to be called during branch recreation")
	}
	if !contains(gh.callOrder, "WaitForRepo") {
		t.Error("expected WaitForRepo to be called during branch recreation")
	}

	// The DB must be updated with the new PR URL.
	got, err := store.GetContribution(id)
	if err != nil {
		t.Fatalf("GetContribution: %v", err)
	}
	if got.PRURL != newPRURL {
		t.Errorf("PR URL: want %q, got %q", newPRURL, got.PRURL)
	}
	if got.Status != "submitted" {
		t.Errorf("Status: want %q, got %q", "submitted", got.Status)
	}
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func TestResubmitFailsIfNotSubmitted(t *testing.T) {
	store := openTestStore(t)
	id, _, _ := seedContribution(t, store, nil)

	gh := &mockGitHub{user: "testuser"}
	svc := NewService(store, gh, defaultMockTMDB())

	err := svc.Resubmit(context.Background(), id, "The Matrix", 1999, "movie")
	if err == nil {
		t.Fatal("expected error for non-submitted contribution, got nil")
	}
	if !strings.Contains(err.Error(), "not submitted") {
		t.Errorf("error %q should contain %q", err.Error(), "not submitted")
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

func TestSubmitIncludesMetadataAndImages(t *testing.T) {
	store := openTestStore(t)
	id, _, _ := seedContribution(t, store, nil)

	gh := &mockGitHub{
		user:          "testuser",
		forkName:      "testuser/data",
		defaultBranch: "master",
		defaultSHA:    "abc123sha",
		prURL:         "https://github.com/TheDiscDb/data/pull/42",
	}

	svc := NewService(store, gh, defaultMockTMDB())
	if _, err := svc.Submit(context.Background(), id, "The Matrix", 1999, "movie"); err != nil {
		t.Fatalf("Submit: %v", err)
	}

	files := gh.commitFiles[0]
	// Build a path → file map for easy lookup.
	byPath := make(map[string]ghpkg.FileEntry)
	for _, f := range files {
		byPath[f.Path] = f
	}

	// metadata.json at title level.
	meta, ok := byPath["data/movie/The Matrix (1999)/metadata.json"]
	if !ok {
		t.Error("metadata.json missing from commit")
	} else if !strings.Contains(meta.Content, `"the-matrix-1999"`) {
		t.Errorf("metadata.json missing expected slug, content: %s", meta.Content)
	}

	// tmdb.json at title level.
	tmdbFile, ok := byPath["data/movie/The Matrix (1999)/tmdb.json"]
	if !ok {
		t.Error("tmdb.json missing from commit")
	} else if len(tmdbFile.Content) == 0 {
		t.Error("tmdb.json is empty")
	}

	// cover.jpg at title level (binary).
	cover, ok := byPath["data/movie/The Matrix (1999)/cover.jpg"]
	if !ok {
		t.Error("cover.jpg missing from commit")
	} else if len(cover.Blob) == 0 {
		t.Error("cover.jpg Blob is empty")
	}

	// front.jpg at release level (binary).
	front, ok := byPath["data/movie/The Matrix (1999)/1999-blu-ray/front.jpg"]
	if !ok {
		t.Error("front.jpg missing from commit")
	} else if len(front.Blob) == 0 {
		t.Error("front.jpg Blob is empty")
	}

	// release.json should include ImageUrl.
	rel, ok := byPath["data/movie/The Matrix (1999)/1999-blu-ray/release.json"]
	if !ok {
		t.Error("release.json missing from commit")
	} else if !strings.Contains(rel.Content, `"ImageUrl"`) {
		t.Errorf("release.json missing ImageUrl field, content: %s", rel.Content)
	}
}

func TestSubmitOmitsImagesWhenNoPoster(t *testing.T) {
	store := openTestStore(t)
	id, _, _ := seedContribution(t, store, nil)

	gh := &mockGitHub{
		user:          "testuser",
		forkName:      "testuser/data",
		defaultBranch: "master",
		defaultSHA:    "abc123sha",
		prURL:         "https://github.com/TheDiscDb/data/pull/42",
	}

	// TMDB returns no poster path.
	mockTMDB := &mockTMDB{
		raw: json.RawMessage(`{"id":603,"title":"The Matrix"}`),
		details: &tmdb.MediaDetails{
			ID:          603,
			Title:       "The Matrix",
			PosterPath:  "", // no poster
			ReleaseDate: "1999-03-31",
		},
	}

	svc := NewService(store, gh, mockTMDB)
	if _, err := svc.Submit(context.Background(), id, "The Matrix", 1999, "movie"); err != nil {
		t.Fatalf("Submit: %v", err)
	}

	files := gh.commitFiles[0]
	// Should be 6 files (no images).
	if len(files) != 6 {
		t.Errorf("expected 6 files when no poster, got %d", len(files))
		for _, f := range files {
			t.Logf("  file: %s", f.Path)
		}
	}
	for _, f := range files {
		if strings.HasSuffix(f.Path, ".jpg") {
			t.Errorf("unexpected image file in commit: %s", f.Path)
		}
	}
}

func TestSubmitOmitsImagesWhenDownloadFails(t *testing.T) {
	store := openTestStore(t)
	id, _, _ := seedContribution(t, store, nil)

	gh := &mockGitHub{
		user:          "testuser",
		forkName:      "testuser/data",
		defaultBranch: "master",
		defaultSHA:    "abc123sha",
		prURL:         "https://github.com/TheDiscDb/data/pull/42",
	}

	// Image download fails.
	mockTMDB := &mockTMDB{
		raw: json.RawMessage(`{"id":603,"title":"The Matrix","poster_path":"/test.jpg"}`),
		details: &tmdb.MediaDetails{
			ID:          603,
			Title:       "The Matrix",
			PosterPath:  "/test.jpg",
			ReleaseDate: "1999-03-31",
		},
		imgErr: fmt.Errorf("connection refused"),
	}

	svc := NewService(store, gh, mockTMDB)
	// Submit should succeed despite image download failure.
	if _, err := svc.Submit(context.Background(), id, "The Matrix", 1999, "movie"); err != nil {
		t.Fatalf("Submit should succeed when image download fails: %v", err)
	}

	// Should be 6 files (no images).
	if len(gh.commitFiles[0]) != 6 {
		t.Errorf("expected 6 files when image download fails, got %d", len(gh.commitFiles[0]))
	}
}

func TestSubmitFailsTMDBError(t *testing.T) {
	store := openTestStore(t)
	id, _, _ := seedContribution(t, store, nil)

	gh := &mockGitHub{user: "testuser", forkName: "testuser/data"}

	mockTMDB := &mockTMDB{
		getErr: fmt.Errorf("tmdb: unexpected status 500: internal server error"),
	}

	svc := NewService(store, gh, mockTMDB)
	_, err := svc.Submit(context.Background(), id, "The Matrix", 1999, "movie")
	if err == nil {
		t.Fatal("expected error when TMDB fetch fails, got nil")
	}
	if !strings.Contains(err.Error(), "fetch TMDB details") {
		t.Errorf("error %q should contain %q", err.Error(), "fetch TMDB details")
	}
}

func TestSubmitFailsNonNumericTmdbID(t *testing.T) {
	store := openTestStore(t)

	scan := testScan()
	scanData, _ := json.Marshal(scan)
	ri := ReleaseInfo{UPC: "883929543236", RegionCode: "A", Year: 1999, Format: "Blu-ray", Slug: "1999-blu-ray"}
	riJSON, _ := json.Marshal(ri)
	labels := []TitleLabel{{TitleIndex: 0, Type: "MainMovie", Name: "The Matrix", FileName: "The Matrix.mkv"}}
	labelsJSON, _ := json.Marshal(labels)

	c := db.Contribution{DiscKey: "non-numeric-tmdb", DiscName: "THE_MATRIX", RawOutput: scan.RawOutput, ScanJSON: string(scanData)}
	id, err := store.SaveContribution(c)
	if err != nil {
		t.Fatalf("SaveContribution: %v", err)
	}
	// Store a non-numeric TMDB ID (e.g. accidentally stored an IMDb ID).
	if err := store.UpdateContributionDraft(id, "tt0133093", string(riJSON), string(labelsJSON)); err != nil {
		t.Fatalf("UpdateContributionDraft: %v", err)
	}

	gh := &mockGitHub{user: "testuser", forkName: "testuser/data"}
	svc := NewService(store, gh, defaultMockTMDB())
	_, err = svc.Submit(context.Background(), id, "The Matrix", 1999, "movie")
	if err == nil {
		t.Fatal("expected error for non-numeric tmdb_id, got nil")
	}
	if !strings.Contains(err.Error(), "not a valid integer") {
		t.Errorf("error %q should contain %q", err.Error(), "not a valid integer")
	}
}

// --- SubmitUpdate validation tests ---

func seedUpdateContribution(t *testing.T, store *db.Store, matchInfo MatchInfo, labels []TitleLabel) int64 {
	t.Helper()

	scan := testScan()
	scanData, err := json.Marshal(scan)
	if err != nil {
		t.Fatalf("marshal scan: %v", err)
	}

	miJSON, err := json.Marshal(matchInfo)
	if err != nil {
		t.Fatalf("marshal match_info: %v", err)
	}

	labelsJSON, err := json.Marshal(labels)
	if err != nil {
		t.Fatalf("marshal labels: %v", err)
	}

	c := db.Contribution{
		DiscKey:          "update-disc-key",
		DiscName:         "THE_MATRIX",
		RawOutput:        scan.RawOutput,
		ScanJSON:         string(scanData),
		ContributionType: "update",
		MatchInfo:        string(miJSON),
		TitleLabels:      string(labelsJSON),
	}
	id, err := store.SaveContribution(c)
	if err != nil {
		t.Fatalf("SaveContribution: %v", err)
	}
	return id
}

func TestSubmitUpdate_validatesMatchInfo(t *testing.T) {
	// A contribution with empty match_info should return an error.
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
		DiscKey:          "update-empty-match-info",
		DiscName:         "THE_MATRIX",
		RawOutput:        scan.RawOutput,
		ScanJSON:         string(scanData),
		ContributionType: "update",
		MatchInfo:        "", // empty — should fail
		TitleLabels:      string(labelsJSON),
	}
	id, err := store.SaveContribution(c)
	if err != nil {
		t.Fatalf("SaveContribution: %v", err)
	}

	gh := &mockGitHub{user: "testuser", forkName: "testuser/data"}
	svc := NewService(store, gh, defaultMockTMDB())

	_, err = svc.SubmitUpdate(context.Background(), id)
	if err == nil {
		t.Fatal("expected error for empty match_info, got nil")
	}
	if !strings.Contains(err.Error(), "match_info") {
		t.Errorf("error %q should mention match_info", err.Error())
	}
}

func TestSubmitUpdate_requiresAtLeastOneTypedTitle(t *testing.T) {
	// All titles with empty type → error.
	store := openTestStore(t)

	mi := MatchInfo{
		MediaSlug:   "the-matrix-1999",
		MediaType:   "movie",
		MediaTitle:  "The Matrix",
		MediaYear:   1999,
		ReleaseSlug: "1999-blu-ray",
		DiscIndex:   1,
	}
	labels := []TitleLabel{
		{TitleIndex: 0, Type: "", Name: "", FileName: ""}, // no type
	}
	id := seedUpdateContribution(t, store, mi, labels)

	gh := &mockGitHub{user: "testuser", forkName: "testuser/data"}
	svc := NewService(store, gh, defaultMockTMDB())

	_, err := svc.SubmitUpdate(context.Background(), id)
	if err == nil {
		t.Fatal("expected error when all title labels have empty type, got nil")
	}
	if !strings.Contains(err.Error(), "typed title") {
		t.Errorf("error %q should mention typed title", err.Error())
	}
}
