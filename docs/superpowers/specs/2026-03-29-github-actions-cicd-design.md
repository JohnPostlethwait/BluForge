# GitHub Actions CI/CD Design

**Date:** 2026-03-29

## Overview

Configure GitHub Actions to run tests on pull requests and publish a Docker image to GitHub Container Registry (GHCR) on version tags.

## Workflows

### `ci.yml` — Pull Request Checks

**Trigger:** `pull_request` targeting `main`

**Job: `test`**

1. Checkout code
2. Set up Go 1.25
3. `go vet ./...`
4. `go test ./... -race`

No secrets required. The generated `_templ.go` files are committed to the repo so `templ generate` is not needed in CI.

### `release.yml` — Docker Publish on Version Tags

**Trigger:** `push` with tags matching `v*`

**Job: `publish`**

1. Checkout code
2. Set up Go 1.25
3. `CGO_ENABLED=0 go build -o bluforge .` — verify binary compiles before publishing
4. Set up Docker Buildx
5. Log in to GHCR using built-in `GITHUB_TOKEN` (no extra secrets needed)
6. Build and push Docker image to `ghcr.io/johnpostlethwait/bluforge`
   - Tags: exact version (e.g. `v1.0.0`) + `latest`
   - Platform: `linux/amd64`

## Key Decisions

| Decision | Choice | Rationale |
|---|---|---|
| Registry | GHCR | Free, no extra credentials, tightly integrated with GitHub |
| Publish trigger | Version tags (`v*`) only | Clean release workflow; `latest` always reflects newest tag |
| Test trigger | PRs to `main` only | Keeps CI fast; no redundant runs on every push |
| Platforms | `linux/amd64` only | Sufficient for target deployment environments |
| Workflow structure | Two separate files | Each file has one clear purpose |
| Auth | `GITHUB_TOKEN` | Built-in, no manual secret setup required |

## Notes

- GHCR packages default to **private** on first push. To make the image publicly pullable, go to the package settings on GitHub and change visibility to public.
- Tests do **not** gate the release workflow. A tag can be pushed without tests having run on that exact commit. This is acceptable for a self-hosted personal project.
