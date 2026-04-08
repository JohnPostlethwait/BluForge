package contribute

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"

	ghpkg "github.com/johnpostlethwait/bluforge/internal/github"
	"github.com/johnpostlethwait/bluforge/internal/makemkv"
)

// SubmitUpdate executes the full update-contribution submission flow:
//  1. Load and validate the contribution from the database.
//  2. Parse stored JSON fields (MatchInfo, []TitleLabel, DiscScan).
//  3. Fetch existing disc JSON from the upstream repo.
//  4. Merge user edits into the existing disc JSON.
//  5. Download cover/front images if absent from the upstream repo.
//  6. Fork, branch, commit, and open a PR on GitHub.
//  7. Update the contribution status to "submitted" with the PR URL.
//
// Returns the PR URL on success.
func (s *Service) SubmitUpdate(ctx context.Context, contributionID int64) (string, error) {
	// 1. Load contribution.
	c, err := s.store.GetContribution(contributionID)
	if err != nil {
		return "", fmt.Errorf("contribute: load contribution %d: %w", contributionID, err)
	}
	if c == nil {
		return "", fmt.Errorf("contribute: contribution %d not found", contributionID)
	}
	if c.Status == "submitted" {
		return c.PRURL, nil // Already submitted — return existing PR URL.
	}

	// 2. Validate required fields.
	if c.MatchInfo == "" {
		return "", fmt.Errorf("contribute: contribution %d has no match_info — complete the draft first", contributionID)
	}
	if c.TitleLabels == "" {
		return "", fmt.Errorf("contribute: contribution %d has no title_labels — complete the draft first", contributionID)
	}

	// 3. Parse stored JSON.
	var mi MatchInfo
	if err := json.Unmarshal([]byte(c.MatchInfo), &mi); err != nil {
		return "", fmt.Errorf("contribute: parse match_info: %w", err)
	}

	var labels []TitleLabel
	if err := json.Unmarshal([]byte(c.TitleLabels), &labels); err != nil {
		return "", fmt.Errorf("contribute: parse title_labels: %w", err)
	}

	// Validate that at least one label has a non-empty type.
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
	forkOwner := strings.SplitN(fork, "/", 2)[0]

	// 4b-ii. Wait for the fork to be ready.
	if err := s.github.WaitForRepo(ctx, forkOwner, upstreamRepo); err != nil {
		return "", fmt.Errorf("contribute: wait for fork: %w", err)
	}

	// 4c. Build file paths.
	mediaDir := MediaDirPath(mi.MediaType, mi.MediaTitle, mi.MediaYear)
	releaseDir := mediaDir + "/" + mi.ReleaseSlug
	discFileName := fmt.Sprintf("disc%02d.json", mi.DiscIndex)
	discPath := releaseDir + "/" + discFileName

	// 4d. Fetch existing disc JSON from upstream repo.
	existingDiscJSON, err := s.github.GetFileContent(ctx, upstreamOwner, upstreamRepo, discPath)
	if err != nil {
		return "", fmt.Errorf("contribute: fetch existing disc JSON at %s: %w", discPath, err)
	}

	// 4e. Merge user edits into existing disc JSON.
	merged, err := MergeDiscJSON(existingDiscJSON, &scan, labels)
	if err != nil {
		return "", fmt.Errorf("contribute: merge disc JSON: %w", err)
	}
	mergedBytes, err := json.MarshalIndent(merged, "", "  ")
	if err != nil {
		return "", fmt.Errorf("contribute: marshal merged disc JSON: %w", err)
	}
	mergedContent := string(mergedBytes) + "\n"

	// 4f. Build file list — start with the merged disc JSON.
	files := []ghpkg.FileEntry{
		{Path: discPath, Content: mergedContent},
	}

	// 4g. Check/download cover and front images if absent from the repo.
	coverPath := mediaDir + "/cover.jpg"
	frontPath := releaseDir + "/front.jpg"

	coverExists, err := s.github.FileExists(ctx, upstreamOwner, upstreamRepo, coverPath)
	if err != nil {
		return "", fmt.Errorf("contribute: check cover image existence: %w", err)
	}
	frontExists, err := s.github.FileExists(ctx, upstreamOwner, upstreamRepo, frontPath)
	if err != nil {
		return "", fmt.Errorf("contribute: check front image existence: %w", err)
	}

	if (!coverExists || !frontExists) && mi.ImageURL != "" {
		imgBytes, imgErr := downloadFromURL(ctx, mi.ImageURL)
		if imgErr != nil {
			slog.Warn("contribute: failed to download disc image; submission will proceed without images",
				"image_url", mi.ImageURL, "error", imgErr)
		} else if len(imgBytes) > 0 {
			if !coverExists {
				files = append(files, ghpkg.FileEntry{Path: coverPath, Blob: imgBytes})
			}
			if !frontExists {
				files = append(files, ghpkg.FileEntry{Path: frontPath, Blob: imgBytes})
			}
		}
	}

	// 4h. Get default branch SHA from the upstream repo.
	baseBranch, baseSHA, err := s.github.GetDefaultBranchSHA(ctx, upstreamOwner, upstreamRepo)
	if err != nil {
		return "", fmt.Errorf("contribute: get default branch SHA: %w", err)
	}

	// 4i. Create branch.
	titleSlug := slugify(mi.MediaTitle, mi.MediaYear)
	branchName := ghpkg.ContributionBranchName(titleSlug, mi.ReleaseSlug) + "-update"
	if err := s.github.CreateBranch(ctx, forkOwner, upstreamRepo, branchName, baseSHA); err != nil {
		if !strings.Contains(err.Error(), "Reference already exists") {
			return "", fmt.Errorf("contribute: create branch: %w", err)
		}
	}

	// 4j. Commit files.
	commitMsg := fmt.Sprintf("Update %s (%d) - %s", mi.MediaTitle, mi.MediaYear, mi.ReleaseSlug)
	if err := s.github.CommitFiles(ctx, forkOwner, upstreamRepo, branchName, files, commitMsg); err != nil {
		return "", fmt.Errorf("contribute: commit files: %w", err)
	}

	// 4k. Open pull request.
	prTitle := fmt.Sprintf("Update %s (%d) - %s", mi.MediaTitle, mi.MediaYear, mi.ReleaseSlug)
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

// ResubmitUpdate pushes a corrective commit to the existing update PR branch without
// creating a new fork, branch, or pull request. Use this when the generated files had
// a bug and need to be regenerated and re-pushed.
//
// The contribution must already be in "submitted" status.
func (s *Service) ResubmitUpdate(ctx context.Context, contributionID int64) error {
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

	var mi MatchInfo
	if err := json.Unmarshal([]byte(c.MatchInfo), &mi); err != nil {
		return fmt.Errorf("contribute: parse match_info: %w", err)
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

	// Fetch and merge disc JSON.
	mediaDir := MediaDirPath(mi.MediaType, mi.MediaTitle, mi.MediaYear)
	releaseDir := mediaDir + "/" + mi.ReleaseSlug
	discFileName := fmt.Sprintf("disc%02d.json", mi.DiscIndex)
	discPath := releaseDir + "/" + discFileName

	existingDiscJSON, err := s.github.GetFileContent(ctx, upstreamOwner, upstreamRepo, discPath)
	if err != nil {
		return fmt.Errorf("contribute: fetch existing disc JSON at %s: %w", discPath, err)
	}

	merged, err := MergeDiscJSON(existingDiscJSON, &scan, labels)
	if err != nil {
		return fmt.Errorf("contribute: merge disc JSON: %w", err)
	}
	mergedBytes, err := json.MarshalIndent(merged, "", "  ")
	if err != nil {
		return fmt.Errorf("contribute: marshal merged disc JSON: %w", err)
	}
	mergedContent := string(mergedBytes) + "\n"

	files := []ghpkg.FileEntry{
		{Path: discPath, Content: mergedContent},
	}

	// Check/download images if absent.
	coverPath := mediaDir + "/cover.jpg"
	frontPath := releaseDir + "/front.jpg"
	coverExists, err := s.github.FileExists(ctx, upstreamOwner, upstreamRepo, coverPath)
	if err != nil {
		return fmt.Errorf("contribute: check cover image existence: %w", err)
	}
	frontExists, err := s.github.FileExists(ctx, upstreamOwner, upstreamRepo, frontPath)
	if err != nil {
		return fmt.Errorf("contribute: check front image existence: %w", err)
	}
	if (!coverExists || !frontExists) && mi.ImageURL != "" {
		imgBytes, imgErr := downloadFromURL(ctx, mi.ImageURL)
		if imgErr != nil {
			slog.Warn("contribute: resubmit update: failed to download disc image; proceeding without images",
				"image_url", mi.ImageURL, "error", imgErr)
		} else if len(imgBytes) > 0 {
			if !coverExists {
				files = append(files, ghpkg.FileEntry{Path: coverPath, Blob: imgBytes})
			}
			if !frontExists {
				files = append(files, ghpkg.FileEntry{Path: frontPath, Blob: imgBytes})
			}
		}
	}

	titleSlug := slugify(mi.MediaTitle, mi.MediaYear)
	branchName := ghpkg.ContributionBranchName(titleSlug, mi.ReleaseSlug) + "-update"
	commitMsg := fmt.Sprintf("Fix %s (%d) - %s: regenerate update contribution files", mi.MediaTitle, mi.MediaYear, mi.ReleaseSlug)

	err = s.github.CommitFiles(ctx, githubUser, upstreamRepo, branchName, files, commitMsg)
	if errors.Is(err, ghpkg.ErrBranchNotFound) {
		slog.Info("contribute: resubmit update: branch not found, recreating branch and opening new PR",
			"branch", branchName)
		return s.resubmitUpdateFresh(ctx, c.ID, githubUser, branchName, mi, files, commitMsg)
	}
	if err != nil {
		return fmt.Errorf("contribute: resubmit update commit files: %w", err)
	}

	// Reopen the PR if it was closed.
	if prNum := parsePRNumber(c.PRURL); prNum > 0 {
		if rerr := s.github.ReopenPR(ctx, upstreamOwner, upstreamRepo, prNum); rerr != nil {
			slog.Warn("contribute: resubmit update: could not reopen PR; files were pushed but PR may still be closed",
				"pr_url", c.PRURL, "error", rerr)
		}
	}

	return nil
}

// resubmitUpdateFresh is called by ResubmitUpdate when the contribution branch no longer
// exists on GitHub. It recreates the branch from the upstream default branch, commits the
// files, opens a new PR, and updates the DB.
func (s *Service) resubmitUpdateFresh(ctx context.Context, contributionID int64, githubUser, branchName string, mi MatchInfo, files []ghpkg.FileEntry, commitMsg string) error {
	fork, err := s.github.EnsureFork(ctx, upstreamOwner, upstreamRepo)
	if err != nil {
		return fmt.Errorf("contribute: resubmit update ensure fork: %w", err)
	}
	forkOwner := strings.SplitN(fork, "/", 2)[0]

	if err := s.github.WaitForRepo(ctx, forkOwner, upstreamRepo); err != nil {
		return fmt.Errorf("contribute: resubmit update wait for fork: %w", err)
	}

	baseBranch, baseSHA, err := s.github.GetDefaultBranchSHA(ctx, upstreamOwner, upstreamRepo)
	if err != nil {
		return fmt.Errorf("contribute: resubmit update get default branch SHA: %w", err)
	}

	if err := s.github.CreateBranch(ctx, forkOwner, upstreamRepo, branchName, baseSHA); err != nil {
		if !strings.Contains(err.Error(), "Reference already exists") {
			return fmt.Errorf("contribute: resubmit update create branch: %w", err)
		}
	}

	if err := s.github.CommitFiles(ctx, forkOwner, upstreamRepo, branchName, files, commitMsg); err != nil {
		return fmt.Errorf("contribute: resubmit update commit files (fresh): %w", err)
	}

	prTitle := fmt.Sprintf("Update %s (%d) - %s", mi.MediaTitle, mi.MediaYear, mi.ReleaseSlug)
	prHead := githubUser + ":" + branchName
	prURL, err := s.github.CreatePR(ctx, upstreamOwner, upstreamRepo, prHead, baseBranch, prTitle, prBody)
	if err != nil {
		return fmt.Errorf("contribute: resubmit update create PR: %w", err)
	}

	if err := s.store.UpdateContributionStatus(contributionID, "submitted", prURL); err != nil {
		return fmt.Errorf("contribute: resubmit update update status: %w", err)
	}

	return nil
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
