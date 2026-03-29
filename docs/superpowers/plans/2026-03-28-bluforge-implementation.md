# BluForge Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a Docker web application that orchestrates Blu-ray/DVD ripping via MakeMKV, identifies disc contents via TheDiscDB, and outputs organized, properly named media files.

**Architecture:** Go backend with four subsystems (Drive Manager, Disc Scanner, Content Identifier, Rip Engine) serving a Templ + HTMX frontend. MakeMKV integration via `makemkvcon` CLI in robot mode. TheDiscDB GraphQL API for content identification. SQLite for persistence.

**Tech Stack:** Go 1.23+, Echo HTTP framework, Templ templates, HTMX, SSE, SQLite (via modernc.org/sqlite), GraphQL client (shurcooL/graphql or Khan/genqlient), Docker/Alpine

---

## File Structure

```
bluforge/
├── main.go                          # Entry point: config load, DB init, start server + drive manager
├── go.mod
├── go.sum
├── Dockerfile
├── docker-compose.yml
│
├── internal/
│   ├── config/
│   │   ├── config.go                # AppConfig struct, env var loading, YAML read/write
│   │   └── config_test.go
│   │
│   ├── makemkv/
│   │   ├── parser.go                # Robot-mode line parser (DRV, CINFO, TINFO, SINFO, MSG, PRGV)
│   │   ├── parser_test.go
│   │   ├── types.go                 # DriveInfo, DiscInfo, TitleInfo, StreamInfo, Progress, Message
│   │   ├── executor.go              # Shells out to makemkvcon, returns stdout reader
│   │   └── executor_test.go
│   │
│   ├── drivemanager/
│   │   ├── manager.go               # Polling loop, drive state machines, event emission
│   │   ├── manager_test.go
│   │   ├── state.go                 # DriveState enum, state machine transitions
│   │   └── state_test.go
│   │
│   ├── scanner/
│   │   ├── scanner.go               # Runs makemkvcon info, builds DiscScan from parsed output
│   │   └── scanner_test.go
│   │
│   ├── discdb/
│   │   ├── client.go                # TheDiscDB GraphQL client (queries: mediaItems, releases, discs)
│   │   ├── client_test.go
│   │   ├── types.go                 # MediaItem, Release, Disc, DiscTitle, ExternalIDs
│   │   ├── matcher.go               # Matching logic: scan results -> TheDiscDB release
│   │   ├── matcher_test.go
│   │   ├── cache.go                 # SQLite-backed response cache with TTL
│   │   └── cache_test.go
│   │
│   ├── ripper/
│   │   ├── engine.go                # Rip job management, goroutine-per-drive, progress tracking
│   │   ├── engine_test.go
│   │   ├── job.go                   # RipJob struct, status lifecycle
│   │   └── job_test.go
│   │
│   ├── organizer/
│   │   ├── organizer.go             # Template rendering, path building, atomic move
│   │   ├── organizer_test.go
│   │   ├── sanitize.go              # Cross-platform filename sanitization
│   │   └── sanitize_test.go
│   │
│   ├── db/
│   │   ├── db.go                    # SQLite connection, migrations, schema
│   │   ├── db_test.go
│   │   ├── jobs.go                  # RipJob CRUD (insert, update status, query history)
│   │   ├── jobs_test.go
│   │   ├── mappings.go              # Remembered disc->release mappings CRUD
│   │   ├── mappings_test.go
│   │   ├── settings.go              # AppConfig persistence (read/write YAML-equivalent to SQLite)
│   │   └── settings_test.go
│   │
│   └── web/
│       ├── server.go                # Echo setup, routes, middleware, static files
│       ├── sse.go                   # SSE hub: broadcast drive events + rip progress to clients
│       ├── sse_test.go
│       ├── handlers_dashboard.go    # GET / — dashboard with all drives
│       ├── handlers_drive.go        # GET /drives/:id, POST /drives/:id/search, POST /drives/:id/rip
│       ├── handlers_queue.go        # GET /queue — active/pending/completed jobs
│       ├── handlers_history.go      # GET /history — past rips, search/filter
│       ├── handlers_settings.go     # GET/POST /settings
│       └── handlers_contribute.go   # POST /drives/:id/contribute — TheDiscDB submission
│
├── templates/
│   ├── layout.templ                 # Base HTML layout (head, nav, dark theme, HTMX/SSE scripts)
│   ├── dashboard.templ              # Drive cards with state, disc info, progress bars
│   ├── drive_detail.templ           # Scan results, search UI, title checkboxes, rip button
│   ├── drive_search_results.templ   # HTMX partial: TheDiscDB search results
│   ├── queue.templ                  # Job list with progress
│   ├── history.templ                # Past rips table with filters
│   ├── settings.templ               # Config form with template preview
│   ├── contribute.templ             # Title labeling form for TheDiscDB contribution
│   └── components/
│       ├── progress_bar.templ       # Reusable progress bar (SSE-swappable)
│       ├── drive_card.templ         # Single drive summary card
│       └── nav.templ                # Navigation bar
│
├── static/
│   └── style.css                    # Dark theme with blue accents, responsive layout
│
├── migrations/
│   ├── 001_initial.sql              # Schema: rip_jobs, disc_mappings, settings, discdb_cache
│   └── embed.go                     # Embeds SQL files via go:embed
│
└── testutil/
    └── fixtures.go                  # Shared test helpers: sample MakeMKV output, mock executor
```

---

### Task 1: Project Scaffolding & Go Module

**Files:**
- Create: `go.mod`
- Create: `main.go`
- Create: `Dockerfile`
- Create: `docker-compose.yml`

- [ ] **Step 1: Initialize Go module**

```bash
cd /Users/johnpostlethwait/Documents/workspace/BluForge
go mod init github.com/johnpostlethwait/bluforge
```

- [ ] **Step 2: Create minimal main.go**

```go
// main.go
package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Println("BluForge starting...")
	os.Exit(0)
}
```

- [ ] **Step 3: Verify it compiles and runs**

Run: `go run main.go`
Expected: `BluForge starting...`

- [ ] **Step 4: Create Dockerfile**

```dockerfile
# Dockerfile
FROM golang:1.23-alpine AS builder

RUN apk add --no-cache gcc musl-dev

WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o bluforge .

FROM alpine:3.20

RUN apk add --no-cache makemkv ffmpeg

WORKDIR /app
COPY --from=builder /build/bluforge .

EXPOSE 9160

VOLUME ["/config", "/output"]

ENTRYPOINT ["/app/bluforge"]
```

- [ ] **Step 5: Create docker-compose.yml**

```yaml
# docker-compose.yml
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

- [ ] **Step 6: Commit**

```bash
git add go.mod main.go Dockerfile docker-compose.yml
git commit -m "feat: project scaffolding with Go module, Dockerfile, and docker-compose"
```

---

### Task 2: Configuration System

**Files:**
- Create: `internal/config/config.go`
- Create: `internal/config/config_test.go`

- [ ] **Step 1: Write the failing test for env var defaults**

```go
// internal/config/config_test.go
package config

import (
	"os"
	"testing"
)

func TestLoadReturnsDefaults(t *testing.T) {
	// Clear any BLUFORGE_ env vars
	for _, e := range os.Environ() {
		if len(e) > 9 && e[:9] == "BLUFORGE_" {
			key := e[:strings.Index(e, "=")]
			os.Unsetenv(key)
		}
	}

	cfg := LoadFromEnv()

	if cfg.Port != 9160 {
		t.Errorf("expected port 9160, got %d", cfg.Port)
	}
	if cfg.OutputDir != "/output" {
		t.Errorf("expected /output, got %s", cfg.OutputDir)
	}
	if cfg.AutoRip != false {
		t.Errorf("expected AutoRip false, got true")
	}
	if cfg.MinTitleLength != 120 {
		t.Errorf("expected MinTitleLength 120, got %d", cfg.MinTitleLength)
	}
	if cfg.PollInterval != 5 {
		t.Errorf("expected PollInterval 5, got %d", cfg.PollInterval)
	}
	if cfg.MovieTemplate != "Movies/{{.Title}} ({{.Year}})/{{.Title}} ({{.Year}})" {
		t.Errorf("unexpected MovieTemplate: %s", cfg.MovieTemplate)
	}
	if cfg.SeriesTemplate != "TV/{{.Show}}/Season {{.Season}}/{{.Show}} - S{{.Season}}E{{.Episode}} - {{.EpisodeTitle}}" {
		t.Errorf("unexpected SeriesTemplate: %s", cfg.SeriesTemplate)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/config/ -v -run TestLoadReturnsDefaults`
Expected: FAIL — `LoadFromEnv` not defined

- [ ] **Step 3: Implement config loading**

```go
// internal/config/config.go
package config

import (
	"os"
	"strconv"
)

type AppConfig struct {
	Port              int    `yaml:"port"`
	OutputDir         string `yaml:"output_dir"`
	AutoRip           bool   `yaml:"auto_rip"`
	MinTitleLength    int    `yaml:"min_title_length"`
	PollInterval      int    `yaml:"poll_interval"`
	MovieTemplate     string `yaml:"movie_template"`
	SeriesTemplate    string `yaml:"series_template"`
	GitHubClientID    string `yaml:"github_client_id"`
	GitHubClientSecret string `yaml:"github_client_secret"`
	DuplicateAction   string `yaml:"duplicate_action"` // "skip" or "overwrite"
}

func LoadFromEnv() AppConfig {
	return AppConfig{
		Port:              envInt("BLUFORGE_PORT", 9160),
		OutputDir:         envStr("BLUFORGE_OUTPUT_DIR", "/output"),
		AutoRip:           envBool("BLUFORGE_AUTO_RIP", false),
		MinTitleLength:    envInt("BLUFORGE_MIN_TITLE_LENGTH", 120),
		PollInterval:      envInt("BLUFORGE_POLL_INTERVAL", 5),
		MovieTemplate:     envStr("BLUFORGE_MOVIE_TEMPLATE", "Movies/{{.Title}} ({{.Year}})/{{.Title}} ({{.Year}})"),
		SeriesTemplate:    envStr("BLUFORGE_SERIES_TEMPLATE", "TV/{{.Show}}/Season {{.Season}}/{{.Show}} - S{{.Season}}E{{.Episode}} - {{.EpisodeTitle}}"),
		GitHubClientID:    envStr("BLUFORGE_GITHUB_CLIENT_ID", ""),
		GitHubClientSecret: envStr("BLUFORGE_GITHUB_CLIENT_SECRET", ""),
		DuplicateAction:   envStr("BLUFORGE_DUPLICATE_ACTION", "skip"),
	}
}

func envStr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			return i
		}
	}
	return fallback
}

func envBool(key string, fallback bool) bool {
	if v := os.Getenv(key); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
	}
	return fallback
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/config/ -v -run TestLoadReturnsDefaults`
Expected: PASS

- [ ] **Step 5: Write test for env var overrides**

```go
// Add to internal/config/config_test.go

func TestLoadRespectsEnvVars(t *testing.T) {
	os.Setenv("BLUFORGE_PORT", "3000")
	os.Setenv("BLUFORGE_AUTO_RIP", "true")
	os.Setenv("BLUFORGE_MIN_TITLE_LENGTH", "60")
	defer func() {
		os.Unsetenv("BLUFORGE_PORT")
		os.Unsetenv("BLUFORGE_AUTO_RIP")
		os.Unsetenv("BLUFORGE_MIN_TITLE_LENGTH")
	}()

	cfg := LoadFromEnv()

	if cfg.Port != 3000 {
		t.Errorf("expected port 3000, got %d", cfg.Port)
	}
	if cfg.AutoRip != true {
		t.Errorf("expected AutoRip true, got false")
	}
	if cfg.MinTitleLength != 60 {
		t.Errorf("expected MinTitleLength 60, got %d", cfg.MinTitleLength)
	}
}
```

- [ ] **Step 6: Run test to verify it passes**

Run: `go test ./internal/config/ -v -run TestLoadRespectsEnvVars`
Expected: PASS

- [ ] **Step 7: Write test for YAML file override of env vars**

```go
// Add to internal/config/config_test.go

func TestLoadFromFileOverridesEnv(t *testing.T) {
	os.Setenv("BLUFORGE_PORT", "3000")
	defer os.Unsetenv("BLUFORGE_PORT")

	dir := t.TempDir()
	yamlContent := []byte("port: 7777\nauto_rip: true\n")
	configPath := filepath.Join(dir, "config.yaml")
	os.WriteFile(configPath, yamlContent, 0644)

	cfg := Load(configPath)

	if cfg.Port != 7777 {
		t.Errorf("expected port 7777 (from file), got %d", cfg.Port)
	}
	if cfg.AutoRip != true {
		t.Errorf("expected AutoRip true (from file), got false")
	}
	// OutputDir should still be env default since file didn't set it
	if cfg.OutputDir != "/output" {
		t.Errorf("expected OutputDir /output (default), got %s", cfg.OutputDir)
	}
}
```

- [ ] **Step 8: Run test to verify it fails**

Run: `go test ./internal/config/ -v -run TestLoadFromFileOverridesEnv`
Expected: FAIL — `Load` not defined

- [ ] **Step 9: Implement Load with YAML file override**

```go
// Add to internal/config/config.go

import (
	"os"
	"strconv"

	"gopkg.in/yaml.v3"
)

// Load reads env var defaults, then overrides with YAML config file if it exists.
// The config file is the source of truth — env vars only seed initial values.
func Load(configPath string) AppConfig {
	cfg := LoadFromEnv()

	data, err := os.ReadFile(configPath)
	if err != nil {
		return cfg
	}

	var fileCfg AppConfig
	if err := yaml.Unmarshal(data, &fileCfg); err != nil {
		return cfg
	}

	// File values override env defaults (zero values mean "not set in file")
	if fileCfg.Port != 0 {
		cfg.Port = fileCfg.Port
	}
	if fileCfg.OutputDir != "" {
		cfg.OutputDir = fileCfg.OutputDir
	}
	if fileCfg.AutoRip {
		cfg.AutoRip = fileCfg.AutoRip
	}
	if fileCfg.MinTitleLength != 0 {
		cfg.MinTitleLength = fileCfg.MinTitleLength
	}
	if fileCfg.PollInterval != 0 {
		cfg.PollInterval = fileCfg.PollInterval
	}
	if fileCfg.MovieTemplate != "" {
		cfg.MovieTemplate = fileCfg.MovieTemplate
	}
	if fileCfg.SeriesTemplate != "" {
		cfg.SeriesTemplate = fileCfg.SeriesTemplate
	}
	if fileCfg.GitHubClientID != "" {
		cfg.GitHubClientID = fileCfg.GitHubClientID
	}
	if fileCfg.GitHubClientSecret != "" {
		cfg.GitHubClientSecret = fileCfg.GitHubClientSecret
	}
	if fileCfg.DuplicateAction != "" {
		cfg.DuplicateAction = fileCfg.DuplicateAction
	}

	return cfg
}

// Save writes the config to a YAML file.
func Save(cfg AppConfig, configPath string) error {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	return os.WriteFile(configPath, data, 0644)
}
```

- [ ] **Step 10: Install yaml dependency and run tests**

Run: `go get gopkg.in/yaml.v3 && go test ./internal/config/ -v`
Expected: All PASS

- [ ] **Step 11: Commit**

```bash
git add internal/config/ go.mod go.sum
git commit -m "feat: configuration system with env var defaults and YAML file override"
```

---

### Task 3: MakeMKV Types & Robot-Mode Parser

**Files:**
- Create: `internal/makemkv/types.go`
- Create: `internal/makemkv/parser.go`
- Create: `internal/makemkv/parser_test.go`
- Create: `testutil/fixtures.go`

- [ ] **Step 1: Create MakeMKV types**

```go
// internal/makemkv/types.go
package makemkv

// DriveInfo represents a single DRV line from makemkvcon.
type DriveInfo struct {
	Index    int
	Visible  int
	Enabled  int
	Flags    int
	DriveName string
	DiscName  string
}

// DiscInfo holds disc-level attributes from CINFO lines.
type DiscInfo struct {
	Attributes map[int]string // attribute_id -> value
}

func (d DiscInfo) Name() string     { return d.Attributes[2] }
func (d DiscInfo) Type() string     { return d.Attributes[1] }

// TitleInfo holds title-level attributes from TINFO lines.
type TitleInfo struct {
	Index      int
	Attributes map[int]string // attribute_id -> value
	Streams    []StreamInfo
}

func (t TitleInfo) Name() string       { return t.Attributes[2] }
func (t TitleInfo) ChapterCount() string { return t.Attributes[8] }
func (t TitleInfo) Duration() string   { return t.Attributes[9] }
func (t TitleInfo) SizeHuman() string  { return t.Attributes[10] }
func (t TitleInfo) SizeBytes() string  { return t.Attributes[11] }
func (t TitleInfo) Filename() string   { return t.Attributes[27] }
func (t TitleInfo) SegmentMap() string { return t.Attributes[16] }
func (t TitleInfo) SourceFile() string { return t.Attributes[33] }

// StreamInfo holds stream-level attributes from SINFO lines.
type StreamInfo struct {
	TitleIndex int
	StreamIndex int
	Attributes  map[int]string
}

// Progress holds PRGV progress values.
type Progress struct {
	Current int
	Total   int
	Max     int
}

// Message holds a MSG line.
type Message struct {
	Code    int
	Flags   int
	Count   int
	Text    string
	Format  string
	Params  []string
}

// Event is a tagged union of all parser outputs.
type Event struct {
	Type     string // "DRV", "TCOUT", "CINFO", "TINFO", "SINFO", "MSG", "PRGV"
	Drive    *DriveInfo
	Disc     *DiscInfo
	Title    *TitleInfo
	Stream   *StreamInfo
	Progress *Progress
	Message  *Message
	Count    int // for TCOUT
}
```

- [ ] **Step 2: Write failing test for DRV line parsing**

```go
// internal/makemkv/parser_test.go
package makemkv

import (
	"strings"
	"testing"
)

func TestParseDRVLine(t *testing.T) {
	input := `DRV:0,2,999,1,"BD-RE HL-DT-ST BD-RE  WH16NS40","DEADPOOL_2","/dev/sr0"`
	reader := strings.NewReader(input + "\n")

	events, err := ParseAll(reader)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	ev := events[0]
	if ev.Type != "DRV" {
		t.Errorf("expected type DRV, got %s", ev.Type)
	}
	if ev.Drive.Index != 0 {
		t.Errorf("expected index 0, got %d", ev.Drive.Index)
	}
	if ev.Drive.DriveName != "BD-RE HL-DT-ST BD-RE  WH16NS40" {
		t.Errorf("unexpected drive name: %s", ev.Drive.DriveName)
	}
	if ev.Drive.DiscName != "DEADPOOL_2" {
		t.Errorf("unexpected disc name: %s", ev.Drive.DiscName)
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/makemkv/ -v -run TestParseDRVLine`
Expected: FAIL — `ParseAll` not defined

- [ ] **Step 4: Write failing test for TINFO line parsing**

```go
// Add to internal/makemkv/parser_test.go

func TestParseTINFOLine(t *testing.T) {
	input := `TINFO:0,9,0,"1:42:37"` + "\n" + `TINFO:0,11,0,"22520651776"` + "\n" + `TINFO:0,27,0,"title_t00.mkv"` + "\n"
	reader := strings.NewReader(input)

	events, err := ParseAll(reader)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("expected 3 events, got %d", len(events))
	}

	// Check duration attribute
	ev := events[0]
	if ev.Type != "TINFO" {
		t.Errorf("expected type TINFO, got %s", ev.Type)
	}
	if ev.Title.Index != 0 {
		t.Errorf("expected title index 0, got %d", ev.Title.Index)
	}
	if ev.Title.Attributes[9] != "1:42:37" {
		t.Errorf("expected duration 1:42:37, got %s", ev.Title.Attributes[9])
	}
}
```

- [ ] **Step 5: Write failing test for PRGV line parsing**

```go
// Add to internal/makemkv/parser_test.go

func TestParsePRGVLine(t *testing.T) {
	input := `PRGV:125,1000,65536` + "\n"
	reader := strings.NewReader(input)

	events, err := ParseAll(reader)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	ev := events[0]
	if ev.Type != "PRGV" {
		t.Errorf("expected type PRGV, got %s", ev.Type)
	}
	if ev.Progress.Current != 125 {
		t.Errorf("expected current 125, got %d", ev.Progress.Current)
	}
	if ev.Progress.Total != 1000 {
		t.Errorf("expected total 1000, got %d", ev.Progress.Total)
	}
	if ev.Progress.Max != 65536 {
		t.Errorf("expected max 65536, got %d", ev.Progress.Max)
	}
}
```

- [ ] **Step 6: Write failing test for MSG line parsing**

```go
// Add to internal/makemkv/parser_test.go

func TestParseMSGLine(t *testing.T) {
	input := `MSG:1005,0,1,"Operation successfully completed","",""` + "\n"
	reader := strings.NewReader(input)

	events, err := ParseAll(reader)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	ev := events[0]
	if ev.Type != "MSG" {
		t.Errorf("expected type MSG, got %s", ev.Type)
	}
	if ev.Message.Code != 1005 {
		t.Errorf("expected code 1005, got %d", ev.Message.Code)
	}
	if ev.Message.Text != "Operation successfully completed" {
		t.Errorf("unexpected text: %s", ev.Message.Text)
	}
}
```

- [ ] **Step 7: Write failing test for mixed multi-line output**

```go
// Add to internal/makemkv/parser_test.go

func TestParseMultiLineOutput(t *testing.T) {
	input := `TCOUT:3
CINFO:2,0,"DEADPOOL_2"
TINFO:0,9,0,"1:42:37"
TINFO:0,11,0,"22520651776"
TINFO:1,9,0,"0:04:12"
SINFO:0,0,2,0,"English"
MSG:1005,0,1,"Operation successfully completed","",""
`
	reader := strings.NewReader(input)

	events, err := ParseAll(reader)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) != 7 {
		t.Fatalf("expected 7 events, got %d", len(events))
	}

	if events[0].Type != "TCOUT" || events[0].Count != 3 {
		t.Errorf("expected TCOUT with count 3, got %s/%d", events[0].Type, events[0].Count)
	}
	if events[1].Type != "CINFO" {
		t.Errorf("expected CINFO, got %s", events[1].Type)
	}
	if events[6].Type != "MSG" {
		t.Errorf("expected MSG last, got %s", events[6].Type)
	}
}
```

- [ ] **Step 8: Implement the parser**

```go
// internal/makemkv/parser.go
package makemkv

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// ParseAll reads all lines from r and returns parsed events.
func ParseAll(r io.Reader) ([]Event, error) {
	var events []Event
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		ev, err := ParseLine(line)
		if err != nil {
			continue // skip unparseable lines
		}
		events = append(events, ev)
	}
	return events, scanner.Err()
}

// ParseLine parses a single robot-mode output line.
func ParseLine(line string) (Event, error) {
	colonIdx := strings.Index(line, ":")
	if colonIdx < 0 {
		return Event{}, fmt.Errorf("no colon in line: %s", line)
	}

	prefix := line[:colonIdx]
	rest := line[colonIdx+1:]

	switch prefix {
	case "DRV":
		return parseDRV(rest)
	case "TCOUT":
		return parseTCOUT(rest)
	case "CINFO":
		return parseCINFO(rest)
	case "TINFO":
		return parseTINFO(rest)
	case "SINFO":
		return parseSINFO(rest)
	case "MSG":
		return parseMSG(rest)
	case "PRGV":
		return parsePRGV(rest)
	case "PRGT", "PRGC":
		return Event{Type: prefix}, nil
	default:
		return Event{}, fmt.Errorf("unknown prefix: %s", prefix)
	}
}

// parseCSV splits a comma-separated line respecting quoted strings with backslash escapes.
func parseCSV(s string) []string {
	var fields []string
	var current strings.Builder
	inQuotes := false
	escaped := false

	for i := 0; i < len(s); i++ {
		ch := s[i]
		if escaped {
			current.WriteByte(ch)
			escaped = false
			continue
		}
		if ch == '\\' && inQuotes {
			escaped = true
			continue
		}
		if ch == '"' {
			inQuotes = !inQuotes
			continue
		}
		if ch == ',' && !inQuotes {
			fields = append(fields, current.String())
			current.Reset()
			continue
		}
		current.WriteByte(ch)
	}
	fields = append(fields, current.String())
	return fields
}

func parseDRV(rest string) (Event, error) {
	fields := parseCSV(rest)
	if len(fields) < 6 {
		return Event{}, fmt.Errorf("DRV needs 6 fields, got %d", len(fields))
	}
	index, _ := strconv.Atoi(fields[0])
	visible, _ := strconv.Atoi(fields[1])
	enabled, _ := strconv.Atoi(fields[2])
	flags, _ := strconv.Atoi(fields[3])
	return Event{
		Type: "DRV",
		Drive: &DriveInfo{
			Index:     index,
			Visible:   visible,
			Enabled:   enabled,
			Flags:     flags,
			DriveName: fields[4],
			DiscName:  fields[5],
		},
	}, nil
}

func parseTCOUT(rest string) (Event, error) {
	count, err := strconv.Atoi(strings.TrimSpace(rest))
	if err != nil {
		return Event{}, err
	}
	return Event{Type: "TCOUT", Count: count}, nil
}

func parseCINFO(rest string) (Event, error) {
	fields := parseCSV(rest)
	if len(fields) < 3 {
		return Event{}, fmt.Errorf("CINFO needs 3 fields, got %d", len(fields))
	}
	attrID, _ := strconv.Atoi(fields[0])
	return Event{
		Type: "CINFO",
		Disc: &DiscInfo{
			Attributes: map[int]string{attrID: fields[2]},
		},
	}, nil
}

func parseTINFO(rest string) (Event, error) {
	fields := parseCSV(rest)
	if len(fields) < 4 {
		return Event{}, fmt.Errorf("TINFO needs 4 fields, got %d", len(fields))
	}
	titleIdx, _ := strconv.Atoi(fields[0])
	attrID, _ := strconv.Atoi(fields[1])
	return Event{
		Type: "TINFO",
		Title: &TitleInfo{
			Index:      titleIdx,
			Attributes: map[int]string{attrID: fields[3]},
		},
	}, nil
}

func parseSINFO(rest string) (Event, error) {
	fields := parseCSV(rest)
	if len(fields) < 5 {
		return Event{}, fmt.Errorf("SINFO needs 5 fields, got %d", len(fields))
	}
	titleIdx, _ := strconv.Atoi(fields[0])
	streamIdx, _ := strconv.Atoi(fields[1])
	attrID, _ := strconv.Atoi(fields[2])
	return Event{
		Type: "SINFO",
		Stream: &StreamInfo{
			TitleIndex:  titleIdx,
			StreamIndex: streamIdx,
			Attributes:  map[int]string{attrID: fields[4]},
		},
	}, nil
}

func parseMSG(rest string) (Event, error) {
	fields := parseCSV(rest)
	if len(fields) < 5 {
		return Event{}, fmt.Errorf("MSG needs 5+ fields, got %d", len(fields))
	}
	code, _ := strconv.Atoi(fields[0])
	flags, _ := strconv.Atoi(fields[1])
	count, _ := strconv.Atoi(fields[2])
	var params []string
	if len(fields) > 5 {
		params = fields[5:]
	}
	return Event{
		Type: "MSG",
		Message: &Message{
			Code:   code,
			Flags:  flags,
			Count:  count,
			Text:   fields[3],
			Format: fields[4],
			Params: params,
		},
	}, nil
}

func parsePRGV(rest string) (Event, error) {
	fields := strings.Split(strings.TrimSpace(rest), ",")
	if len(fields) < 3 {
		return Event{}, fmt.Errorf("PRGV needs 3 fields, got %d", len(fields))
	}
	current, _ := strconv.Atoi(fields[0])
	total, _ := strconv.Atoi(fields[1])
	max, _ := strconv.Atoi(fields[2])
	return Event{
		Type: "PRGV",
		Progress: &Progress{
			Current: current,
			Total:   total,
			Max:     max,
		},
	}, nil
}
```

- [ ] **Step 9: Run all tests to verify they pass**

Run: `go test ./internal/makemkv/ -v`
Expected: All PASS

- [ ] **Step 10: Create test fixtures helper**

```go
// testutil/fixtures.go
package testutil

// SampleDriveListOutput is realistic makemkvcon -r info disc:9999 output with 2 drives.
const SampleDriveListOutput = `DRV:0,2,999,1,"BD-RE HL-DT-ST BD-RE  WH16NS40","DEADPOOL_2","/dev/sr0"
DRV:1,2,999,1,"BD-RE HL-DT-ST BD-RE  WH16NS40","","/dev/sr1"
DRV:2,0,999,0,"","",""
`

// SampleDiscInfoOutput is realistic makemkvcon -r info disc:0 output.
const SampleDiscInfoOutput = `MSG:1005,0,0,"Using LibreDrive mode (v06 id=DEADBEEF0123)","",""
TCOUT:3
CINFO:1,6209,"Blu-ray disc"
CINFO:2,0,"DEADPOOL_2"
CINFO:28,0,"eng"
CINFO:30,0,"DEADPOOL_2"
CINFO:33,0,"0"
TINFO:0,2,0,"Deadpool 2"
TINFO:0,8,0,"24"
TINFO:0,9,0,"1:59:30"
TINFO:0,10,0,"28.4 GB"
TINFO:0,11,0,"30520000000"
TINFO:0,16,0,"00001.mpls"
TINFO:0,27,0,"title_t00.mkv"
TINFO:0,33,0,"00001.mpls"
TINFO:1,2,0,"Deadpool 2 Super Duper Cut"
TINFO:1,8,0,"26"
TINFO:1,9,0,"2:14:01"
TINFO:1,10,0,"32.1 GB"
TINFO:1,11,0,"34471000000"
TINFO:1,16,0,"00002.mpls"
TINFO:1,27,0,"title_t01.mkv"
TINFO:1,33,0,"00002.mpls"
TINFO:2,2,0,"Extras"
TINFO:2,8,0,"1"
TINFO:2,9,0,"0:02:15"
TINFO:2,10,0,"215 MB"
TINFO:2,11,0,"215000000"
TINFO:2,16,0,"00010.mpls"
TINFO:2,27,0,"title_t02.mkv"
TINFO:2,33,0,"00010.mpls"
SINFO:0,0,1,6201,"Video"
SINFO:0,0,19,0,"1080p"
SINFO:0,0,20,0,"16:9"
SINFO:0,0,21,0,"23.976 (24000/1001)"
SINFO:0,1,1,6202,"Audio"
SINFO:0,1,2,0,"Surround 7.1"
SINFO:0,1,3,0,"eng"
SINFO:0,1,4,0,"DTS-HD Master Audio"
MSG:1005,0,1,"Operation successfully completed","",""
`

// SampleProgressOutput is realistic makemkvcon mkv progress output.
const SampleProgressOutput = `PRGV:0,1000,65536
PRGV:100,1000,65536
PRGV:500,1000,65536
PRGV:1000,1000,65536
MSG:1005,0,1,"Operation successfully completed","",""
`
```

- [ ] **Step 11: Commit**

```bash
git add internal/makemkv/ testutil/
git commit -m "feat: MakeMKV types and robot-mode parser with tests"
```

---

### Task 4: MakeMKV Executor (Process Wrapper)

**Files:**
- Create: `internal/makemkv/executor.go`
- Create: `internal/makemkv/executor_test.go`

- [ ] **Step 1: Write failing test for executor interface**

```go
// internal/makemkv/executor_test.go
package makemkv

import (
	"context"
	"strings"
	"testing"
)

type mockCmdRunner struct {
	output string
	err    error
}

func (m *mockCmdRunner) Run(ctx context.Context, args ...string) (*strings.Reader, error) {
	return strings.NewReader(m.output), m.err
}

func TestExecutorListDrives(t *testing.T) {
	mock := &mockCmdRunner{
		output: `DRV:0,2,999,1,"BD-RE HL-DT-ST","DEADPOOL_2","/dev/sr0"
DRV:1,0,999,0,"","",""
`,
	}
	exec := NewExecutor(WithRunner(mock))

	drives, err := exec.ListDrives(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(drives) != 2 {
		t.Fatalf("expected 2 drives, got %d", len(drives))
	}
	if drives[0].DiscName != "DEADPOOL_2" {
		t.Errorf("expected DEADPOOL_2, got %s", drives[0].DiscName)
	}
}

func TestExecutorScanDisc(t *testing.T) {
	mock := &mockCmdRunner{
		output: `TCOUT:2
CINFO:2,0,"DEADPOOL_2"
TINFO:0,9,0,"1:59:30"
TINFO:0,11,0,"30520000000"
TINFO:0,27,0,"title_t00.mkv"
TINFO:0,33,0,"00001.mpls"
TINFO:1,9,0,"0:02:15"
TINFO:1,11,0,"215000000"
TINFO:1,27,0,"title_t01.mkv"
TINFO:1,33,0,"00010.mpls"
MSG:1005,0,1,"Operation successfully completed","",""
`,
	}
	exec := NewExecutor(WithRunner(mock))

	scan, err := exec.ScanDisc(context.Background(), 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if scan.DiscName != "DEADPOOL_2" {
		t.Errorf("expected disc name DEADPOOL_2, got %s", scan.DiscName)
	}
	if scan.TitleCount != 2 {
		t.Fatalf("expected 2 titles, got %d", scan.TitleCount)
	}
	if scan.Titles[0].Attributes[33] != "00001.mpls" {
		t.Errorf("expected source file 00001.mpls, got %s", scan.Titles[0].Attributes[33])
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/makemkv/ -v -run TestExecutor`
Expected: FAIL — `NewExecutor`, `WithRunner`, etc. not defined

- [ ] **Step 3: Implement executor**

```go
// internal/makemkv/executor.go
package makemkv

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
	"strings"
)

// CmdRunner abstracts command execution for testing.
type CmdRunner interface {
	Run(ctx context.Context, args ...string) (*strings.Reader, error)
}

// realRunner executes actual makemkvcon commands.
type realRunner struct {
	binary string
}

func (r *realRunner) Run(ctx context.Context, args ...string) (*strings.Reader, error) {
	cmd := exec.CommandContext(ctx, r.binary, args...)
	out, err := cmd.Output()
	if err != nil {
		return strings.NewReader(string(out)), err
	}
	return strings.NewReader(string(out)), nil
}

// Executor wraps makemkvcon operations.
type Executor struct {
	runner CmdRunner
}

type Option func(*Executor)

func WithRunner(r CmdRunner) Option {
	return func(e *Executor) { e.runner = r }
}

func NewExecutor(opts ...Option) *Executor {
	e := &Executor{
		runner: &realRunner{binary: "makemkvcon"},
	}
	for _, o := range opts {
		o(e)
	}
	return e
}

// ListDrives runs makemkvcon -r --cache=1 info disc:9999 and returns drive info.
func (e *Executor) ListDrives(ctx context.Context) ([]DriveInfo, error) {
	reader, err := e.runner.Run(ctx, "-r", "--cache=1", "info", "disc:9999")
	if err != nil {
		slog.Error("makemkvcon list drives failed", "error", err)
		// Still try to parse partial output
	}
	return e.parseDrives(reader)
}

func (e *Executor) parseDrives(r io.Reader) ([]DriveInfo, error) {
	events, err := ParseAll(r)
	if err != nil {
		return nil, err
	}
	var drives []DriveInfo
	for _, ev := range events {
		if ev.Type == "DRV" && ev.Drive != nil {
			drives = append(drives, *ev.Drive)
		}
	}
	return drives, nil
}

// DiscScan is the aggregated result of scanning a single disc.
type DiscScan struct {
	DriveIndex int
	DiscName   string
	DiscType   string
	TitleCount int
	Titles     []TitleInfo
	Messages   []Message
}

// ScanDisc runs makemkvcon -r info disc:N and returns aggregated scan results.
func (e *Executor) ScanDisc(ctx context.Context, driveIndex int) (*DiscScan, error) {
	source := fmt.Sprintf("disc:%d", driveIndex)
	reader, err := e.runner.Run(ctx, "-r", "info", source)
	if err != nil {
		slog.Error("makemkvcon scan disc failed", "drive", driveIndex, "error", err)
	}
	return e.parseScan(reader, driveIndex)
}

func (e *Executor) parseScan(r io.Reader, driveIndex int) (*DiscScan, error) {
	events, err := ParseAll(r)
	if err != nil {
		return nil, err
	}

	scan := &DiscScan{DriveIndex: driveIndex}
	titleMap := make(map[int]*TitleInfo)

	for _, ev := range events {
		switch ev.Type {
		case "TCOUT":
			scan.TitleCount = ev.Count
		case "CINFO":
			if ev.Disc != nil {
				if name, ok := ev.Disc.Attributes[2]; ok {
					scan.DiscName = name
				}
				if dtype, ok := ev.Disc.Attributes[1]; ok {
					scan.DiscType = dtype
				}
			}
		case "TINFO":
			if ev.Title != nil {
				existing, ok := titleMap[ev.Title.Index]
				if !ok {
					t := *ev.Title
					titleMap[ev.Title.Index] = &t
				} else {
					for k, v := range ev.Title.Attributes {
						existing.Attributes[k] = v
					}
				}
			}
		case "SINFO":
			if ev.Stream != nil {
				if t, ok := titleMap[ev.Stream.TitleIndex]; ok {
					t.Streams = append(t.Streams, *ev.Stream)
				}
			}
		case "MSG":
			if ev.Message != nil {
				scan.Messages = append(scan.Messages, *ev.Message)
			}
		}
	}

	// Convert map to sorted slice
	for i := 0; i < len(titleMap); i++ {
		if t, ok := titleMap[i]; ok {
			scan.Titles = append(scan.Titles, *t)
		}
	}

	return scan, nil
}

// StartRip runs makemkvcon mkv disc:N titleID outputDir and streams events via callback.
func (e *Executor) StartRip(ctx context.Context, driveIndex int, titleID int, outputDir string, onEvent func(Event)) error {
	source := fmt.Sprintf("disc:%d", driveIndex)
	titleStr := fmt.Sprintf("%d", titleID)

	reader, err := e.runner.Run(ctx, "-r", "mkv", source, titleStr, outputDir)
	if err != nil && reader == nil {
		return fmt.Errorf("failed to start rip: %w", err)
	}

	events, parseErr := ParseAll(reader)
	if parseErr != nil {
		return parseErr
	}
	for _, ev := range events {
		if onEvent != nil {
			onEvent(ev)
		}
	}

	return err
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/makemkv/ -v`
Expected: All PASS

- [ ] **Step 5: Commit**

```bash
git add internal/makemkv/executor.go internal/makemkv/executor_test.go
git commit -m "feat: MakeMKV executor with testable command runner abstraction"
```

---

### Task 5: Cross-Platform Filename Sanitization

**Files:**
- Create: `internal/organizer/sanitize.go`
- Create: `internal/organizer/sanitize_test.go`

- [ ] **Step 1: Write failing tests**

```go
// internal/organizer/sanitize_test.go
package organizer

import "testing"

func TestSanitizeStripsInvalidChars(t *testing.T) {
	cases := []struct {
		input    string
		expected string
	}{
		{"Movie: The Sequel", "Movie - The Sequel"},
		{`He said "hello"`, "He said 'hello'"},
		{"What?", "What"},
		{"file<name>.mkv", "filename.mkv"},
		{"path/to\\file", "pathtofile"},
		{"pipe|here", "pipehere"},
		{"star*wars", "starwars"},
	}
	for _, tc := range cases {
		result := SanitizeFilename(tc.input)
		if result != tc.expected {
			t.Errorf("SanitizeFilename(%q) = %q, want %q", tc.input, result, tc.expected)
		}
	}
}

func TestSanitizeControlChars(t *testing.T) {
	input := "hello\x00world\x1Fend\x7F"
	result := SanitizeFilename(input)
	if result != "helloworldend" {
		t.Errorf("expected 'helloworldend', got %q", result)
	}
}

func TestSanitizeReservedWindowsNames(t *testing.T) {
	cases := []struct {
		input    string
		expected string
	}{
		{"CON", "_CON"},
		{"PRN", "_PRN"},
		{"NUL", "_NUL"},
		{"COM1", "_COM1"},
		{"LPT9", "_LPT9"},
		{"con", "_con"},
		{"CONSOLE", "CONSOLE"}, // not reserved — only exact matches
	}
	for _, tc := range cases {
		result := SanitizeFilename(tc.input)
		if result != tc.expected {
			t.Errorf("SanitizeFilename(%q) = %q, want %q", tc.input, result, tc.expected)
		}
	}
}

func TestSanitizeWhitespace(t *testing.T) {
	cases := []struct {
		input    string
		expected string
	}{
		{"  hello  world  ", "hello world"},
		{"multiple   spaces", "multiple spaces"},
		{"\thello\t", "hello"},
	}
	for _, tc := range cases {
		result := SanitizeFilename(tc.input)
		if result != tc.expected {
			t.Errorf("SanitizeFilename(%q) = %q, want %q", tc.input, result, tc.expected)
		}
	}
}

func TestSanitizeMaxLength(t *testing.T) {
	long := ""
	for i := 0; i < 300; i++ {
		long += "a"
	}
	result := SanitizeFilename(long)
	if len(result) > 255 {
		t.Errorf("expected max 255 bytes, got %d", len(result))
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/organizer/ -v`
Expected: FAIL — `SanitizeFilename` not defined

- [ ] **Step 3: Implement sanitizer**

```go
// internal/organizer/sanitize.go
package organizer

import (
	"regexp"
	"strings"
)

var (
	controlChars    = regexp.MustCompile(`[\x00-\x1f\x7f]`)
	multipleSpaces  = regexp.MustCompile(`\s+`)
	reservedWindows = regexp.MustCompile(`(?i)^(CON|PRN|AUX|NUL|COM[1-9]|LPT[1-9])$`)
)

// SanitizeFilename makes a filename safe for Linux, macOS, and Windows.
func SanitizeFilename(name string) string {
	// Replace colons with " -" (common in titles)
	name = strings.ReplaceAll(name, ":", " -")

	// Replace double quotes with single quotes
	name = strings.ReplaceAll(name, `"`, "'")

	// Strip characters invalid on any platform: < > / \ | ? *
	for _, ch := range []string{"<", ">", "/", "\\", "|", "?", "*"} {
		name = strings.ReplaceAll(name, ch, "")
	}

	// Strip control characters
	name = controlChars.ReplaceAllString(name, "")

	// Normalize whitespace
	name = multipleSpaces.ReplaceAllString(name, " ")
	name = strings.TrimSpace(name)

	// Avoid reserved Windows names
	if reservedWindows.MatchString(name) {
		name = "_" + name
	}

	// Enforce max length (255 bytes)
	if len(name) > 255 {
		name = name[:255]
	}

	return name
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/organizer/ -v`
Expected: All PASS

- [ ] **Step 5: Commit**

```bash
git add internal/organizer/sanitize.go internal/organizer/sanitize_test.go
git commit -m "feat: cross-platform filename sanitization"
```

---

### Task 6: File Organizer (Template Rendering & Atomic Move)

**Files:**
- Create: `internal/organizer/organizer.go`
- Create: `internal/organizer/organizer_test.go`

- [ ] **Step 1: Write failing tests**

```go
// internal/organizer/organizer_test.go
package organizer

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBuildMoviePath(t *testing.T) {
	o := New("Movies/{{.Title}} ({{.Year}})/{{.Title}} ({{.Year}})", "")
	result, err := o.BuildMoviePath(MovieMeta{
		Title: "Deadpool 2",
		Year:  "2018",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := "Movies/Deadpool 2 (2018)/Deadpool 2 (2018).mkv"
	if result != expected {
		t.Errorf("expected %q, got %q", expected, result)
	}
}

func TestBuildMoviePathWithPart(t *testing.T) {
	o := New("Movies/{{.Title}} ({{.Year}})/{{.Title}} ({{.Year}})", "")
	result, err := o.BuildMoviePath(MovieMeta{
		Title: "Kill Bill",
		Year:  "2003",
		Part:  "1",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := "Movies/Kill Bill (2003)/Kill Bill (2003) - Part 1.mkv"
	if result != expected {
		t.Errorf("expected %q, got %q", expected, result)
	}
}

func TestBuildSeriesPath(t *testing.T) {
	o := New("", "TV/{{.Show}}/Season {{.Season}}/{{.Show}} - S{{.Season}}E{{.Episode}} - {{.EpisodeTitle}}")
	result, err := o.BuildSeriesPath(SeriesMeta{
		Show:         "Breaking Bad",
		Season:       "01",
		Episode:      "01",
		EpisodeTitle: "Pilot",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := "TV/Breaking Bad/Season 01/Breaking Bad - S01E01 - Pilot.mkv"
	if result != expected {
		t.Errorf("expected %q, got %q", expected, result)
	}
}

func TestBuildPathSanitizesOutput(t *testing.T) {
	o := New("Movies/{{.Title}} ({{.Year}})/{{.Title}} ({{.Year}})", "")
	result, err := o.BuildMoviePath(MovieMeta{
		Title: "What If...?",
		Year:  "2021",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// "?" should be stripped, "..." stays
	expected := "Movies/What If... (2021)/What If... (2021).mkv"
	if result != expected {
		t.Errorf("expected %q, got %q", expected, result)
	}
}

func TestAtomicMove(t *testing.T) {
	tmpDir := t.TempDir()
	src := filepath.Join(tmpDir, "source.mkv")
	dstDir := filepath.Join(tmpDir, "output", "Movies", "Test (2024)")
	dst := filepath.Join(dstDir, "Test (2024).mkv")

	os.WriteFile(src, []byte("fake mkv content"), 0644)

	err := AtomicMove(src, dst)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Source should not exist
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Error("source file should not exist after move")
	}

	// Destination should exist with correct content
	data, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("failed to read destination: %v", err)
	}
	if string(data) != "fake mkv content" {
		t.Errorf("unexpected content: %s", string(data))
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/organizer/ -v -run "TestBuild|TestAtomic"`
Expected: FAIL — types and functions not defined

- [ ] **Step 3: Implement organizer**

```go
// internal/organizer/organizer.go
package organizer

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"
)

type MovieMeta struct {
	Title string
	Year  string
	Part  string
}

type SeriesMeta struct {
	Show         string
	Season       string
	Episode      string
	EpisodeTitle string
}

type ExtraMeta struct {
	Title       string
	Year        string
	Show        string
	Season      string
	ExtraTitle  string
	ContentType string // "Movie" or "Series"
}

type Organizer struct {
	movieTmpl  *template.Template
	seriesTmpl *template.Template
}

func New(movieTemplate, seriesTemplate string) *Organizer {
	o := &Organizer{}
	if movieTemplate != "" {
		o.movieTmpl = template.Must(template.New("movie").Parse(movieTemplate))
	}
	if seriesTemplate != "" {
		o.seriesTmpl = template.Must(template.New("series").Parse(seriesTemplate))
	}
	return o
}

func (o *Organizer) BuildMoviePath(meta MovieMeta) (string, error) {
	if o.movieTmpl == nil {
		return "", fmt.Errorf("no movie template configured")
	}

	var buf strings.Builder
	if err := o.movieTmpl.Execute(&buf, meta); err != nil {
		return "", fmt.Errorf("template execute: %w", err)
	}

	result := buf.String()

	// Sanitize each path component
	parts := strings.Split(result, "/")
	for i, p := range parts {
		parts[i] = SanitizeFilename(p)
	}
	result = strings.Join(parts, "/")

	// Add part suffix if multi-part
	if meta.Part != "" {
		result += " - Part " + meta.Part
	}

	return result + ".mkv", nil
}

func (o *Organizer) BuildSeriesPath(meta SeriesMeta) (string, error) {
	if o.seriesTmpl == nil {
		return "", fmt.Errorf("no series template configured")
	}

	var buf strings.Builder
	if err := o.seriesTmpl.Execute(&buf, meta); err != nil {
		return "", fmt.Errorf("template execute: %w", err)
	}

	result := buf.String()

	// Sanitize each path component
	parts := strings.Split(result, "/")
	for i, p := range parts {
		parts[i] = SanitizeFilename(p)
	}
	result = strings.Join(parts, "/")

	return result + ".mkv", nil
}

// BuildUnmatchedPath returns a path for discs not found in TheDiscDB.
func BuildUnmatchedPath(discName, filename string) string {
	return filepath.Join("Unmatched", SanitizeFilename(discName), SanitizeFilename(filename))
}

// BuildExtrasPath returns a path for extras content.
func (o *Organizer) BuildExtrasPath(meta ExtraMeta) string {
	if meta.ContentType == "Series" {
		return filepath.Join(
			"TV", SanitizeFilename(meta.Show),
			fmt.Sprintf("Season %s", meta.Season),
			"Extras",
			SanitizeFilename(meta.ExtraTitle)+".mkv",
		)
	}
	return filepath.Join(
		"Movies", SanitizeFilename(fmt.Sprintf("%s (%s)", meta.Title, meta.Year)),
		"Extras",
		SanitizeFilename(meta.ExtraTitle)+".mkv",
	)
}

// AtomicMove moves src to dst, creating parent directories as needed.
// Uses rename for same-filesystem atomicity; falls back to copy+delete for cross-device.
func AtomicMove(src, dst string) error {
	dstDir := filepath.Dir(dst)
	if err := os.MkdirAll(dstDir, 0755); err != nil {
		return fmt.Errorf("create destination dir: %w", err)
	}

	// Try rename first (atomic on same filesystem)
	if err := os.Rename(src, dst); err == nil {
		return nil
	}

	// Fallback: copy + delete (cross-device)
	data, err := os.ReadFile(src)
	if err != nil {
		return fmt.Errorf("read source: %w", err)
	}
	if err := os.WriteFile(dst, data, 0644); err != nil {
		return fmt.Errorf("write destination: %w", err)
	}
	return os.Remove(src)
}

// FileExists checks if a file already exists at the given path.
func FileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/organizer/ -v`
Expected: All PASS

- [ ] **Step 5: Commit**

```bash
git add internal/organizer/organizer.go internal/organizer/organizer_test.go
git commit -m "feat: file organizer with template rendering, atomic move, and duplicate detection"
```

---

### Task 7: SQLite Database Layer

**Files:**
- Create: `migrations/001_initial.sql`
- Create: `migrations/embed.go`
- Create: `internal/db/db.go`
- Create: `internal/db/db_test.go`
- Create: `internal/db/jobs.go`
- Create: `internal/db/jobs_test.go`
- Create: `internal/db/mappings.go`
- Create: `internal/db/mappings_test.go`
- Create: `internal/db/settings.go`
- Create: `internal/db/settings_test.go`

- [ ] **Step 1: Create migration SQL**

```sql
-- migrations/001_initial.sql
CREATE TABLE IF NOT EXISTS rip_jobs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    drive_index INTEGER NOT NULL,
    disc_name TEXT NOT NULL,
    title_index INTEGER NOT NULL,
    title_name TEXT NOT NULL DEFAULT '',
    content_type TEXT NOT NULL DEFAULT '',  -- MainMovie, Episode, Extra, Trailer, etc.
    output_path TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'pending', -- pending, ripping, organizing, completed, failed, skipped
    progress INTEGER NOT NULL DEFAULT 0,   -- 0-100
    error_message TEXT NOT NULL DEFAULT '',
    size_bytes INTEGER NOT NULL DEFAULT 0,
    duration TEXT NOT NULL DEFAULT '',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS disc_mappings (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    disc_key TEXT NOT NULL UNIQUE,        -- hash of disc_name + title_count + segment_maps
    disc_name TEXT NOT NULL,
    media_item_id TEXT NOT NULL,           -- TheDiscDB media item ID
    release_id TEXT NOT NULL,              -- TheDiscDB release ID
    media_title TEXT NOT NULL DEFAULT '',
    media_year TEXT NOT NULL DEFAULT '',
    media_type TEXT NOT NULL DEFAULT '',   -- Movie or Series
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS discdb_cache (
    cache_key TEXT PRIMARY KEY,
    response_json TEXT NOT NULL,
    expires_at DATETIME NOT NULL
);

CREATE TABLE IF NOT EXISTS settings (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_rip_jobs_status ON rip_jobs(status);
CREATE INDEX IF NOT EXISTS idx_rip_jobs_created ON rip_jobs(created_at);
CREATE INDEX IF NOT EXISTS idx_disc_mappings_disc_key ON disc_mappings(disc_key);
CREATE INDEX IF NOT EXISTS idx_discdb_cache_expires ON discdb_cache(expires_at);
```

- [ ] **Step 2: Create embed file**

```go
// migrations/embed.go
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
```

- [ ] **Step 3: Write failing test for DB initialization**

```go
// internal/db/db_test.go
package db

import (
	"testing"
)

func TestOpenCreatesSchema(t *testing.T) {
	dbPath := ":memory:"
	store, err := Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	defer store.Close()

	// Verify tables exist by querying them
	var count int
	err = store.db.QueryRow("SELECT count(*) FROM rip_jobs").Scan(&count)
	if err != nil {
		t.Fatalf("rip_jobs table should exist: %v", err)
	}
	err = store.db.QueryRow("SELECT count(*) FROM disc_mappings").Scan(&count)
	if err != nil {
		t.Fatalf("disc_mappings table should exist: %v", err)
	}
	err = store.db.QueryRow("SELECT count(*) FROM discdb_cache").Scan(&count)
	if err != nil {
		t.Fatalf("discdb_cache table should exist: %v", err)
	}
	err = store.db.QueryRow("SELECT count(*) FROM settings").Scan(&count)
	if err != nil {
		t.Fatalf("settings table should exist: %v", err)
	}
}
```

- [ ] **Step 4: Run test to verify it fails**

Run: `go get modernc.org/sqlite && go test ./internal/db/ -v -run TestOpenCreatesSchema`
Expected: FAIL — `Open` not defined

- [ ] **Step 5: Implement database initialization**

```go
// internal/db/db.go
package db

import (
	"database/sql"
	"fmt"
	"log/slog"

	"github.com/johnpostlethwait/bluforge/migrations"
	_ "modernc.org/sqlite"
)

type Store struct {
	db *sql.DB
}

func Open(dbPath string) (*Store, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	// Enable WAL mode for concurrent reads
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		return nil, fmt.Errorf("enable WAL: %w", err)
	}

	store := &Store{db: db}
	if err := store.migrate(); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}

	slog.Info("database initialized", "path", dbPath)
	return store, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) migrate() error {
	sqlBytes, err := migrations.FS.ReadFile("001_initial.sql")
	if err != nil {
		return fmt.Errorf("read migration: %w", err)
	}
	if _, err := s.db.Exec(string(sqlBytes)); err != nil {
		return fmt.Errorf("exec migration: %w", err)
	}
	return nil
}
```

- [ ] **Step 6: Run test to verify it passes**

Run: `go test ./internal/db/ -v -run TestOpenCreatesSchema`
Expected: PASS

- [ ] **Step 7: Write failing tests for job CRUD**

```go
// internal/db/jobs_test.go
package db

import (
	"testing"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	store, err := Open(":memory:")
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func TestCreateAndGetJob(t *testing.T) {
	store := testStore(t)

	job := RipJob{
		DriveIndex: 0,
		DiscName:   "DEADPOOL_2",
		TitleIndex: 0,
		TitleName:  "Deadpool 2",
		ContentType: "MainMovie",
		SizeBytes:  30520000000,
		Duration:   "1:59:30",
	}

	id, err := store.CreateJob(job)
	if err != nil {
		t.Fatalf("create job: %v", err)
	}
	if id == 0 {
		t.Fatal("expected non-zero id")
	}

	got, err := store.GetJob(id)
	if err != nil {
		t.Fatalf("get job: %v", err)
	}
	if got.DiscName != "DEADPOOL_2" {
		t.Errorf("expected DEADPOOL_2, got %s", got.DiscName)
	}
	if got.Status != "pending" {
		t.Errorf("expected pending, got %s", got.Status)
	}
}

func TestUpdateJobStatus(t *testing.T) {
	store := testStore(t)

	id, _ := store.CreateJob(RipJob{DriveIndex: 0, DiscName: "TEST", TitleIndex: 0})

	err := store.UpdateJobStatus(id, "ripping", 50, "")
	if err != nil {
		t.Fatalf("update job: %v", err)
	}

	got, _ := store.GetJob(id)
	if got.Status != "ripping" {
		t.Errorf("expected ripping, got %s", got.Status)
	}
	if got.Progress != 50 {
		t.Errorf("expected progress 50, got %d", got.Progress)
	}
}

func TestListJobsByStatus(t *testing.T) {
	store := testStore(t)

	store.CreateJob(RipJob{DriveIndex: 0, DiscName: "A", TitleIndex: 0})
	id2, _ := store.CreateJob(RipJob{DriveIndex: 1, DiscName: "B", TitleIndex: 0})
	store.UpdateJobStatus(id2, "ripping", 25, "")

	pending, err := store.ListJobsByStatus("pending")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(pending) != 1 {
		t.Errorf("expected 1 pending, got %d", len(pending))
	}

	ripping, _ := store.ListJobsByStatus("ripping")
	if len(ripping) != 1 {
		t.Errorf("expected 1 ripping, got %d", len(ripping))
	}
}
```

- [ ] **Step 8: Run tests to verify they fail**

Run: `go test ./internal/db/ -v -run TestCreate`
Expected: FAIL — `RipJob`, `CreateJob`, etc. not defined

- [ ] **Step 9: Implement job CRUD**

```go
// internal/db/jobs.go
package db

import (
	"fmt"
	"time"
)

type RipJob struct {
	ID           int64
	DriveIndex   int
	DiscName     string
	TitleIndex   int
	TitleName    string
	ContentType  string
	OutputPath   string
	Status       string // pending, ripping, organizing, completed, failed, skipped
	Progress     int
	ErrorMessage string
	SizeBytes    int64
	Duration     string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

func (s *Store) CreateJob(job RipJob) (int64, error) {
	result, err := s.db.Exec(
		`INSERT INTO rip_jobs (drive_index, disc_name, title_index, title_name, content_type, size_bytes, duration)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		job.DriveIndex, job.DiscName, job.TitleIndex, job.TitleName, job.ContentType, job.SizeBytes, job.Duration,
	)
	if err != nil {
		return 0, fmt.Errorf("insert job: %w", err)
	}
	return result.LastInsertId()
}

func (s *Store) GetJob(id int64) (*RipJob, error) {
	row := s.db.QueryRow(
		`SELECT id, drive_index, disc_name, title_index, title_name, content_type, output_path,
		        status, progress, error_message, size_bytes, duration, created_at, updated_at
		 FROM rip_jobs WHERE id = ?`, id,
	)
	return scanJob(row)
}

func (s *Store) UpdateJobStatus(id int64, status string, progress int, errMsg string) error {
	_, err := s.db.Exec(
		`UPDATE rip_jobs SET status = ?, progress = ?, error_message = ?, updated_at = CURRENT_TIMESTAMP
		 WHERE id = ?`,
		status, progress, errMsg, id,
	)
	return err
}

func (s *Store) UpdateJobOutput(id int64, outputPath string) error {
	_, err := s.db.Exec(
		`UPDATE rip_jobs SET output_path = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
		outputPath, id,
	)
	return err
}

func (s *Store) ListJobsByStatus(status string) ([]RipJob, error) {
	rows, err := s.db.Query(
		`SELECT id, drive_index, disc_name, title_index, title_name, content_type, output_path,
		        status, progress, error_message, size_bytes, duration, created_at, updated_at
		 FROM rip_jobs WHERE status = ? ORDER BY created_at DESC`, status,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanJobs(rows)
}

func (s *Store) ListAllJobs(limit, offset int) ([]RipJob, error) {
	rows, err := s.db.Query(
		`SELECT id, drive_index, disc_name, title_index, title_name, content_type, output_path,
		        status, progress, error_message, size_bytes, duration, created_at, updated_at
		 FROM rip_jobs ORDER BY created_at DESC LIMIT ? OFFSET ?`, limit, offset,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanJobs(rows)
}

type scanner interface {
	Scan(dest ...any) error
}

func scanJob(row scanner) (*RipJob, error) {
	var j RipJob
	err := row.Scan(
		&j.ID, &j.DriveIndex, &j.DiscName, &j.TitleIndex, &j.TitleName, &j.ContentType,
		&j.OutputPath, &j.Status, &j.Progress, &j.ErrorMessage, &j.SizeBytes, &j.Duration,
		&j.CreatedAt, &j.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &j, nil
}

func scanJobs(rows interface {
	Next() bool
	Scan(dest ...any) error
}) ([]RipJob, error) {
	var jobs []RipJob
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, *j)
	}
	return jobs, nil
}
```

- [ ] **Step 10: Run job tests to verify they pass**

Run: `go test ./internal/db/ -v -run "TestCreate|TestUpdate|TestList"`
Expected: All PASS

- [ ] **Step 11: Write failing tests for disc mappings**

```go
// internal/db/mappings_test.go
package db

import "testing"

func TestSaveAndGetMapping(t *testing.T) {
	store := testStore(t)

	mapping := DiscMapping{
		DiscKey:      "DEADPOOL_2_3_abc123",
		DiscName:     "DEADPOOL_2",
		MediaItemID:  "12345",
		ReleaseID:    "67890",
		MediaTitle:   "Deadpool 2",
		MediaYear:    "2018",
		MediaType:    "Movie",
	}

	err := store.SaveMapping(mapping)
	if err != nil {
		t.Fatalf("save mapping: %v", err)
	}

	got, err := store.GetMapping("DEADPOOL_2_3_abc123")
	if err != nil {
		t.Fatalf("get mapping: %v", err)
	}
	if got == nil {
		t.Fatal("expected mapping, got nil")
	}
	if got.MediaTitle != "Deadpool 2" {
		t.Errorf("expected Deadpool 2, got %s", got.MediaTitle)
	}
}

func TestGetMappingNotFound(t *testing.T) {
	store := testStore(t)

	got, err := store.GetMapping("nonexistent")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != nil {
		t.Error("expected nil for missing mapping")
	}
}

func TestDeleteMapping(t *testing.T) {
	store := testStore(t)

	store.SaveMapping(DiscMapping{DiscKey: "key1", DiscName: "D1", MediaItemID: "1", ReleaseID: "1"})

	err := store.DeleteMapping("key1")
	if err != nil {
		t.Fatalf("delete: %v", err)
	}

	got, _ := store.GetMapping("key1")
	if got != nil {
		t.Error("mapping should be deleted")
	}
}

func TestSaveMappingUpserts(t *testing.T) {
	store := testStore(t)

	store.SaveMapping(DiscMapping{DiscKey: "key1", DiscName: "D1", MediaItemID: "1", ReleaseID: "1", MediaTitle: "Old"})
	store.SaveMapping(DiscMapping{DiscKey: "key1", DiscName: "D1", MediaItemID: "2", ReleaseID: "2", MediaTitle: "New"})

	got, _ := store.GetMapping("key1")
	if got.MediaTitle != "New" {
		t.Errorf("expected upserted title 'New', got %s", got.MediaTitle)
	}
}
```

- [ ] **Step 12: Implement disc mappings CRUD**

```go
// internal/db/mappings.go
package db

import (
	"database/sql"
	"time"
)

type DiscMapping struct {
	ID          int64
	DiscKey     string
	DiscName    string
	MediaItemID string
	ReleaseID   string
	MediaTitle  string
	MediaYear   string
	MediaType   string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

func (s *Store) SaveMapping(m DiscMapping) error {
	_, err := s.db.Exec(
		`INSERT INTO disc_mappings (disc_key, disc_name, media_item_id, release_id, media_title, media_year, media_type)
		 VALUES (?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(disc_key) DO UPDATE SET
		   media_item_id = excluded.media_item_id,
		   release_id = excluded.release_id,
		   media_title = excluded.media_title,
		   media_year = excluded.media_year,
		   media_type = excluded.media_type,
		   updated_at = CURRENT_TIMESTAMP`,
		m.DiscKey, m.DiscName, m.MediaItemID, m.ReleaseID, m.MediaTitle, m.MediaYear, m.MediaType,
	)
	return err
}

func (s *Store) GetMapping(discKey string) (*DiscMapping, error) {
	row := s.db.QueryRow(
		`SELECT id, disc_key, disc_name, media_item_id, release_id, media_title, media_year, media_type, created_at, updated_at
		 FROM disc_mappings WHERE disc_key = ?`, discKey,
	)
	var m DiscMapping
	err := row.Scan(&m.ID, &m.DiscKey, &m.DiscName, &m.MediaItemID, &m.ReleaseID, &m.MediaTitle, &m.MediaYear, &m.MediaType, &m.CreatedAt, &m.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &m, nil
}

func (s *Store) DeleteMapping(discKey string) error {
	_, err := s.db.Exec(`DELETE FROM disc_mappings WHERE disc_key = ?`, discKey)
	return err
}

func (s *Store) ListMappings() ([]DiscMapping, error) {
	rows, err := s.db.Query(
		`SELECT id, disc_key, disc_name, media_item_id, release_id, media_title, media_year, media_type, created_at, updated_at
		 FROM disc_mappings ORDER BY updated_at DESC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var mappings []DiscMapping
	for rows.Next() {
		var m DiscMapping
		if err := rows.Scan(&m.ID, &m.DiscKey, &m.DiscName, &m.MediaItemID, &m.ReleaseID, &m.MediaTitle, &m.MediaYear, &m.MediaType, &m.CreatedAt, &m.UpdatedAt); err != nil {
			return nil, err
		}
		mappings = append(mappings, m)
	}
	return mappings, nil
}
```

- [ ] **Step 13: Run mapping tests to verify they pass**

Run: `go test ./internal/db/ -v -run "TestSaveAnd|TestGetMapping|TestDelete|TestSaveMapping"`
Expected: All PASS

- [ ] **Step 14: Write failing tests for settings persistence**

```go
// internal/db/settings_test.go
package db

import "testing"

func TestSetAndGetSetting(t *testing.T) {
	store := testStore(t)

	err := store.SetSetting("auto_rip", "true")
	if err != nil {
		t.Fatalf("set: %v", err)
	}

	val, err := store.GetSetting("auto_rip")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if val != "true" {
		t.Errorf("expected 'true', got %q", val)
	}
}

func TestGetSettingDefault(t *testing.T) {
	store := testStore(t)

	val, err := store.GetSettingDefault("nonexistent", "fallback")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if val != "fallback" {
		t.Errorf("expected 'fallback', got %q", val)
	}
}

func TestSetSettingUpserts(t *testing.T) {
	store := testStore(t)

	store.SetSetting("key", "old")
	store.SetSetting("key", "new")

	val, _ := store.GetSettingDefault("key", "")
	if val != "new" {
		t.Errorf("expected 'new', got %q", val)
	}
}
```

- [ ] **Step 15: Implement settings persistence**

```go
// internal/db/settings.go
package db

import "database/sql"

func (s *Store) SetSetting(key, value string) error {
	_, err := s.db.Exec(
		`INSERT INTO settings (key, value) VALUES (?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		key, value,
	)
	return err
}

func (s *Store) GetSetting(key string) (string, error) {
	var value string
	err := s.db.QueryRow(`SELECT value FROM settings WHERE key = ?`, key).Scan(&value)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return value, err
}

func (s *Store) GetSettingDefault(key, fallback string) (string, error) {
	val, err := s.GetSetting(key)
	if err != nil {
		return fallback, err
	}
	if val == "" {
		return fallback, nil
	}
	return val, nil
}
```

- [ ] **Step 16: Run all DB tests**

Run: `go test ./internal/db/ -v`
Expected: All PASS

- [ ] **Step 17: Commit**

```bash
git add migrations/ internal/db/
git commit -m "feat: SQLite database layer with jobs, disc mappings, settings, and cache tables"
```

---

### Task 8: Drive State Machine

**Files:**
- Create: `internal/drivemanager/state.go`
- Create: `internal/drivemanager/state_test.go`

- [ ] **Step 1: Write failing tests for state transitions**

```go
// internal/drivemanager/state_test.go
package drivemanager

import "testing"

func TestValidTransitions(t *testing.T) {
	cases := []struct {
		from DriveState
		to   DriveState
		ok   bool
	}{
		{StateEmpty, StateDetected, true},
		{StateDetected, StateScanning, true},
		{StateScanning, StateIdentified, true},
		{StateScanning, StateNotFound, true},
		{StateIdentified, StateReady, true},
		{StateReady, StateRipping, true},
		{StateRipping, StateOrganizing, true},
		{StateOrganizing, StateComplete, true},
		{StateComplete, StateEjecting, true},
		{StateEjecting, StateEmpty, true},
		{StateNotFound, StateRipping, true},
		// Invalid transitions
		{StateEmpty, StateRipping, false},
		{StateRipping, StateEmpty, false},
		{StateComplete, StateRipping, false},
	}

	for _, tc := range cases {
		result := IsValidTransition(tc.from, tc.to)
		if result != tc.ok {
			t.Errorf("transition %s -> %s: expected %v, got %v", tc.from, tc.to, tc.ok, result)
		}
	}
}

func TestDriveStateTransition(t *testing.T) {
	d := NewDriveState(0, "/dev/sr0")

	if d.State() != StateEmpty {
		t.Errorf("expected Empty, got %s", d.State())
	}

	err := d.TransitionTo(StateDetected)
	if err != nil {
		t.Fatalf("transition failed: %v", err)
	}
	if d.State() != StateDetected {
		t.Errorf("expected Detected, got %s", d.State())
	}

	err = d.TransitionTo(StateRipping)
	if err == nil {
		t.Error("expected error for invalid transition Detected -> Ripping")
	}
}

func TestForceReset(t *testing.T) {
	d := NewDriveState(0, "/dev/sr0")
	d.TransitionTo(StateDetected)
	d.TransitionTo(StateScanning)

	d.ForceReset()
	if d.State() != StateEmpty {
		t.Errorf("expected Empty after reset, got %s", d.State())
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/drivemanager/ -v`
Expected: FAIL — types not defined

- [ ] **Step 3: Implement drive state machine**

```go
// internal/drivemanager/state.go
package drivemanager

import (
	"fmt"
	"sync"
)

type DriveState string

const (
	StateEmpty      DriveState = "empty"
	StateDetected   DriveState = "detected"
	StateScanning   DriveState = "scanning"
	StateIdentified DriveState = "identified"
	StateNotFound   DriveState = "not_found"
	StateReady      DriveState = "ready"
	StateRipping    DriveState = "ripping"
	StateOrganizing DriveState = "organizing"
	StateComplete   DriveState = "complete"
	StateEjecting   DriveState = "ejecting"
)

// validTransitions defines the allowed state machine transitions.
var validTransitions = map[DriveState][]DriveState{
	StateEmpty:      {StateDetected},
	StateDetected:   {StateScanning},
	StateScanning:   {StateIdentified, StateNotFound},
	StateIdentified: {StateReady},
	StateNotFound:   {StateRipping}, // rip with generic names
	StateReady:      {StateRipping},
	StateRipping:    {StateOrganizing},
	StateOrganizing: {StateComplete},
	StateComplete:   {StateEjecting},
	StateEjecting:   {StateEmpty},
}

func IsValidTransition(from, to DriveState) bool {
	for _, allowed := range validTransitions[from] {
		if allowed == to {
			return true
		}
	}
	return false
}

// DriveStateMachine tracks the state of a single optical drive.
type DriveStateMachine struct {
	mu          sync.RWMutex
	index       int
	devicePath  string
	state       DriveState
	discName    string
}

func NewDriveState(index int, devicePath string) *DriveStateMachine {
	return &DriveStateMachine{
		index:      index,
		devicePath: devicePath,
		state:      StateEmpty,
	}
}

func (d *DriveStateMachine) State() DriveState {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.state
}

func (d *DriveStateMachine) Index() int {
	return d.index
}

func (d *DriveStateMachine) DevicePath() string {
	return d.devicePath
}

func (d *DriveStateMachine) DiscName() string {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.discName
}

func (d *DriveStateMachine) SetDiscName(name string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.discName = name
}

func (d *DriveStateMachine) TransitionTo(newState DriveState) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if !IsValidTransition(d.state, newState) {
		return fmt.Errorf("invalid transition: %s -> %s", d.state, newState)
	}

	d.state = newState
	if newState == StateEmpty {
		d.discName = ""
	}
	return nil
}

// ForceReset forces the drive back to Empty state (for error recovery or disconnect).
func (d *DriveStateMachine) ForceReset() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.state = StateEmpty
	d.discName = ""
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/drivemanager/ -v`
Expected: All PASS

- [ ] **Step 5: Commit**

```bash
git add internal/drivemanager/
git commit -m "feat: drive state machine with validated transitions"
```

---

### Task 9: Drive Manager (Polling & Event Emission)

**Files:**
- Create: `internal/drivemanager/manager.go`
- Create: `internal/drivemanager/manager_test.go`

- [ ] **Step 1: Write failing tests**

```go
// internal/drivemanager/manager_test.go
package drivemanager

import (
	"context"
	"testing"
	"time"

	"github.com/johnpostlethwait/bluforge/internal/makemkv"
)

type mockExecutor struct {
	drives []makemkv.DriveInfo
}

func (m *mockExecutor) ListDrives(ctx context.Context) ([]makemkv.DriveInfo, error) {
	return m.drives, nil
}

func (m *mockExecutor) ScanDisc(ctx context.Context, driveIndex int) (*makemkv.DiscScan, error) {
	return &makemkv.DiscScan{DriveIndex: driveIndex, DiscName: "TEST_DISC", TitleCount: 1}, nil
}

func TestManagerDetectsDiscInsert(t *testing.T) {
	mock := &mockExecutor{
		drives: []makemkv.DriveInfo{
			{Index: 0, Visible: 2, Enabled: 999, Flags: 1, DriveName: "Drive0", DiscName: "MOVIE_DISC"},
		},
	}

	events := make(chan DriveEvent, 10)
	mgr := NewManager(mock, func(ev DriveEvent) {
		events <- ev
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	mgr.PollOnce(ctx)

	select {
	case ev := <-events:
		if ev.Type != EventDiscInserted {
			t.Errorf("expected EventDiscInserted, got %s", ev.Type)
		}
		if ev.DriveIndex != 0 {
			t.Errorf("expected drive 0, got %d", ev.DriveIndex)
		}
		if ev.DiscName != "MOVIE_DISC" {
			t.Errorf("expected MOVIE_DISC, got %s", ev.DiscName)
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for insert event")
	}
}

func TestManagerDetectsDiscEject(t *testing.T) {
	mock := &mockExecutor{
		drives: []makemkv.DriveInfo{
			{Index: 0, Visible: 2, Enabled: 999, Flags: 1, DriveName: "Drive0", DiscName: "MOVIE_DISC"},
		},
	}

	events := make(chan DriveEvent, 10)
	mgr := NewManager(mock, func(ev DriveEvent) {
		events <- ev
	})

	ctx := context.Background()

	// First poll: detect disc
	mgr.PollOnce(ctx)
	<-events // consume insert event

	// Remove disc
	mock.drives[0].DiscName = ""
	mock.drives[0].Flags = 0

	// Second poll: detect eject
	mgr.PollOnce(ctx)

	select {
	case ev := <-events:
		if ev.Type != EventDiscEjected {
			t.Errorf("expected EventDiscEjected, got %s", ev.Type)
		}
	default:
		t.Fatal("expected eject event")
	}
}

func TestManagerMultipleDrives(t *testing.T) {
	mock := &mockExecutor{
		drives: []makemkv.DriveInfo{
			{Index: 0, Visible: 2, Enabled: 999, Flags: 1, DriveName: "Drive0", DiscName: "DISC_A"},
			{Index: 1, Visible: 2, Enabled: 999, Flags: 1, DriveName: "Drive1", DiscName: "DISC_B"},
		},
	}

	events := make(chan DriveEvent, 10)
	mgr := NewManager(mock, func(ev DriveEvent) {
		events <- ev
	})

	mgr.PollOnce(context.Background())

	// Should get two insert events
	var insertCount int
	for i := 0; i < 2; i++ {
		select {
		case ev := <-events:
			if ev.Type == EventDiscInserted {
				insertCount++
			}
		default:
			break
		}
	}
	if insertCount != 2 {
		t.Errorf("expected 2 insert events, got %d", insertCount)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/drivemanager/ -v -run TestManager`
Expected: FAIL — `DriveEvent`, `NewManager`, etc. not defined

- [ ] **Step 3: Implement drive manager**

```go
// internal/drivemanager/manager.go
package drivemanager

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/johnpostlethwait/bluforge/internal/makemkv"
)

type EventType string

const (
	EventDiscInserted    EventType = "disc_inserted"
	EventDiscEjected     EventType = "disc_ejected"
	EventDriveDisconnect EventType = "drive_disconnected"
	EventStateChange     EventType = "state_changed"
)

type DriveEvent struct {
	Type       EventType
	DriveIndex int
	DiscName   string
	State      DriveState
}

// DriveExecutor is the subset of makemkv.Executor that Manager needs.
type DriveExecutor interface {
	ListDrives(ctx context.Context) ([]makemkv.DriveInfo, error)
	ScanDisc(ctx context.Context, driveIndex int) (*makemkv.DiscScan, error)
}

type Manager struct {
	mu       sync.RWMutex
	executor DriveExecutor
	drives   map[int]*DriveStateMachine
	known    map[int]string // driveIndex -> last known disc name
	onEvent  func(DriveEvent)
}

func NewManager(executor DriveExecutor, onEvent func(DriveEvent)) *Manager {
	return &Manager{
		executor: executor,
		drives:   make(map[int]*DriveStateMachine),
		known:    make(map[int]string),
		onEvent:  onEvent,
	}
}

// PollOnce runs a single drive enumeration and emits events for changes.
func (m *Manager) PollOnce(ctx context.Context) {
	drives, err := m.executor.ListDrives(ctx)
	if err != nil {
		slog.Error("failed to list drives", "error", err)
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	seen := make(map[int]bool)

	for _, d := range drives {
		seen[d.Index] = true
		hasDisc := d.DiscName != "" && d.Flags > 0

		prevDisc, existed := m.known[d.Index]

		// Ensure drive state machine exists
		if _, ok := m.drives[d.Index]; !ok {
			m.drives[d.Index] = NewDriveState(d.Index, d.DriveName)
		}

		if hasDisc && (!existed || prevDisc == "") {
			// Disc inserted
			m.known[d.Index] = d.DiscName
			m.drives[d.Index].SetDiscName(d.DiscName)
			if m.drives[d.Index].State() == StateEmpty {
				m.drives[d.Index].TransitionTo(StateDetected)
			}
			m.emit(DriveEvent{
				Type:       EventDiscInserted,
				DriveIndex: d.Index,
				DiscName:   d.DiscName,
				State:      m.drives[d.Index].State(),
			})
		} else if !hasDisc && existed && prevDisc != "" {
			// Disc ejected
			m.known[d.Index] = ""
			m.drives[d.Index].ForceReset()
			m.emit(DriveEvent{
				Type:       EventDiscEjected,
				DriveIndex: d.Index,
				State:      StateEmpty,
			})
		}
	}

	// Check for drives that disappeared
	for idx := range m.known {
		if !seen[idx] {
			m.drives[idx].ForceReset()
			delete(m.known, idx)
			m.emit(DriveEvent{
				Type:       EventDriveDisconnect,
				DriveIndex: idx,
				State:      StateEmpty,
			})
		}
	}
}

// Run starts the polling loop. Blocks until ctx is cancelled.
func (m *Manager) Run(ctx context.Context, interval time.Duration) {
	slog.Info("drive manager started", "interval", interval)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// Initial poll
	m.PollOnce(ctx)

	for {
		select {
		case <-ctx.Done():
			slog.Info("drive manager stopped")
			return
		case <-ticker.C:
			m.PollOnce(ctx)
		}
	}
}

// GetDrive returns the state machine for a specific drive.
func (m *Manager) GetDrive(index int) *DriveStateMachine {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.drives[index]
}

// GetAllDrives returns a snapshot of all known drives.
func (m *Manager) GetAllDrives() []*DriveStateMachine {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var result []*DriveStateMachine
	for _, d := range m.drives {
		result = append(result, d)
	}
	return result
}

func (m *Manager) emit(ev DriveEvent) {
	if m.onEvent != nil {
		m.onEvent(ev)
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/drivemanager/ -v`
Expected: All PASS

- [ ] **Step 5: Commit**

```bash
git add internal/drivemanager/manager.go internal/drivemanager/manager_test.go
git commit -m "feat: drive manager with polling, event emission, and multi-drive support"
```

---

### Task 10: TheDiscDB GraphQL Client

**Files:**
- Create: `internal/discdb/types.go`
- Create: `internal/discdb/client.go`
- Create: `internal/discdb/client_test.go`

- [ ] **Step 1: Create TheDiscDB types**

```go
// internal/discdb/types.go
package discdb

type MediaItem struct {
	ID             string
	Title          string
	Slug           string
	Year           int
	Type           string // "Movie" or "Series"
	RuntimeMinutes int
	ImageURL       string
	ExternalIDs    ExternalIDs
	Releases       []Release
}

type ExternalIDs struct {
	IMDB string
	TMDB string
	TVDB string
}

type Release struct {
	ID         string
	Title      string
	Slug       string
	UPC        string
	ASIN       string
	Year       int
	RegionCode string
	Locale     string
	ImageURL   string
	Discs      []Disc
}

type Disc struct {
	ID     string
	Index  int
	Name   string
	Format string // "Blu-Ray", "DVD", "4K UHD"
	Slug   string
	Titles []DiscTitle
}

type DiscTitle struct {
	ID         string
	Index      int
	SourceFile string // e.g., "00001.mpls"
	ItemType   string // MainMovie, Episode, Extra, Trailer, DeletedScene
	HasItem    bool
	Duration   string
	Size       string
	SegmentMap string
	Season     int
	Episode    int
	Item       *ContentItem
}

type ContentItem struct {
	Title   string
	Season  int
	Episode int
	Type    string
}

// SearchResult wraps a media item with its matched release for display.
type SearchResult struct {
	MediaItem MediaItem
	Release   Release
	Disc      Disc
}
```

- [ ] **Step 2: Write failing test for client search**

```go
// internal/discdb/client_test.go
package discdb

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSearchByTitle(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/graphql/" {
			t.Errorf("expected /graphql/, got %s", r.URL.Path)
		}

		response := map[string]any{
			"data": map[string]any{
				"mediaItems": map[string]any{
					"nodes": []map[string]any{
						{
							"id":    "1",
							"title": "Deadpool 2",
							"slug":  "deadpool-2-2018",
							"year":  2018,
							"type":  "Movie",
							"externalids": map[string]any{
								"imdb": "tt5463162",
								"tmdb": "383498",
							},
							"releases": []map[string]any{
								{
									"id":         "r1",
									"title":      "Standard Blu-ray",
									"upc":        "024543547853",
									"asin":       "B07D5NQ3GN",
									"regionCode": "A",
									"discs": []map[string]any{
										{
											"id":     "d1",
											"index":  0,
											"name":   "Disc 1",
											"format": "Blu-Ray",
											"titles": []map[string]any{
												{
													"id":         "dt1",
													"index":      0,
													"sourceFile": "00001.mpls",
													"itemType":   "MainMovie",
													"duration":   "1:59:30",
													"segmentMap": "1,2,3",
												},
											},
										},
									},
								},
							},
						},
					},
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	client := NewClient(WithBaseURL(server.URL + "/graphql/"))

	results, err := client.SearchByTitle(context.Background(), "Deadpool 2")
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Title != "Deadpool 2" {
		t.Errorf("expected Deadpool 2, got %s", results[0].Title)
	}
	if len(results[0].Releases) != 1 {
		t.Fatalf("expected 1 release, got %d", len(results[0].Releases))
	}
	if results[0].Releases[0].UPC != "024543547853" {
		t.Errorf("unexpected UPC: %s", results[0].Releases[0].UPC)
	}
	disc := results[0].Releases[0].Discs[0]
	if disc.Titles[0].SourceFile != "00001.mpls" {
		t.Errorf("expected 00001.mpls, got %s", disc.Titles[0].SourceFile)
	}
}

func TestSearchByUPC(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response := map[string]any{
			"data": map[string]any{
				"mediaItems": map[string]any{
					"nodes": []map[string]any{
						{
							"id":    "1",
							"title": "Deadpool 2",
							"slug":  "deadpool-2-2018",
							"year":  2018,
							"type":  "Movie",
							"releases": []map[string]any{
								{
									"id":  "r1",
									"upc": "024543547853",
									"discs": []map[string]any{},
								},
							},
						},
					},
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	client := NewClient(WithBaseURL(server.URL + "/graphql/"))

	results, err := client.SearchByUPC(context.Background(), "024543547853")
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
}
```

- [ ] **Step 3: Run tests to verify they fail**

Run: `go test ./internal/discdb/ -v -run TestSearch`
Expected: FAIL — `NewClient`, `WithBaseURL`, etc. not defined

- [ ] **Step 4: Implement GraphQL client**

```go
// internal/discdb/client.go
package discdb

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"
)

const defaultBaseURL = "https://thediscdb.com/graphql/"

type Client struct {
	baseURL    string
	httpClient *http.Client
}

type ClientOption func(*Client)

func WithBaseURL(url string) ClientOption {
	return func(c *Client) { c.baseURL = url }
}

func NewClient(opts ...ClientOption) *Client {
	c := &Client{
		baseURL: defaultBaseURL,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

type graphqlRequest struct {
	Query     string         `json:"query"`
	Variables map[string]any `json:"variables,omitempty"`
}

type graphqlResponse struct {
	Data   json.RawMessage `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

func (c *Client) query(ctx context.Context, gql string, vars map[string]any) (json.RawMessage, error) {
	body, err := json.Marshal(graphqlRequest{Query: gql, Variables: vars})
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", c.baseURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("execute request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	var gqlResp graphqlResponse
	if err := json.Unmarshal(respBody, &gqlResp); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}

	if len(gqlResp.Errors) > 0 {
		slog.Error("graphql errors", "errors", gqlResp.Errors)
		return nil, fmt.Errorf("graphql error: %s", gqlResp.Errors[0].Message)
	}

	return gqlResp.Data, nil
}

const searchByTitleQuery = `
query SearchByTitle($title: String!) {
  mediaItems(where: { title: { contains: $title } }, first: 20) {
    nodes {
      id title slug year type runtimeMinutes imageUrl
      externalids { imdb tmdb tvdb }
      releases {
        id title slug upc asin isbn year regionCode locale imageUrl
        discs {
          id index name format slug
          titles {
            id index sourceFile itemType hasItem duration size segmentMap season episode
            item { title season episode type }
          }
        }
      }
    }
  }
}`

const searchByUPCQuery = `
query SearchByUPC($upc: String!) {
  mediaItems(where: { releases: { some: { upc: { eq: $upc } } } }, first: 10) {
    nodes {
      id title slug year type runtimeMinutes imageUrl
      externalids { imdb tmdb tvdb }
      releases {
        id title slug upc asin isbn year regionCode locale imageUrl
        discs {
          id index name format slug
          titles {
            id index sourceFile itemType hasItem duration size segmentMap season episode
            item { title season episode type }
          }
        }
      }
    }
  }
}`

const searchByASINQuery = `
query SearchByASIN($asin: String!) {
  mediaItems(where: { releases: { some: { asin: { eq: $asin } } } }, first: 10) {
    nodes {
      id title slug year type runtimeMinutes imageUrl
      externalids { imdb tmdb tvdb }
      releases {
        id title slug upc asin isbn year regionCode locale imageUrl
        discs {
          id index name format slug
          titles {
            id index sourceFile itemType hasItem duration size segmentMap season episode
            item { title season episode type }
          }
        }
      }
    }
  }
}`

func (c *Client) SearchByTitle(ctx context.Context, title string) ([]MediaItem, error) {
	return c.searchMediaItems(ctx, searchByTitleQuery, map[string]any{"title": title})
}

func (c *Client) SearchByUPC(ctx context.Context, upc string) ([]MediaItem, error) {
	return c.searchMediaItems(ctx, searchByUPCQuery, map[string]any{"upc": upc})
}

func (c *Client) SearchByASIN(ctx context.Context, asin string) ([]MediaItem, error) {
	return c.searchMediaItems(ctx, searchByASINQuery, map[string]any{"asin": asin})
}

func (c *Client) searchMediaItems(ctx context.Context, query string, vars map[string]any) ([]MediaItem, error) {
	data, err := c.query(ctx, query, vars)
	if err != nil {
		return nil, err
	}

	var result struct {
		MediaItems struct {
			Nodes []MediaItem `json:"nodes"`
		} `json:"mediaItems"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("unmarshal media items: %w", err)
	}

	return result.MediaItems.Nodes, nil
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/discdb/ -v -run TestSearch`
Expected: All PASS

- [ ] **Step 6: Commit**

```bash
git add internal/discdb/types.go internal/discdb/client.go internal/discdb/client_test.go
git commit -m "feat: TheDiscDB GraphQL client with search by title, UPC, and ASIN"
```

---

### Task 11: TheDiscDB Matcher (Scan Results -> Content Mapping)

**Files:**
- Create: `internal/discdb/matcher.go`
- Create: `internal/discdb/matcher_test.go`

- [ ] **Step 1: Write failing tests**

```go
// internal/discdb/matcher_test.go
package discdb

import (
	"testing"

	"github.com/johnpostlethwait/bluforge/internal/makemkv"
)

func TestMatchBySourceFile(t *testing.T) {
	scan := &makemkv.DiscScan{
		DiscName:   "DEADPOOL_2",
		TitleCount: 2,
		Titles: []makemkv.TitleInfo{
			{Index: 0, Attributes: map[int]string{33: "00001.mpls", 9: "1:59:30", 11: "30520000000"}},
			{Index: 1, Attributes: map[int]string{33: "00010.mpls", 9: "0:02:15", 11: "215000000"}},
		},
	}

	disc := Disc{
		Titles: []DiscTitle{
			{Index: 0, SourceFile: "00001.mpls", ItemType: "MainMovie", Item: &ContentItem{Title: "Deadpool 2"}},
			{Index: 1, SourceFile: "00010.mpls", ItemType: "Extra", Item: &ContentItem{Title: "Gag Reel"}},
		},
	}

	matches := MatchTitles(scan, disc)

	if len(matches) != 2 {
		t.Fatalf("expected 2 matches, got %d", len(matches))
	}
	if matches[0].ContentType != "MainMovie" {
		t.Errorf("expected MainMovie, got %s", matches[0].ContentType)
	}
	if matches[0].ContentTitle != "Deadpool 2" {
		t.Errorf("expected Deadpool 2, got %s", matches[0].ContentTitle)
	}
	if matches[1].ContentType != "Extra" {
		t.Errorf("expected Extra, got %s", matches[1].ContentType)
	}
}

func TestMatchNoSourceFileMatch(t *testing.T) {
	scan := &makemkv.DiscScan{
		DiscName:   "MYSTERY_DISC",
		TitleCount: 1,
		Titles: []makemkv.TitleInfo{
			{Index: 0, Attributes: map[int]string{33: "99999.mpls", 9: "1:30:00"}},
		},
	}

	disc := Disc{
		Titles: []DiscTitle{
			{Index: 0, SourceFile: "00001.mpls", ItemType: "MainMovie"},
		},
	}

	matches := MatchTitles(scan, disc)

	if len(matches) != 1 {
		t.Fatalf("expected 1 match entry, got %d", len(matches))
	}
	if matches[0].Matched {
		t.Error("expected unmatched")
	}
}

func TestScoreReleaseMatch(t *testing.T) {
	scan := &makemkv.DiscScan{
		DiscName:   "DEADPOOL_2",
		TitleCount: 2,
		Titles: []makemkv.TitleInfo{
			{Index: 0, Attributes: map[int]string{33: "00001.mpls"}},
			{Index: 1, Attributes: map[int]string{33: "00010.mpls"}},
		},
	}

	goodRelease := Release{
		UPC: "024543547853",
		Discs: []Disc{
			{
				Titles: []DiscTitle{
					{SourceFile: "00001.mpls"},
					{SourceFile: "00010.mpls"},
				},
			},
		},
	}

	badRelease := Release{
		UPC: "999999999999",
		Discs: []Disc{
			{
				Titles: []DiscTitle{
					{SourceFile: "00099.mpls"},
				},
			},
		},
	}

	goodScore := ScoreRelease(scan, goodRelease)
	badScore := ScoreRelease(scan, badRelease)

	if goodScore <= badScore {
		t.Errorf("good release (%d) should score higher than bad release (%d)", goodScore, badScore)
	}
}

func TestBuildDiscKey(t *testing.T) {
	scan := &makemkv.DiscScan{
		DiscName:   "DEADPOOL_2",
		TitleCount: 2,
		Titles: []makemkv.TitleInfo{
			{Index: 0, Attributes: map[int]string{33: "00001.mpls"}},
			{Index: 1, Attributes: map[int]string{33: "00010.mpls"}},
		},
	}

	key := BuildDiscKey(scan)
	if key == "" {
		t.Error("expected non-empty disc key")
	}

	// Same scan should produce same key
	key2 := BuildDiscKey(scan)
	if key != key2 {
		t.Error("same scan should produce same key")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/discdb/ -v -run "TestMatch|TestScore|TestBuildDiscKey"`
Expected: FAIL — `MatchTitles`, `ScoreRelease`, `BuildDiscKey` not defined

- [ ] **Step 3: Implement matcher**

```go
// internal/discdb/matcher.go
package discdb

import (
	"crypto/sha256"
	"fmt"
	"sort"
	"strings"

	"github.com/johnpostlethwait/bluforge/internal/makemkv"
)

// ContentMatch is the result of matching a MakeMKV title to TheDiscDB content.
type ContentMatch struct {
	TitleIndex   int
	SourceFile   string
	Matched      bool
	ContentType  string // MainMovie, Episode, Extra, Trailer, DeletedScene
	ContentTitle string
	Season       int
	Episode      int
}

// MatchTitles matches MakeMKV scan titles to TheDiscDB disc titles by source file.
func MatchTitles(scan *makemkv.DiscScan, disc Disc) []ContentMatch {
	// Build lookup from source file to disc title
	lookup := make(map[string]DiscTitle)
	for _, dt := range disc.Titles {
		lookup[dt.SourceFile] = dt
	}

	var matches []ContentMatch
	for _, title := range scan.Titles {
		sourceFile := title.SourceFile()
		match := ContentMatch{
			TitleIndex: title.Index,
			SourceFile: sourceFile,
		}

		if dt, ok := lookup[sourceFile]; ok {
			match.Matched = true
			match.ContentType = dt.ItemType
			if dt.Item != nil {
				match.ContentTitle = dt.Item.Title
				match.Season = dt.Item.Season
				match.Episode = dt.Item.Episode
			}
			match.Season = dt.Season
			match.Episode = dt.Episode
		}

		matches = append(matches, match)
	}

	return matches
}

// ScoreRelease scores how well a release matches a scan based on source file overlap.
func ScoreRelease(scan *makemkv.DiscScan, release Release) int {
	scanFiles := make(map[string]bool)
	for _, t := range scan.Titles {
		if sf := t.SourceFile(); sf != "" {
			scanFiles[sf] = true
		}
	}

	score := 0
	totalDiscFiles := 0

	for _, disc := range release.Discs {
		for _, dt := range disc.Titles {
			totalDiscFiles++
			if scanFiles[dt.SourceFile] {
				score += 10 // 10 points per matching source file
			}
		}
	}

	// Bonus for title count match
	if scan.TitleCount == totalDiscFiles {
		score += 5
	}

	return score
}

// BestRelease finds the best matching release from a list of media items.
// Returns the best match and its score, or nil if no matches.
func BestRelease(scan *makemkv.DiscScan, items []MediaItem) (*SearchResult, int) {
	var best *SearchResult
	bestScore := 0

	for _, item := range items {
		for _, release := range item.Releases {
			for _, disc := range release.Discs {
				score := ScoreRelease(scan, release)
				if score > bestScore {
					bestScore = score
					result := SearchResult{
						MediaItem: item,
						Release:   release,
						Disc:      disc,
					}
					best = &result
				}
			}
		}
	}

	return best, bestScore
}

// BuildDiscKey creates a unique key for a scanned disc based on name, title count, and source files.
func BuildDiscKey(scan *makemkv.DiscScan) string {
	var sourceFiles []string
	for _, t := range scan.Titles {
		if sf := t.SourceFile(); sf != "" {
			sourceFiles = append(sourceFiles, sf)
		}
	}
	sort.Strings(sourceFiles)

	input := fmt.Sprintf("%s|%d|%s", scan.DiscName, scan.TitleCount, strings.Join(sourceFiles, ","))
	hash := sha256.Sum256([]byte(input))
	return fmt.Sprintf("%x", hash[:16])
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/discdb/ -v`
Expected: All PASS

- [ ] **Step 5: Commit**

```bash
git add internal/discdb/matcher.go internal/discdb/matcher_test.go
git commit -m "feat: TheDiscDB matcher with source file matching, release scoring, and disc key generation"
```

---

### Task 12: TheDiscDB Cache

**Files:**
- Create: `internal/discdb/cache.go`
- Create: `internal/discdb/cache_test.go`

- [ ] **Step 1: Write failing tests**

```go
// internal/discdb/cache_test.go
package discdb

import (
	"testing"
	"time"

	"github.com/johnpostlethwait/bluforge/internal/db"
)

func testStore(t *testing.T) *db.Store {
	t.Helper()
	store, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func TestCacheSetAndGet(t *testing.T) {
	store := testStore(t)
	cache := NewCache(store, 1*time.Hour)

	err := cache.Set("key1", []byte(`{"test": true}`))
	if err != nil {
		t.Fatalf("set: %v", err)
	}

	data, err := cache.Get("key1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if string(data) != `{"test": true}` {
		t.Errorf("unexpected data: %s", string(data))
	}
}

func TestCacheExpiry(t *testing.T) {
	store := testStore(t)
	cache := NewCache(store, 1*time.Millisecond)

	cache.Set("key1", []byte(`data`))
	time.Sleep(5 * time.Millisecond)

	data, err := cache.Get("key1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if data != nil {
		t.Error("expected nil for expired entry")
	}
}

func TestCacheMiss(t *testing.T) {
	store := testStore(t)
	cache := NewCache(store, 1*time.Hour)

	data, err := cache.Get("nonexistent")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if data != nil {
		t.Error("expected nil for missing key")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/discdb/ -v -run TestCache`
Expected: FAIL — `NewCache` not defined

- [ ] **Step 3: Implement cache**

```go
// internal/discdb/cache.go
package discdb

import (
	"database/sql"
	"time"

	"github.com/johnpostlethwait/bluforge/internal/db"
)

type Cache struct {
	store *db.Store
	ttl   time.Duration
}

func NewCache(store *db.Store, ttl time.Duration) *Cache {
	return &Cache{store: store, ttl: ttl}
}

func (c *Cache) Get(key string) ([]byte, error) {
	var data string
	var expiresAt time.Time

	err := c.store.QueryRow(
		`SELECT response_json, expires_at FROM discdb_cache WHERE cache_key = ?`, key,
	).Scan(&data, &expiresAt)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	if time.Now().After(expiresAt) {
		// Expired — clean up
		c.store.Exec(`DELETE FROM discdb_cache WHERE cache_key = ?`, key)
		return nil, nil
	}

	return []byte(data), nil
}

func (c *Cache) Set(key string, data []byte) error {
	expiresAt := time.Now().Add(c.ttl)
	_, err := c.store.Exec(
		`INSERT INTO discdb_cache (cache_key, response_json, expires_at)
		 VALUES (?, ?, ?)
		 ON CONFLICT(cache_key) DO UPDATE SET
		   response_json = excluded.response_json,
		   expires_at = excluded.expires_at`,
		key, string(data), expiresAt,
	)
	return err
}
```

- [ ] **Step 4: Expose QueryRow and Exec on Store**

The cache needs direct SQL access. Add these methods to `internal/db/db.go`:

```go
// Add to internal/db/db.go

func (s *Store) QueryRow(query string, args ...any) *sql.Row {
	return s.db.QueryRow(query, args...)
}

func (s *Store) Exec(query string, args ...any) (sql.Result, error) {
	return s.db.Exec(query, args...)
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/discdb/ -v -run TestCache`
Expected: All PASS

- [ ] **Step 6: Commit**

```bash
git add internal/discdb/cache.go internal/discdb/cache_test.go internal/db/db.go
git commit -m "feat: SQLite-backed TheDiscDB response cache with TTL"
```

---

### Task 13: Rip Engine

**Files:**
- Create: `internal/ripper/job.go`
- Create: `internal/ripper/job_test.go`
- Create: `internal/ripper/engine.go`
- Create: `internal/ripper/engine_test.go`

- [ ] **Step 1: Write job status tests**

```go
// internal/ripper/job_test.go
package ripper

import "testing"

func TestJobStatusTransitions(t *testing.T) {
	j := NewJob(0, 0, "DEADPOOL_2", "/tmp/rip")
	if j.Status != StatusPending {
		t.Errorf("expected pending, got %s", j.Status)
	}

	j.Start()
	if j.Status != StatusRipping {
		t.Errorf("expected ripping, got %s", j.Status)
	}

	j.UpdateProgress(50)
	if j.Progress != 50 {
		t.Errorf("expected 50, got %d", j.Progress)
	}

	j.Complete("/output/Deadpool 2 (2018).mkv")
	if j.Status != StatusCompleted {
		t.Errorf("expected completed, got %s", j.Status)
	}
	if j.OutputPath != "/output/Deadpool 2 (2018).mkv" {
		t.Errorf("unexpected output path: %s", j.OutputPath)
	}
}

func TestJobFail(t *testing.T) {
	j := NewJob(0, 0, "TEST", "/tmp")
	j.Start()
	j.Fail("disc read error")

	if j.Status != StatusFailed {
		t.Errorf("expected failed, got %s", j.Status)
	}
	if j.Error != "disc read error" {
		t.Errorf("unexpected error: %s", j.Error)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/ripper/ -v -run TestJob`
Expected: FAIL — `NewJob` not defined

- [ ] **Step 3: Implement job**

```go
// internal/ripper/job.go
package ripper

import "time"

type JobStatus string

const (
	StatusPending    JobStatus = "pending"
	StatusRipping    JobStatus = "ripping"
	StatusOrganizing JobStatus = "organizing"
	StatusCompleted  JobStatus = "completed"
	StatusFailed     JobStatus = "failed"
	StatusSkipped    JobStatus = "skipped"
)

type Job struct {
	ID         int64
	DriveIndex int
	TitleIndex int
	DiscName   string
	TitleName  string
	OutputDir  string
	OutputPath string
	Status     JobStatus
	Progress   int // 0-100
	Error      string
	StartedAt  time.Time
	FinishedAt time.Time
}

func NewJob(driveIndex, titleIndex int, discName, outputDir string) *Job {
	return &Job{
		DriveIndex: driveIndex,
		TitleIndex: titleIndex,
		DiscName:   discName,
		OutputDir:  outputDir,
		Status:     StatusPending,
	}
}

func (j *Job) Start() {
	j.Status = StatusRipping
	j.StartedAt = time.Now()
}

func (j *Job) UpdateProgress(pct int) {
	j.Progress = pct
}

func (j *Job) Complete(outputPath string) {
	j.Status = StatusCompleted
	j.OutputPath = outputPath
	j.Progress = 100
	j.FinishedAt = time.Now()
}

func (j *Job) Fail(errMsg string) {
	j.Status = StatusFailed
	j.Error = errMsg
	j.FinishedAt = time.Now()
}

func (j *Job) Skip() {
	j.Status = StatusSkipped
	j.FinishedAt = time.Now()
}
```

- [ ] **Step 4: Run job tests to verify they pass**

Run: `go test ./internal/ripper/ -v -run TestJob`
Expected: All PASS

- [ ] **Step 5: Write failing engine tests**

```go
// internal/ripper/engine_test.go
package ripper

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/johnpostlethwait/bluforge/internal/makemkv"
)

type mockRipExecutor struct {
	mu     sync.Mutex
	calls  []ripCall
	events []makemkv.Event
	err    error
}

type ripCall struct {
	driveIndex int
	titleID    int
	outputDir  string
}

func (m *mockRipExecutor) StartRip(ctx context.Context, driveIndex int, titleID int, outputDir string, onEvent func(makemkv.Event)) error {
	m.mu.Lock()
	m.calls = append(m.calls, ripCall{driveIndex, titleID, outputDir})
	m.mu.Unlock()

	for _, ev := range m.events {
		if onEvent != nil {
			onEvent(ev)
		}
	}
	return m.err
}

func TestEngineRejectsSecondRipOnSameDrive(t *testing.T) {
	mock := &mockRipExecutor{
		events: []makemkv.Event{
			{Type: "PRGV", Progress: &makemkv.Progress{Current: 0, Total: 100, Max: 100}},
		},
	}

	engine := NewEngine(mock)

	// Start first rip (will block on mock)
	job1 := NewJob(0, 0, "DISC_A", "/tmp")
	err := engine.Submit(job1)
	if err != nil {
		t.Fatalf("first submit should succeed: %v", err)
	}

	// Wait for it to start
	time.Sleep(50 * time.Millisecond)

	// Second rip on same drive should be rejected
	job2 := NewJob(0, 1, "DISC_A", "/tmp")
	err = engine.Submit(job2)
	if err == nil {
		t.Error("expected error for second rip on same drive")
	}
}

func TestEngineAllowsConcurrentDrives(t *testing.T) {
	mock := &mockRipExecutor{
		events: []makemkv.Event{
			{Type: "PRGV", Progress: &makemkv.Progress{Current: 100, Total: 100, Max: 100}},
			{Type: "MSG", Message: &makemkv.Message{Code: 1005, Text: "Operation successfully completed"}},
		},
	}

	engine := NewEngine(mock)

	job1 := NewJob(0, 0, "DISC_A", "/tmp")
	job2 := NewJob(1, 0, "DISC_B", "/tmp")

	err1 := engine.Submit(job1)
	err2 := engine.Submit(job2)

	if err1 != nil {
		t.Errorf("drive 0 submit should succeed: %v", err1)
	}
	if err2 != nil {
		t.Errorf("drive 1 submit should succeed: %v", err2)
	}

	// Wait for completion
	time.Sleep(100 * time.Millisecond)

	mock.mu.Lock()
	defer mock.mu.Unlock()
	if len(mock.calls) != 2 {
		t.Errorf("expected 2 rip calls, got %d", len(mock.calls))
	}
}
```

- [ ] **Step 6: Run tests to verify they fail**

Run: `go test ./internal/ripper/ -v -run TestEngine`
Expected: FAIL — `NewEngine`, `Submit` not defined

- [ ] **Step 7: Implement engine**

```go
// internal/ripper/engine.go
package ripper

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/johnpostlethwait/bluforge/internal/makemkv"
)

// RipExecutor is the subset of makemkv.Executor that Engine needs.
type RipExecutor interface {
	StartRip(ctx context.Context, driveIndex int, titleID int, outputDir string, onEvent func(makemkv.Event)) error
}

type Engine struct {
	mu        sync.Mutex
	executor  RipExecutor
	active    map[int]*Job // driveIndex -> active job
	onUpdate  func(*Job)
}

func NewEngine(executor RipExecutor) *Engine {
	return &Engine{
		executor: executor,
		active:   make(map[int]*Job),
	}
}

// OnUpdate sets a callback for job status changes (progress, completion, failure).
func (e *Engine) OnUpdate(fn func(*Job)) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.onUpdate = fn
}

// Submit starts a rip job. Returns error if the drive already has an active rip.
func (e *Engine) Submit(job *Job) error {
	e.mu.Lock()
	if _, busy := e.active[job.DriveIndex]; busy {
		e.mu.Unlock()
		return fmt.Errorf("drive %d already has an active rip", job.DriveIndex)
	}
	e.active[job.DriveIndex] = job
	e.mu.Unlock()

	go e.run(job)
	return nil
}

func (e *Engine) run(job *Job) {
	defer func() {
		e.mu.Lock()
		delete(e.active, job.DriveIndex)
		e.mu.Unlock()
	}()

	job.Start()
	e.notify(job)

	slog.Info("rip started", "drive", job.DriveIndex, "title", job.TitleIndex, "disc", job.DiscName)

	err := e.executor.StartRip(context.Background(), job.DriveIndex, job.TitleIndex, job.OutputDir, func(ev makemkv.Event) {
		if ev.Type == "PRGV" && ev.Progress != nil {
			pct := 0
			if ev.Progress.Max > 0 {
				pct = int(float64(ev.Progress.Current) / float64(ev.Progress.Max) * 100)
			}
			job.UpdateProgress(pct)
			e.notify(job)
		}
	})

	if err != nil {
		job.Fail(err.Error())
		slog.Error("rip failed", "drive", job.DriveIndex, "error", err)
	} else {
		job.Status = StatusOrganizing
		slog.Info("rip completed, organizing", "drive", job.DriveIndex, "title", job.TitleIndex)
	}
	e.notify(job)
}

// IsActive returns true if the given drive has an active rip.
func (e *Engine) IsActive(driveIndex int) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	_, ok := e.active[driveIndex]
	return ok
}

// ActiveJobs returns a snapshot of all active jobs.
func (e *Engine) ActiveJobs() []*Job {
	e.mu.Lock()
	defer e.mu.Unlock()
	var jobs []*Job
	for _, j := range e.active {
		jobs = append(jobs, j)
	}
	return jobs
}

func (e *Engine) notify(job *Job) {
	e.mu.Lock()
	fn := e.onUpdate
	e.mu.Unlock()
	if fn != nil {
		fn(job)
	}
}
```

- [ ] **Step 8: Run tests to verify they pass**

Run: `go test ./internal/ripper/ -v`
Expected: All PASS

- [ ] **Step 9: Commit**

```bash
git add internal/ripper/
git commit -m "feat: rip engine with per-drive concurrency, progress tracking, and job lifecycle"
```

---

### Task 14: SSE Hub

**Files:**
- Create: `internal/web/sse.go`
- Create: `internal/web/sse_test.go`

- [ ] **Step 1: Write failing tests**

```go
// internal/web/sse_test.go
package web

import (
	"testing"
	"time"
)

func TestSSEHubBroadcast(t *testing.T) {
	hub := NewSSEHub()

	ch1 := hub.Subscribe()
	ch2 := hub.Subscribe()
	defer hub.Unsubscribe(ch1)
	defer hub.Unsubscribe(ch2)

	hub.Broadcast(SSEEvent{Event: "test", Data: `{"msg":"hello"}`})

	select {
	case ev := <-ch1:
		if ev.Event != "test" {
			t.Errorf("expected 'test', got %s", ev.Event)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("ch1 timed out")
	}

	select {
	case ev := <-ch2:
		if ev.Data != `{"msg":"hello"}` {
			t.Errorf("unexpected data: %s", ev.Data)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("ch2 timed out")
	}
}

func TestSSEHubUnsubscribe(t *testing.T) {
	hub := NewSSEHub()

	ch := hub.Subscribe()
	hub.Unsubscribe(ch)

	// Should not panic
	hub.Broadcast(SSEEvent{Event: "test", Data: "data"})
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/web/ -v -run TestSSEHub`
Expected: FAIL — `NewSSEHub` not defined

- [ ] **Step 3: Implement SSE hub**

```go
// internal/web/sse.go
package web

import "sync"

type SSEEvent struct {
	Event string // SSE event name
	Data  string // JSON payload
}

type SSEHub struct {
	mu      sync.RWMutex
	clients map[chan SSEEvent]struct{}
}

func NewSSEHub() *SSEHub {
	return &SSEHub{
		clients: make(map[chan SSEEvent]struct{}),
	}
}

func (h *SSEHub) Subscribe() chan SSEEvent {
	ch := make(chan SSEEvent, 32)
	h.mu.Lock()
	h.clients[ch] = struct{}{}
	h.mu.Unlock()
	return ch
}

func (h *SSEHub) Unsubscribe(ch chan SSEEvent) {
	h.mu.Lock()
	delete(h.clients, ch)
	h.mu.Unlock()
	close(ch)
}

func (h *SSEHub) Broadcast(ev SSEEvent) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for ch := range h.clients {
		select {
		case ch <- ev:
		default:
			// Drop event if client is slow — don't block other clients
		}
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/web/ -v -run TestSSEHub`
Expected: All PASS

- [ ] **Step 5: Commit**

```bash
git add internal/web/sse.go internal/web/sse_test.go
git commit -m "feat: SSE hub for broadcasting real-time events to web clients"
```

---

### Task 15: Web Server & Routes Skeleton

**Files:**
- Create: `internal/web/server.go`
- Create: `static/style.css`

- [ ] **Step 1: Install Echo dependency**

Run: `go get github.com/labstack/echo/v4`

- [ ] **Step 2: Install Templ**

Run: `go install github.com/a-h/templ/cmd/templ@latest && go get github.com/a-h/templ`

- [ ] **Step 3: Create web server with routes**

```go
// internal/web/server.go
package web

import (
	"fmt"
	"log/slog"
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"

	"github.com/johnpostlethwait/bluforge/internal/config"
	"github.com/johnpostlethwait/bluforge/internal/db"
	"github.com/johnpostlethwait/bluforge/internal/discdb"
	"github.com/johnpostlethwait/bluforge/internal/drivemanager"
	"github.com/johnpostlethwait/bluforge/internal/ripper"
)

type Server struct {
	echo         *echo.Echo
	cfg          *config.AppConfig
	store        *db.Store
	driveMgr     *drivemanager.Manager
	ripEngine    *ripper.Engine
	discdbClient *discdb.Client
	sseHub       *SSEHub
}

type ServerDeps struct {
	Config       *config.AppConfig
	Store        *db.Store
	DriveManager *drivemanager.Manager
	RipEngine    *ripper.Engine
	DiscDBClient *discdb.Client
}

func NewServer(deps ServerDeps) *Server {
	s := &Server{
		echo:         echo.New(),
		cfg:          deps.Config,
		store:        deps.Store,
		driveMgr:     deps.DriveManager,
		ripEngine:    deps.RipEngine,
		discdbClient: deps.DiscDBClient,
		sseHub:       NewSSEHub(),
	}

	s.echo.HideBanner = true

	// Middleware
	s.echo.Use(middleware.Recover())
	s.echo.Use(middleware.RequestLoggerWithConfig(middleware.RequestLoggerConfig{
		LogStatus: true,
		LogURI:    true,
		LogMethod: true,
		LogValuesFunc: func(c echo.Context, v middleware.RequestLoggerValues) error {
			slog.Info("request",
				"method", v.Method,
				"uri", v.URI,
				"status", v.Status,
			)
			return nil
		},
	}))

	// Static files
	s.echo.Static("/static", "static")

	// Routes
	s.echo.GET("/", s.handleDashboard)
	s.echo.GET("/drives/:id", s.handleDriveDetail)
	s.echo.POST("/drives/:id/search", s.handleDriveSearch)
	s.echo.POST("/drives/:id/rip", s.handleStartRip)
	s.echo.POST("/drives/:id/rescan", s.handleRescan)
	s.echo.GET("/queue", s.handleQueue)
	s.echo.GET("/history", s.handleHistory)
	s.echo.GET("/settings", s.handleSettingsGet)
	s.echo.POST("/settings", s.handleSettingsPost)
	s.echo.GET("/events", s.handleSSE)

	return s
}

func (s *Server) Start() error {
	addr := fmt.Sprintf(":%d", s.cfg.Port)
	slog.Info("starting web server", "addr", addr)
	return s.echo.Start(addr)
}

func (s *Server) Stop() error {
	return s.echo.Close()
}

// Placeholder handlers — each will be implemented in its own handlers_*.go file

func (s *Server) handleDashboard(c echo.Context) error {
	return c.String(http.StatusOK, "BluForge Dashboard — coming soon")
}

func (s *Server) handleDriveDetail(c echo.Context) error {
	return c.String(http.StatusOK, "Drive Detail — coming soon")
}

func (s *Server) handleDriveSearch(c echo.Context) error {
	return c.String(http.StatusOK, "Search — coming soon")
}

func (s *Server) handleStartRip(c echo.Context) error {
	return c.String(http.StatusOK, "Rip — coming soon")
}

func (s *Server) handleRescan(c echo.Context) error {
	return c.String(http.StatusOK, "Rescan — coming soon")
}

func (s *Server) handleQueue(c echo.Context) error {
	return c.String(http.StatusOK, "Queue — coming soon")
}

func (s *Server) handleHistory(c echo.Context) error {
	return c.String(http.StatusOK, "History — coming soon")
}

func (s *Server) handleSettingsGet(c echo.Context) error {
	return c.String(http.StatusOK, "Settings — coming soon")
}

func (s *Server) handleSettingsPost(c echo.Context) error {
	return c.Redirect(http.StatusSeeOther, "/settings")
}

func (s *Server) handleSSE(c echo.Context) error {
	c.Response().Header().Set("Content-Type", "text/event-stream")
	c.Response().Header().Set("Cache-Control", "no-cache")
	c.Response().Header().Set("Connection", "keep-alive")

	ch := s.sseHub.Subscribe()
	defer s.sseHub.Unsubscribe(ch)

	for {
		select {
		case ev := <-ch:
			fmt.Fprintf(c.Response(), "event: %s\ndata: %s\n\n", ev.Event, ev.Data)
			c.Response().Flush()
		case <-c.Request().Context().Done():
			return nil
		}
	}
}
```

- [ ] **Step 4: Create dark theme CSS**

```css
/* static/style.css */
:root {
    --bg-primary: #0f1419;
    --bg-secondary: #1a2332;
    --bg-tertiary: #243044;
    --accent-blue: #3b82f6;
    --accent-blue-hover: #2563eb;
    --accent-blue-dim: #1e40af;
    --text-primary: #e2e8f0;
    --text-secondary: #94a3b8;
    --text-muted: #64748b;
    --border: #334155;
    --success: #22c55e;
    --warning: #eab308;
    --error: #ef4444;
    --progress-bg: #1e293b;
}

* {
    margin: 0;
    padding: 0;
    box-sizing: border-box;
}

body {
    font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif;
    background: var(--bg-primary);
    color: var(--text-primary);
    line-height: 1.6;
}

a { color: var(--accent-blue); text-decoration: none; }
a:hover { color: var(--accent-blue-hover); }

/* Layout */
.container { max-width: 1200px; margin: 0 auto; padding: 0 1rem; }

/* Navigation */
nav {
    background: var(--bg-secondary);
    border-bottom: 1px solid var(--border);
    padding: 0.75rem 0;
}
nav .container { display: flex; align-items: center; gap: 2rem; }
nav .logo { font-size: 1.25rem; font-weight: 700; color: var(--accent-blue); }
nav .nav-links { display: flex; gap: 1.5rem; }
nav .nav-links a { color: var(--text-secondary); font-size: 0.9rem; }
nav .nav-links a:hover, nav .nav-links a.active { color: var(--text-primary); }

/* Cards */
.card {
    background: var(--bg-secondary);
    border: 1px solid var(--border);
    border-radius: 0.5rem;
    padding: 1.25rem;
    margin-bottom: 1rem;
}
.card-header {
    display: flex;
    justify-content: space-between;
    align-items: center;
    margin-bottom: 0.75rem;
}
.card-title { font-size: 1.1rem; font-weight: 600; }

/* Drive cards */
.drive-grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(350px, 1fr)); gap: 1rem; margin-top: 1rem; }

/* Status badges */
.badge {
    display: inline-block;
    padding: 0.2rem 0.6rem;
    border-radius: 9999px;
    font-size: 0.75rem;
    font-weight: 600;
    text-transform: uppercase;
}
.badge-empty { background: var(--bg-tertiary); color: var(--text-muted); }
.badge-detected, .badge-scanning { background: var(--accent-blue-dim); color: var(--accent-blue); }
.badge-identified, .badge-ready { background: #064e3b; color: var(--success); }
.badge-ripping { background: #713f12; color: var(--warning); }
.badge-complete { background: #064e3b; color: var(--success); }
.badge-failed { background: #450a0a; color: var(--error); }
.badge-not_found { background: #451a03; color: var(--warning); }

/* Progress bar */
.progress-bar {
    width: 100%;
    height: 0.5rem;
    background: var(--progress-bg);
    border-radius: 9999px;
    overflow: hidden;
    margin: 0.5rem 0;
}
.progress-fill {
    height: 100%;
    background: var(--accent-blue);
    border-radius: 9999px;
    transition: width 0.3s ease;
}

/* Buttons */
.btn {
    display: inline-flex;
    align-items: center;
    gap: 0.5rem;
    padding: 0.5rem 1rem;
    border: none;
    border-radius: 0.375rem;
    font-size: 0.875rem;
    font-weight: 500;
    cursor: pointer;
    transition: background 0.2s;
}
.btn-primary { background: var(--accent-blue); color: white; }
.btn-primary:hover { background: var(--accent-blue-hover); }
.btn-secondary { background: var(--bg-tertiary); color: var(--text-primary); border: 1px solid var(--border); }
.btn-secondary:hover { background: var(--border); }
.btn-danger { background: var(--error); color: white; }
.btn-danger:hover { background: #dc2626; }
.btn:disabled { opacity: 0.5; cursor: not-allowed; }

/* Forms */
input, select, textarea {
    background: var(--bg-tertiary);
    border: 1px solid var(--border);
    color: var(--text-primary);
    padding: 0.5rem 0.75rem;
    border-radius: 0.375rem;
    font-size: 0.875rem;
    width: 100%;
}
input:focus, select:focus, textarea:focus {
    outline: none;
    border-color: var(--accent-blue);
}
label {
    display: block;
    margin-bottom: 0.25rem;
    font-size: 0.875rem;
    color: var(--text-secondary);
}
.form-group { margin-bottom: 1rem; }

/* Tables */
table { width: 100%; border-collapse: collapse; }
th, td { padding: 0.75rem; text-align: left; border-bottom: 1px solid var(--border); }
th { color: var(--text-secondary); font-size: 0.8rem; text-transform: uppercase; font-weight: 600; }
tr:hover { background: var(--bg-tertiary); }

/* Page header */
.page-header {
    display: flex;
    justify-content: space-between;
    align-items: center;
    padding: 1.5rem 0;
}
.page-title { font-size: 1.5rem; font-weight: 700; }

/* Utility */
.text-muted { color: var(--text-muted); }
.text-secondary { color: var(--text-secondary); }
.text-success { color: var(--success); }
.text-warning { color: var(--warning); }
.text-error { color: var(--error); }
.mt-1 { margin-top: 0.5rem; }
.mt-2 { margin-top: 1rem; }
.mb-1 { margin-bottom: 0.5rem; }
.flex { display: flex; }
.gap-1 { gap: 0.5rem; }
.gap-2 { gap: 1rem; }
.items-center { align-items: center; }
.justify-between { justify-content: space-between; }

/* Responsive */
@media (max-width: 768px) {
    .drive-grid { grid-template-columns: 1fr; }
    nav .container { flex-direction: column; gap: 0.5rem; }
}
```

- [ ] **Step 5: Verify it compiles**

Run: `go build ./...`
Expected: Build succeeds

- [ ] **Step 6: Commit**

```bash
git add internal/web/server.go static/style.css go.mod go.sum
git commit -m "feat: web server skeleton with Echo routes, SSE endpoint, and dark blue theme CSS"
```

---

### Task 16: Templ Templates (Layout + Dashboard + Drive Detail)

**Files:**
- Create: `templates/layout.templ`
- Create: `templates/components/nav.templ`
- Create: `templates/components/drive_card.templ`
- Create: `templates/components/progress_bar.templ`
- Create: `templates/dashboard.templ`
- Create: `templates/drive_detail.templ`
- Create: `templates/drive_search_results.templ`

- [ ] **Step 1: Create layout template**

```go
// templates/layout.templ
package templates

templ Layout(title string) {
	<!DOCTYPE html>
	<html lang="en">
	<head>
		<meta charset="UTF-8"/>
		<meta name="viewport" content="width=device-width, initial-scale=1.0"/>
		<title>{ title } — BluForge</title>
		<link rel="stylesheet" href="/static/style.css"/>
		<script src="https://unpkg.com/htmx.org@2.0.4"></script>
		<script src="https://unpkg.com/htmx-ext-sse@2.2.2/sse.js"></script>
	</head>
	<body>
		@Nav()
		<main class="container" style="padding-top: 1rem;">
			{ children... }
		</main>
	</body>
	</html>
}
```

- [ ] **Step 2: Create nav component**

```go
// templates/components/nav.templ
package templates

templ Nav() {
	<nav>
		<div class="container">
			<a href="/" class="logo">BluForge</a>
			<div class="nav-links">
				<a href="/">Dashboard</a>
				<a href="/queue">Queue</a>
				<a href="/history">History</a>
				<a href="/settings">Settings</a>
			</div>
		</div>
	</nav>
}
```

- [ ] **Step 3: Create progress bar component**

```go
// templates/components/progress_bar.templ
package templates

import "fmt"

templ ProgressBar(jobID string, percent int) {
	<div id={ fmt.Sprintf("progress-%s", jobID) } class="progress-bar">
		<div class="progress-fill" style={ fmt.Sprintf("width: %d%%", percent) }></div>
	</div>
	<span class="text-secondary" style="font-size: 0.8rem;">{ fmt.Sprintf("%d%%", percent) }</span>
}
```

- [ ] **Step 4: Create drive card component**

```go
// templates/components/drive_card.templ
package templates

import "fmt"

type DriveCardData struct {
	Index    int
	Name     string
	DiscName string
	State    string
	Progress int
}

templ DriveCard(d DriveCardData) {
	<div class="card" id={ fmt.Sprintf("drive-%d", d.Index) }>
		<div class="card-header">
			<span class="card-title">{ d.Name }</span>
			<span class={ fmt.Sprintf("badge badge-%s", d.State) }>{ d.State }</span>
		</div>
		if d.DiscName != "" {
			<p>{ d.DiscName }</p>
		} else {
			<p class="text-muted">No disc</p>
		}
		if d.State == "ripping" {
			@ProgressBar(fmt.Sprintf("drive-%d", d.Index), d.Progress)
		}
		if d.DiscName != "" {
			<div class="mt-1">
				<a href={ templ.URL(fmt.Sprintf("/drives/%d", d.Index)) } class="btn btn-secondary">Details</a>
			</div>
		}
	</div>
}
```

- [ ] **Step 5: Create dashboard template**

```go
// templates/dashboard.templ
package templates

type DashboardData struct {
	Drives []DriveCardData
}

templ Dashboard(data DashboardData) {
	@Layout("Dashboard") {
		<div class="page-header">
			<h1 class="page-title">Drives</h1>
		</div>
		<div class="drive-grid"
			hx-ext="sse"
			sse-connect="/events"
			sse-swap="drive-update"
			hx-swap="innerHTML"
		>
			for _, d := range data.Drives {
				@DriveCard(d)
			}
			if len(data.Drives) == 0 {
				<div class="card">
					<p class="text-muted">No drives detected. Make sure optical drives are passed through to the container.</p>
				</div>
			}
		</div>
	}
}
```

- [ ] **Step 6: Create drive detail template**

```go
// templates/drive_detail.templ
package templates

import "fmt"

type TitleRow struct {
	Index      int
	Name       string
	Duration   string
	Size       string
	SourceFile string
	Matched    bool
	ContentType string
	ContentName string
	Selected   bool
}

type DriveDetailData struct {
	DriveIndex int
	DriveName  string
	DiscName   string
	State      string
	Titles     []TitleRow
	MatchedMedia string
	MatchedRelease string
	HasMapping  bool
}

templ DriveDetail(data DriveDetailData) {
	@Layout(fmt.Sprintf("Drive %d", data.DriveIndex)) {
		<div class="page-header">
			<h1 class="page-title">{ data.DriveName }</h1>
			<span class={ fmt.Sprintf("badge badge-%s", data.State) }>{ data.State }</span>
		</div>

		<div class="card">
			<div class="card-header">
				<span class="card-title">Disc: { data.DiscName }</span>
				if data.HasMapping {
					<form method="POST" action={ templ.URL(fmt.Sprintf("/drives/%d/rescan", data.DriveIndex)) }>
						<button type="submit" class="btn btn-secondary">Re-scan</button>
					</form>
				}
			</div>
			if data.MatchedMedia != "" {
				<p>Matched: <strong>{ data.MatchedMedia }</strong> — { data.MatchedRelease }</p>
			}
		</div>

		<!-- Search UI -->
		<div class="card">
			<div class="card-header">
				<span class="card-title">Search TheDiscDB</span>
			</div>
			<form hx-post={ fmt.Sprintf("/drives/%d/search", data.DriveIndex) }
				  hx-target="#search-results"
				  hx-swap="innerHTML"
				  class="flex gap-1 items-center">
				<div class="form-group" style="flex: 1; margin-bottom: 0;">
					<input type="text" name="query" placeholder="Movie/show name, TMDB ID, UPC, or ASIN"/>
				</div>
				<select name="search_type" style="width: auto;">
					<option value="title">Title</option>
					<option value="upc">UPC</option>
					<option value="asin">ASIN</option>
				</select>
				<button type="submit" class="btn btn-primary">Search</button>
			</form>
			<div id="search-results" class="mt-2"></div>
		</div>

		<!-- Titles table -->
		if len(data.Titles) > 0 {
			<div class="card">
				<div class="card-header">
					<span class="card-title">Titles ({ fmt.Sprintf("%d", len(data.Titles)) })</span>
				</div>
				<form method="POST" action={ templ.URL(fmt.Sprintf("/drives/%d/rip", data.DriveIndex)) }>
					<table>
						<thead>
							<tr>
								<th>Rip</th>
								<th>#</th>
								<th>Name</th>
								<th>Duration</th>
								<th>Size</th>
								<th>Source</th>
								<th>Content</th>
							</tr>
						</thead>
						<tbody>
							for _, t := range data.Titles {
								<tr>
									<td><input type="checkbox" name="titles" value={ fmt.Sprintf("%d", t.Index) }
										if t.Selected { checked } /></td>
									<td>{ fmt.Sprintf("%d", t.Index) }</td>
									<td>{ t.Name }</td>
									<td>{ t.Duration }</td>
									<td>{ t.Size }</td>
									<td class="text-muted">{ t.SourceFile }</td>
									<td>
										if t.Matched {
											<span class="badge badge-identified">{ t.ContentType }</span>
											{ t.ContentName }
										} else {
											<span class="text-muted">Unmatched</span>
										}
									</td>
								</tr>
							}
						</tbody>
					</table>
					<div class="mt-2">
						<button type="submit" class="btn btn-primary">Rip Selected</button>
					</div>
				</form>
			</div>
		}
	}
}
```

- [ ] **Step 7: Create search results partial**

```go
// templates/drive_search_results.templ
package templates

import "fmt"

type SearchResultRow struct {
	MediaTitle  string
	MediaYear   int
	MediaType   string
	ReleaseTitle string
	ReleaseUPC  string
	ReleaseASIN string
	RegionCode  string
	Format      string
	DiscCount   int
	ReleaseID   string
	MediaItemID string
}

templ DriveSearchResults(driveIndex int, results []SearchResultRow) {
	if len(results) == 0 {
		<p class="text-muted">No results found.</p>
	} else {
		<table>
			<thead>
				<tr>
					<th>Title</th>
					<th>Year</th>
					<th>Type</th>
					<th>Release</th>
					<th>UPC</th>
					<th>Region</th>
					<th>Format</th>
					<th></th>
				</tr>
			</thead>
			<tbody>
				for _, r := range results {
					<tr>
						<td>{ r.MediaTitle }</td>
						<td>{ fmt.Sprintf("%d", r.MediaYear) }</td>
						<td>{ r.MediaType }</td>
						<td>{ r.ReleaseTitle }</td>
						<td class="text-muted">{ r.ReleaseUPC }</td>
						<td>{ r.RegionCode }</td>
						<td>{ r.Format }</td>
						<td>
							<form method="POST" action={ templ.URL(fmt.Sprintf("/drives/%d/select", driveIndex)) }>
								<input type="hidden" name="media_item_id" value={ r.MediaItemID }/>
								<input type="hidden" name="release_id" value={ r.ReleaseID }/>
								<button type="submit" class="btn btn-primary">Select</button>
							</form>
						</td>
					</tr>
				}
			</tbody>
		</table>
	}
}
```

- [ ] **Step 8: Generate Go code from templates**

Run: `templ generate`
Expected: Generates `*_templ.go` files alongside each `.templ` file

- [ ] **Step 9: Verify it compiles**

Run: `go build ./...`
Expected: Build succeeds

- [ ] **Step 10: Commit**

```bash
git add templates/ static/
git commit -m "feat: Templ templates for layout, dashboard, drive detail, and search results with dark blue theme"
```

---

### Task 17: Wire Up Dashboard & Drive Detail Handlers

**Files:**
- Create: `internal/web/handlers_dashboard.go`
- Create: `internal/web/handlers_drive.go`

- [ ] **Step 1: Implement dashboard handler**

```go
// internal/web/handlers_dashboard.go
package web

import (
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/johnpostlethwait/bluforge/templates"
)

func (s *Server) handleDashboard(c echo.Context) error {
	drives := s.driveMgr.GetAllDrives()

	var cards []templates.DriveCardData
	for _, d := range drives {
		card := templates.DriveCardData{
			Index:    d.Index(),
			Name:     d.DevicePath(),
			DiscName: d.DiscName(),
			State:    string(d.State()),
		}
		cards = append(cards, card)
	}

	data := templates.DashboardData{Drives: cards}
	return templates.Dashboard(data).Render(c.Request().Context(), c.Response().Writer)
}
```

- [ ] **Step 2: Implement drive detail and search handlers**

```go
// internal/web/handlers_drive.go
package web

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/labstack/echo/v4"

	"github.com/johnpostlethwait/bluforge/internal/discdb"
	"github.com/johnpostlethwait/bluforge/internal/ripper"
	"github.com/johnpostlethwait/bluforge/templates"
)

func (s *Server) handleDriveDetail(c echo.Context) error {
	idStr := c.Param("id")
	driveIndex, err := strconv.Atoi(idStr)
	if err != nil {
		return c.String(http.StatusBadRequest, "invalid drive index")
	}

	drive := s.driveMgr.GetDrive(driveIndex)
	if drive == nil {
		return c.String(http.StatusNotFound, "drive not found")
	}

	data := templates.DriveDetailData{
		DriveIndex: driveIndex,
		DriveName:  drive.DevicePath(),
		DiscName:   drive.DiscName(),
		State:      string(drive.State()),
	}

	// TODO: populate Titles from scan cache, HasMapping from DB
	return templates.DriveDetail(data).Render(c.Request().Context(), c.Response().Writer)
}

func (s *Server) handleDriveSearch(c echo.Context) error {
	idStr := c.Param("id")
	driveIndex, err := strconv.Atoi(idStr)
	if err != nil {
		return c.String(http.StatusBadRequest, "invalid drive index")
	}

	query := c.FormValue("query")
	searchType := c.FormValue("search_type")

	var items []discdb.MediaItem
	switch searchType {
	case "upc":
		items, err = s.discdbClient.SearchByUPC(c.Request().Context(), query)
	case "asin":
		items, err = s.discdbClient.SearchByASIN(c.Request().Context(), query)
	default:
		items, err = s.discdbClient.SearchByTitle(c.Request().Context(), query)
	}

	if err != nil {
		return c.String(http.StatusInternalServerError, fmt.Sprintf("search failed: %v", err))
	}

	var rows []templates.SearchResultRow
	for _, item := range items {
		for _, rel := range item.Releases {
			format := ""
			discCount := len(rel.Discs)
			if discCount > 0 {
				format = rel.Discs[0].Format
			}
			rows = append(rows, templates.SearchResultRow{
				MediaTitle:   item.Title,
				MediaYear:    item.Year,
				MediaType:    item.Type,
				ReleaseTitle: rel.Title,
				ReleaseUPC:   rel.UPC,
				ReleaseASIN:  rel.ASIN,
				RegionCode:   rel.RegionCode,
				Format:       format,
				DiscCount:    discCount,
				ReleaseID:    rel.ID,
				MediaItemID:  item.ID,
			})
		}
	}

	return templates.DriveSearchResults(driveIndex, rows).Render(c.Request().Context(), c.Response().Writer)
}

func (s *Server) handleStartRip(c echo.Context) error {
	idStr := c.Param("id")
	driveIndex, err := strconv.Atoi(idStr)
	if err != nil {
		return c.String(http.StatusBadRequest, "invalid drive index")
	}

	titleStrs := c.Request().Form["titles"]
	if len(titleStrs) == 0 {
		return c.String(http.StatusBadRequest, "no titles selected")
	}

	drive := s.driveMgr.GetDrive(driveIndex)
	if drive == nil {
		return c.String(http.StatusNotFound, "drive not found")
	}

	for _, ts := range titleStrs {
		titleIdx, err := strconv.Atoi(ts)
		if err != nil {
			continue
		}

		job := ripper.NewJob(driveIndex, titleIdx, drive.DiscName(), s.cfg.OutputDir)
		if err := s.ripEngine.Submit(job); err != nil {
			return c.String(http.StatusConflict, err.Error())
		}
	}

	return c.Redirect(http.StatusSeeOther, "/queue")
}

func (s *Server) handleRescan(c echo.Context) error {
	idStr := c.Param("id")
	driveIndex, err := strconv.Atoi(idStr)
	if err != nil {
		return c.String(http.StatusBadRequest, "invalid drive index")
	}

	// Delete remembered mapping for this drive's disc
	drive := s.driveMgr.GetDrive(driveIndex)
	if drive != nil {
		// TODO: compute disc key from cached scan and delete mapping
	}

	return c.Redirect(http.StatusSeeOther, fmt.Sprintf("/drives/%d", driveIndex))
}
```

- [ ] **Step 3: Remove placeholder handlers from server.go**

Update `internal/web/server.go` to remove the placeholder handler method bodies (they're now in separate files). Keep only the route registrations and the `handleSSE` method in server.go.

- [ ] **Step 4: Verify it compiles**

Run: `go build ./...`
Expected: Build succeeds

- [ ] **Step 5: Commit**

```bash
git add internal/web/handlers_dashboard.go internal/web/handlers_drive.go internal/web/server.go
git commit -m "feat: wire up dashboard and drive detail handlers with TheDiscDB search"
```

---

### Task 18: Queue, History, and Settings Handlers + Templates

**Files:**
- Create: `templates/queue.templ`
- Create: `templates/history.templ`
- Create: `templates/settings.templ`
- Create: `internal/web/handlers_queue.go`
- Create: `internal/web/handlers_history.go`
- Create: `internal/web/handlers_settings.go`

- [ ] **Step 1: Create queue template**

```go
// templates/queue.templ
package templates

import "fmt"

type QueueJobRow struct {
	ID          int64
	DiscName    string
	TitleName   string
	ContentType string
	Status      string
	Progress    int
	Error       string
	DriveIndex  int
}

templ Queue(active []QueueJobRow, pending []QueueJobRow, completed []QueueJobRow) {
	@Layout("Queue") {
		<div class="page-header">
			<h1 class="page-title">Rip Queue</h1>
		</div>

		if len(active) > 0 {
			<h2 style="font-size: 1.1rem; margin-bottom: 0.5rem;">Active</h2>
			for _, j := range active {
				<div class="card" id={ fmt.Sprintf("job-%d", j.ID) }
					hx-ext="sse" sse-connect="/events" sse-swap={ fmt.Sprintf("job-%d", j.ID) }>
					<div class="card-header">
						<span class="card-title">{ j.DiscName } — { j.TitleName }</span>
						<span class="badge badge-ripping">{ j.Status }</span>
					</div>
					@ProgressBar(fmt.Sprintf("%d", j.ID), j.Progress)
				</div>
			}
		}

		if len(pending) > 0 {
			<h2 style="font-size: 1.1rem; margin: 1rem 0 0.5rem;">Pending</h2>
			for _, j := range pending {
				<div class="card">
					<div class="card-header">
						<span class="card-title">{ j.DiscName } — { j.TitleName }</span>
						<span class="badge badge-empty">pending</span>
					</div>
				</div>
			}
		}

		if len(completed) > 0 {
			<h2 style="font-size: 1.1rem; margin: 1rem 0 0.5rem;">Recently Completed</h2>
			for _, j := range completed {
				<div class="card">
					<div class="card-header">
						<span class="card-title">{ j.DiscName } — { j.TitleName }</span>
						if j.Status == "completed" {
							<span class="badge badge-complete">completed</span>
						} else if j.Status == "failed" {
							<span class="badge badge-failed">failed</span>
						} else {
							<span class="badge badge-empty">{ j.Status }</span>
						}
					</div>
					if j.Error != "" {
						<p class="text-error">{ j.Error }</p>
					}
				</div>
			}
		}

		if len(active) == 0 && len(pending) == 0 && len(completed) == 0 {
			<div class="card">
				<p class="text-muted">No rip jobs yet.</p>
			</div>
		}
	}
}
```

- [ ] **Step 2: Create history template**

```go
// templates/history.templ
package templates

import "fmt"

type HistoryRow struct {
	ID          int64
	DiscName    string
	TitleName   string
	ContentType string
	OutputPath  string
	Status      string
	Duration    string
	SizeBytes   int64
	CreatedAt   string
}

templ History(rows []HistoryRow, page int, hasMore bool) {
	@Layout("History") {
		<div class="page-header">
			<h1 class="page-title">Rip History</h1>
		</div>

		<div class="card">
			<table>
				<thead>
					<tr>
						<th>Disc</th>
						<th>Title</th>
						<th>Type</th>
						<th>Output</th>
						<th>Status</th>
						<th>Date</th>
					</tr>
				</thead>
				<tbody>
					if len(rows) == 0 {
						<tr><td colspan="6" class="text-muted">No history yet.</td></tr>
					}
					for _, r := range rows {
						<tr>
							<td>{ r.DiscName }</td>
							<td>{ r.TitleName }</td>
							<td>{ r.ContentType }</td>
							<td class="text-secondary">{ r.OutputPath }</td>
							<td>
								if r.Status == "completed" {
									<span class="badge badge-complete">{ r.Status }</span>
								} else if r.Status == "failed" {
									<span class="badge badge-failed">{ r.Status }</span>
								} else {
									<span class="badge badge-empty">{ r.Status }</span>
								}
							</td>
							<td class="text-muted">{ r.CreatedAt }</td>
						</tr>
					}
				</tbody>
			</table>

			<div class="flex justify-between mt-2">
				if page > 1 {
					<a href={ templ.URL(fmt.Sprintf("/history?page=%d", page-1)) } class="btn btn-secondary">Previous</a>
				} else {
					<span></span>
				}
				if hasMore {
					<a href={ templ.URL(fmt.Sprintf("/history?page=%d", page+1)) } class="btn btn-secondary">Next</a>
				}
			</div>
		</div>
	}
}
```

- [ ] **Step 3: Create settings template**

```go
// templates/settings.templ
package templates

templ Settings(cfg SettingsData) {
	@Layout("Settings") {
		<div class="page-header">
			<h1 class="page-title">Settings</h1>
		</div>

		<form method="POST" action="/settings">
			<div class="card">
				<div class="card-header">
					<span class="card-title">General</span>
				</div>
				<div class="form-group">
					<label for="output_dir">Output Directory</label>
					<input type="text" id="output_dir" name="output_dir" value={ cfg.OutputDir }/>
				</div>
				<div class="form-group">
					<label for="auto_rip">
						<input type="checkbox" id="auto_rip" name="auto_rip" style="width: auto;"
							if cfg.AutoRip { checked }/>
						Enable Auto-Rip Mode
					</label>
				</div>
				<div class="form-group">
					<label for="min_title_length">Minimum Title Length (seconds)</label>
					<input type="number" id="min_title_length" name="min_title_length" value={ cfg.MinTitleLength }/>
				</div>
				<div class="form-group">
					<label for="poll_interval">Drive Poll Interval (seconds)</label>
					<input type="number" id="poll_interval" name="poll_interval" value={ cfg.PollInterval }/>
				</div>
				<div class="form-group">
					<label for="duplicate_action">When File Already Exists</label>
					<select id="duplicate_action" name="duplicate_action">
						<option value="skip" if cfg.DuplicateAction == "skip" { selected }>Skip</option>
						<option value="overwrite" if cfg.DuplicateAction == "overwrite" { selected }>Overwrite</option>
					</select>
				</div>
			</div>

			<div class="card">
				<div class="card-header">
					<span class="card-title">Naming Templates</span>
				</div>
				<div class="form-group">
					<label for="movie_template">Movie Template</label>
					<input type="text" id="movie_template" name="movie_template" value={ cfg.MovieTemplate }/>
					<small class="text-muted">Variables: { "{{.Title}}, {{.Year}}, {{.Part}}" }</small>
				</div>
				<div class="form-group">
					<label for="series_template">Series Template</label>
					<input type="text" id="series_template" name="series_template" value={ cfg.SeriesTemplate }/>
					<small class="text-muted">Variables: { "{{.Show}}, {{.Season}}, {{.Episode}}, {{.EpisodeTitle}}" }</small>
				</div>
			</div>

			<div class="card">
				<div class="card-header">
					<span class="card-title">TheDiscDB Contribution (Optional)</span>
				</div>
				<p class="text-secondary mb-1">Connect your GitHub account to contribute unmatched disc data back to TheDiscDB.</p>
				<div class="form-group">
					<label for="github_client_id">GitHub Client ID</label>
					<input type="text" id="github_client_id" name="github_client_id" value={ cfg.GitHubClientID }/>
				</div>
				<div class="form-group">
					<label for="github_client_secret">GitHub Client Secret</label>
					<input type="password" id="github_client_secret" name="github_client_secret" value={ cfg.GitHubClientSecret }/>
				</div>
			</div>

			<button type="submit" class="btn btn-primary">Save Settings</button>
		</form>
	}
}

type SettingsData struct {
	OutputDir         string
	AutoRip           bool
	MinTitleLength    string
	PollInterval      string
	DuplicateAction   string
	MovieTemplate     string
	SeriesTemplate    string
	GitHubClientID    string
	GitHubClientSecret string
}
```

- [ ] **Step 4: Implement queue handler**

```go
// internal/web/handlers_queue.go
package web

import (
	"github.com/labstack/echo/v4"
	"github.com/johnpostlethwait/bluforge/templates"
)

func (s *Server) handleQueue(c echo.Context) error {
	activeJobs := s.ripEngine.ActiveJobs()

	var active []templates.QueueJobRow
	for _, j := range activeJobs {
		active = append(active, templates.QueueJobRow{
			ID:         j.ID,
			DiscName:   j.DiscName,
			TitleName:  j.TitleName,
			Status:     string(j.Status),
			Progress:   j.Progress,
			Error:      j.Error,
			DriveIndex: j.DriveIndex,
		})
	}

	pendingJobs, _ := s.store.ListJobsByStatus("pending")
	var pending []templates.QueueJobRow
	for _, j := range pendingJobs {
		pending = append(pending, templates.QueueJobRow{
			ID:       j.ID,
			DiscName: j.DiscName,
			TitleName: j.TitleName,
			Status:   j.Status,
		})
	}

	completedJobs, _ := s.store.ListJobsByStatus("completed")
	failedJobs, _ := s.store.ListJobsByStatus("failed")
	var completed []templates.QueueJobRow
	for _, j := range append(completedJobs, failedJobs...) {
		completed = append(completed, templates.QueueJobRow{
			ID:       j.ID,
			DiscName: j.DiscName,
			TitleName: j.TitleName,
			Status:   j.Status,
			Error:    j.ErrorMessage,
		})
	}

	return templates.Queue(active, pending, completed).Render(c.Request().Context(), c.Response().Writer)
}
```

- [ ] **Step 5: Implement history handler**

```go
// internal/web/handlers_history.go
package web

import (
	"strconv"

	"github.com/labstack/echo/v4"
	"github.com/johnpostlethwait/bluforge/templates"
)

const historyPageSize = 50

func (s *Server) handleHistory(c echo.Context) error {
	page, _ := strconv.Atoi(c.QueryParam("page"))
	if page < 1 {
		page = 1
	}

	offset := (page - 1) * historyPageSize
	jobs, _ := s.store.ListAllJobs(historyPageSize+1, offset)

	hasMore := len(jobs) > historyPageSize
	if hasMore {
		jobs = jobs[:historyPageSize]
	}

	var rows []templates.HistoryRow
	for _, j := range jobs {
		rows = append(rows, templates.HistoryRow{
			ID:          j.ID,
			DiscName:    j.DiscName,
			TitleName:   j.TitleName,
			ContentType: j.ContentType,
			OutputPath:  j.OutputPath,
			Status:      j.Status,
			Duration:    j.Duration,
			SizeBytes:   j.SizeBytes,
			CreatedAt:   j.CreatedAt.Format("2006-01-02 15:04"),
		})
	}

	return templates.History(rows, page, hasMore).Render(c.Request().Context(), c.Response().Writer)
}
```

- [ ] **Step 6: Implement settings handlers**

```go
// internal/web/handlers_settings.go
package web

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/labstack/echo/v4"
	"github.com/johnpostlethwait/bluforge/internal/config"
	"github.com/johnpostlethwait/bluforge/templates"
)

func (s *Server) handleSettingsGet(c echo.Context) error {
	data := templates.SettingsData{
		OutputDir:          s.cfg.OutputDir,
		AutoRip:            s.cfg.AutoRip,
		MinTitleLength:     fmt.Sprintf("%d", s.cfg.MinTitleLength),
		PollInterval:       fmt.Sprintf("%d", s.cfg.PollInterval),
		DuplicateAction:    s.cfg.DuplicateAction,
		MovieTemplate:      s.cfg.MovieTemplate,
		SeriesTemplate:     s.cfg.SeriesTemplate,
		GitHubClientID:     s.cfg.GitHubClientID,
		GitHubClientSecret: s.cfg.GitHubClientSecret,
	}
	return templates.Settings(data).Render(c.Request().Context(), c.Response().Writer)
}

func (s *Server) handleSettingsPost(c echo.Context) error {
	s.cfg.OutputDir = c.FormValue("output_dir")
	s.cfg.AutoRip = c.FormValue("auto_rip") == "on"
	s.cfg.MinTitleLength, _ = strconv.Atoi(c.FormValue("min_title_length"))
	s.cfg.PollInterval, _ = strconv.Atoi(c.FormValue("poll_interval"))
	s.cfg.DuplicateAction = c.FormValue("duplicate_action")
	s.cfg.MovieTemplate = c.FormValue("movie_template")
	s.cfg.SeriesTemplate = c.FormValue("series_template")
	s.cfg.GitHubClientID = c.FormValue("github_client_id")
	s.cfg.GitHubClientSecret = c.FormValue("github_client_secret")

	configPath := "/config/config.yaml"
	if err := config.Save(*s.cfg, configPath); err != nil {
		return c.String(http.StatusInternalServerError, fmt.Sprintf("failed to save: %v", err))
	}

	return c.Redirect(http.StatusSeeOther, "/settings")
}
```

- [ ] **Step 7: Generate templates and verify build**

Run: `templ generate && go build ./...`
Expected: Build succeeds

- [ ] **Step 8: Commit**

```bash
git add templates/ internal/web/handlers_*.go
git commit -m "feat: queue, history, and settings pages with handlers"
```

---

### Task 19: Main Entry Point (Wire Everything Together)

**Files:**
- Modify: `main.go`

- [ ] **Step 1: Wire up all components in main.go**

```go
// main.go
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/johnpostlethwait/bluforge/internal/config"
	"github.com/johnpostlethwait/bluforge/internal/db"
	"github.com/johnpostlethwait/bluforge/internal/discdb"
	"github.com/johnpostlethwait/bluforge/internal/drivemanager"
	"github.com/johnpostlethwait/bluforge/internal/makemkv"
	"github.com/johnpostlethwait/bluforge/internal/ripper"
	"github.com/johnpostlethwait/bluforge/internal/web"
)

func main() {
	// Structured logging to stdout for docker logs
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})))

	slog.Info("BluForge starting")

	// Load config: env vars provide defaults, config file overrides
	configPath := "/config/config.yaml"
	cfg := config.Load(configPath)
	slog.Info("config loaded", "port", cfg.Port, "output", cfg.OutputDir, "auto_rip", cfg.AutoRip)

	// Initialize database
	dbPath := "/config/bluforge.db"
	store, err := db.Open(dbPath)
	if err != nil {
		slog.Error("failed to open database", "error", err)
		os.Exit(1)
	}
	defer store.Close()

	// Initialize components
	mkvExec := makemkv.NewExecutor()
	discdbClient := discdb.NewClient()

	// SSE hub for real-time updates
	sseHub := web.NewSSEHub()

	// Drive manager
	driveMgr := drivemanager.NewManager(mkvExec, func(ev drivemanager.DriveEvent) {
		slog.Info("drive event", "type", ev.Type, "drive", ev.DriveIndex, "disc", ev.DiscName)
		data, _ := json.Marshal(ev)
		sseHub.Broadcast(web.SSEEvent{Event: "drive-update", Data: string(data)})
	})

	// Rip engine
	ripEngine := ripper.NewEngine(mkvExec)
	ripEngine.OnUpdate(func(job *ripper.Job) {
		slog.Info("rip update", "drive", job.DriveIndex, "status", job.Status, "progress", job.Progress)
		data, _ := json.Marshal(job)
		sseHub.Broadcast(web.SSEEvent{
			Event: fmt.Sprintf("job-%d", job.ID),
			Data:  string(data),
		})
	})

	// Web server
	server := web.NewServer(web.ServerDeps{
		Config:       &cfg,
		Store:        store,
		DriveManager: driveMgr,
		RipEngine:    ripEngine,
		DiscDBClient: discdbClient,
	})

	// Graceful shutdown
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Start drive manager polling
	go driveMgr.Run(ctx, time.Duration(cfg.PollInterval)*time.Second)

	// Start web server
	go func() {
		if err := server.Start(); err != nil {
			slog.Error("server error", "error", err)
		}
	}()

	slog.Info("BluForge ready", "url", fmt.Sprintf("http://localhost:%d", cfg.Port))

	// Wait for shutdown signal
	<-ctx.Done()
	slog.Info("shutting down...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = shutdownCtx // server.Stop() doesn't need ctx currently
	server.Stop()
	slog.Info("BluForge stopped")
}
```

- [ ] **Step 2: Verify it compiles**

Run: `go build -o bluforge .`
Expected: Binary builds successfully

- [ ] **Step 3: Commit**

```bash
git add main.go
git commit -m "feat: wire up main entry point connecting all subsystems"
```

---

### Task 20: Disk Space Check & Duplicate Handling

**Files:**
- Modify: `internal/ripper/engine.go`
- Create: `internal/ripper/space_test.go`

- [ ] **Step 1: Write failing tests for space check**

```go
// internal/ripper/space_test.go
package ripper

import (
	"testing"
)

func TestCheckDiskSpaceSufficient(t *testing.T) {
	// Use temp dir which should have space
	dir := t.TempDir()
	err := CheckDiskSpace(dir, 1024) // 1KB needed
	if err != nil {
		t.Errorf("expected no error for small size, got: %v", err)
	}
}

func TestCheckDiskSpaceInsufficient(t *testing.T) {
	dir := t.TempDir()
	err := CheckDiskSpace(dir, 1<<62) // impossibly large
	if err == nil {
		t.Error("expected error for impossibly large size")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/ripper/ -v -run TestCheckDiskSpace`
Expected: FAIL — `CheckDiskSpace` not defined

- [ ] **Step 3: Implement space check**

```go
// Add to internal/ripper/engine.go

import "syscall"

// CheckDiskSpace returns an error if the output directory doesn't have enough space.
func CheckDiskSpace(path string, neededBytes int64) error {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return fmt.Errorf("check disk space: %w", err)
	}

	availableBytes := int64(stat.Bavail) * int64(stat.Bsize)
	if availableBytes < neededBytes {
		return fmt.Errorf("insufficient disk space: need %d bytes, have %d bytes available",
			neededBytes, availableBytes)
	}
	return nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/ripper/ -v -run TestCheckDiskSpace`
Expected: All PASS

- [ ] **Step 5: Commit**

```bash
git add internal/ripper/
git commit -m "feat: disk space check before rip start"
```

---

### Task 21: Contribution Handler (TheDiscDB Opt-In Flow)

**Files:**
- Create: `templates/contribute.templ`
- Create: `internal/web/handlers_contribute.go`

- [ ] **Step 1: Create contribution template**

```go
// templates/contribute.templ
package templates

import "fmt"

type ContributeTitleRow struct {
	Index      int
	Name       string
	Duration   string
	SourceFile string
	ItemType   string // pre-selected if known
	Season     string
	Episode    string
}

type ContributeData struct {
	DriveIndex int
	DiscName   string
	Titles     []ContributeTitleRow
	HasGitHub  bool
}

templ Contribute(data ContributeData) {
	@Layout("Contribute to TheDiscDB") {
		<div class="page-header">
			<h1 class="page-title">Contribute Disc Data</h1>
		</div>

		if !data.HasGitHub {
			<div class="card">
				<p class="text-warning">GitHub authentication is not configured. Configure it in
					<a href="/settings">Settings</a> to contribute disc data to TheDiscDB.</p>
			</div>
		} else {
			<div class="card">
				<p class="text-secondary mb-1">
					This disc ({ data.DiscName }) isn't in TheDiscDB yet. Help the community by labeling the titles below.
					Your contribution will be submitted for review.
				</p>
			</div>

			<form method="POST" action={ templ.URL(fmt.Sprintf("/drives/%d/contribute", data.DriveIndex)) }>
				<div class="card">
					<div class="card-header">
						<span class="card-title">Label Titles</span>
					</div>
					<table>
						<thead>
							<tr>
								<th>#</th>
								<th>Name</th>
								<th>Duration</th>
								<th>Source</th>
								<th>Type</th>
								<th>Season</th>
								<th>Episode</th>
							</tr>
						</thead>
						<tbody>
							for _, t := range data.Titles {
								<tr>
									<td>{ fmt.Sprintf("%d", t.Index) }</td>
									<td>{ t.Name }</td>
									<td>{ t.Duration }</td>
									<td class="text-muted">{ t.SourceFile }</td>
									<td>
										<select name={ fmt.Sprintf("type_%d", t.Index) }>
											<option value="">-- Select --</option>
											<option value="MainMovie" if t.ItemType == "MainMovie" { selected }>Main Movie</option>
											<option value="Episode" if t.ItemType == "Episode" { selected }>Episode</option>
											<option value="Extra" if t.ItemType == "Extra" { selected }>Extra</option>
											<option value="Trailer" if t.ItemType == "Trailer" { selected }>Trailer</option>
											<option value="DeletedScene" if t.ItemType == "DeletedScene" { selected }>Deleted Scene</option>
										</select>
									</td>
									<td>
										<input type="text" name={ fmt.Sprintf("season_%d", t.Index) }
											value={ t.Season } style="width: 4rem;"
											placeholder="S"/>
									</td>
									<td>
										<input type="text" name={ fmt.Sprintf("episode_%d", t.Index) }
											value={ t.Episode } style="width: 4rem;"
											placeholder="E"/>
									</td>
								</tr>
							}
						</tbody>
					</table>
				</div>

				<button type="submit" class="btn btn-primary">Submit to TheDiscDB</button>
			</form>
		}
	}
}
```

- [ ] **Step 2: Create contribution handler**

```go
// internal/web/handlers_contribute.go
package web

import (
	"fmt"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/labstack/echo/v4"
	"github.com/johnpostlethwait/bluforge/templates"
)

func (s *Server) handleContributeGet(c echo.Context) error {
	idStr := c.Param("id")
	driveIndex, err := strconv.Atoi(idStr)
	if err != nil {
		return c.String(http.StatusBadRequest, "invalid drive index")
	}

	drive := s.driveMgr.GetDrive(driveIndex)
	if drive == nil {
		return c.String(http.StatusNotFound, "drive not found")
	}

	hasGitHub := s.cfg.GitHubClientID != "" && s.cfg.GitHubClientSecret != ""

	data := templates.ContributeData{
		DriveIndex: driveIndex,
		DiscName:   drive.DiscName(),
		HasGitHub:  hasGitHub,
		// TODO: populate Titles from cached scan
	}

	return templates.Contribute(data).Render(c.Request().Context(), c.Response().Writer)
}

func (s *Server) handleContributePost(c echo.Context) error {
	idStr := c.Param("id")
	driveIndex, err := strconv.Atoi(idStr)
	if err != nil {
		return c.String(http.StatusBadRequest, "invalid drive index")
	}

	// Collect labeled title data from form
	slog.Info("contribution submitted", "drive", driveIndex)

	// TODO: Submit to TheDiscDB contribution API when API access is confirmed
	// For now, log the contribution data and show a message
	// See spec note: need to coordinate with TheDiscDB maintainer (lfoust) first

	slog.Warn("TheDiscDB contribution API integration pending — contribution logged locally")

	return c.Redirect(http.StatusSeeOther, fmt.Sprintf("/drives/%d", driveIndex))
}
```

- [ ] **Step 3: Add routes to server.go**

Add these routes to the `NewServer` function in `internal/web/server.go`:

```go
s.echo.GET("/drives/:id/contribute", s.handleContributeGet)
s.echo.POST("/drives/:id/contribute", s.handleContributePost)
```

- [ ] **Step 4: Generate templates and verify build**

Run: `templ generate && go build ./...`
Expected: Build succeeds

- [ ] **Step 5: Commit**

```bash
git add templates/contribute.templ internal/web/handlers_contribute.go internal/web/server.go
git commit -m "feat: TheDiscDB contribution flow UI and handler (API submission pending maintainer coordination)"
```

---

### Task 22: Integration Test — Full Rip Flow with Mocks

**Files:**
- Create: `internal/integration_test.go`

- [ ] **Step 1: Write integration test covering scan -> match -> rip flow**

```go
// internal/integration_test.go
package internal

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/johnpostlethwait/bluforge/internal/discdb"
	"github.com/johnpostlethwait/bluforge/internal/drivemanager"
	"github.com/johnpostlethwait/bluforge/internal/makemkv"
	"github.com/johnpostlethwait/bluforge/internal/organizer"
	"github.com/johnpostlethwait/bluforge/internal/ripper"
	"github.com/johnpostlethwait/bluforge/testutil"
)

type fullMockExecutor struct{}

func (m *fullMockExecutor) ListDrives(ctx context.Context) ([]makemkv.DriveInfo, error) {
	reader := strings.NewReader(testutil.SampleDriveListOutput)
	events, _ := makemkv.ParseAll(reader)
	var drives []makemkv.DriveInfo
	for _, ev := range events {
		if ev.Type == "DRV" && ev.Drive != nil {
			drives = append(drives, *ev.Drive)
		}
	}
	return drives, nil
}

func (m *fullMockExecutor) ScanDisc(ctx context.Context, driveIndex int) (*makemkv.DiscScan, error) {
	reader := strings.NewReader(testutil.SampleDiscInfoOutput)
	events, _ := makemkv.ParseAll(reader)
	scan := &makemkv.DiscScan{DriveIndex: driveIndex}
	titleMap := make(map[int]*makemkv.TitleInfo)

	for _, ev := range events {
		switch ev.Type {
		case "TCOUT":
			scan.TitleCount = ev.Count
		case "CINFO":
			if ev.Disc != nil {
				if name, ok := ev.Disc.Attributes[2]; ok {
					scan.DiscName = name
				}
			}
		case "TINFO":
			if ev.Title != nil {
				existing, ok := titleMap[ev.Title.Index]
				if !ok {
					t := *ev.Title
					titleMap[ev.Title.Index] = &t
				} else {
					for k, v := range ev.Title.Attributes {
						existing.Attributes[k] = v
					}
				}
			}
		}
	}
	for i := 0; i < len(titleMap); i++ {
		if t, ok := titleMap[i]; ok {
			scan.Titles = append(scan.Titles, *t)
		}
	}
	return scan, nil
}

func (m *fullMockExecutor) StartRip(ctx context.Context, driveIndex int, titleID int, outputDir string, onEvent func(makemkv.Event)) error {
	reader := strings.NewReader(testutil.SampleProgressOutput)
	events, _ := makemkv.ParseAll(reader)
	for _, ev := range events {
		if onEvent != nil {
			onEvent(ev)
		}
	}
	return nil
}

func TestFullRipFlow(t *testing.T) {
	mock := &fullMockExecutor{}

	// 1. Drive manager detects disc
	var insertEvent drivemanager.DriveEvent
	mgr := drivemanager.NewManager(mock, func(ev drivemanager.DriveEvent) {
		if ev.Type == drivemanager.EventDiscInserted {
			insertEvent = ev
		}
	})
	mgr.PollOnce(context.Background())

	if insertEvent.DiscName != "DEADPOOL_2" {
		t.Fatalf("expected DEADPOOL_2, got %s", insertEvent.DiscName)
	}

	// 2. Scanner gets disc info
	scan, err := mock.ScanDisc(context.Background(), 0)
	if err != nil {
		t.Fatalf("scan failed: %v", err)
	}
	if scan.TitleCount != 3 {
		t.Fatalf("expected 3 titles, got %d", scan.TitleCount)
	}

	// 3. Matcher matches titles against mock TheDiscDB data
	disc := discdb.Disc{
		Titles: []discdb.DiscTitle{
			{SourceFile: "00001.mpls", ItemType: "MainMovie", Item: &discdb.ContentItem{Title: "Deadpool 2"}},
			{SourceFile: "00002.mpls", ItemType: "MainMovie", Item: &discdb.ContentItem{Title: "Deadpool 2 Super Duper Cut"}},
			{SourceFile: "00010.mpls", ItemType: "Extra", Item: &discdb.ContentItem{Title: "Gag Reel"}},
		},
	}
	matches := discdb.MatchTitles(scan, disc)

	matchedCount := 0
	for _, m := range matches {
		if m.Matched {
			matchedCount++
		}
	}
	if matchedCount != 3 {
		t.Errorf("expected 3 matched titles, got %d", matchedCount)
	}

	// 4. Organizer builds output paths
	org := organizer.New(
		"Movies/{{.Title}} ({{.Year}})/{{.Title}} ({{.Year}})",
		"",
	)
	path, err := org.BuildMoviePath(organizer.MovieMeta{Title: "Deadpool 2", Year: "2018"})
	if err != nil {
		t.Fatalf("build path: %v", err)
	}
	if path != "Movies/Deadpool 2 (2018)/Deadpool 2 (2018).mkv" {
		t.Errorf("unexpected path: %s", path)
	}

	// 5. Rip engine runs the rip
	engine := ripper.NewEngine(mock)
	job := ripper.NewJob(0, 0, "DEADPOOL_2", t.TempDir())

	var finalProgress int
	engine.OnUpdate(func(j *ripper.Job) {
		finalProgress = j.Progress
	})

	err = engine.Submit(job)
	if err != nil {
		t.Fatalf("submit failed: %v", err)
	}

	// Wait for completion
	time.Sleep(200 * time.Millisecond)

	if finalProgress != 100 {
		t.Errorf("expected final progress 100, got %d", finalProgress)
	}
}
```

- [ ] **Step 2: Run integration test**

Run: `go test ./internal/ -v -run TestFullRipFlow`
Expected: PASS

- [ ] **Step 3: Commit**

```bash
git add internal/integration_test.go
git commit -m "test: full rip flow integration test with mocked MakeMKV and TheDiscDB"
```

---

### Task 23: Final Build Verification & Docker Build

- [ ] **Step 1: Run all tests**

Run: `go test ./... -v`
Expected: All PASS

- [ ] **Step 2: Build the binary**

Run: `go build -o bluforge .`
Expected: Binary builds successfully

- [ ] **Step 3: Verify Docker build** (if Docker is available)

Run: `docker build -t bluforge:dev .`
Expected: Image builds successfully (may fail if no Docker — that's OK for development)

- [ ] **Step 4: Final commit with any fixes**

```bash
git add -A
git commit -m "chore: final build verification and cleanup"
```

---

## Spec Coverage Check

| Spec Section | Task(s) |
|---|---|
| Tech Stack (Go, Echo, Templ, HTMX, SSE, SQLite) | 1, 15, 16 |
| Drive Manager (polling, state machine, events) | 8, 9 |
| Disc Scanner (makemkvcon info, parsing) | 3, 4 |
| Content Identifier (TheDiscDB matching) | 10, 11 |
| Rip Engine (concurrent rips, progress) | 13 |
| Robot-Mode Parser | 3 |
| TheDiscDB GraphQL Client | 10 |
| Matching Strategy (auto + user search) | 11, 17 |
| Remembered Mappings | 7 (mappings CRUD) |
| TheDiscDB Cache | 12 |
| Contribution Flow (opt-in) | 21 |
| Output Organization (templates, paths) | 6 |
| Filename Sanitization (cross-platform) | 5 |
| Duplicate Handling | 6, 20 |
| Data Model (entities, SQLite) | 7 |
| Configuration (env -> file -> UI) | 2, 18 |
| Docker (Dockerfile, compose) | 1, 23 |
| Web UI (Dashboard, Drive Detail, Queue, History, Settings) | 15, 16, 17, 18 |
| SSE (real-time updates) | 14 |
| Dark theme with blue accents | 15 |
| Error Handling (logging to stdout/stderr) | 19 |
| Disk Space Check | 20 |
| Multi-drive concurrent rips | 9, 13 |
| Integration Test | 22 |
