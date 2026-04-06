# Contribution PAT Gate & Fork Wait — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Gate the contribution UI on the GitHub PAT being configured, and wait for async fork creation before creating branches.

**Architecture:** Two independent fixes. (1) Pass a `GitHubTokenConfigured bool` from handlers into templates to gate UI elements. (2) Add a `WaitForRepo` polling method to the GitHub client and call it in `contribute.Service.Submit` after `EnsureFork`.

**Tech Stack:** Go, templ (type-safe HTML templates), go-github v72, Echo v4, Alpine.js (client-side state in existing templates)

---

## File Map

| File | Change |
|------|--------|
| `internal/github/client.go` | Add `WaitForRepo` method |
| `internal/contribute/service.go` | Add `WaitForRepo` to `GitHubClient` interface; call it after `EnsureFork` |
| `internal/contribute/service_test.go` | Add `waitErr` to `mockGitHub`; add `WaitForRepo` stub; add `TestSubmitWaitsForFork` |
| `internal/web/handlers_contribute.go` | Pass `GitHubTokenConfigured` to both template data structs |
| `internal/web/handlers_contribute_test.go` | Add PAT-gate assertions on list and detail page rendering |
| `templates/contributions.templ` | Add `GitHubTokenConfigured bool` to `ContributionsData`; warning banner; disable Contribute buttons |
| `templates/contribution_detail.templ` | Add `GitHubTokenConfigured bool` to `ContributionDetailData`; warning banner; server-side `disabled?` on Submit |

---

## Task 1: Add `WaitForRepo` to the GitHub Client

**Files:**
- Modify: `internal/github/client.go`

- [ ] **Step 1: Write the failing test**

Add this test to a new file `internal/github/client_wait_test.go`:

```go
package github_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	gh "github.com/google/go-github/v72/github"
	"golang.org/x/oauth2"

	ghpkg "github.com/johnpostlethwait/bluforge/internal/github"
)

// newTestClient wires a Client against a test HTTP server.
func newTestClient(t *testing.T, srv *httptest.Server) *ghpkg.Client {
	t.Helper()
	ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "test-token"})
	tc := oauth2.NewClient(context.Background(), ts)
	ghClient := gh.NewClient(tc)
	ghClient.BaseURL, _ = ghClient.BaseURL.Parse(srv.URL + "/")
	ghClient.UploadURL, _ = ghClient.UploadURL.Parse(srv.URL + "/")
	return ghpkg.NewClientFromGH(ghClient)
}

func TestWaitForRepo_SucceedsImmediately(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":1,"name":"data","full_name":"testuser/data"}`))
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	ctx := context.Background()
	if err := c.WaitForRepo(ctx, "testuser", "data"); err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
}

func TestWaitForRepo_TimesOutWhenRepoNeverAppears(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"message":"Not Found"}`))
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	// Use a context that cancels quickly so the test doesn't take 30 seconds.
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err := c.WaitForRepo(ctx, "testuser", "data")
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail (compilation error expected)**

```bash
go test ./internal/github/... -run TestWaitForRepo -v
```

Expected: compilation errors — `NewClientFromGH` and `WaitForRepo` don't exist yet.

- [ ] **Step 3: Add `NewClientFromGH` constructor and `WaitForRepo` method to `internal/github/client.go`**

After the existing `NewClient` function, add:

```go
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
			return fmt.Errorf("github: wait for repo %s/%s: timed out", owner, repo)
		case <-time.After(2 * time.Second):
			// retry
		}
	}
}
```

Also add `"time"` to the import block in `internal/github/client.go`.

- [ ] **Step 4: Run the tests to verify they pass**

```bash
go test ./internal/github/... -run TestWaitForRepo -v
```

Expected: both `TestWaitForRepo_SucceedsImmediately` and `TestWaitForRepo_TimesOutWhenRepoNeverAppears` PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/github/client.go internal/github/client_wait_test.go
git commit -m "feat(github): add WaitForRepo polling method"
```

---

## Task 2: Wire `WaitForRepo` into the Contribution Service

**Files:**
- Modify: `internal/contribute/service.go` (interface + Submit call)
- Modify: `internal/contribute/service_test.go` (mock + new test)

- [ ] **Step 1: Extend `mockGitHub` in `service_test.go` and add the new test**

Add `waitErr error` field to `mockGitHub` (after `prErr`):

```go
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
	waitErr       error  // ← add this
}
```

Add the `WaitForRepo` method to `mockGitHub` (after `CreatePR`):

```go
func (m *mockGitHub) WaitForRepo(ctx context.Context, owner, repo string) error {
	return m.waitErr
}
```

Add this new test after `TestSubmitBranchAlreadyExistsContinues`:

```go
func TestSubmitFailsWaitForFork(t *testing.T) {
	store := openTestStore(t)
	id, _, _ := seedContribution(t, store, nil)

	gh := &mockGitHub{
		user:     "testuser",
		forkName: "testuser/data",
		waitErr:  fmt.Errorf("github: wait for repo testuser/data: timed out"),
	}

	svc := NewService(store, gh)
	_, err := svc.Submit(context.Background(), id, "The Matrix", 1999, "movie")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "wait for fork") {
		t.Errorf("error %q should contain %q", err.Error(), "wait for fork")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails (compilation error expected)**

```bash
go test ./internal/contribute/... -run TestSubmitFailsWaitForFork -v
```

Expected: compilation error — `WaitForRepo` not in the `GitHubClient` interface.

- [ ] **Step 3: Add `WaitForRepo` to the `GitHubClient` interface in `service.go`**

Update the interface (it currently ends with `CreatePR`):

```go
// GitHubClient defines the GitHub operations needed for contributions.
type GitHubClient interface {
	GetUser(ctx context.Context) (string, error)
	EnsureFork(ctx context.Context, owner, repo string) (string, error)
	GetDefaultBranchSHA(ctx context.Context, owner, repo string) (string, string, error)
	CreateBranch(ctx context.Context, owner, repo, branchName, baseSHA string) error
	CommitFiles(ctx context.Context, owner, repo, branch string, files []ghpkg.FileEntry, message string) error
	CreatePR(ctx context.Context, upstreamOwner, upstreamRepo, head, baseBranch, title, body string) (string, error)
	WaitForRepo(ctx context.Context, owner, repo string) error  // ← add this
}
```

- [ ] **Step 4: Call `WaitForRepo` in `Submit` after `EnsureFork`**

In `service.go`, after the `EnsureFork` block (around line 107), insert the wait call. The existing code looks like:

```go
// 4b. Ensure fork exists.
fork, err := s.github.EnsureFork(ctx, upstreamOwner, upstreamRepo)
if err != nil {
    return "", fmt.Errorf("contribute: ensure fork: %w", err)
}
// fork is "user/data"; extract the owner part.
forkOwner := strings.SplitN(fork, "/", 2)[0]
```

Add immediately after `forkOwner` is set:

```go
// 4b-ii. Wait for the fork to be ready (GitHub fork creation is async).
if err := s.github.WaitForRepo(ctx, forkOwner, upstreamRepo); err != nil {
    return "", fmt.Errorf("contribute: wait for fork: %w", err)
}
```

- [ ] **Step 5: Run all contribute service tests**

```bash
go test ./internal/contribute/... -v
```

Expected: all existing tests plus `TestSubmitFailsWaitForFork` PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/contribute/service.go internal/contribute/service_test.go
git commit -m "feat(contribute): wait for fork to be ready before creating branch"
```

---

## Task 3: PAT Gate — Templates and Handlers

**Files:**
- Modify: `templates/contributions.templ`
- Modify: `templates/contribution_detail.templ`
- Modify: `internal/web/handlers_contribute.go`
- Modify: `internal/web/handlers_contribute_test.go`

- [ ] **Step 1: Write the failing handler tests**

Add these two tests to `handlers_contribute_test.go` (after `TestHandleContributions_WithEntries`):

```go
func TestHandleContributions_NoPATShowsBanner(t *testing.T) {
	srv, store := setupContribServer(t)

	// Seed a pending contribution so the table renders.
	_ = seedTestContribution(t, store)

	// cfg.GitHubToken is empty by default (setupContribServer uses &config.AppConfig{}).
	req := httptest.NewRequest(http.MethodGet, "/contributions", nil)
	rec := httptest.NewRecorder()
	c := srv.echo.NewContext(req, rec)

	if err := srv.handleContributions(c); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "GitHub token is not configured") {
		t.Errorf("expected PAT warning banner, got body:\n%s", body)
	}
	// Contribute button must be a disabled <button>, not an <a>.
	if strings.Contains(body, `href="/contributions/`) {
		t.Errorf("expected no 'Contribute' link when PAT missing, but found href")
	}
	if !strings.Contains(body, "disabled") {
		t.Errorf("expected disabled attribute on Contribute button when PAT missing")
	}
}

func TestHandleContributionDetail_NoPATShowsBanner(t *testing.T) {
	srv, store := setupContribServer(t)
	id := seedTestContribution(t, store)

	// cfg.GitHubToken is empty by default.
	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/contributions/%d", id), nil)
	rec := httptest.NewRecorder()
	c := srv.echo.NewContext(req, rec)
	c.SetParamNames("id")
	c.SetParamValues(fmt.Sprintf("%d", id))

	if err := srv.handleContributionDetail(c); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "GitHub token is not configured") {
		t.Errorf("expected PAT warning banner, got body:\n%s", body)
	}
	if !strings.Contains(body, "disabled") {
		t.Errorf("expected disabled attribute on Submit button when PAT missing")
	}
}
```

- [ ] **Step 2: Run to verify they fail**

```bash
go test ./internal/web/... -run "TestHandleContributions_NoPATShowsBanner|TestHandleContributionDetail_NoPATShowsBanner" -v
```

Expected: FAIL — banner text not present, no `disabled` attribute.

- [ ] **Step 3: Update `ContributionsData` in `templates/contributions.templ`**

Add the field to the data struct:

```go
// ContributionsData holds the data for the contributions list page.
type ContributionsData struct {
	Contributions          []db.Contribution
	GitHubTokenConfigured  bool
}
```

Add the warning banner just before the `if len(data.Contributions) == 0` check (inside `@Layout("Contributions") {`):

```templ
if !data.GitHubTokenConfigured {
    <div class="banner banner-warning mb-4">
        GitHub token is not configured. Contributions cannot be submitted until you add a Personal Access Token in <a href="/settings">Settings</a>.
    </div>
}
```

Replace the "Contribute" link in the table rows with a conditional that disables the button when no PAT:

```templ
if c.Status == "submitted" && c.PRURL != "" {
    <a href={ templ.SafeURL(c.PRURL) } target="_blank" rel="noopener" class="btn btn-sm">View PR</a>
} else if c.Status == "pending" || c.Status == "drafting" {
    <div class="flex gap-2">
        if data.GitHubTokenConfigured {
            <a href={ templ.SafeURL(fmt.Sprintf("/contributions/%d", c.ID)) } class="btn btn-sm btn-primary">Contribute</a>
        } else {
            <button type="button" class="btn btn-sm btn-primary" disabled>Contribute</button>
        }
        <form method="POST" action={ templ.SafeURL(fmt.Sprintf("/contributions/%d/delete", c.ID)) } onsubmit="return confirm('Delete this contribution?')">
            <button type="submit" class="btn btn-sm btn-danger">Delete</button>
        </form>
    </div>
}
```

- [ ] **Step 4: Update `ContributionDetailData` in `templates/contribution_detail.templ`**

Add the field to the data struct:

```go
// ContributionDetailData holds data for the contribution detail/editing form.
type ContributionDetailData struct {
	Contribution          db.Contribution
	CSRFToken             string
	GitHubTokenConfigured bool
}
```

Add the warning banner just after the `<div class="page-header ...">` closing tag (before `<div x-data>`):

```templ
if !data.GitHubTokenConfigured {
    <div class="banner banner-warning mb-4">
        GitHub token is not configured. Contributions cannot be submitted until you add a Personal Access Token in <a href="/settings">Settings</a>.
    </div>
}
```

Update the Submit button to use server-side `disabled?` (keep the existing Alpine `:disabled` too):

```templ
<button type="submit" class="btn btn-primary"
    disabled?={ !data.GitHubTokenConfigured }
    :disabled="!$store.contrib.isValid">
    Submit to TheDiscDB
</button>
```

- [ ] **Step 5: Update `handleContributions` and `handleContributionDetail` in `handlers_contribute.go`**

In `handleContributions`, update the `ContributionsData` literal:

```go
return templates.Contributions(templates.ContributionsData{
    Contributions:         contributions,
    GitHubTokenConfigured: cfg.GitHubToken != "",
}).Render(c.Request().Context(), c.Response().Writer)
```

Add `cfg := s.GetConfig()` before the `templates.Contributions(...)` call (it isn't currently there in this handler):

```go
func (s *Server) handleContributions(c echo.Context) error {
	contributions, err := s.store.ListContributions("")
	if err != nil {
		slog.Error("failed to list contributions", "error", err)
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to load contributions.")
	}

	if contributions == nil {
		contributions = []db.Contribution{}
	}

	cfg := s.GetConfig()
	return templates.Contributions(templates.ContributionsData{
		Contributions:         contributions,
		GitHubTokenConfigured: cfg.GitHubToken != "",
	}).Render(c.Request().Context(), c.Response().Writer)
}
```

In `handleContributionDetail`, update the `ContributionDetailData` literal:

```go
cfg := s.GetConfig()
return templates.ContributionDetail(templates.ContributionDetailData{
    Contribution:          *contrib,
    CSRFToken:             csrfToken(c),
    GitHubTokenConfigured: cfg.GitHubToken != "",
}).Render(c.Request().Context(), c.Response().Writer)
```

- [ ] **Step 6: Add the `banner` CSS classes to `static/style.css`**

Check whether `.banner` and `.banner-warning` already exist:

```bash
grep -n "banner" /Users/johnpostlethwait/Documents/workspace/BluForge/static/style.css
```

If they don't exist, append to `static/style.css`:

```css
/* Banners */
.banner {
    padding: 0.75rem 1rem;
    border-radius: 6px;
    font-size: 0.9rem;
}
.banner-warning {
    background: rgba(234, 179, 8, 0.15);
    border: 1px solid rgba(234, 179, 8, 0.4);
    color: #ca8a04;
}
.banner-warning a {
    color: inherit;
    text-decoration: underline;
}
```

- [ ] **Step 7: Regenerate templ files**

```bash
templ generate
```

Expected: `templates/contributions_templ.go` and `templates/contribution_detail_templ.go` regenerated with no errors.

- [ ] **Step 8: Run the new handler tests**

```bash
go test ./internal/web/... -run "TestHandleContributions_NoPATShowsBanner|TestHandleContributionDetail_NoPATShowsBanner" -v
```

Expected: both PASS.

- [ ] **Step 9: Run the full web handler test suite**

```bash
go test ./internal/web/... -v
```

Expected: all existing tests still PASS.

- [ ] **Step 10: Commit**

```bash
git add templates/contributions.templ templates/contributions_templ.go \
        templates/contribution_detail.templ templates/contribution_detail_templ.go \
        internal/web/handlers_contribute.go internal/web/handlers_contribute_test.go \
        static/style.css
git commit -m "feat(contribute): gate UI on GitHub PAT; warn when token missing"
```

---

## Task 4: Final Verification

- [ ] **Step 1: Run the entire test suite**

```bash
go test ./... -race
```

Expected: all packages PASS with no race conditions.

- [ ] **Step 2: Vet**

```bash
go vet ./...
```

Expected: no issues.
