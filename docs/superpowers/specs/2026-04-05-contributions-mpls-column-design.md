# Contributions MPLS Column Design

**Date:** 2026-04-05
**Status:** Approved

## Summary

Add a read-only MPLS column to the Titles table on the contribution detail page, showing the MPLS playlist filename (e.g. `00300.mpls`) for each title. This gives contributors visibility into which playlist file maps to each title — useful for disc identification and cross-referencing with TheDiscDB.

## Scope

Single file change: `templates/contribution_detail.templ`

No backend changes required. The MPLS filename is already stored in the contribution's `scan_json` (attribute 16 on each `TitleInfo`, accessible via `SourceFile()`).

## Changes

### `contributionFormInit` (Go helper)

Add `MPLS` field to the local `titleEntry` struct:

```go
type titleEntry struct {
    TitleIndex int    `json:"title_index"`
    Name       string `json:"name"`
    Duration   string `json:"duration"`
    Size       string `json:"size"`
    FileName   string `json:"file_name"`
    MPLS       string `json:"mpls"`   // ← new
    Type       string `json:"type"`
    Label      string `json:"label"`
    Season     string `json:"season"`
    Episode    string `json:"episode"`
}
```

Populate it when building entries:

```go
entry := titleEntry{
    ...
    MPLS: t.SourceFile(),
}
```

`SourceFile()` returns `""` for DVDs (no MPLS files), resulting in a blank cell.

### Table Header

Insert `<th>MPLS</th>` as the second column, between `#` and `Duration`.

### Table Row

Insert a read-only cell as the second column, between `#` and `Duration`:

```html
<td x-text="t.mpls" class="text-muted text-sm font-mono whitespace-nowrap"></td>
```

Styling rationale: `font-mono` to visually distinguish it as a technical identifier; `text-muted` + `text-sm` to de-emphasize it relative to editable fields; `whitespace-nowrap` to prevent wrapping of the filename.

## Behavior

- **Blu-ray/UHD:** Shows the MPLS playlist filename, e.g. `00300.mpls`
- **DVD:** Cell is empty (no MPLS on DVDs; `SourceFile()` returns `""`)
- **Read-only:** No user interaction — purely informational
