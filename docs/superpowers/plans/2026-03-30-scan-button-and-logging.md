# Scan Button Fix & Scan Logging Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Move the Scan button into the Titles card and add structured logging + error surfacing throughout the scan flow so failures are visible in both Docker logs and the UI.

**Architecture:** Add `slog` calls at each layer of the scan pipeline (runner → executor → orchestrator → handler). Add a `scanError` field to the Alpine store and display it in the Titles card. Move the Scan button HTML from its standalone card into the Titles card header.

**Tech Stack:** Go (`log/slog`), Templ templates, Alpine.js, Echo HTTP framework

---

### Task 1: Add logging to `realRunner.Run()` in executor

**Files:**
- Modify: `internal/makemkv/executor.go:25-36`

- [ ] **Step 1: Add `log/slog` import and logging to `realRunner.Run()`**

Add `"log/slog"` to the import block (which currently has `"context"`, `"fmt"`, `"os/exec"`, `"strings"`, `"time"`).

Replace the `Run` method:

```go
func (r *realRunner) Run(ctx context.Context, args ...string) (*strings.Reader, error) {
	ctx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()

	slog.Info("makemkvcon: executing", "args", args)

	cmd := exec.CommandContext(ctx, "makemkvcon", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		slog.Error("makemkvcon: command failed", "args", args, "error", err, "output_bytes", len(out))
		// Return output even on error so callers can inspect messages.
		return strings.NewReader(string(out)), err
	}

	slog.Info("makemkvcon: command completed", "args", args, "output_bytes", len(out))
	return strings.NewReader(string(out)), nil
}
```

- [ ] **Step 2: Run tests to verify no regressions**

Run: `go test ./internal/makemkv/ -v`
Expected: All existing tests pass (tests use mockCmdRunner so the new logging in realRunner is not exercised, but the file must compile).

- [ ] **Step 3: Run vet**

Run: `go vet ./internal/makemkv/`
Expected: No issues

---

### Task 2: Add logging to `Executor.ScanDisc()` in executor

**Files:**
- Modify: `internal/makemkv/executor.go:113-203`

- [ ] **Step 1: Add logging around the scan in `ScanDisc()`**

Replace the `ScanDisc` method. The only changes are three `slog` calls — one at entry, one on error, one on success:

```go
func (e *Executor) ScanDisc(ctx context.Context, driveIndex int) (*DiscScan, error) {
	slog.Info("executor: starting disc scan", "drive_index", driveIndex)

	target := fmt.Sprintf("disc:%d", driveIndex)
	r, err := e.runner.Run(ctx, "-r", "info", target)
	if err != nil {
		slog.Error("executor: disc scan command failed", "drive_index", driveIndex, "error", err)
		return nil, fmt.Errorf("makemkv: scan disc %d: %w", driveIndex, err)
	}

	events, err := ParseAll(r)
	if err != nil {
		slog.Error("executor: disc scan parse failed", "drive_index", driveIndex, "error", err)
		return nil, fmt.Errorf("makemkv: scan disc %d parse: %w", driveIndex, err)
	}

	scan := &DiscScan{DriveIndex: driveIndex}
	discAttrs := make(map[int]string)
	titleMap := make(map[int]*TitleInfo)      // title index -> merged TitleInfo
	streamMap := make(map[int][]StreamInfo)   // title index -> accumulated streams
	// Track per-title, per-stream accumulated attributes.
	type streamKey struct{ title, stream int }
	streamAttrMap := make(map[streamKey]map[int]string)

	for _, ev := range events {
		switch ev.Type {
		case "TCOUT":
			scan.TitleCount = ev.Count

		case "CINFO":
			if ev.Disc != nil {
				for k, v := range ev.Disc.Attributes {
					discAttrs[k] = v
				}
			}

		case "TINFO":
			if ev.Title == nil {
				continue
			}
			ti, ok := titleMap[ev.Title.Index]
			if !ok {
				ti = &TitleInfo{
					Index:      ev.Title.Index,
					Attributes: make(map[int]string),
				}
				titleMap[ev.Title.Index] = ti
			}
			for k, v := range ev.Title.Attributes {
				ti.Attributes[k] = v
			}

		case "SINFO":
			if ev.Stream == nil {
				continue
			}
			sk := streamKey{ev.Stream.TitleIndex, ev.Stream.StreamIndex}
			if streamAttrMap[sk] == nil {
				streamAttrMap[sk] = make(map[int]string)
			}
			for k, v := range ev.Stream.Attributes {
				streamAttrMap[sk][k] = v
			}

		case "MSG":
			if ev.Message != nil {
				scan.Messages = append(scan.Messages, *ev.Message)
			}
		}
	}

	// Build the merged disc metadata.
	discInfo := &DiscInfo{Attributes: discAttrs}
	scan.DiscName = discInfo.Name()
	scan.DiscType = discInfo.Type()

	// Flatten streamAttrMap into per-title stream slices.
	for sk, attrs := range streamAttrMap {
		si := StreamInfo{
			TitleIndex:  sk.title,
			StreamIndex: sk.stream,
			Attributes:  attrs,
		}
		streamMap[sk.title] = append(streamMap[sk.title], si)
	}

	// Build ordered title list.
	scan.Titles = make([]TitleInfo, 0, len(titleMap))
	for idx, ti := range titleMap {
		ti.Streams = streamMap[idx]
		scan.Titles = append(scan.Titles, *ti)
	}

	slog.Info("executor: disc scan completed", "drive_index", driveIndex, "disc_name", scan.DiscName, "title_count", len(scan.Titles))

	return scan, nil
}
```

- [ ] **Step 2: Run tests**

Run: `go test ./internal/makemkv/ -v`
Expected: All tests pass

---

### Task 3: Add logging to `Orchestrator.ScanDisc()`

**Files:**
- Modify: `internal/workflow/orchestrator.go:218-234`

- [ ] **Step 1: Add logging to `ScanDisc()`**

Replace the method:

```go
func (o *Orchestrator) ScanDisc(ctx context.Context, driveIndex int) (*makemkv.DiscScan, error) {
	if o.scanner == nil {
		slog.Error("orchestrator: scan requested but no scanner configured")
		return nil, fmt.Errorf("no scanner configured")
	}

	slog.Info("orchestrator: starting disc scan", "drive_index", driveIndex)

	scan, err := o.scanner.ScanDisc(ctx, driveIndex)
	if err != nil {
		slog.Error("orchestrator: disc scan failed", "drive_index", driveIndex, "error", err)
		return nil, err
	}

	key := fmt.Sprintf("%d:%s", driveIndex, scan.DiscName)
	o.scanMu.Lock()
	o.scanCache[key] = scan
	o.scanMu.Unlock()

	slog.Info("orchestrator: disc scan cached", "drive_index", driveIndex, "cache_key", key)

	return scan, nil
}
```

- [ ] **Step 2: Run tests**

Run: `go test ./internal/workflow/ -v`
Expected: All tests pass

---

### Task 4: Add logging to `handleDriveScan()` handler

**Files:**
- Modify: `internal/web/handlers_drive.go:233-274`

- [ ] **Step 1: Add logging at entry and exit of `handleDriveScan()`**

Replace the method:

```go
func (s *Server) handleDriveScan(c echo.Context) error {
	idx, err := parseDriveIndex(c)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid drive id")
	}

	slog.Info("scan requested", "drive_index", idx)

	drv := s.driveMgr.GetDrive(idx)
	if drv == nil {
		slog.Warn("scan requested for unknown drive", "drive_index", idx)
		return echo.NewHTTPError(http.StatusNotFound, "drive not found")
	}

	if s.orchestrator == nil {
		slog.Error("scan requested but orchestrator not configured")
		return echo.NewHTTPError(http.StatusServiceUnavailable, "scanner not configured")
	}

	scan, scanErr := s.orchestrator.ScanDisc(c.Request().Context(), idx)
	if scanErr != nil {
		slog.Error("disc scan failed", "drive_index", idx, "error", scanErr)
		return echo.NewHTTPError(http.StatusInternalServerError, fmt.Sprintf("disc scan failed: %v", scanErr))
	}

	// Save disc mapping if a release was selected in the session.
	if session := s.driveSessions.Get(idx); session != nil && session.ReleaseID != "" && s.store != nil {
		discKey := discdb.BuildDiscKey(scan)
		if discKey != "" {
			if err := s.store.SaveMapping(db.DiscMapping{
				DiscKey:     discKey,
				MediaItemID: session.MediaItemID,
				ReleaseID:   session.ReleaseID,
				MediaTitle:  session.MediaTitle,
				MediaYear:   session.MediaYear,
				MediaType:   session.MediaType,
			}); err != nil {
				slog.Warn("failed to save disc mapping", "disc_key", discKey, "error", err)
			}
		}
	}

	titles := scanToTitleJSON(scan)
	slog.Info("scan completed", "drive_index", idx, "title_count", len(titles))
	return c.JSON(http.StatusOK, titles)
}
```

Note: The error message now includes the underlying error (`fmt.Sprintf("disc scan failed: %v", scanErr)`) so the JS can display it. This requires adding `"fmt"` to the import if not already present — but it is already imported in `handlers_drive.go`.

- [ ] **Step 2: Run tests**

Run: `go test ./internal/web/ -v`
Expected: All tests pass

- [ ] **Step 3: Run full test suite + vet**

Run: `go vet ./... && go test ./...`
Expected: All pass, no vet warnings

- [ ] **Step 4: Commit logging changes**

```bash
git add internal/makemkv/executor.go internal/workflow/orchestrator.go internal/web/handlers_drive.go
git commit -m "feat: add structured logging throughout disc scan pipeline

Adds slog calls at each layer: realRunner.Run(), Executor.ScanDisc(),
Orchestrator.ScanDisc(), and handleDriveScan(). This ensures every scan
attempt produces Docker-visible log output for debugging failures."
```

---

### Task 5: Add `scanError` to Alpine store and surface errors in JS

**Files:**
- Modify: `internal/web/json_helpers.go:53-66` (add `ScanError` field to `DriveStoreJSON`)
- Modify: `templates/drive_detail.templ:89-103` (update `scanDisc()` JS to capture errors)
- Modify: `templates/drive_detail.templ:198-212` (add error display in Titles card)

- [ ] **Step 1: Add `ScanError` field to `DriveStoreJSON`**

In `internal/web/json_helpers.go`, add the `ScanError` field to the struct:

```go
type DriveStoreJSON struct {
	DriveIndex      int                  `json:"driveIndex"`
	DriveName       string               `json:"driveName"`
	DiscName        string               `json:"discName"`
	State           string               `json:"state"`
	Scanning        bool                 `json:"scanning"`
	ScanError       string               `json:"scanError"`
	HasMapping      bool                 `json:"hasMapping"`
	MatchedMedia    string               `json:"matchedMedia"`
	MatchedRelease  string               `json:"matchedRelease"`
	Titles          []TitleJSON          `json:"titles"`
	SelectedRelease *SelectedReleaseJSON `json:"selectedRelease"`
	SearchResults   []SearchResultJSON   `json:"searchResults"`
	RipProgress     interface{}          `json:"ripProgress"`
}
```

- [ ] **Step 2: Update `scanDisc()` JS to capture and display errors**

In `templates/drive_detail.templ`, replace the `scanDisc` function (lines 89-103):

```javascript
async function scanDisc(driveIndex) {
	Alpine.store('drive').scanning = true
	Alpine.store('drive').scanError = ''
	Alpine.store('drive').titles = []

	try {
		const resp = await fetch('/drives/' + driveIndex + '/scan', {
			method: 'POST',
			headers: { 'Accept': 'application/json' },
		})

		if (resp.ok) {
			Alpine.store('drive').titles = await resp.json()
		} else {
			const text = await resp.text()
			Alpine.store('drive').scanError = text || 'Scan failed (HTTP ' + resp.status + ')'
		}
	} catch (err) {
		Alpine.store('drive').scanError = 'Scan request failed: ' + err.message
	} finally {
		Alpine.store('drive').scanning = false
	}
}
```

- [ ] **Step 3: Add error display in the Titles card**

In `templates/drive_detail.templ`, inside the Titles card (after the `<div class="section-title">Titles</div>` line, before the scanning template), add an error template block:

```html
<template x-if="$store.drive.scanError">
	<div class="alert alert-error mt-3">
		<strong>Scan failed:</strong> <span x-text="$store.drive.scanError"></span>
	</div>
</template>
```

The Titles card section (lines 198-259) should become:

```
<!-- Titles Card -->
<div class="card" x-data>
	<div class="section-title">Titles</div>

	<template x-if="$store.drive.scanError">
		<div class="alert alert-error mt-3">
			<strong>Scan failed:</strong> <span x-text="$store.drive.scanError"></span>
		</div>
	</template>

	<template x-if="$store.drive.scanning">
		... (unchanged)
	</template>

	<template x-if="!$store.drive.scanning && $store.drive.titles.length === 0">
		... (unchanged)
	</template>

	... (rest unchanged)
</div>
```

- [ ] **Step 4: Run vet + tests**

Run: `go vet ./... && go test ./...`
Expected: All pass

---

### Task 6: Move Scan button into the Titles card

**Files:**
- Modify: `templates/drive_detail.templ:192-200`

- [ ] **Step 1: Remove the standalone Scan button card and move it into the Titles card**

Delete the standalone scan button card (lines 193-196):

```html
<!-- Scan Button -->
<div class="card" style="margin-bottom: 1rem;" x-data>
	@templ.Raw(fmt.Sprintf(`<button class="btn btn-primary" x-on:click="scanDisc(%d)" x-bind:disabled="$store.drive.scanning || $store.drive.state === 'empty'"><span x-show="!$store.drive.scanning">Scan Disc</span><span x-show="$store.drive.scanning">Scanning…</span></button>`, data.DriveIndex))
</div>
```

In the Titles card, update the header to include the button inline with the title. Replace:

```html
<div class="section-title">Titles</div>
```

With:

```html
<div style="display:flex; justify-content:space-between; align-items:center;">
	<div class="section-title" style="margin-bottom:0;">Titles</div>
	@templ.Raw(fmt.Sprintf(`<button class="btn btn-primary btn-sm" x-on:click="scanDisc(%d)" x-bind:disabled="$store.drive.scanning || $store.drive.state === 'empty'"><span x-show="!$store.drive.scanning">Scan Disc</span><span x-show="$store.drive.scanning">Scanning…</span></button>`, data.DriveIndex))
</div>
```

- [ ] **Step 2: Run `templ generate`**

Run: `templ generate`
Expected: Generates updated `_templ.go` file without errors

- [ ] **Step 3: Run vet + tests**

Run: `go vet ./... && go test ./...`
Expected: All pass

- [ ] **Step 4: Commit template changes**

```bash
git add templates/drive_detail.templ templates/drive_detail_templ.go internal/web/json_helpers.go
git commit -m "feat: move Scan button into Titles card and surface scan errors in UI

Moves the Scan Disc button from its own card into the Titles section
header. Adds scanError field to Alpine store and error display in the
Titles card. Updates scanDisc() JS to catch and display HTTP errors and
network failures."
```
