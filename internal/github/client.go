package github

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	gh "github.com/google/go-github/v72/github"
	"golang.org/x/oauth2"
)

// Client wraps go-github for TheDiscDB contribution operations.
type Client struct {
	gh *gh.Client
}

// NewClient creates a GitHub client authenticated with the given PAT.
func NewClient(token string) (*Client, error) {
	if token == "" {
		return nil, fmt.Errorf("github: token is required")
	}
	ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: token})
	tc := oauth2.NewClient(context.Background(), ts)
	return &Client{gh: gh.NewClient(tc)}, nil
}

// NewClientFromGH creates a Client from an already-configured go-github client.
// Used in tests to wire a custom base URL.
func NewClientFromGH(ghClient *gh.Client) *Client {
	return &Client{gh: ghClient}
}

// WaitForRepo polls until owner/repo is reachable or ctx is cancelled.
// It adds an internal 30-second cap on top of the caller's context.
// Retries every 2 seconds.
func (c *Client) WaitForRepo(ctx context.Context, owner, repo string) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	for {
		_, _, err := c.gh.Repositories.Get(ctx, owner, repo)
		if err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("github: wait for repo %s/%s: %w", owner, repo, ctx.Err())
		case <-time.After(2 * time.Second):
			// retry
		}
	}
}

// GetUser returns the authenticated user's login name.
func (c *Client) GetUser(ctx context.Context) (string, error) {
	user, _, err := c.gh.Users.Get(ctx, "")
	if err != nil {
		return "", fmt.Errorf("github: get user: %w", err)
	}
	return user.GetLogin(), nil
}

// EnsureFork forks owner/repo to the authenticated user's account.
// Returns the fork's full name (e.g. "user/data"). If the fork already
// exists, returns it without error.
func (c *Client) EnsureFork(ctx context.Context, owner, repo string) (string, error) {
	fork, _, err := c.gh.Repositories.CreateFork(ctx, owner, repo, &gh.RepositoryCreateForkOptions{})
	if err != nil {
		// 202 Accepted is the normal response — the fork is being created.
		if _, ok := err.(*gh.AcceptedError); ok {
			user, userErr := c.GetUser(ctx)
			if userErr != nil {
				return "", userErr
			}
			return user + "/" + repo, nil
		}
		// Fork already exists.
		if strings.Contains(err.Error(), "already exists") {
			user, userErr := c.GetUser(ctx)
			if userErr != nil {
				return "", userErr
			}
			return user + "/" + repo, nil
		}
		return "", fmt.Errorf("github: create fork: %w", err)
	}
	return fork.GetFullName(), nil
}

// GetDefaultBranchSHA returns the default branch name and the SHA of its HEAD commit.
func (c *Client) GetDefaultBranchSHA(ctx context.Context, owner, repo string) (branch string, sha string, err error) {
	repository, _, err := c.gh.Repositories.Get(ctx, owner, repo)
	if err != nil {
		return "", "", fmt.Errorf("github: get repo: %w", err)
	}
	branch = repository.GetDefaultBranch()
	ref, _, err := c.gh.Git.GetRef(ctx, owner, repo, "refs/heads/"+branch)
	if err != nil {
		return "", "", fmt.Errorf("github: get ref %s: %w", branch, err)
	}
	return branch, ref.GetObject().GetSHA(), nil
}

// CreateBranch creates a new branch on owner/repo from the given base SHA.
func (c *Client) CreateBranch(ctx context.Context, owner, repo, branchName, baseSHA string) error {
	ref := &gh.Reference{
		Ref:    gh.Ptr("refs/heads/" + branchName),
		Object: &gh.GitObject{SHA: gh.Ptr(baseSHA)},
	}
	_, _, err := c.gh.Git.CreateRef(ctx, owner, repo, ref)
	if err != nil {
		return fmt.Errorf("github: create branch %s: %w", branchName, err)
	}
	return nil
}

// FileEntry represents a file to commit.
type FileEntry struct {
	Path    string
	Content string // text content; mutually exclusive with Blob
	Blob    []byte // binary content; if non-nil, Content is ignored and a blob is created
}

// CreateBlob creates a base64-encoded blob in the repository and returns its SHA.
// Use the returned SHA in a tree entry for binary files.
func (c *Client) CreateBlob(ctx context.Context, owner, repo string, data []byte) (string, error) {
	blob := &gh.Blob{
		Content:  gh.Ptr(base64.StdEncoding.EncodeToString(data)),
		Encoding: gh.Ptr("base64"),
	}
	created, _, err := c.gh.Git.CreateBlob(ctx, owner, repo, blob)
	if err != nil {
		return "", fmt.Errorf("github: create blob: %w", err)
	}
	return created.GetSHA(), nil
}

// CommitFiles commits multiple files to a branch in a single commit using
// the Git Trees API.
func (c *Client) CommitFiles(ctx context.Context, owner, repo, branch string, files []FileEntry, message string) error {
	// Get the branch ref.
	ref, _, err := c.gh.Git.GetRef(ctx, owner, repo, "refs/heads/"+branch)
	if err != nil {
		return fmt.Errorf("github: get branch ref: %w", err)
	}
	parentSHA := ref.GetObject().GetSHA()

	// Get the parent commit's tree.
	parentCommit, _, err := c.gh.Git.GetCommit(ctx, owner, repo, parentSHA)
	if err != nil {
		return fmt.Errorf("github: get parent commit: %w", err)
	}
	baseTreeSHA := parentCommit.GetTree().GetSHA()

	// Build tree entries.
	var entries []*gh.TreeEntry
	for _, f := range files {
		if f.Blob != nil {
			blobSHA, err := c.CreateBlob(ctx, owner, repo, f.Blob)
			if err != nil {
				return fmt.Errorf("github: create blob for %s: %w", f.Path, err)
			}
			entries = append(entries, &gh.TreeEntry{
				Path: gh.Ptr(f.Path),
				Mode: gh.Ptr("100644"),
				Type: gh.Ptr("blob"),
				SHA:  gh.Ptr(blobSHA),
			})
		} else {
			entries = append(entries, &gh.TreeEntry{
				Path:    gh.Ptr(f.Path),
				Mode:    gh.Ptr("100644"),
				Type:    gh.Ptr("blob"),
				Content: gh.Ptr(f.Content),
			})
		}
	}

	// Create tree.
	tree, _, err := c.gh.Git.CreateTree(ctx, owner, repo, baseTreeSHA, entries)
	if err != nil {
		return fmt.Errorf("github: create tree: %w", err)
	}

	// Create commit.
	commit := &gh.Commit{
		Message: gh.Ptr(message),
		Tree:    tree,
		Parents: []*gh.Commit{{SHA: gh.Ptr(parentSHA)}},
	}
	newCommit, _, err := c.gh.Git.CreateCommit(ctx, owner, repo, commit, nil)
	if err != nil {
		return fmt.Errorf("github: create commit: %w", err)
	}

	// Update branch ref.
	ref.Object.SHA = newCommit.SHA
	_, _, err = c.gh.Git.UpdateRef(ctx, owner, repo, ref, false)
	if err != nil {
		return fmt.Errorf("github: update ref: %w", err)
	}
	return nil
}

// CreatePR opens a pull request. head should be "user:branch" format.
func (c *Client) CreatePR(ctx context.Context, upstreamOwner, upstreamRepo, head, baseBranch, title, body string) (string, error) {
	pr := &gh.NewPullRequest{
		Title: gh.Ptr(title),
		Body:  gh.Ptr(body),
		Head:  gh.Ptr(head),
		Base:  gh.Ptr(baseBranch),
	}
	created, _, err := c.gh.PullRequests.Create(ctx, upstreamOwner, upstreamRepo, pr)
	if err != nil {
		return "", fmt.Errorf("github: create PR: %w", err)
	}
	return created.GetHTMLURL(), nil
}

// ContributionBranchName returns the standard branch name for a contribution.
func ContributionBranchName(titleSlug, releaseSlug string) string {
	return "contribution/" + titleSlug + "/" + releaseSlug
}
