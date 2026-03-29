# BluForge Integration Layer Design

## Goal

Wire BluForge's independently-built subsystems (rip engine, organizer, database, TheDiscDB client, cache, drive manager) into a functioning end-to-end pipeline. After this work, inserting a disc and clicking "Rip" produces organized, properly named media files with full job history — and auto-rip mode works autonomously.

## Scope

This design covers six integration gaps and one portability fix identified in the code review:

1. **Workflow Orchestrator** — new package that owns the rip pipeline
2. **Rip Engine completion hooks** — per-job callbacks for post-rip actions
3. **Config thread safety** — synchronized access to shared AppConfig
4. **TheDiscDB cache & disc mappings** — wire existing implementations into handlers
5. **Error surfacing** — propagate errors to the UI instead of swallowing them
6. **Contribute handler** — redirect to TheDiscDB web instead of stub
7. **CheckDiskSpace portability** — build tag for Unix-only syscall

Out of scope: TheDiscDB contribution API integration (deferred — redirect to their web UI instead).

---

## 1. Workflow Orchestrator

New package: `internal/workflow/`

### Orchestrator struct

```go
type Orchestrator struct {
    store     *db.Store
    engine    *ripper.Engine
    organizer *organizer.Organizer
    discdb    *discdb.Client
    cache     *discdb.Cache
    driveMgr  *drivemanager.Manager
    sseHub    *web.SSEHub
}
```

Config values (output dir, templates, auto-rip flag) are passed at call time by the caller, not held as a pointer. This avoids the orchestrator participating in config synchronization.

### Public Methods

#### ManualRip

```go
func (o *Orchestrator) ManualRip(ctx context.Context, params ManualRipParams) (*RipResult, error)
```

`ManualRipParams` contains: drive index, title selections (index + metadata per title), release match data (movie vs series, title, year, season, episode info), output dir, movie/series templates, duplicate action.

Flow per title:
1. Check disk space via `CheckDiskSpace(outputDir, estimatedBytes)`
2. Build the destination path via organizer (`BuildMoviePath` / `BuildSeriesPath` / `BuildUnmatchedPath`)
3. Check for duplicates — if file exists at destination, apply duplicate action (skip or overwrite)
4. Create DB job via `store.CreateJob()`
5. Create `ripper.Job` with an `OnComplete` callback
6. Submit to engine
7. On completion callback: `organizer.AtomicMove(tempFile, destPath)` → `store.UpdateJobOutput(jobID, finalPath)` → `store.UpdateJobStatus(jobID, "completed")` → SSE broadcast
8. On failure callback: `store.UpdateJobStatus(jobID, "failed")` → SSE broadcast

After all titles submitted: save disc mapping via `store.SaveMapping(discKey, releaseJSON)`.

Returns `RipResult` containing per-title status: submitted, skipped (duplicate), or failed (with reason).

#### AutoRip

```go
func (o *Orchestrator) AutoRip(ctx context.Context, event drivemanager.DriveEvent, cfg AutoRipConfig) error
```

`AutoRipConfig` contains: output dir, movie/series templates, duplicate action.

Flow:
1. Only acts on `EventDiscInserted`
2. Scan disc via `driveMgr.exec.ScanDisc(driveIndex)`
3. Build disc key via `discdb.BuildDiscKey(scan)`
4. Check `store.GetMapping(discKey)` — if found, use cached release data
5. If no mapping: auto-search by disc name via `discdb.SearchByTitle(discName)`
6. Score results via `discdb.BestRelease(scan, results)` — require UPC/ASIN match for confidence
7. If high-confidence match: proceed with match data
8. If no confident match: use unmatched path (`Unmatched/{DiscName}/`)
9. Submit all titles through the same persist → rip → organize pipeline as ManualRip

#### Rescan

```go
func (o *Orchestrator) Rescan(ctx context.Context, driveIndex int) error
```

Flow:
1. Get current drive info from drive manager
2. Build disc key from current scan
3. Delete mapping via `store.DeleteMapping(discKey)`
4. Return (handler redirects to drive detail, which shows fresh search UI)

---

## 2. Rip Engine Changes

### Per-Job Completion Hook

Add to `ripper.Job`:

```go
OnComplete func(job *Job, err error)
```

The `Engine.run()` method calls this after the rip finishes (success or failure), passing `nil` error on success or the rip error on failure. The engine removes the job from its `active` map first, then calls `OnComplete` — this frees the drive for new rips immediately while post-rip actions (file organization, DB persistence) run in the same goroutine.

The orchestrator sets `OnComplete` when creating each job. The hook handles: file organization, DB persistence, SSE broadcast. Since the hook runs after the drive is freed, slow file moves don't block new rips on the same drive.

The engine's existing `OnUpdate` callback continues to handle progress broadcasting (SSE). The new `OnComplete` handles post-rip actions. These are separate concerns.

### Disk Space Check

`CheckDiskSpace` in `internal/ripper/engine.go` gets a `//go:build !windows` build tag. A stub `checkdiskspace_windows.go` with `//go:build windows` returns `nil` (always passes). The orchestrator calls `CheckDiskSpace` before submitting each job.

---

## 3. Config Thread Safety

Add to `Server`:

```go
type Server struct {
    // ... existing fields ...
    cfgMu sync.RWMutex
}
```

Two new methods:

```go
func (s *Server) getConfig() config.AppConfig {
    s.cfgMu.RLock()
    defer s.cfgMu.RUnlock()
    return *s.cfg  // copy by value
}

func (s *Server) updateConfig(fn func(*config.AppConfig)) error {
    s.cfgMu.Lock()
    defer s.cfgMu.Unlock()
    fn(s.cfg)
    return config.Save(*s.cfg, "/config/config.yaml")
}
```

All handlers that read config call `s.getConfig()`. `handleSettingsSave` calls `s.updateConfig(fn)`. The orchestrator receives config values (output dir, templates, etc.) as parameters at call time — it never holds a config pointer.

---

## 4. TheDiscDB Cache & Disc Mappings

### Cache Integration

The `discdb.Cache` is instantiated in `main.go` using the same SQLite `*db.Store` connection and passed to the orchestrator.

Search flow in handlers:

1. Build cache key: `"{searchType}:{query}"` (e.g., `"title:Deadpool 2"`)
2. Check `cache.Get(key)` — if hit, deserialize and return
3. If miss, call `discdbClient.SearchBy*()`
4. On success, serialize results and `cache.Set(key, data)`
5. Return results to template

Cache TTL defaults to 24 hours (configurable in AppConfig if needed later).

### Disc Mappings Integration

Disc key built via `discdb.BuildDiscKey(scan)` (SHA256 of disc name + title count + segment maps — already implemented).

- **Save:** `ManualRip()` calls `store.SaveMapping(discKey, serializedReleaseData)` after successful rip submission
- **Load:** Drive detail handler checks `store.GetMapping(discKey)` — if found, pre-populates match data in the template (skips search step)
- **Delete:** `Rescan()` calls `store.DeleteMapping(discKey)` to clear the remembered mapping

Mapping data stored as JSON containing: release info, content matches per title, movie/series metadata.

---

## 5. Error Surfacing

### Search Errors

The search results template data gains an `Error string` field. When `discdbClient.SearchBy*()` fails:

- Set `Error: "Search failed — TheDiscDB may be unavailable. Please try again."`
- Render the template with the error — the template shows an alert banner when Error is non-empty
- Log the actual error to stdout for Docker log debugging

### Rip Submission Errors

`ManualRip()` returns `*RipResult` with per-title outcomes:

```go
type TitleResult struct {
    TitleIndex int
    Status     string // "submitted", "skipped", "failed"
    Reason     string // empty on success, explanation on skip/fail
}

type RipResult struct {
    Titles []TitleResult
}
```

The handler inspects the result:
- All submitted: redirect to `/queue`
- Some/all failed: redirect to `/drives/:id?error={url-encoded message}` with a summary of what failed

The drive detail template checks for the `error` query parameter and renders an alert banner.

### Logging

All errors are logged to stdout via `slog.Error()` per the original spec requirement that all errors surface in `docker logs`.

---

## 6. Contribute Handler

Replace the stub `handleContributeSubmit` and the contribute form template:

- The contribute page becomes informational: explains that contributions are made through TheDiscDB's website
- Displays the current disc name and basic info for reference
- Provides a link to `https://thediscdb.com/contribute`
- Remove the form fields that pretend to collect contribution data

`handleContributeSubmit` is removed (no form to submit). The `POST /drives/:id/contribute` route is removed. Only the `GET /drives/:id/contribute` route remains, serving the informational page.

---

## 7. Main.go Wiring Changes

The `main.go` event callback gains auto-rip logic:

```go
driveMgr := drivemanager.NewManager(executor, func(ev drivemanager.DriveEvent) {
    // Existing: log + SSE broadcast
    slog.Info("drive event", ...)
    sseHub.Broadcast(...)

    // New: auto-rip trigger
    if ev.Type == drivemanager.EventDiscInserted && cfg.AutoRip {
        go orchestrator.AutoRip(context.Background(), ev, workflow.AutoRipConfig{
            OutputDir:       cfg.OutputDir,
            MovieTemplate:   cfg.MovieTemplate,
            SeriesTemplate:  cfg.SeriesTemplate,
            DuplicateAction: cfg.DuplicateAction,
        })
    }
})
```

Config values are read at event time (after the config mutex is released by the server) and passed as a snapshot to `AutoRip`.

### Dependency Construction Order in main.go

```
1. config.Load()
2. db.Open()
3. makemkv.NewExecutor()
4. discdb.NewClient()
5. discdb.NewCache(store)        // NEW
6. web.NewSSEHub()
7. organizer.New(cfg.MovieTemplate, cfg.SeriesTemplate)  // NEW
8. ripper.NewEngine(executor)
9. workflow.NewOrchestrator(...)  // NEW — receives store, engine, organizer, discdb, cache, sseHub
10. drivemanager.NewManager(executor, eventCallback)  // callback references orchestrator
11. web.NewServer(deps)          // deps gains orchestrator
```

---

## Testing Strategy

### Unit Tests: Orchestrator (`internal/workflow/`)

All dependencies mocked via interfaces. Tests:

- `TestManualRip_Success` — verify: DB job created, rip submitted, on-complete organizes file, DB updated with final path, mapping saved
- `TestManualRip_DiskSpaceFail` — verify: returns error for title, no job created
- `TestManualRip_DuplicateSkip` — verify: file exists + action=skip → title skipped in result
- `TestManualRip_EngineReject` — verify: drive already ripping → error in result, not silent
- `TestAutoRip_WithMapping` — verify: mapping found → skip search → rip with cached data
- `TestAutoRip_SearchMatch` — verify: no mapping → search → high confidence → rip
- `TestAutoRip_NoMatch` — verify: no mapping → search → no confidence → rip to Unmatched
- `TestAutoRip_Disabled` — verify: auto-rip=false → no action
- `TestRescan` — verify: mapping deleted

### Unit Tests: Config Thread Safety

- `TestGetConfig_ConcurrentAccess` — multiple goroutines read config, run with `-race`
- `TestUpdateConfig_Persists` — update config → read YAML file → verify values

### Unit Tests: Cache in Search Flow

- `TestSearch_CacheHit` — mock cache returns data → API not called
- `TestSearch_CacheMiss` — mock cache returns nil → API called → cache.Set called
- `TestSearch_APIError_SurfacedToTemplate` — API fails → error field set in template data

### Integration Test (extend `internal/integration_test.go`)

- `TestFullPipeline_ManualRip` — detect → search → match → rip → verify DB job + organized file path
- `TestFullPipeline_AutoRip_WithMapping` — insert disc → mapping exists → auto-rip → verify organized output
- `TestFullPipeline_AutoRip_Unmatched` — insert disc → no match → verify Unmatched folder path
- `TestFullPipeline_Rescan` — save mapping → rescan → verify mapping deleted

### Handler Tests

- `TestHandleDriveSearch_Error` — mock discdb failure → verify error in response
- `TestHandleDriveRip_PartialFailure` — some titles fail → verify error flash in redirect
