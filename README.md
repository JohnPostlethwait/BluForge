# BluForge

A self-hosted web application that orchestrates Blu-ray and DVD ripping via [MakeMKV](https://www.makemkv.com/), identifies disc contents through [TheDiscDB](https://thediscdb.com/), and outputs organized, properly named media files.

## Features

- **Automatic disc detection** -- polls optical drives and fires events on disc insert/eject
- **Auto-rip mode** -- optionally rip all titles as soon as a disc is inserted
- **TheDiscDB integration** -- search by title, UPC, or ASIN to match discs against known metadata
- **Intelligent file organization** -- configurable Go templates for movie and TV series naming
- **Live progress** -- real-time rip progress and drive status via Server-Sent Events
- **Job queue and history** -- persistent job tracking in SQLite with queue and history views
- **Disc mapping cache** -- remembers matched discs for instant re-identification
- **Duplicate detection** -- skip or overwrite existing files based on your preference
- **Contribute workflow** -- links to TheDiscDB for community disc data contributions

## Tech Stack

| Layer | Technology |
|-------|-----------|
| Language | Go 1.25 |
| Web framework | [Echo](https://echo.labstack.com/) v4 |
| Templating | [Templ](https://templ.guide/) (type-safe, compiled HTML) |
| Interactivity | [HTMX](https://htmx.org/) + SSE |
| Database | SQLite via [modernc.org/sqlite](https://pkg.go.dev/modernc.org/sqlite) (pure Go, no CGO) |
| Ripping | [MakeMKV](https://www.makemkv.com/) CLI (`makemkvcon`) |
| Metadata | [TheDiscDB](https://thediscdb.com/) GraphQL API |

## Quick Start

### Docker Compose (recommended)

```yaml
services:
  bluforge:
    build: .
    ports:
      - "9160:9160"
    volumes:
      - ./config:/config
      - ./output:/output
    devices:
      - /dev/sr0:/dev/sr0
      - /dev/sg0:/dev/sg0
    environment:
      - BLUFORGE_AUTO_RIP=false
```

```bash
docker-compose up -d
```

Open **http://localhost:9160** in your browser.

### Docker Run

```bash
docker build -t bluforge .

docker run -d \
  --name bluforge \
  -p 9160:9160 \
  -v ./config:/config \
  -v ./output:/output \
  --device /dev/sr0:/dev/sr0 \
  --device /dev/sg0:/dev/sg0 \
  bluforge
```

### From Source

Requires Go 1.25+ and [MakeMKV](https://www.makemkv.com/) installed.

```bash
go build -o bluforge .
./bluforge
```

## Configuration

BluForge loads configuration from `/config/config.yaml` with environment variable overrides (`BLUFORGE_` prefix). All settings have sensible defaults and can also be changed from the Settings page in the web UI.

| Setting | Env Var | Default | Description |
|---------|---------|---------|-------------|
| `port` | `BLUFORGE_PORT` | `9160` | HTTP server port |
| `output_dir` | `BLUFORGE_OUTPUT_DIR` | `/output` | Ripped content destination |
| `auto_rip` | `BLUFORGE_AUTO_RIP` | `false` | Rip automatically on disc insert |
| `min_title_length` | `BLUFORGE_MIN_TITLE_LENGTH` | `120` | Minimum title duration (seconds) |
| `poll_interval` | `BLUFORGE_POLL_INTERVAL` | `5` | Drive polling interval (seconds) |
| `movie_template` | `BLUFORGE_MOVIE_TEMPLATE` | `Movies/{{.Title}} ({{.Year}})/{{.Title}} ({{.Year}})` | Movie output path template |
| `series_template` | `BLUFORGE_SERIES_TEMPLATE` | `TV/{{.Show}}/Season {{.Season}}/...` | TV series output path template |
| `duplicate_action` | `BLUFORGE_DUPLICATE_ACTION` | `skip` | Handle duplicates: `skip` or `overwrite` |

### Path Templates

Movie and series output paths use Go `text/template` syntax.

**Movie variables:** `{{.Title}}`, `{{.Year}}`

**Series variables:** `{{.Show}}`, `{{.Season}}`, `{{.Episode}}`, `{{.EpisodeTitle}}`

## Volumes

| Path | Purpose |
|------|---------|
| `/config` | Configuration file (`config.yaml`) and SQLite database (`bluforge.db`) |
| `/output` | Ripped and organized media files |

## Architecture

```
main.go                      Wiring and startup
internal/
  config/                    YAML + env var configuration
  db/                        SQLite store (jobs, mappings, cache)
  discdb/                    TheDiscDB GraphQL client, caching, disc matching
  drivemanager/              Drive polling and disc detection state machine
  makemkv/                   MakeMKV CLI wrapper and output parser
  organizer/                 Path templating and atomic file moves
  ripper/                    Rip job engine with concurrent execution
  web/                       Echo server, route handlers, SSE hub
  workflow/                  Orchestrator coordinating the end-to-end pipeline
templates/                   Templ components (compiled to Go)
static/                      CSS
migrations/                  Embedded SQLite schema migrations
```

The **workflow orchestrator** is the central coordinator. Both manual rips from the UI and automatic rips on disc insert flow through the same pipeline: scan, match, validate (disk space, duplicates), persist job, rip via MakeMKV, organize output, update status.

## Development

```bash
# Run tests
go test ./...

# Run tests with race detector
go test ./... -race

# Run vet
go vet ./...

# Regenerate templ files after editing .templ
templ generate

# Build
go build -o bluforge .

# Docker build
docker build -t bluforge:dev .
```

## How It Works

1. **Drive polling** detects an inserted disc and broadcasts an SSE event
2. The user navigates to the drive detail page, which scans the disc for available titles
3. **TheDiscDB search** matches the disc against known metadata (by title, UPC, or ASIN)
4. The user selects titles to rip (or auto-rip handles this automatically)
5. The **orchestrator** validates disk space, checks for duplicates, creates database records, and submits jobs to the rip engine
6. **MakeMKV** extracts the selected titles; progress streams to the browser via SSE
7. Completed files are organized into the output directory using the configured path templates
8. A **disc mapping** is saved so the disc is instantly recognized if inserted again

## Requirements

- **MakeMKV** (`makemkvcon`) must be available in the runtime environment
- An optical drive (Blu-ray or DVD) accessible at `/dev/sr0` (or configured device path)
- For Docker: the drive device must be passed through with `--device`

## License & Third-Party Software

BluForge is open source software. It does **not** bundle or distribute MakeMKV.

**MakeMKV** (`makemkvcon`) is proprietary software by [GuinpinSoft inc.](https://www.makemkv.com/) and must be installed separately by the user. Use of MakeMKV is subject to the [MakeMKV End User License Agreement](https://www.makemkv.com/eula/). Users are responsible for their own compliance with that license.
