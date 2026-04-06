# Active Rip Step 5 UX Design

**Date:** 2026-04-05  
**Status:** Approved

## Problem

When a drive is actively ripping, clicking it on the dashboard brings the user to Step 5 ("Confirm & Rip") of the disc wizard. This page is incorrectly showing:

- "Ripping 0 title(s)" — `selectedTitleCount()` reads from `titles.filter(t => t.selected)`, which is empty when arriving via an active rip (no Alpine selection state)
- A live "Start Rip" button that could trigger a duplicate rip
- The "If file already exists:" duplicate action dropdown (irrelevant during an active rip)
- No way to navigate to the Activity page to see actual progress

## Solution Overview

Add an `ripActive` boolean flag and `activeJobCount` int to the drive Alpine store. Step 5 branches on `ripActive` to show either the pre-rip confirmation form (unchanged) or an in-progress view.

## Section 1: Data Model

### `DriveStoreJSON` (json_helpers.go)

Add two new fields:

```go
RipActive      bool `json:"ripActive"`
ActiveJobCount int  `json:"activeJobCount"`
```

### `handleDriveDetail` (handlers_drive.go)

In the existing `ripEngine.IsActive(idx)` branch (which already sets `currentStep = 5`), also set:

```go
driveStore.RipActive = true
for _, j := range s.ripEngine.ActiveJobs() {
    if j.DriveIndex == idx {
        driveStore.ActiveJobCount++
    }
}
```

## Section 2: Alpine State

### SSE `rip-update` listener (drive_detail.templ)

A closure-scope `Set` (outside Alpine, so it isn't proxied) tracks which job IDs are currently active. This handles both the page-load case (server pre-populates `ripActive`/`activeJobCount`) and the mid-session case (rip starts while the user is already on the page).

Extend the existing listener that currently only sets `currentStep = 5`. Declare `const _ripJobIDs = new Set()` in the `alpine:init` closure alongside the `evtSource` setup, then:

```js
const _ripJobIDs = new Set()

evtSource.addEventListener('rip-update', (e) => {
    const update = JSON.parse(e.data)
    if (update.DriveIndex !== Alpine.store('drive').driveIndex) return

    const store = Alpine.store('drive')
    if (store.currentStep !== 5) store.currentStep = 5

    if (update.Status === 'ripping' || update.Status === 'organizing') {
        _ripJobIDs.add(update.ID)
    } else if (update.Status === 'completed' || update.Status === 'failed' || update.Status === 'skipped') {
        _ripJobIDs.delete(update.ID)
    }

    store.ripActive = _ripJobIDs.size > 0
    // Use max so the count never drops below what the server told us
    // until SSE has given us a complete picture of active jobs.
    store.activeJobCount = _ripJobIDs.size > 0
        ? Math.max(store.activeJobCount, _ripJobIDs.size)
        : 0
})
```

No change needed to the `disc_ejected` handler — it already resets `currentStep = 1` which implicitly hides step 5.

No change needed to the `disc_ejected` handler — it already resets `currentStep = 1` which implicitly hides step 5.

## Section 3: Step 5 Template

### Pre-rip view (`ripActive === false`)

No changes. The existing confirmation form, duplicate dropdown, and Start Rip button remain exactly as they are.

### Active rip view (`ripActive === true`)

Replace the step 5 card contents with:

- **Header:** "Rip in Progress" — Back button hidden
- **Body:** `"Ripping X title(s) from DISC_NAME"` with a spinner — X is `activeJobCount`, decremented live as SSE job-complete events arrive
- **Track summary:** Existing audio/subtitle/forced-subs summary line — keep as-is
- **CTA:** `"View Activity →"` link button to `/activity`
- **Hidden:** duplicate action dropdown, Start Rip button, the static selected-titles `<ul>`

## Out of Scope

- Cancel all jobs for a drive from this page. Users who want to cancel can navigate to /activity and cancel individual jobs there.
- Per-job progress display inline on this page (already available on /activity).
