# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## HARD RULES — Never Violate These

- **ALWAYS invoke `superpowers:using-git-worktrees` before writing/editing any source files or creating any implementation plan** for multi-step tasks. Every agent working in parallel MUST have its own isolated worktree. Skip only if already confirmed to be in a worktree (check `git worktree list`).
- **NEVER run `git push`** unless the user's CURRENT message explicitly contains the word "push". "Fix it", "commit this", "investigate" — none of these mean push.
- **NEVER create or push a git tag** unless the user's CURRENT message explicitly asks for it (e.g. "tag as v0.1.3"). Tags trigger release workflows.
- **NEVER run `rm`, `rm -f`, or `rm -rf`** without asking the user first, even on generated files.
- **NEVER use compound `cd && git` or `cd && go` commands** — always separate them:
  - Use `git -C <dir> <cmd>` instead of `cd <dir> && git <cmd>`
  - Use separate `cd` and `go` calls instead of `cd <dir> && go <cmd>`

## Build & Development Commands

```bash
# Build
go build -o bluforge .

# Run tests
go test ./...

# Run a single test
go test ./internal/discdb/ -run TestMatchTitles

# Run tests with race detector
go test ./... -race

# Vet
go vet ./...

# Regenerate templ files (required after editing .templ files)
templ generate

# Docker
docker build -t bluforge:dev .
docker compose up
```

## Architecture

BluForge is a self-hosted web app for orchestrating Blu-ray/DVD ripping via MakeMKV CLI integration, with disc identification through TheDiscDB GraphQL API.

**Pipeline flow:** Drive polling → Disc detection → Content identification → Ripping → File organization

**Entry point** (`main.go`): Wires all subsystems together, starts drive manager polling and web server in goroutines, handles graceful shutdown on SIGINT/SIGTERM.

### Key Packages

| Package | Role |
|---------|------|
| `internal/drivemanager` | Polls drives, maintains per-drive state (Empty→Detected); rip progress tracked separately by ripper.Job |
| `internal/makemkv` | Wraps `makemkvcon` CLI; parses robot-mode output (DRV, CINFO, TINFO, SINFO, PRGV, MSG lines); enriches titles with MPLS language data |
| `internal/mpls` | Parses Blu-ray MPLS (Movie PlayList) binary files to extract stream language codes and codec types |
| `internal/discdb` | TheDiscDB GraphQL client with SQLite-backed response cache (24h TTL); includes title matching and release scoring |
| `internal/ripper` | Concurrent rip engine (one active rip per drive); Job FSM: Pending→Ripping→Organizing→Completed/Failed |
| `internal/organizer` | Renders output paths via `text/template`; atomic temp-dir-then-move for file safety |
| `internal/workflow` | Orchestrator coordinating the full pipeline (scan→match→validate space→create job→submit→save mapping) |
| `internal/web` | Echo HTTP server, HTMX handlers, SSE hub for real-time progress broadcasting |
| `internal/config` | YAML + env var loading (BLUFORGE_* prefix); thread-safe updates via RWMutex |
| `internal/db` | SQLite (pure Go, no CGO) with WAL mode; embedded migrations from `migrations/` |

### Dependency Flow

```
main.go → config, db, makemkv, discdb, drivemanager, organizer, ripper, workflow, web
workflow → db, ripper, organizer, discdb
web → config, db, drivemanager, ripper, discdb, workflow
```

The orchestrator receives a simple `func(event, data string)` callback for SSE broadcasting, wired in `main.go`.

## Key Patterns

- **Functional options** for testability: `NewExecutor(WithRunner(mockRunner))`, `NewClient(WithBaseURL(...))`
- **Interface-based coupling**: `RipExecutor`, `DriveExecutor`, `DiscScanner` — minimal interfaces enable mock injection
- **Dependency injection via structs**: `ServerDeps`, `OrchestratorDeps` collect all dependencies
- **Thread-safe state**: `DriveStateMachine` and `Engine` use `sync.RWMutex`/`sync.Mutex`
- **Templ templates**: `.templ` files compile to `_templ.go` — always run `templ generate` after editing `.templ` files

## Frontend

- **Templ** for type-safe Go HTML templates (in `templates/`)
- **Alpine.js** for client-side reactive state on dynamic pages (dashboard, drive detail, activity)
- **HTMX** for form submissions and page navigation
- **SSE** for real-time drive events and rip progress
- **Static CSS** in `static/style.css` (dark theme)

### Alpine.js + SSE Design Pattern

Alpine-enabled pages (dashboard, drive_detail, activity) follow this pattern:

```
SSE delivers JSON → Alpine.store() updates → Alpine templates re-render
HTMX handles form POSTs and page navigation
Accept header determines response format (JSON vs HTML)
```

**Key rules:**
- SSE events carry JSON data. Alpine manages `EventSource` directly (not via HTMX SSE extension).
- `Alpine.store()` holds shared reactive state, hydrated from a server-rendered `<script>` tag on page load.
- Endpoints check the `Accept` header: `application/json` returns JSON, otherwise returns HTML.
- Requests that need JSON responses use Alpine `fetch()` with `Accept: application/json` (not HTMX `hx-post`).
- HTMX is used only for requests that expect HTML responses (page navigation, form submissions that redirect).
- The settings page has no Alpine or HTMX — it is a plain HTML `<form method="POST">` with no real-time updates.

### SSE Hub Architecture

`internal/web/sse.go` implements `SSEHub`: a `map[chan SSEEvent]struct{}` protected by `sync.RWMutex`. Each subscriber gets a buffered channel (capacity 32). `Broadcast` fans out to all channels; if a client's channel is full the event is silently dropped rather than blocking the broadcaster. The `workflow` orchestrator calls `hub.Broadcast` via a `func(event, data string)` callback wired in `main.go`.

## MPLS Language Enrichment

Blu-ray discs store stream metadata (audio/subtitle languages, codec types) in MPLS binary files under `BDMV/PLAYLIST/`. The `internal/mpls` package parses these files and `internal/makemkv` uses the results to attach language data to scanned titles.

**Design decision: always create streams from MPLS, never enrich SINFO.** MakeMKV's SINFO output uses Matroska-style codec prefixes (`A_AC3`, `S_HDMV/PGS`) on standard Blu-ray but human-readable names (`DTS-HD MA`, `Mpeg4`) on UHD discs. The `IsAudio()`/`IsSubtitle()` classifiers depend on those prefixes, so enriching existing SINFO streams by type fails on UHD. Instead, `applyMPLSLanguages` always appends new streams from MPLS data. Existing SINFO streams are preserved for any codec/bitrate info they carry.

**Directory ordering:** `BDMV/PLAYLIST/` is tried first (authoritative). `BDMV/BACKUP/PLAYLIST/` is a fallback only. UHD discs often have stub MPLS files in BACKUP with valid PlayItem headers but empty STN_Tables (no audio/subtitle entries). The `hasStreams()` check ensures these stubs don't short-circuit the search.

**DVD compatibility:** DVDs use IFO/VOB format, not MPLS. MakeMKV provides language codes directly in SINFO for DVDs, so MPLS enrichment is skipped (no MPLS files exist on the disc).

## Database

SQLite at `/config/bluforge.db` with WAL mode. Tables: `rip_jobs`, `disc_mappings`, `discdb_cache`, `settings`. Migrations are embedded via `//go:embed` in `migrations/embed.go` and run automatically on startup.

## Configuration

Precedence (lowest→highest): hardcoded defaults → env vars (BLUFORGE_*) → YAML (`/config/config.yaml`). Settings are also editable at runtime via the web UI (POST to /settings updates in-memory config and saves to YAML).

## Testing

Tests use functional options to inject mocks. Fixtures in `testutil/fixtures.go` provide sample MakeMKV output. Integration tests in the project root test the full pipeline.
