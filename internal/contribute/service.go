package contribute

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/johnpostlethwait/bluforge/internal/db"
	ghpkg "github.com/johnpostlethwait/bluforge/internal/github"
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

// Execute runs the unified contribution submission pipeline:
//  1. Load contribution from DB
//  2. Parse stored JSON (ReleaseInfo, TitleLabels, DiscScan, and MatchInfo for update type)
//  3. Validate: at least one title label has a non-empty type
//  4. Get GitHub user
//  5. Build files — branch on contribution_type ("add" vs "update")
//  6. Commit — branch on status ("pending"/"draft" → submitFresh; "submitted" → pushToExisting)
//  7. Update DB status to "submitted" with PR URL
//  8. Return PR URL
func (s *Service) Execute(ctx context.Context, contributionID int64) (string, error) {
	// 1. Load contribution.
	c, err := s.store.GetContribution(contributionID)
	if err != nil {
		return "", fmt.Errorf("contribute: load contribution %d: %w", contributionID, err)
	}
	if c == nil {
		return "", fmt.Errorf("contribute: contribution %d not found", contributionID)
	}

	// 2. Parse stored JSON.
	var ri ReleaseInfo
	if c.ReleaseInfo != "" {
		if err := json.Unmarshal([]byte(c.ReleaseInfo), &ri); err != nil {
			return "", fmt.Errorf("contribute: parse release_info: %w", err)
		}
	}

	var labels []TitleLabel
	if c.TitleLabels != "" {
		if err := json.Unmarshal([]byte(c.TitleLabels), &labels); err != nil {
			return "", fmt.Errorf("contribute: parse title_labels: %w", err)
		}
	}

	var scan makemkv.DiscScan
	if c.ScanJSON != "" {
		if err := json.Unmarshal([]byte(c.ScanJSON), &scan); err != nil {
			return "", fmt.Errorf("contribute: parse scan_json: %w", err)
		}
	}

	// 3. Validate: at least one title label has a non-empty type.
	hasTyped := false
	for _, l := range labels {
		if l.Type != "" {
			hasTyped = true
			break
		}
	}
	if !hasTyped {
		return "", fmt.Errorf("contribute: contribution %d has no typed title label — assign at least one title type", contributionID)
	}

	// 4. Get GitHub user.
	githubUser, err := s.github.GetUser(ctx)
	if err != nil {
		return "", fmt.Errorf("contribute: get github user: %w", err)
	}

	// 5. Build files — branch on ContributionType.
	var files []ghpkg.FileEntry
	var branchName, commitMsg, prTitle string

	switch c.ContributionType {
	case "update":
		var mi MatchInfo
		if c.MatchInfo != "" {
			if err := json.Unmarshal([]byte(c.MatchInfo), &mi); err != nil {
				return "", fmt.Errorf("contribute: parse match_info: %w", err)
			}
		}
		files, branchName, commitMsg, prTitle, err = s.buildUpdateFiles(ctx, c, ri, mi, labels, &scan, githubUser)
	default: // "add"
		files, branchName, commitMsg, prTitle, err = s.buildAddFiles(ctx, c, ri, labels, &scan, githubUser)
	}
	if err != nil {
		return "", err
	}

	// 6. Commit — branch on Status.
	var prURL string
	if c.Status == "submitted" {
		prURL, err = s.pushToExisting(ctx, contributionID, githubUser, branchName, files, commitMsg, prTitle, c.PRURL)
	} else {
		prURL, err = s.submitFresh(ctx, githubUser, branchName, files, commitMsg, prTitle)
	}
	if err != nil {
		return "", err
	}

	// 7. Update DB status.
	if err := s.store.UpdateContributionStatus(contributionID, "submitted", prURL); err != nil {
		return "", fmt.Errorf("contribute: update status: %w", err)
	}

	return prURL, nil
}

// buildAddFiles generates files for a new "add" contribution.
// Returns (files, branchName, commitMsg, prTitle, error).
func (s *Service) buildAddFiles(ctx context.Context, c *db.Contribution, ri ReleaseInfo, labels []TitleLabel, scan *makemkv.DiscScan, githubUser string) ([]ghpkg.FileEntry, string, string, string, error) {
	// Validate required fields for add contributions.
	if c.TmdbID == "" {
		return nil, "", "", "", fmt.Errorf("contribute: contribution %d has no tmdb_id — complete the draft first", c.ID)
	}
	if c.ReleaseInfo == "" {
		return nil, "", "", "", fmt.Errorf("contribute: contribution %d has no release_info — complete the draft first", c.ID)
	}

	// Determine media metadata from the contribution record.
	mediaTitle := c.DiscName
	mediaYear := ri.Year
	mediaType := ri.MediaType
	if mediaType == "" {
		mediaType = "movie"
	}

	// Parse TMDB ID and fetch details.
	tmdbIDInt, err := strconv.Atoi(c.TmdbID)
	if err != nil {
		return nil, "", "", "", fmt.Errorf("contribute: tmdb_id %q is not a valid integer: %w", c.TmdbID, err)
	}

	tmdbRaw, tmdbDetails, err := s.tmdb.GetDetails(ctx, tmdbIDInt, mediaType)
	if err != nil {
		return nil, "", "", "", fmt.Errorf("contribute: fetch TMDB details for id %d: %w", tmdbIDInt, err)
	}

	// Download poster image — soft failure.
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

	// Compute paths.
	titleSlug := slugify(mediaTitle, mediaYear)
	releaseSlug := ReleaseSlug(ri.Year, ri.Format)
	mediaDir := MediaDirPath(mediaType, mediaTitle, mediaYear)
	releaseDir := mediaDir + "/" + releaseSlug

	// Generate file contents.
	files := []ghpkg.FileEntry{
		{Path: releaseDir + "/release.json", Content: GenerateReleaseJSON(ri, githubUser)},
		{Path: releaseDir + "/disc01.json", Content: GenerateDiscJSON(scan, ri.Format)},
		{Path: releaseDir + "/disc01-summary.txt", Content: GenerateSummary(scan, labels)},
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
			slog.Warn("contribute: failed to download front cover image; submission will proceed without front.jpg",
				"front_image_url", ri.FrontImageURL, "error", frontErr)
		} else if len(frontBytes) > 0 {
			files = append(files, ghpkg.FileEntry{Path: releaseDir + "/front.jpg", Blob: frontBytes})
		}
	}

	branchName := ghpkg.ContributionBranchName(titleSlug, releaseSlug)

	// Commit/PR message varies by status.
	var commitMsg, prTitle string
	if c.Status == "submitted" {
		commitMsg = fmt.Sprintf("Fix %s (%d) - %s: regenerate all contribution files", mediaTitle, mediaYear, ri.Format)
	} else {
		commitMsg = fmt.Sprintf("Add %s (%d) - %s", mediaTitle, mediaYear, ri.Format)
	}
	prTitle = fmt.Sprintf("Add %s (%d) - %s", mediaTitle, mediaYear, ri.Format)

	return files, branchName, commitMsg, prTitle, nil
}

// buildUpdateFiles generates files for an "update" contribution.
// Returns (files, branchName, commitMsg, prTitle, error).
func (s *Service) buildUpdateFiles(ctx context.Context, c *db.Contribution, ri ReleaseInfo, mi MatchInfo, labels []TitleLabel, scan *makemkv.DiscScan, githubUser string) ([]ghpkg.FileEntry, string, string, string, error) {
	// Build file paths from MatchInfo.
	mediaDir := MediaDirPath(mi.MediaType, mi.MediaTitle, mi.MediaYear)
	releaseDir := mediaDir + "/" + mi.ReleaseSlug
	discFileName := fmt.Sprintf("disc%02d.json", mi.DiscIndex)
	discPath := releaseDir + "/" + discFileName

	// Fetch existing disc JSON from upstream repo.
	existingDiscJSON, err := s.github.GetFileContent(ctx, upstreamOwner, upstreamRepo, discPath)
	if err != nil {
		return nil, "", "", "", fmt.Errorf("contribute: fetch existing disc JSON at %s: %w", discPath, err)
	}

	// Merge user edits into existing disc JSON.
	merged, err := MergeDiscJSON(existingDiscJSON, scan, labels)
	if err != nil {
		return nil, "", "", "", fmt.Errorf("contribute: merge disc JSON: %w", err)
	}
	mergedBytes, err := json.MarshalIndent(merged, "", "  ")
	if err != nil {
		return nil, "", "", "", fmt.Errorf("contribute: marshal merged disc JSON: %w", err)
	}
	mergedContent := string(mergedBytes) + "\n"

	files := []ghpkg.FileEntry{
		{Path: discPath, Content: mergedContent},
	}

	// Handle cover image — only upload if missing from upstream.
	coverPath := mediaDir + "/cover.jpg"
	coverExists, err := s.github.FileExists(ctx, upstreamOwner, upstreamRepo, coverPath)
	if err != nil {
		return nil, "", "", "", fmt.Errorf("contribute: check cover image existence: %w", err)
	}
	if !coverExists && mi.ImageURL != "" {
		imgBytes, imgErr := downloadFromURL(ctx, mi.ImageURL)
		if imgErr != nil {
			slog.Warn("contribute: failed to download cover image; submission will proceed without cover.jpg",
				"image_url", mi.ImageURL, "error", imgErr)
		} else if len(imgBytes) > 0 {
			files = append(files, ghpkg.FileEntry{Path: coverPath, Blob: imgBytes})
		}
	}

	// front.jpg: always upload if the user supplied a URL (replaces existing).
	frontPath := releaseDir + "/front.jpg"
	if ri.FrontImageURL != "" {
		frontBytes, frontErr := downloadFromURL(ctx, ri.FrontImageURL)
		if frontErr != nil {
			slog.Warn("contribute: failed to download front cover image; submission will proceed without front.jpg",
				"front_image_url", ri.FrontImageURL, "error", frontErr)
		} else if len(frontBytes) > 0 {
			files = append(files, ghpkg.FileEntry{Path: frontPath, Blob: frontBytes})
		}
	}

	// Patch release.json with ASIN and/or ReleaseDate if provided.
	if ri.ASIN != "" || ri.ReleaseDate != "" {
		releasePath := releaseDir + "/release.json"
		if patchedRelease, patchErr := patchReleaseJSON(ctx, s.github, ri, releasePath); patchErr != nil {
			slog.Warn("contribute: failed to patch release.json; submission will proceed without it",
				"path", releasePath, "error", patchErr)
		} else if patchedRelease != "" {
			files = append(files, ghpkg.FileEntry{Path: releasePath, Content: patchedRelease})
		}
	}

	// Branch name and messages.
	titleSlug := slugify(mi.MediaTitle, mi.MediaYear)
	branchName := ghpkg.ContributionBranchName(titleSlug, mi.ReleaseSlug) + "-update"

	var commitMsg, prTitle string
	if c.Status == "submitted" {
		commitMsg = fmt.Sprintf("Fix %s (%d) - %s: regenerate update contribution files", mi.MediaTitle, mi.MediaYear, mi.ReleaseSlug)
	} else {
		commitMsg = fmt.Sprintf("Update %s (%d) - %s", mi.MediaTitle, mi.MediaYear, mi.ReleaseSlug)
	}
	prTitle = fmt.Sprintf("Update %s (%d) - %s", mi.MediaTitle, mi.MediaYear, mi.ReleaseSlug)

	return files, branchName, commitMsg, prTitle, nil
}

// submitFresh forks the upstream repo (if needed), creates a branch, commits files,
// and opens a new PR. Returns the PR URL.
func (s *Service) submitFresh(ctx context.Context, githubUser, branchName string, files []ghpkg.FileEntry, commitMsg, prTitle string) (string, error) {
	fork, err := s.github.EnsureFork(ctx, upstreamOwner, upstreamRepo)
	if err != nil {
		return "", fmt.Errorf("contribute: ensure fork: %w", err)
	}
	forkOwner := strings.SplitN(fork, "/", 2)[0]

	if err := s.github.WaitForRepo(ctx, forkOwner, upstreamRepo); err != nil {
		return "", fmt.Errorf("contribute: wait for fork: %w", err)
	}

	baseBranch, baseSHA, err := s.github.GetDefaultBranchSHA(ctx, upstreamOwner, upstreamRepo)
	if err != nil {
		return "", fmt.Errorf("contribute: get default branch SHA: %w", err)
	}

	if err := s.github.CreateBranch(ctx, forkOwner, upstreamRepo, branchName, baseSHA); err != nil {
		// Branch may already exist from a prior partial attempt — continue if so.
		if !strings.Contains(err.Error(), "Reference already exists") {
			return "", fmt.Errorf("contribute: create branch: %w", err)
		}
	}

	if err := s.github.CommitFiles(ctx, forkOwner, upstreamRepo, branchName, files, commitMsg); err != nil {
		return "", fmt.Errorf("contribute: commit files: %w", err)
	}

	prHead := githubUser + ":" + branchName
	prURL, err := s.github.CreatePR(ctx, upstreamOwner, upstreamRepo, prHead, baseBranch, prTitle, prBody)
	if err != nil {
		return "", fmt.Errorf("contribute: create PR: %w", err)
	}

	return prURL, nil
}

// pushToExisting commits files to an existing branch. If the branch no longer exists
// (e.g. PR was closed and branch deleted), it falls back to submitFresh. If the PR
// was closed, it attempts to reopen it.
func (s *Service) pushToExisting(ctx context.Context, contributionID int64, githubUser, branchName string, files []ghpkg.FileEntry, commitMsg, prTitle, existingPRURL string) (string, error) {
	err := s.github.CommitFiles(ctx, githubUser, upstreamRepo, branchName, files, commitMsg)
	if errors.Is(err, ghpkg.ErrBranchNotFound) {
		slog.Info("contribute: branch not found, recreating branch and opening new PR",
			"branch", branchName)
		return s.submitFresh(ctx, githubUser, branchName, files, commitMsg, prTitle)
	}
	if err != nil {
		return "", fmt.Errorf("contribute: commit files to existing branch: %w", err)
	}

	// Reopen the PR if it was closed.
	if prNum := parsePRNumber(existingPRURL); prNum > 0 {
		if rerr := s.github.ReopenPR(ctx, upstreamOwner, upstreamRepo, prNum); rerr != nil {
			slog.Warn("contribute: could not reopen PR; files were pushed but PR may still be closed",
				"pr_url", existingPRURL, "error", rerr)
		}
	}

	return existingPRURL, nil
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

// patchReleaseJSON fetches the existing release.json from the upstream repo,
// patches in any ASIN and/or ReleaseDate from ri, and returns the updated JSON.
// Returns an empty string (no error) when the file does not exist upstream.
func patchReleaseJSON(ctx context.Context, gh GitHubClient, ri ReleaseInfo, releasePath string) (string, error) {
	exists, err := gh.FileExists(ctx, upstreamOwner, upstreamRepo, releasePath)
	if err != nil {
		return "", fmt.Errorf("check release.json existence: %w", err)
	}
	if !exists {
		return "", nil // Nothing to patch — new contributions handle release.json creation.
	}

	existingJSON, err := gh.GetFileContent(ctx, upstreamOwner, upstreamRepo, releasePath)
	if err != nil {
		return "", fmt.Errorf("fetch release.json: %w", err)
	}

	var rel ReleaseJSON
	if err := json.Unmarshal([]byte(existingJSON), &rel); err != nil {
		return "", fmt.Errorf("parse release.json: %w", err)
	}

	changed := false
	if ri.ASIN != "" && rel.Asin != ri.ASIN {
		rel.Asin = ri.ASIN
		changed = true
	}
	if ri.ReleaseDate != "" {
		if t, err := time.Parse("2006-01-02", ri.ReleaseDate); err == nil {
			formatted := t.UTC().Format(time.RFC3339)
			if rel.ReleaseDate != formatted {
				rel.ReleaseDate = formatted
				changed = true
			}
		}
	}

	if !changed {
		return "", nil
	}

	data, err := json.MarshalIndent(rel, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal patched release.json: %w", err)
	}
	return string(data) + "\n", nil
}

// downloadFromURL fetches the content at url and returns the raw bytes.
func downloadFromURL(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("download: create request: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download: do request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download: unexpected status %d for %s", resp.StatusCode, url)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("download: read body: %w", err)
	}
	return data, nil
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
