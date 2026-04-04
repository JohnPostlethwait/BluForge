package contribute

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	ghpkg "github.com/johnpostlethwait/bluforge/internal/github"
	"github.com/johnpostlethwait/bluforge/internal/db"
	"github.com/johnpostlethwait/bluforge/internal/makemkv"
)

// GitHubClient defines the GitHub operations needed for contributions.
type GitHubClient interface {
	GetUser(ctx context.Context) (string, error)
	EnsureFork(ctx context.Context, owner, repo string) (string, error)
	GetDefaultBranchSHA(ctx context.Context, owner, repo string) (string, error)
	CreateBranch(ctx context.Context, owner, repo, branchName, baseSHA string) error
	CommitFiles(ctx context.Context, owner, repo, branch string, files []ghpkg.FileEntry, message string) error
	CreatePR(ctx context.Context, upstreamOwner, upstreamRepo, head, baseBranch, title, body string) (string, error)
}

const (
	upstreamOwner = "TheDiscDb"
	upstreamRepo  = "data"
)

// Store is the subset of db.Store used by the service.
type Store interface {
	GetContribution(id int64) (*db.Contribution, error)
	UpdateContributionStatus(id int64, status, prURL string) error
}

// Service orchestrates the full TheDiscDB contribution submission flow.
type Service struct {
	store  Store
	github GitHubClient
}

// NewService creates a new contribution Service.
func NewService(store Store, github GitHubClient) *Service {
	return &Service{store: store, github: github}
}

// Submit executes the full contribution submission flow:
//  1. Load and validate the contribution from the database.
//  2. Parse stored JSON fields (ReleaseInfo, []TitleLabel, DiscScan).
//  3. Generate TheDiscDB files.
//  4. Fork, branch, commit, and open a PR on GitHub.
//  5. Update the contribution status to "submitted" with the PR URL.
//
// Returns the PR URL on success.
func (s *Service) Submit(ctx context.Context, contributionID int64, mediaTitle string, mediaYear int, mediaType string) (string, error) {
	// 1. Load contribution.
	c, err := s.store.GetContribution(contributionID)
	if err != nil {
		return "", fmt.Errorf("contribute: load contribution %d: %w", contributionID, err)
	}
	if c == nil {
		return "", fmt.Errorf("contribute: contribution %d not found", contributionID)
	}

	// 2. Validate required fields.
	if c.TmdbID == "" {
		return "", fmt.Errorf("contribute: contribution %d has no tmdb_id — complete the draft first", contributionID)
	}
	if c.ReleaseInfo == "" {
		return "", fmt.Errorf("contribute: contribution %d has no release_info — complete the draft first", contributionID)
	}
	if c.TitleLabels == "" {
		return "", fmt.Errorf("contribute: contribution %d has no title_labels — complete the draft first", contributionID)
	}

	// 3. Parse stored JSON.
	var ri ReleaseInfo
	if err := json.Unmarshal([]byte(c.ReleaseInfo), &ri); err != nil {
		return "", fmt.Errorf("contribute: parse release_info: %w", err)
	}

	var labels []TitleLabel
	if err := json.Unmarshal([]byte(c.TitleLabels), &labels); err != nil {
		return "", fmt.Errorf("contribute: parse title_labels: %w", err)
	}

	var scan makemkv.DiscScan
	if err := json.Unmarshal([]byte(c.ScanJSON), &scan); err != nil {
		return "", fmt.Errorf("contribute: parse scan_json: %w", err)
	}

	// 4a. Get authenticated GitHub user.
	githubUser, err := s.github.GetUser(ctx)
	if err != nil {
		return "", fmt.Errorf("contribute: get github user: %w", err)
	}

	// 4b. Ensure fork exists.
	fork, err := s.github.EnsureFork(ctx, upstreamOwner, upstreamRepo)
	if err != nil {
		return "", fmt.Errorf("contribute: ensure fork: %w", err)
	}
	// fork is "user/data"; extract the owner part.
	forkOwner := strings.SplitN(fork, "/", 2)[0]

	// 4c. Generate file contents.
	releaseSlug := ReleaseSlug(ri.Year, ri.Format)
	mediaDir := MediaDirPath(mediaType, mediaTitle, mediaYear)
	releaseDir := mediaDir + "/" + releaseSlug

	releaseJSON := GenerateReleaseJSON(ri, githubUser)
	discJSON := GenerateDiscJSON(&scan, ri.Format)
	summary := GenerateSummary(&scan, labels)
	rawOutput := c.RawOutput

	files := []ghpkg.FileEntry{
		{Path: releaseDir + "/release.json", Content: releaseJSON},
		{Path: releaseDir + "/disc01.json", Content: discJSON},
		{Path: releaseDir + "/disc01-summary.txt", Content: summary},
		{Path: releaseDir + "/disc01-raw.txt", Content: rawOutput},
	}

	// 4d. Get default branch SHA from the upstream repo (not the fork).
	baseSHA, err := s.github.GetDefaultBranchSHA(ctx, upstreamOwner, upstreamRepo)
	if err != nil {
		return "", fmt.Errorf("contribute: get default branch SHA: %w", err)
	}

	// 4e. Create branch.
	titleSlug := slugify(mediaTitle, mediaYear)
	branchName := ghpkg.ContributionBranchName(titleSlug, releaseSlug)
	if err := s.github.CreateBranch(ctx, forkOwner, upstreamRepo, branchName, baseSHA); err != nil {
		return "", fmt.Errorf("contribute: create branch: %w", err)
	}

	// 4f. Commit files.
	commitMsg := fmt.Sprintf("Add %s (%d) - %s", mediaTitle, mediaYear, ri.Format)
	if err := s.github.CommitFiles(ctx, forkOwner, upstreamRepo, branchName, files, commitMsg); err != nil {
		return "", fmt.Errorf("contribute: commit files: %w", err)
	}

	// 4g. Open pull request. head is "user:branch" format.
	prTitle := fmt.Sprintf("Add %s (%d) - %s", mediaTitle, mediaYear, ri.Format)
	prHead := githubUser + ":" + branchName
	prURL, err := s.github.CreatePR(ctx, upstreamOwner, upstreamRepo, prHead, "main", prTitle, "")
	if err != nil {
		return "", fmt.Errorf("contribute: create PR: %w", err)
	}

	// 5. Update contribution status.
	if err := s.store.UpdateContributionStatus(contributionID, "submitted", prURL); err != nil {
		return "", fmt.Errorf("contribute: update status: %w", err)
	}

	return prURL, nil
}

// nonAlphanumHyphen matches characters that are not alphanumeric or a hyphen.
var nonAlphanumHyphen = regexp.MustCompile(`[^a-z0-9-]`)

// slugify returns a URL-safe slug for a title and year.
// Spaces are replaced with hyphens, non-alphanumeric characters are stripped,
// and the result is lowercased.
func slugify(title string, year int) string {
	lower := strings.ToLower(title)
	withHyphens := strings.ReplaceAll(lower, " ", "-")
	clean := nonAlphanumHyphen.ReplaceAllString(withHyphens, "")
	// Collapse multiple consecutive hyphens.
	multi := regexp.MustCompile(`-{2,}`)
	clean = multi.ReplaceAllString(clean, "-")
	clean = strings.Trim(clean, "-")
	return fmt.Sprintf("%s-%d", clean, year)
}
