# Resubmit: Handle Deleted Branch/PR (Branch-Not-Found Fallback)

## Context

When a user submits a contribution, BluForge creates a fork, branch, and PR on GitHub. If
the user later closes the PR and deletes the branch on GitHub, then clicks "Fix PR" to
resubmit, the flow fails:

```
Failed to resubmit contribution: contribute: resubmit commit files:
github: get branch ref: GET .../git/ref/heads/contribution/lincoln-2012/2012-blu-ray: 404 Not Found
```

`Resubmit` assumes the branch from the original `Submit` still exists. It calls
`CommitFiles`, which opens with `GetRef` on the branch — 404 if deleted. The fix: detect
the missing branch and fall through to a full re-create (new branch + new PR).

## Design

### 1. Sentinel error in `internal/github/client.go`

Add a package-level sentinel:

```go
var ErrBranchNotFound = errors.New("branch not found")
```

In `CommitFiles`, after `GetRef` fails, check if the HTTP response is 404 and wrap:

```go
ref, _, err := c.gh.Git.GetRef(ctx, owner, repo, "refs/heads/"+branch)
if err != nil {
    var ghErr *gh.ErrorResponse
    if errors.As(err, &ghErr) && ghErr.Response.StatusCode == http.StatusNotFound {
        return fmt.Errorf("github: get branch ref: %w", ErrBranchNotFound)
    }
    return fmt.Errorf("github: get branch ref: %w", err)
}
```

This lets callers use `errors.Is(err, ghpkg.ErrBranchNotFound)` to branch on this specific
case without string-matching error messages.

### 2. Fallback path in `internal/contribute/service.go` — `Resubmit`

After building `files` and `commitMsg`, attempt `CommitFiles` as before. On
`ErrBranchNotFound`, execute the full re-create flow using the already-assembled `files`:

```
EnsureFork → WaitForRepo → GetDefaultBranchSHA → CreateBranch → CommitFiles (retry) →
CreatePR → UpdateContributionStatus
```

This mirrors what `Submit` does. The files are already built at that point, so the only
extra work is the GitHub API calls to recreate the branch and PR.

On success, `UpdateContributionStatus` saves the new PR URL to the DB, and the web handler
redirects to the contribution detail page — which already displays the PR URL. No handler
changes needed.

## Files Changed

| File | Change |
|------|--------|
| `internal/github/client.go` | Add `ErrBranchNotFound` sentinel; modify `CommitFiles` to wrap 404 GetRef errors |
| `internal/contribute/service.go` | In `Resubmit`, check for `ErrBranchNotFound` and re-create branch + PR + update DB |

## No Changes Needed

- `GitHubClient` interface — no new methods
- Web handler — already redirects to detail page; new PR URL flows through DB
- DB schema — no new fields

## Verification

1. Simulate the error path: submit a contribution, delete the branch on GitHub, click "Fix
   PR". Should succeed and show a new PR URL on the contribution detail page.
2. Normal resubmit (branch still exists): should still work as before — no regression.
3. Run `go test ./internal/contribute/ ./internal/github/` — add/update unit tests for:
   - `CommitFiles` returns `ErrBranchNotFound` on 404 GetRef response
   - `Resubmit` falls through to re-create when `CommitFiles` returns `ErrBranchNotFound`
