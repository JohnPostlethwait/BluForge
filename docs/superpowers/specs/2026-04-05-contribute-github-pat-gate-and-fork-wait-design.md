# Design: Contribution GitHub PAT Gate & Fork Wait

**Date:** 2026-04-05
**Status:** Approved

---

## Problem

Two bugs affect the `/contribute` feature:

1. **No PAT gate:** Users can open a contribution, fill in all disc metadata, and only discover on submit that their GitHub token isn't configured. The error fires at `handleContributionSubmit` (line 107 of `handlers_contribute.go`), far too late.

2. **Fork not ready on first use:** After the GitHub API creates a fork it returns `202 Accepted` (async). The current code (`EnsureFork` in `internal/github/client.go`) returns the expected fork name immediately without waiting. The next call (`CreateBranch`) then hits a 404 because the fork repo doesn't exist yet.

---

## Issue 1: GitHub PAT Gate

### What changes

**`templates/contributions.templ`**

- `ContributionsData` gains a `GitHubTokenConfigured bool` field.
- When `!GitHubTokenConfigured`, render a warning banner above the table:
  > "GitHub token is not configured. Contributions cannot be submitted until you add a Personal Access Token in [Settings](/settings)."
- The "Contribute" button for every pending/drafting row is rendered as a disabled `<button>` instead of an `<a>` link when the PAT is missing. The "Delete" button is unaffected.

**`templates/contribution_detail.templ`**

- `ContributionDetailData` gains a `GitHubTokenConfigured bool` field.
- When `!GitHubTokenConfigured`, render the same warning banner at the top of the page.
- The "Submit to TheDiscDB" button is disabled (in addition to the existing Alpine `isValid` check). Disable is applied as a plain HTML `disabled` attribute (server-rendered), not just Alpine, so it works even before Alpine initialises.

**`internal/web/handlers_contribute.go`**

- `handleContributions`: reads `s.GetConfig().GitHubToken != ""` and passes the bool to `ContributionsData`.
- `handleContributionDetail`: same — passes the bool to `ContributionDetailData`.
- No change to `handleContributionSubmit`; the existing PAT check at submit time remains as a safety net.

---

## Issue 2: Fork Not Ready (Wait for Fork)

### What changes

**`internal/github/client.go`**

Add a new method:

```go
// WaitForRepo polls until owner/repo is reachable (HTTP 200) or the context
// is cancelled. It retries every 2 seconds, up to a 30-second total timeout.
func (c *Client) WaitForRepo(ctx context.Context, owner, repo string) error
```

Uses `Repositories.Get`; returns nil when the repo exists, wraps the last error if the deadline is exceeded.

**`internal/contribute/service.go`**

- The `GitHubClient` interface gains `WaitForRepo(ctx context.Context, owner, repo string) error`.
- In `Submit`, after `EnsureFork` succeeds, call `c.github.WaitForRepo(ctx, forkOwner, upstreamRepo)` before `CreateBranch`. If it times out, return a descriptive error.

### Polling behaviour

| Parameter | Value |
|-----------|-------|
| Poll interval | 2 seconds |
| Max wait | 30 seconds |
| Timeout source | context passed by the HTTP handler (plus internal 30s cap via `context.WithTimeout`) |

---

## What is NOT changing

- The submit handler's existing PAT check is kept as a defensive fallback.
- No changes to the settings page, config loading, or database schema.
- No changes to `EnsureFork` itself — polling is a separate responsibility.

---

## Testing

- **Issue 1:** Update `handlers_contribute_test.go` to assert the warning banner renders when `GitHubToken` is empty, and that the Contribute button has `disabled` when it is.
- **Issue 2:** The `GitHubClient` interface mock in `contribute/service_test.go` gains a `WaitForRepo` stub. Add a test case that simulates `EnsureFork` returning the fork name followed by `WaitForRepo` being called before `CreateBranch`.
