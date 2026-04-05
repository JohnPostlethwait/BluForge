# Rename Duplicate Action — Design Spec

**Date:** 2026-04-02  
**Status:** Approved

## Context

BluForge's "Step 4 Review Titles" page lets the user choose what to do when the output file already exists on disk: **Skip** (don't rip) or **Overwrite** (replace it). The user wants a third option — **Rename** — that still rips the title but writes the output to a non-colliding filename by appending ` (1)`, ` (2)`, etc. to the stem.

The settings page and config validation already support the `"rename"` value; the feature just hasn't been wired up end-to-end.

---

## Naming Format

When a collision is detected, the renamed file uses the format:

```
Iron Man.mkv           ← original (already exists)
Iron Man (1).mkv       ← first rename
Iron Man (2).mkv       ← if (1) also exists
```

The number is incremented until a free path is found.

---

## Changes

### 1. `templates/drive_detail.templ` — Add option to Step 4 dropdown

Add `<option value="rename">Rename</option>` to the "If file already exists:" select at lines 770–773.

```html
<select name="duplicate_action" style="width:auto; min-width:140px;">
    <option value="skip" selected>Skip</option>
    <option value="overwrite">Overwrite</option>
    <option value="rename">Rename</option>
</select>
```

### 2. `internal/organizer/organizer.go` — Add `NonCollidingPath`

New package-level function. Returns `path` unchanged if it doesn't exist. Otherwise, finds the first free path in the `stem (N).ext` series.

```go
// NonCollidingPath returns path if it does not exist on disk. Otherwise it
// appends " (1)", " (2)", etc. to the stem until a free path is found.
func NonCollidingPath(path string) string {
    if !FileExists(path) {
        return path
    }
    ext  := filepath.Ext(filepath.Base(path))
    stem := strings.TrimSuffix(filepath.Base(path), ext)
    dir  := filepath.Dir(path)
    for i := 1; ; i++ {
        candidate := filepath.Join(dir, fmt.Sprintf("%s (%d)%s", stem, i, ext))
        if !FileExists(candidate) {
            return candidate
        }
    }
}
```

### 3. `internal/workflow/orchestrator.go` — Handle "rename" in duplicate check

Replace the current single-case `if` at lines 124–131 with a `switch` that also handles `"rename"`:

```go
if organizer.FileExists(fullDest) {
    switch params.DuplicateAction {
    case "skip":
        return TitleResult{
            TitleIndex: sel.TitleIndex,
            Status:     "skipped",
            Reason:     fmt.Sprintf("duplicate exists: %s", destPath),
        }
    case "rename":
        fullDest = organizer.NonCollidingPath(fullDest)
    }
    // "overwrite" falls through — AtomicMove overwrites by default
}
```

Because `fullDest` is captured by reference in the `OnComplete` closure (registered later), reassigning it here is sufficient — the closure will use the new path automatically. `UpdateJobOutput` records the actual path written.

---

## Tests

### `internal/organizer/organizer_test.go` — `TestNonCollidingPath`

| Case | Setup | Expected result |
|---|---|---|
| No collision | No files on disk | Returns original path unchanged |
| One collision | Original exists | Returns `stem (1).ext` |
| Multiple collisions | Original + `(1)` exist | Returns `stem (2).ext` |

### `internal/workflow/orchestrator_test.go` — `TestManualRip_DuplicateRename`

Pre-create the destination file (`Test Movie/Main Feature.mkv`), run a rip with `DuplicateAction: "rename"`, and assert:
- Result status is `"completed"` (not `"skipped"`)
- Output path ends in `Main Feature (1).mkv`
- The original file is untouched

---

## Files Modified

| File | Change |
|---|---|
| `templates/drive_detail.templ` | Add `<option value="rename">Rename</option>` |
| `internal/organizer/organizer.go` | Add `NonCollidingPath` function |
| `internal/organizer/organizer_test.go` | Add `TestNonCollidingPath` |
| `internal/workflow/orchestrator.go` | Extend duplicate check with `"rename"` case |
| `internal/workflow/orchestrator_test.go` | Add `TestManualRip_DuplicateRename` |

No changes needed to: config, handlers, types, settings template (all already support `"rename"`).
