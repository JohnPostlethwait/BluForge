# BluForge Design Spec

A Docker web application that orchestrates Blu-ray/DVD ripping via MakeMKV, identifies disc contents via TheDiscDB, and outputs organized, properly named media files.

## Tech Stack

- **Backend:** Go with Echo or Chi HTTP framework
- **Frontend:** Templ (type-safe Go templates) + HTMX for interactivity + SSE for live updates
- **Database:** SQLite (on mounted volume) for job history, configuration, and disc mapping cache
- **Styling:** Minimal CSS framework (Pico CSS or similar), dark theme with blue accents
- **Container:** Single Alpine Linux Docker image, port 9160

## Core Architecture

Four main subsystems:

### 1. Drive Manager

Polls all optical drives on a configurable interval (default 5s) by running `makemkvcon -r --cache=1 info disc:9999`. Parses `DRV:` lines to detect insert/eject events. Each drive is an independent state machine operating concurrently:

```
Empty -> Detected -> Scanning -> Identified -> Ready -> Ripping -> Organizing -> Complete -> Ejecting -> Empty
                                      |
                                  NotFound -> (optional) Contributing
                                      |
                                  Ripping (generic names) -> Complete
```

- `Identified` = TheDiscDB match found, content mapped
- `NotFound` = no match; user prompted to contribute (if opted in) or skip
- `Ready` = interactive mode: waiting for user to select titles; auto-rip mode: skipped
- Multiple drives operate fully independently and concurrently

### 2. Disc Scanner

On disc detect, runs `makemkvcon -r info disc:N` for that specific drive. Parses `CINFO`, `TINFO`, `SINFO` lines into structured Go types. Extracts: disc name, disc type (DVD/Blu-ray), title count, per-title duration/size/segment map/source file, per-stream codec/language/track type. Filters titles below minimum length threshold (configurable, default 120s).

### 3. Content Identifier

Takes scan results and matches against TheDiscDB. See "TheDiscDB Integration" section below for full matching strategy.

### 4. Rip Engine

Executes `makemkvcon mkv disc:N <title_id> <output_dir>` per selected title. Each rip is a goroutine with its own `makemkvcon` process. Multiple drives rip simultaneously; one rip per drive at a time. Parses `PRGV` lines in real-time for progress and streams updates via SSE. On completion, moves/renames output files into the organized folder structure using atomic temp-dir-then-move.

## MakeMKV Integration

### Robot-Mode Parser

A single reusable parser reads line-by-line from `makemkvcon` process stdout (robot mode, `-r` flag). Emits typed events: `DriveInfo`, `DiscInfo`, `TitleInfo`, `StreamInfo`, `Progress`, `Message`. Consumer goroutines handle each event type. This parser is shared between polling, scanning, and ripping.

### Key Robot-Mode Line Types

| Line | Format | Purpose |
|------|--------|---------|
| `DRV` | `DRV:index,visible,enabled,flags,drive_name,disc_name` | Drive enumeration |
| `TCOUT` | `TCOUT:count` | Title count |
| `CINFO` | `CINFO:attribute_id,code,value` | Disc-level attributes |
| `TINFO` | `TINFO:title_index,attribute_id,code,value` | Title-level attributes |
| `SINFO` | `SINFO:title_index,stream_index,attribute_id,code,value` | Stream/track attributes |
| `MSG` | `MSG:code,flags,count,message,format,params...` | Messages and errors |
| `PRGV` | `PRGV:current,total,max` | Progress values |

### Key Attribute IDs

- 2 = Title/disc name
- 8 = Chapter count
- 9 = Duration (seconds)
- 10 = File size (human-readable)
- 11 = File size (bytes)
- 27 = Output filename
- 28-29 = Language codes
- 30 = Description/metadata string

## TheDiscDB Integration

### GraphQL API

Endpoint: `POST https://thediscdb.com/graphql/`

Unauthenticated. Three root queries: `mediaItems`, `boxsets`, `mediaItemsByGroup`. Supports cursor-based pagination and Hot Chocolate-style filtering (`eq`, `contains`, `startsWith`, etc.).

Key data returned per disc: `sourceFile` (MakeMKV playlist, e.g., `00001.mpls`), `itemType` (MainMovie/Episode/Extra/Trailer/DeletedScene), `item` (title, season, episode, type), `duration`, `size`, `segmentMap`.

External IDs: `imdb`, `tmdb`, `tvdb` per media item.

### Matching Strategy

```
Disc scanned -> Check remembered mappings -> Found? Use it (user can re-scan to override)
    | not found
Auto-search by disc name -> High confidence + UPC/ASIN exact match? -> Use it
    | no confident match
Interactive: User searches by name/TMDB ID/ASIN/UPC -> Selects exact release
Auto-rip: Queue for manual identification
```

**Automatic match attempt:**
1. Query `mediaItems` by disc name from MakeMKV `CINFO`
2. For each candidate, check releases and discs; score by UPC/ASIN match, disc count, title count, source files/segment maps
3. High-confidence single match with UPC/ASIN confirmation -> use automatically (auto-rip) or present as top suggestion (interactive)

**User-driven search (always available, required when auto-match fails):**
- Search by: movie/show name (free text), TMDB ID, ASIN, or UPC
- Results display release-level details: title, year, region code, format (Blu-ray/DVD/4K), UPC, ASIN
- User picks the exact release; BluForge maps titles by source file / segment map

**Release disambiguation:**
- Multiple releases of the same title (Criterion vs. standard, region A vs. B) always show full release details
- User picks explicitly -- no guessing
- In auto-rip mode, only auto-proceed on exact UPC/ASIN match; otherwise queue for manual identification

**Remembered mappings:**
- Successful disc -> release mappings stored in SQLite (keyed by disc name + title count + segment map hash)
- Next time the same disc is inserted, skip identification entirely
- User can re-scan from Drive Detail page to override and replace the stored mapping
- Mappings editable/clearable from the UI

**Caching:**
- TheDiscDB API responses cached in SQLite with configurable TTL (default 24h)
- Serves cached matches even when TheDiscDB is offline

### Contribution Flow (Opt-In)

TheDiscDB accepts contributions via a database-backed web wizard with a GraphQL mutations API at `/graphql/contributions`. Authentication is GitHub OAuth (read:user scope only, for identity). Contributions go through an admin review queue.

**Note:** The contribution API is not publicly documented. The mutations schema was discovered through the open-source web repo (`TheDiscDb/web`). Before implementing this feature, we should reach out to the TheDiscDB maintainer (`lfoust`) to confirm API stability and discuss partnership/official access. If the API is unavailable or changes, the contribution feature degrades gracefully — users are instead linked to TheDiscDB's web contribution page to submit manually.

**BluForge contribution flow:**

1. User optionally enables GitHub OAuth in BluForge settings (one-time, requires `BLUFORGE_GITHUB_CLIENT_ID` and `BLUFORGE_GITHUB_CLIENT_SECRET` env vars)
2. On unmatched disc, UI offers: "This disc isn't in TheDiscDB yet. Want to contribute it?"
3. BluForge pre-fills: TMDB match (user confirms/changes), MakeMKV log data, disc metadata
4. User labels titles via inline UI on Drive Detail page: dropdown per title for type (MainMovie/Episode/Extra/Trailer/DeletedScene), season/episode fields for series
5. BluForge submits to TheDiscDB's contribution API
6. Contribution enters TheDiscDB's review queue -- no blocking on approval
7. Rip proceeds immediately with generic names (since mapping isn't confirmed until approved)

Users who don't configure GitHub auth are never prompted. The rip flow is never blocked by contribution.

## Output Organization

### Default Folder Structure

```
Movies:    {output_root}/Movies/{Title} ({Year})/{Title} ({Year}).mkv
Series:    {output_root}/TV/{Show Name}/Season {XX}/{Show Name} - S{XX}E{XX} - {Episode Title}.mkv
Extras:    {output_root}/Movies/{Title} ({Year})/Extras/{Extra Title}.mkv
           {output_root}/TV/{Show Name}/Season {XX}/Extras/{Extra Title}.mkv
Unmatched: {output_root}/Unmatched/{Disc Name}/title_tNN.mkv
```

Multi-part movies: `Movie Name (Year) - Part 1.mkv`

### Configurable Naming Templates

Go template syntax with variables: `{{.Title}}`, `{{.Year}}`, `{{.Season}}`, `{{.Episode}}`, `{{.EpisodeTitle}}`, `{{.ContentType}}`, `{{.Show}}`, `{{.Part}}`.

Separate templates for movies and series. Configurable via environment variables, config file, and UI settings page (with live preview in UI).

### Filename Sanitization

Cross-platform sanitization ensuring filenames are valid on Linux, Unix, Windows, and macOS:
- Strip/replace characters invalid on any platform: `< > : " / \ | ? *`
- Strip control characters (0x00-0x1F, 0x7F)
- Avoid reserved Windows names (CON, PRN, NUL, COM1-9, LPT1-9)
- Normalize whitespace (collapse multiple spaces, trim leading/trailing)
- Enforce maximum component length (255 bytes)

### Duplicate Handling

When a rip completes and the target file already exists:
- **Interactive mode:** Prompt the user in the UI -- "File already exists: Skip or Overwrite?"
- **Auto-rip mode:** Skip by default (configurable to overwrite in settings)

## Data Model

### Core Entities

| Entity | Purpose |
|--------|---------|
| `Drive` | Physical drive state, device path, polling status |
| `Disc` | Scanned disc metadata from MakeMKV (name, type, title count) |
| `Title` | Individual MakeMKV title (duration, size, segments, streams) |
| `ContentMatch` | TheDiscDB mapping: title index -> content type, name, season/episode |
| `RipJob` | Active rip: drive, title index, destination, progress, status |
| `AppConfig` | Output path templates, auto-rip settings, TheDiscDB auth, min title length |

### Storage

SQLite database on mounted `/config` volume. Stores:
- Job history (all rips, searchable/filterable)
- Application configuration (written by UI, overrides env var defaults)
- Disc -> release remembered mappings
- TheDiscDB API response cache (with TTL)

## Configuration

### Hierarchy

Environment variables provide initial defaults. Config file (YAML, on `/config` volume) overrides everything. UI settings page writes to the config file. Env vars seed initial values on first run; after that, the config file is the source of truth.

### Environment Variables

All prefixed with `BLUFORGE_`. All optional.

| Variable | Default | Purpose |
|----------|---------|---------|
| `BLUFORGE_PORT` | `9160` | Web UI port |
| `BLUFORGE_OUTPUT_DIR` | `/output` | Base output path |
| `BLUFORGE_AUTO_RIP` | `false` | Enable auto-rip mode |
| `BLUFORGE_MIN_TITLE_LENGTH` | `120` | Seconds, filter short titles |
| `BLUFORGE_POLL_INTERVAL` | `5` | Seconds between drive polls |
| `BLUFORGE_MOVIE_TEMPLATE` | `Movies/{{.Title}} ({{.Year}})/{{.Title}} ({{.Year}})` | Movie naming template |
| `BLUFORGE_SERIES_TEMPLATE` | `TV/{{.Show}}/Season {{.Season}}/{{.Show}} - S{{.Season}}E{{.Episode}} - {{.EpisodeTitle}}` | Series naming template |
| `BLUFORGE_GITHUB_CLIENT_ID` | -- | For TheDiscDB contribution OAuth |
| `BLUFORGE_GITHUB_CLIENT_SECRET` | -- | For TheDiscDB contribution OAuth |

### Docker Compose Example

```yaml
services:
  bluforge:
    image: bluforge:latest
    ports:
      - "9160:9160"
    volumes:
      - ./config:/config
      - /media/rips:/output
    devices:
      - /dev/sr0:/dev/sr0
      - /dev/sr1:/dev/sr1
      - /dev/sg0:/dev/sg0
      - /dev/sg1:/dev/sg1
    environment:
      - BLUFORGE_AUTO_RIP=false
```

### Volume Mounts

| Mount | Purpose |
|-------|---------|
| `/config` | SQLite database, YAML config file, TheDiscDB cache |
| `/output` | Ripped and organized media files |
| `/tmp/rip` | Temporary rip output before organizing (internal, or configurable to a mount for large discs) |

Device passthrough: Optical drives via `--device /dev/srN` plus SCSI generic devices `--device /dev/sgN` for full MakeMKV functionality.

## Web UI

### Technology

Go server renders HTML via Templ. HTMX handles partial page updates, form submissions, and search. SSE pushes live updates for active rips and drive state changes. Dark theme with blue accents, responsive layout via minimal CSS framework (Pico CSS or similar).

### Pages

| Page | Purpose |
|------|---------|
| **Dashboard** | Overview of all drives: current state, disc info, active rips with progress bars. Home screen. |
| **Drive Detail** | Full disc scan results, TheDiscDB match or search UI (by name/TMDB ID/ASIN/UPC), title selection checkboxes, rip button. Re-scan button to override remembered mappings. Inline contribution UI when opted in and disc not found. |
| **Queue** | Pending/active/completed rip jobs across all drives. Progress, status, errors. |
| **History** | Past rips with output paths, disc info, timestamps. Searchable and filterable. |
| **Settings** | Output paths, naming templates (with live preview), auto-rip toggle, min title length, poll interval, GitHub OAuth connection, TheDiscDB contribution preferences, drive-specific overrides, duplicate behavior (skip/overwrite). |

### Real-Time Behavior

- **Active rips:** SSE streams progress percentage, current title, speed, ETA. HTMX swaps progress bar elements on each event.
- **Drive state changes:** SSE notifies on disc insert/eject/scan complete. Dashboard updates without page refresh.
- **Everything else:** Standard HTMX request/response for settings, history, search.

### Theme

Dark navy/charcoal background with blue accent colors for active states, progress bars, and interactive elements. Lighter text for readability. Stays with the "Blu" branding.

## Error Handling

All errors and warnings log to stdout/stderr with structured format (timestamp, severity, context: drive index, job ID, disc name) for visibility in `docker logs bluforge`.

### MakeMKV Process Failures

- `makemkvcon` crashes or non-zero exit: mark rip job as failed, surface error from captured `MSG` lines in UI, drive returns to `Identified`/`Ready` state for retry
- Process hangs (no output for configurable timeout, default 10 minutes): kill process, mark as failed, same recovery

### Drive Issues

- Disc ejected mid-rip: caught as process failure, same recovery
- Drive disappears from poll results: mark drive as disconnected, cancel active rip, show in UI
- Drive reappears: resume polling, treat as fresh empty drive

### TheDiscDB Unavailable

- API timeout or error: rip not blocked, proceed as "not found" flow (generic names)
- Cache serves previously matched discs even when offline
- UI shows connectivity status indicator

### Disk Space

- Before rip start: check available space on output volume against estimated total size (sum of selected title sizes from scan)
- Insufficient space: warn in UI, block rip start
- Write failure during rip: mark job as failed with clear message

### Concurrent Rip Conflicts

- One rip per drive enforced; requests for a drive with an active rip are rejected
- Multiple drives rip simultaneously with no conflicts
- Output writes use temp directory + atomic move to prevent partial files

### Auto-Rip Mode Safeguards

- Duplicates: skip by default (configurable to overwrite)
- Unmatched discs: rip to `Unmatched/` folder, never silently discarded
- Failed rips: retained in job history with error, no automatic retry (user triggers from UI)
