# GHCR Untagged Image Cleanup

**Date:** 2026-04-01

## Context

Each time a `v*` tag is pushed to trigger a release, the existing `latest` and semver-tagged image layers become orphaned (untagged) in GHCR before the new image is pushed. Re-tags and test pushes compound this. Over time, untagged dangling versions accumulate with no automated way to remove them.

## Design

Add a `cleanup` job to `.github/workflows/release.yml` that runs after `publish` completes on every release. It uses `dataaxiom/ghcr-cleanup-action` to delete all untagged package versions from GHCR.

### Job definition

```yaml
  cleanup:
    name: Clean Up Untagged Images
    runs-on: ubuntu-latest
    needs: publish
    permissions:
      packages: write
    steps:
      - uses: dataaxiom/ghcr-cleanup-action@v1
        with:
          token: ${{ secrets.GITHUB_TOKEN }}
          package: bluforge
          delete-untagged: true
```

### Key properties

- **Trigger:** Runs only after `publish` succeeds — no cleanup if the build/push failed
- **Scope:** Deletes only untagged versions; tagged releases (`0.2.1`, `latest`, etc.) are never touched
- **Auth:** Uses the existing `GITHUB_TOKEN` with `packages: write` — no new secrets required
- **Idempotent:** Safe to re-run; if nothing is untagged, the action is a no-op

## Files Modified

- `.github/workflows/release.yml` — append `cleanup` job

## Verification

1. Push a `v*` tag to trigger the release workflow
2. Confirm all three jobs (`test`, `publish`, `cleanup`) appear in the Actions run
3. Check GHCR (`github.com/users/johnpostlethwait/packages/container/bluforge/versions`) — no untagged versions should remain after the run
