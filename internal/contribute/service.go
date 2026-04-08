package contribute

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strconv"
	"strings"

	ghpkg "github.com/johnpostlethwait/bluforge/internal/github"
	"github.com/johnpostlethwait/bluforge/internal/db"
	"github.com/johnpostlethwait/bluforge/internal/makemkv"
	"github.com/johnpostlethwait/bluforge/internal/tmdb"
)

// GitHubClient defines the GitHub operations needed for contributions.
type GitHubClient interface {
	GetUser(ctx context.Context) (string, error)
	EnsureFork(ctx context.Context, owner, repo string) (string, error)
	GetDefaultBranchSHA(ctx context.Context, owner, repo string) (string, string, error)
	CreateBranch(ctx context.Context, owner, repo, branchName, baseSHA string) error
	CommitFiles(ctx context.Context, owner, repo, branch string, files []ghpkg.FileEntry, message string) error
	CreatePR(ctx context.Context, upstreamOwner, upstreamRepo, head, baseBranch, title, body string) (string, error)
	ReopenPR(ctx context.Context, owner, repo string, prNumber int) error
	WaitForRepo(ctx context.Context, owner, repo string) error
	GetFileContent(ctx context.Context, owner, repo, path string) (string, error)
	FileExists(ctx context.Context, owner, repo, path string) (bool, error)
}

const (
	upstreamOwner = "TheDiscDb"
	upstreamRepo  = "data"
)

// prBody is appended to every PR opened against TheDiscDB data repository.
const prBody = "Submitted with the assistance of [BluForge](https://github.com/johnpostlethwait/BluForge)."

// Store is the subset of db.Store used by the service.
type Store interface {
	GetContribution(id int64) (*db.Contribution, error)
	UpdateContributionStatus(id int64, status, prURL string) error
}

// TMDBFetcher defines the TMDB operations needed for contributions.
type TMDBFetcher interface {
	GetDetails(ctx context.Context, id int, mediaType string) (json.RawMessage, *tmdb.MediaDetails, error)
	DownloadImage(ctx context.Context, posterPath, size string) ([]byte, error)
}

// Service orchestrates the full TheDiscDB contribution submission flow.
type Service struct {
	store  Store
	github GitHubClient
	tmdb   TMDBFetcher
}

// NewService creates a new contribution Service.
func NewService(store Store, github GitHubClient, tmdb TMDBFetcher) *Service {
	return &Service{store: store, github: github, tmdb: tmdb}
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
	if c.Status == "submitted" {
		return c.PRURL, nil // Already submitted — return existing PR URL
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

	// 3a. Parse TMDB ID as integer.
	tmdbIDInt, err := strconv.Atoi(c.TmdbID)
	if err != nil {
		return "", fmt.Errorf("contribute: tmdb_id %q is not a valid integer: %w", c.TmdbID, err)
	}

	// 3b. Fetch full TMDB details — required for metadata.json and tmdb.json.
	tmdbRaw, tmdbDetails, err := s.tmdb.GetDetails(ctx, tmdbIDInt, mediaType)
	if err != nil {
		return "", fmt.Errorf("contribute: fetch TMDB details for id %d: %w", tmdbIDInt, err)
	}

	// 3c. Download poster image — soft failure; submission proceeds without images if unavailable.
	var posterBytes []byte
	if tmdbDetails.PosterPath != "" {
		imgBytes, imgErr := s.tmdb.DownloadImage(ctx, tmdbDetails.PosterPath, "original")
		if imgErr != nil {
			slog.Warn("contribute: failed to download TMDB poster; submission will proceed without images",
				"poster_path", tmdbDetails.PosterPath, "error", imgErr)
		} else {
			posterBytes = imgBytes
		}
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

	// 4b-ii. Wait for the fork to be ready (GitHub fork creation is async).
	if err := s.github.WaitForRepo(ctx, forkOwner, upstreamRepo); err != nil {
		return "", fmt.Errorf("contribute: wait for fork: %w", err)
	}

	// 4c. Generate file contents.
	titleSlug := slugify(mediaTitle, mediaYear)
	releaseSlug := ReleaseSlug(ri.Year, ri.Format)
	mediaDir := MediaDirPath(mediaType, mediaTitle, mediaYear)
	releaseDir := mediaDir + "/" + releaseSlug

	releaseJSON := GenerateReleaseJSON(ri, githubUser)
	discJSON := GenerateDiscJSON(&scan, ri.Format)
	summary := GenerateSummary(&scan, labels)
	rawOutput := c.RawOutput
	metadataJSON := GenerateMetadataJSON(tmdbDetails, mediaType, mediaTitle, mediaYear)

	files := []ghpkg.FileEntry{
		{Path: releaseDir + "/release.json", Content: releaseJSON},
		{Path: releaseDir + "/disc01.json", Content: discJSON},
		{Path: releaseDir + "/disc01-summary.txt", Content: summary},
		{Path: releaseDir + "/disc01.txt", Content: rawOutput},
		{Path: mediaDir + "/metadata.json", Content: metadataJSON},
		{Path: mediaDir + "/tmdb.json", Content: string(tmdbRaw)},
	}
	if len(posterBytes) > 0 {
		files = append(files, ghpkg.FileEntry{Path: mediaDir + "/cover.jpg", Blob: posterBytes})
	}
	if ri.FrontImageURL != "" {
		frontBytes, frontErr := downloadFromURL(ctx, ri.FrontImageURL)
		if frontErr != nil {
			slog.Warn("contribute: failed to download front cover image; submission will proceed without front.jpg",
				"front_image_url", ri.FrontImageURL, "error", frontErr)
		} else if len(frontBytes) > 0 {
			files = append(files, ghpkg.FileEntry{Path: releaseDir + "/front.jpg", Blob: frontBytes})
		}
	}

	// 4d. Get default branch SHA from the upstream repo (not the fork).
	baseBranch, baseSHA, err := s.github.GetDefaultBranchSHA(ctx, upstreamOwner, upstreamRepo)
	if err != nil {
		return "", fmt.Errorf("contribute: get default branch SHA: %w", err)
	}

	// 4e. Create branch.
	branchName := ghpkg.ContributionBranchName(titleSlug, releaseSlug)
	if err := s.github.CreateBranch(ctx, forkOwner, upstreamRepo, branchName, baseSHA); err != nil {
		// Branch may already exist from a prior partial attempt — continue if so.
		if !strings.Contains(err.Error(), "Reference already exists") {
			return "", fmt.Errorf("contribute: create branch: %w", err)
		}
	}

	// 4f. Commit files.
	commitMsg := fmt.Sprintf("Add %s (%d) - %s", mediaTitle, mediaYear, ri.Format)
	if err := s.github.CommitFiles(ctx, forkOwner, upstreamRepo, branchName, files, commitMsg); err != nil {
		return "", fmt.Errorf("contribute: commit files: %w", err)
	}

	// 4g. Open pull request. head is "user:branch" format.
	prTitle := fmt.Sprintf("Add %s (%d) - %s", mediaTitle, mediaYear, ri.Format)
	prHead := githubUser + ":" + branchName
	prURL, err := s.github.CreatePR(ctx, upstreamOwner, upstreamRepo, prHead, baseBranch, prTitle, prBody)
	if err != nil {
		return "", fmt.Errorf("contribute: create PR: %w", err)
	}

	// 5. Update contribution status.
	if err := s.store.UpdateContributionStatus(contributionID, "submitted", prURL); err != nil {
		return "", fmt.Errorf("contribute: update status: %w", err)
	}

	return prURL, nil
}

// Resubmit pushes a corrective commit to the existing PR branch without creating
// a new fork, branch, or pull request. Use this when the generated files had a
// bug and need to be regenerated and re-pushed.
//
// The contribution must already be in "submitted" status. media_title, mediaYear,
// and mediaType must match what was used at Submit time so the branch name and
// file paths are reconstructed correctly.
func (s *Service) Resubmit(ctx context.Context, contributionID int64, mediaTitle string, mediaYear int, mediaType string) error {
	c, err := s.store.GetContribution(contributionID)
	if err != nil {
		return fmt.Errorf("contribute: load contribution %d: %w", contributionID, err)
	}
	if c == nil {
		return fmt.Errorf("contribute: contribution %d not found", contributionID)
	}
	if c.Status != "submitted" {
		return fmt.Errorf("contribute: contribution %d is not submitted (status: %s)", contributionID, c.Status)
	}

	var ri ReleaseInfo
	if err := json.Unmarshal([]byte(c.ReleaseInfo), &ri); err != nil {
		return fmt.Errorf("contribute: parse release_info: %w", err)
	}

	var labels []TitleLabel
	if err := json.Unmarshal([]byte(c.TitleLabels), &labels); err != nil {
		return fmt.Errorf("contribute: parse title_labels: %w", err)
	}

	var scan makemkv.DiscScan
	if err := json.Unmarshal([]byte(c.ScanJSON), &scan); err != nil {
		return fmt.Errorf("contribute: parse scan_json: %w", err)
	}

	githubUser, err := s.github.GetUser(ctx)
	if err != nil {
		return fmt.Errorf("contribute: get github user: %w", err)
	}

	// Fetch TMDB details for regenerated files.
	tmdbIDInt, err := strconv.Atoi(c.TmdbID)
	if err != nil {
		return fmt.Errorf("contribute: tmdb_id %q is not a valid integer: %w", c.TmdbID, err)
	}
	tmdbRaw, tmdbDetails, err := s.tmdb.GetDetails(ctx, tmdbIDInt, mediaType)
	if err != nil {
		return fmt.Errorf("contribute: fetch TMDB details for id %d: %w", tmdbIDInt, err)
	}
	var posterBytes []byte
	if tmdbDetails.PosterPath != "" {
		imgBytes, imgErr := s.tmdb.DownloadImage(ctx, tmdbDetails.PosterPath, "original")
		if imgErr != nil {
			slog.Warn("contribute: resubmit: failed to download TMDB poster; proceeding without images",
				"poster_path", tmdbDetails.PosterPath, "error", imgErr)
		} else {
			posterBytes = imgBytes
		}
	}

	releaseSlug := ReleaseSlug(ri.Year, ri.Format)
	titleSlug := slugify(mediaTitle, mediaYear)
	mediaDir := MediaDirPath(mediaType, mediaTitle, mediaYear)
	releaseDir := mediaDir + "/" + releaseSlug
	branchName := ghpkg.ContributionBranchName(titleSlug, releaseSlug)

	files := []ghpkg.FileEntry{
		{Path: releaseDir + "/release.json", Content: GenerateReleaseJSON(ri, githubUser)},
		{Path: releaseDir + "/disc01.json", Content: GenerateDiscJSON(&scan, ri.Format)},
		{Path: releaseDir + "/disc01-summary.txt", Content: GenerateSummary(&scan, labels)},
		{Path: releaseDir + "/disc01.txt", Content: c.RawOutput},
		{Path: mediaDir + "/metadata.json", Content: GenerateMetadataJSON(tmdbDetails, mediaType, mediaTitle, mediaYear)},
		{Path: mediaDir + "/tmdb.json", Content: string(tmdbRaw)},
	}
	if len(posterBytes) > 0 {
		files = append(files, ghpkg.FileEntry{Path: mediaDir + "/cover.jpg", Blob: posterBytes})
	}
	if ri.FrontImageURL != "" {
		frontBytes, frontErr := downloadFromURL(ctx, ri.FrontImageURL)
		if frontErr != nil {
			slog.Warn("contribute: resubmit: failed to download front cover image; proceeding without front.jpg",
				"front_image_url", ri.FrontImageURL, "error", frontErr)
		} else if len(frontBytes) > 0 {
			files = append(files, ghpkg.FileEntry{Path: releaseDir + "/front.jpg", Blob: frontBytes})
		}
	}

	commitMsg := fmt.Sprintf("Fix %s (%d) - %s: regenerate all contribution files", mediaTitle, mediaYear, ri.Format)
	err = s.github.CommitFiles(ctx, githubUser, upstreamRepo, branchName, files, commitMsg)
	if errors.Is(err, ghpkg.ErrBranchNotFound) {
		// The original branch was deleted (e.g. PR was closed and branch removed).
		// Recreate the branch from upstream and open a new PR.
		slog.Info("contribute: resubmit: branch not found, recreating branch and opening new PR",
			"branch", branchName)
		return s.resubmitFresh(ctx, c.ID, githubUser, branchName, mediaTitle, mediaYear, ri.Format, files, commitMsg)
	}
	if err != nil {
		return fmt.Errorf("contribute: resubmit commit files: %w", err)
	}

	// Reopen the PR if it was closed (e.g. user closed it before fixing).
	if prNum := parsePRNumber(c.PRURL); prNum > 0 {
		if rerr := s.github.ReopenPR(ctx, upstreamOwner, upstreamRepo, prNum); rerr != nil {
			slog.Warn("contribute: resubmit: could not reopen PR; files were pushed but PR may still be closed",
				"pr_url", c.PRURL, "error", rerr)
		}
	}

	return nil
}

// parsePRNumber extracts the pull request number from a GitHub PR URL.
// Returns 0 if the URL does not match the expected format.
func parsePRNumber(prURL string) int {
	if prURL == "" {
		return 0
	}
	parts := strings.Split(prURL, "/")
	if len(parts) == 0 {
		return 0
	}
	n, err := strconv.Atoi(parts[len(parts)-1])
	if err != nil {
		return 0
	}
	return n
}

// resubmitFresh is called by Resubmit when the contribution branch no longer exists on
// GitHub (e.g. the PR was closed and the branch deleted). It recreates the branch from
// the upstream default branch, commits the files, opens a new PR, and updates the DB.
func (s *Service) resubmitFresh(ctx context.Context, contributionID int64, githubUser, branchName, mediaTitle string, mediaYear int, format string, files []ghpkg.FileEntry, commitMsg string) error {
	fork, err := s.github.EnsureFork(ctx, upstreamOwner, upstreamRepo)
	if err != nil {
		return fmt.Errorf("contribute: resubmit ensure fork: %w", err)
	}
	forkOwner := strings.SplitN(fork, "/", 2)[0]

	if err := s.github.WaitForRepo(ctx, forkOwner, upstreamRepo); err != nil {
		return fmt.Errorf("contribute: resubmit wait for fork: %w", err)
	}

	baseBranch, baseSHA, err := s.github.GetDefaultBranchSHA(ctx, upstreamOwner, upstreamRepo)
	if err != nil {
		return fmt.Errorf("contribute: resubmit get default branch SHA: %w", err)
	}

	if err := s.github.CreateBranch(ctx, forkOwner, upstreamRepo, branchName, baseSHA); err != nil {
		if !strings.Contains(err.Error(), "Reference already exists") {
			return fmt.Errorf("contribute: resubmit create branch: %w", err)
		}
	}

	if err := s.github.CommitFiles(ctx, forkOwner, upstreamRepo, branchName, files, commitMsg); err != nil {
		return fmt.Errorf("contribute: resubmit commit files (fresh): %w", err)
	}

	prTitle := fmt.Sprintf("Add %s (%d) - %s", mediaTitle, mediaYear, format)
	prHead := githubUser + ":" + branchName
	prURL, err := s.github.CreatePR(ctx, upstreamOwner, upstreamRepo, prHead, baseBranch, prTitle, prBody)
	if err != nil {
		return fmt.Errorf("contribute: resubmit create PR: %w", err)
	}

	if err := s.store.UpdateContributionStatus(contributionID, "submitted", prURL); err != nil {
		return fmt.Errorf("contribute: resubmit update status: %w", err)
	}

	return nil
}

var (
	// nonAlphanumHyphen matches characters that are not alphanumeric or a hyphen.
	nonAlphanumHyphen = regexp.MustCompile(`[^a-z0-9-]`)
	// multiHyphen collapses runs of consecutive hyphens.
	multiHyphen = regexp.MustCompile(`-{2,}`)
)

// slugify returns a URL-safe slug for a title and year.
// Spaces are replaced with hyphens, non-alphanumeric characters are stripped,
// and the result is lowercased.
func slugify(title string, year int) string {
	lower := strings.ToLower(title)
	withHyphens := strings.ReplaceAll(lower, " ", "-")
	clean := nonAlphanumHyphen.ReplaceAllString(withHyphens, "")
	// Collapse multiple consecutive hyphens.
	clean = multiHyphen.ReplaceAllString(clean, "-")
	clean = strings.Trim(clean, "-")
	return fmt.Sprintf("%s-%d", clean, year)
}
