# BluForge

A self-hosted web application that orchestrates Blu-ray and DVD ripping via [MakeMKV](https://www.makemkv.com/), identifies disc contents through [TheDiscDB](https://thediscdb.com/), and outputs organized, properly named media files.

## Why Did I Make This

I made this because while ripping DVD/BluRays/UHD discs is _easy_ with MakeMKV, organizing the files afterwards is not. For movies it's more or less straightforward to know which track is the main feature (the largest one), but not _always_ (looking at you, Star Wars franchise). Shows with multiple episodes and a lot of extras is even worse - they tracks/playlists are not necesarily sequential to the episode order (ahem, Seinfeld 4k). TheDiscDB is a fantastic resource to understand which playlist tracks on a disc match which specific extras/main feature/etc, but nothing I found glued the ripping together with the naming of files. This does.

This is intended as step one of digitizing and storing your disc collection. It is not meant to be a full metadata scraper and organizer, but is instead intended to make those programs (the arr's, Tiny Media Manager, etc) work better by having all the filenames already in place. Rip with BluForge, configure it to move them to a directory of your choice with a naming scheme if your choice, have the rest of your toolchain pick them up from there - scraping and storing metadata, and moving them to your final media library location (Plex, Jellyfin, Emby, etc...)

Huge callout to [Luke Foust](https://github.com/lfoust) for his work creating and maintaining [TheDiscDB](https://thediscdb.com/) and to [GuinpinSoft inc.](https://www.makemkv.com/) for their work creating the excellent MakeMKV and makemkvcon (the CLI mechanism to use it). Without those two things this program would not be possible and I'd be spending a lot more time organizing my disc rips.

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
      - MAKEMKV_ACCEPT_EULA=yes           # Required: must be "yes" to confirm acceptance of the MakeMKV EULA at https://www.makemkv.com/eula/
      - MAKEMKV_VERSION=1.18.3    # MakeMKV version to install. Tested: 1.18.3
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
| *(required)* | `MAKEMKV_ACCEPT_EULA` | *(none)* | Must be `yes` to confirm acceptance of the [MakeMKV EULA](https://www.makemkv.com/eula/) and allow the container to start |
| *(install)* | `MAKEMKV_VERSION` | `1.18.3` | MakeMKV version to download and compile at first startup. Tested version: `1.18.3` |
| *(optional)* | `MAKEMKV_KEY` | *(none)* | MakeMKV registration key. Can also be set at runtime via the Settings page. Free beta key available at the [MakeMKV forum](https://www.makemkv.com/forum/viewtopic.php?t=1053). |
| `port` | `BLUFORGE_PORT` | `9160` | HTTP server port |
| `output_dir` | `BLUFORGE_OUTPUT_DIR` | `/output` | Ripped content destination |
| `auto_rip` | `BLUFORGE_AUTO_RIP` | `false` | Rip automatically on disc insert |
| `min_title_length` | `BLUFORGE_MIN_TITLE_LENGTH` | `120` | Minimum title duration (seconds) |
| `poll_interval` | `BLUFORGE_POLL_INTERVAL` | `5` | Drive polling interval (seconds) |
| `movie_template` | `BLUFORGE_MOVIE_TEMPLATE` | `Movies/{{.Title}} ({{.Year}})/{{.Title}} ({{.Year}})` | Movie output path template |
| `series_template` | `BLUFORGE_SERIES_TEMPLATE` | `TV/{{.Show}}/Season {{.Season}}/...` | TV series output path template |
| `duplicate_action` | `BLUFORGE_DUPLICATE_ACTION` | `skip` | Handle duplicates: `skip`, `overwrite`, or `rename` |
| `github_token` | `BLUFORGE_GITHUB_TOKEN` | *(none)* | GitHub Personal Access Token (`public_repo` scope) for contributing disc data to TheDiscDB |
| `tmdb_api_key` | `BLUFORGE_TMDB_API_KEY` | *(none)* | [TMDB API key](https://www.themoviedb.org/settings/api) for movie/TV search on the contribution page |

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

- **MakeMKV** (`makemkvcon`) is downloaded and compiled at first container startup — set `MAKEMKV_ACCEPT_EULA=yes` and ensure outbound internet access on first run
- An optical drive (Blu-ray or DVD) accessible at `/dev/sr0` (or configured device path)
- For Docker: the drive device must be passed through with `--device`

## License & Third-Party Software

BluForge is released under the [PolyForm Noncommercial License 1.0.0](LICENSE). Free for personal and non-commercial use; commercial use requires explicit permission from the author.

It does **not** bundle or distribute MakeMKV.

**MakeMKV** (`makemkvcon`) is proprietary software by [GuinpinSoft inc.](https://www.makemkv.com/) It is not included in the BluForge image. Instead, it is downloaded from [makemkv.com](https://www.makemkv.com/) and compiled at first container startup. Setting `MAKEMKV_ACCEPT_EULA=yes` confirms that you have read and accepted the [MakeMKV End User License Agreement](https://www.makemkv.com/eula/). The compiled binaries are cached to your `/config` volume to avoid recompilation on subsequent starts.

**TheDiscDB** disc metadata is provided by [TheDiscDB](https://thediscdb.com/), an open-source community project by Luke Foust. The underlying data is licensed under the [MIT License](https://github.com/TheDiscDb/data). BluForge queries the live API and does not bundle any TheDiscDB data.
