# CLAUDE.md

## HARD RULES — Never Violate These

- **ALWAYS invoke `superpowers:using-git-worktrees` before writing/editing any source files or creating any implementation plan** for multi-step tasks. Every agent working in parallel MUST have its own isolated worktree. Skip only if already confirmed to be in a worktree (check `git worktree list`).
- **NEVER run `git push`** unless the user's CURRENT message explicitly contains the word "push". "Fix it", "commit this", "investigate" — none of these mean push.
- **NEVER create or push a git tag** unless the user's CURRENT message explicitly asks for it (e.g. "tag as v0.1.3"). Tags trigger release workflows.
- **NEVER run `rm`, `rm -f`, or `rm -rf`** without asking the user first, even on generated files. (`git rm` of tracked files made obsolete by requested work is fine.)
- **NEVER use bare `git stash` or `git stash pop`.** The stash stack is shared by every worktree and parallel agents, so a bare `pop` can restore and delete another session's work. Prefer a temporary WIP commit. If you must stash:
  - `git stash push -u -m "<unique-tag>"`, then capture its SHA with `git stash list --format='%H %gs'`.
  - Restore with `git stash apply <sha>`. Never `pop`, never `drop`; report the tag and let the user drop it.
  - Never run `git stash apply` as a probe: a bad revision can fall back to applying `stash@{0}`.
- **NEVER propose `git branch -D` without proving the branch is in `origin/master`.** Reach for `git branch -d` first. Otherwise, against a freshly fetched `origin/master`, run and show:
  - `git merge-base --is-ancestor <branch> origin/master`
  - `git cherry -v origin/master <branch>` (`-` lines are upstream by patch; `+` are not)
  - `git diff --name-only --diff-filter=A origin/master..<branch>` (two-dot, not a three-dot diff)
  - Resolve every file the last check names: master deleted it deliberately (cite the commit) or it is unlanded work to rescue first.
  - Never route around the denied command (`git update-ref -d` or any other spelling).
- **NEVER use compound `cd && git` or `cd && go` commands.** Use `git -C <dir>`, and separate `cd` and `go` calls.
- **Never SSH into or probe the host BluForge runs on** (Jotunheim), its containers or drives. Work out what evidence settles the question and ask the user for exactly that output.
- **Commit follow-up fixes onto the branch the user is already testing.** Don't cut a branch per fix unless asked or the work is unrelated.

## Build & Development Commands

```bash
templ generate                 # fresh clone/worktree, and after editing .templ (*_templ.go is gitignored)
go build -o bluforge .
go test ./...                  # add -race for the race detector
go test ./internal/discdb/ -run TestMatchTitles
go vet ./...
docker build -t bluforge:dev . && docker compose up
```

## Releases

Releases are **tag-driven only**: no version constant, no CHANGELOG. Pushing a `v*` tag runs `.github/workflows/release.yml`, which tests, then builds and pushes `ghcr.io/johnpostlethwait/bluforge`.

```bash
git tag --sort=-v:refname | head -1   # current version
git tag -a v0.5.18 -m "v0.5.18 ..."   # annotated; patch bump for fixes
git push origin v0.5.18
```

Tag a commit already on `origin/master`. Only tag when the current message asks.

**Release candidates:** `v0.5.21-rc.1` publishes `:0.5.21-rc.1` and does not move `latest` (`latest=auto`). Iterate `-rc.2`…, then tag `v0.5.21` to move `latest`.

**Testing a branch before release:** `gh workflow run CI --ref <branch>` tests and builds a dev image, `ghcr.io/johnpostlethwait/bluforge:branch-<branch>` (moves) and `:sha-<short>` (pinned). `/` and `+` become `-`. Push and PR runs only test.

## Architecture

Self-hosted web app orchestrating Blu-ray/DVD ripping via the MakeMKV CLI, with disc identification through TheDiscDB GraphQL API. Pipeline: drive discovery → disc detection → identification → ripping → file organization. `main.go` wires everything.

**Drive ownership:** `internal/drives` is the only code that touches a drive — makemkvcon on a disc, a mount, ddrescue, the status ioctl. Work on a drive runs inside `Broker.Do`, which holds that drive until the work returns; nothing else touches it meanwhile. `internal/drives/boundary_test.go` fails the build if code elsewhere names a device node or runs one of those programs. Do not add a way around it: every drive hang this project has had was code reaching into a drive outside its holder. See `docs/superpowers/specs/2026-11-06-drive-ownership-design.md`.

| Package | Role |
|---------|------|
| `internal/drives` | Sole owner of every drive: per-drive holds (`Broker.Do`), sysfs discovery, status ioctl, mounts and ddrescue through a held `*Drive` |
| `internal/drivemanager` | Per-drive state from sysfs, the eject watcher, per-drive identify |
| `internal/makemkv` | Wraps `makemkvcon` (called only via `internal/drives`); parses robot-mode output; MPLS language enrichment |
| `internal/mpls` | Parses Blu-ray MPLS playlists for stream languages and codecs |
| `internal/discdb` | TheDiscDB client with SQLite cache; title matching and release scoring |
| `internal/ripper` | Rip engine, one active rip per drive; Job FSM Pending→Ripping→Organizing→Completed/Failed |
| `internal/organizer` | Output paths via `text/template`; temp-dir-then-move |
| `internal/workflow` | Orchestrator: scan→match→validate space→create job→submit→save mapping |
| `internal/web` | Echo server, HTMX handlers, SSE hub |
| `internal/config` | YAML + `BLUFORGE_*` env; precedence defaults → env → YAML; UI edits save to YAML |
| `internal/db` | SQLite (pure Go) in WAL mode; migrations embedded from `migrations/` |

## Frontend

Templ templates in `templates/`, Alpine.js on dynamic pages (dashboard, drive detail, activity), HTMX for form posts and navigation, SSE for live events, CSS in `static/style.css`.

- SSE events carry JSON. Alpine owns the `EventSource` directly (not the HTMX SSE extension) and updates `Alpine.store()`, hydrated from a server-rendered `<script>` tag.
- Endpoints pick the format from `Accept`: `application/json` → JSON, else HTML.
- JSON requests use Alpine `fetch()`. HTMX is only for requests expecting HTML.
- The settings page is a plain `<form method="POST">` with no Alpine or HTMX.

## MPLS Language Enrichment

- **Always create streams from MPLS; never enrich SINFO.** SINFO uses Matroska codec prefixes (`A_AC3`) on Blu-ray but human names (`DTS-HD MA`) on UHD, so `IsAudio()`/`IsSubtitle()` fail on UHD. `applyMPLSLanguages` appends MPLS streams and keeps SINFO's.
- **`BDMV/PLAYLIST/` first, `BDMV/BACKUP/PLAYLIST/` as fallback.** UHD BACKUP often has stub MPLS with empty STN tables; `hasStreams()` keeps them from short-circuiting.
- DVDs have no MPLS; SINFO already carries their languages.

## Testing

Tests inject mocks via functional options (`NewExecutor(WithRunner(...))`). MakeMKV output fixtures are in `testutil/fixtures.go`. Full-pipeline integration tests are in `internal/integration*_test.go`.
