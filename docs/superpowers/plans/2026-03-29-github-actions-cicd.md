# GitHub Actions CI/CD Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add two GitHub Actions workflows — one to run tests on PRs to `main`, one to build and publish a Docker image to GHCR on version tags.

**Architecture:** Two independent workflow files under `.github/workflows/`. `ci.yml` gates pull requests with vet + race-detected tests. `release.yml` verifies the binary compiles, then builds and pushes a multi-tagged Docker image to GHCR using the built-in `GITHUB_TOKEN`.

**Tech Stack:** GitHub Actions, `actions/setup-go@v5`, `docker/build-push-action@v6`, `docker/metadata-action@v5`, GHCR (`ghcr.io`)

---

### Task 1: Create CI workflow (PR tests)

**Files:**
- Create: `.github/workflows/ci.yml`

- [ ] **Step 1: Create the workflows directory and write `ci.yml`**

Create `.github/workflows/ci.yml` with the following content:

```yaml
name: CI

on:
  pull_request:
    branches:
      - main

jobs:
  test:
    name: Test
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4

      - uses: actions/setup-go@v5
        with:
          go-version-file: go.mod

      - name: Vet
        run: go vet ./...

      - name: Test
        run: go test ./... -race
```

- [ ] **Step 2: Verify the file was written correctly**

Run: `cat .github/workflows/ci.yml`

Expected: Full YAML content printed with no truncation or corruption.

- [ ] **Step 3: Commit**

```bash
git add .github/workflows/ci.yml
git commit -m "ci: add PR test workflow"
```

---

### Task 2: Create release workflow (Docker publish on version tags)

**Files:**
- Create: `.github/workflows/release.yml`

- [ ] **Step 1: Write `release.yml`**

Create `.github/workflows/release.yml` with the following content:

```yaml
name: Release

on:
  push:
    tags:
      - 'v*'

jobs:
  publish:
    name: Build and Push Docker Image
    runs-on: ubuntu-latest
    permissions:
      contents: read
      packages: write

    steps:
      - uses: actions/checkout@v4

      - uses: actions/setup-go@v5
        with:
          go-version-file: go.mod

      - name: Build binary
        run: CGO_ENABLED=0 go build -o bluforge .

      - uses: docker/setup-buildx-action@v3

      - name: Log in to GHCR
        uses: docker/login-action@v3
        with:
          registry: ghcr.io
          username: ${{ github.actor }}
          password: ${{ secrets.GITHUB_TOKEN }}

      - name: Extract Docker metadata
        id: meta
        uses: docker/metadata-action@v5
        with:
          images: ghcr.io/johnpostlethwait/bluforge
          tags: |
            type=semver,pattern={{version}}
            type=raw,value=latest

      - name: Build and push
        uses: docker/build-push-action@v6
        with:
          context: .
          platforms: linux/amd64
          push: true
          tags: ${{ steps.meta.outputs.tags }}
          labels: ${{ steps.meta.outputs.labels }}
```

- [ ] **Step 2: Verify the file was written correctly**

Run: `cat .github/workflows/release.yml`

Expected: Full YAML content printed with no truncation or corruption.

- [ ] **Step 3: Commit**

```bash
git add .github/workflows/release.yml
git commit -m "ci: add Docker release workflow for version tags"
```

---

## Post-implementation notes

- **GHCR visibility:** On first push, the package will be private. To make it public: GitHub → your profile → Packages → `bluforge` → Package Settings → Change visibility to Public.
- **Triggering a release:** Push a tag — `git tag v0.1.0 && git push origin v0.1.0`. This triggers `release.yml` and publishes `ghcr.io/johnpostlethwait/bluforge:0.1.0` and `ghcr.io/johnpostlethwait/bluforge:latest`.
- **`permissions: packages: write`** in `release.yml` is required for `GITHUB_TOKEN` to push to GHCR.
