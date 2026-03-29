# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

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
| `internal/drivemanager` | Polls drives, maintains per-drive state machine (Empty→Detected→Scanning→Identified→Ready→Ripping→Organizing→Complete→Ejecting→Empty) |
| `internal/makemkv` | Wraps `makemkvcon` CLI; parses robot-mode output (DRV, CINFO, TINFO, SINFO, PRGV, MSG lines) |
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

An SSE adapter pattern in `main.go` bridges `web.SSEHub` to `workflow.Broadcaster` to avoid import cycles.

## Key Patterns

- **Functional options** for testability: `NewExecutor(WithRunner(mockRunner))`, `NewClient(WithBaseURL(...))`
- **Interface-based coupling**: `RipExecutor`, `DriveExecutor`, `DiscScanner`, `Broadcaster` — minimal interfaces enable mock injection
- **Dependency injection via structs**: `ServerDeps`, `OrchestratorDeps` collect all dependencies
- **Thread-safe state machines**: `DriveStateMachine` and `Engine` use `sync.RWMutex`/`sync.Mutex`
- **Templ templates**: `.templ` files compile to `_templ.go` — always run `templ generate` after editing `.templ` files

## Frontend

- **Templ** for type-safe Go HTML templates (in `templates/`)
- **HTMX** for form submissions and partial page updates
- **SSE** for real-time drive events and rip progress
- **Static CSS** in `static/style.css` (dark theme)

## Database

SQLite at `/config/bluforge.db` with WAL mode. Tables: `rip_jobs`, `disc_mappings`, `discdb_cache`, `settings`. Migrations are embedded via `//go:embed` in `migrations/embed.go` and run automatically on startup.

## Configuration

Precedence (lowest→highest): hardcoded defaults → env vars (BLUFORGE_*) → YAML (`/config/config.yaml`). Settings are also editable at runtime via the web UI (POST to /settings updates in-memory config and saves to YAML).

## Testing

Tests use functional options to inject mocks. Fixtures in `testutil/fixtures.go` provide sample MakeMKV output. Integration tests in the project root test the full pipeline.
