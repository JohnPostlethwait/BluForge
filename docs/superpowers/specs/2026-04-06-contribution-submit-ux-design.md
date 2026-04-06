# Contribution Submit UX — Loading State & Success Flash

**Date:** 2026-04-06  
**Status:** Approved

## Problem

The "Submit to TheDiscDB" and "Fix PR" buttons on the contribution detail page are plain HTML form submits. Both trigger multi-second GitHub API operations. During submission the page sits idle with no feedback, then abruptly refreshes — leaving users unsure whether anything happened.

## Scope

Two buttons on `contribution_detail.templ`:
1. **Submit to TheDiscDB** — `POST /contributions/{id}/submit`
2. **Fix PR** — `POST /contributions/{id}/resubmit`

## Design

### Loading State

Both forms get an Alpine `x-data="{ submitting: false }"` scope. On `@submit`, `submitting` is set to `true` before the native form submit fires. The button:
- Disables immediately (`submitting || !isValid` for Submit; `submitting` alone for Fix PR)
- Swaps its label to a spinner + contextual text ("Submitting…" / "Fixing PR…")

The existing `.spinner` CSS class is reused. The `x-cloak` attribute hides the spinner span until Alpine initialises to prevent flash-of-content.

```html
<!-- Example: Fix PR button -->
<button type="submit" :disabled="submitting">
  <span x-show="!submitting">Fix PR</span>
  <span x-show="submitting" x-cloak class="flex items-center gap-2">
    <span class="spinner" aria-hidden="true"></span> Fixing PR…
  </span>
</button>
```

### Success Flash Messages

Both server handlers are updated to append `?flash=...` on their redirect URLs:

| Handler | Redirect target | Flash message |
|---|---|---|
| `handleContributionSubmit` | `/contributions` (list) | `"Contribution submitted — PR opened successfully"` |
| `handleContributionResubmit` | `/contributions/{id}` (detail) | `"PR updated — corrected files pushed to branch"` |

**Contribution list page** (`contributions.templ` + `ContributionsData`):
- Add `Flash string` field to `ContributionsData`
- `handleContributions` reads `c.QueryParam("flash")`, caps at 200 chars
- Render dismissable `alert-success` banner below the page header

**Contribution detail page** (`contribution_detail.templ` + `ContributionDetailData`):
- Add `Flash string` field to `ContributionDetailData`
- `handleContributionDetail` reads `c.QueryParam("flash")`, caps at 200 chars
- Render dismissable `alert-success` banner below the page header

Both flash banners use the existing pattern from `activity.templ`:
```html
<div x-data="{ show: true }"
     x-init="(function(){ var u = new URL(window.location); u.searchParams.delete('flash'); history.replaceState(null, '', u.toString()) })()"
     x-show="show" x-cloak
     class="alert alert-success flex justify-between items-center mb-4" role="status">
  <span>{ data.Flash }</span>
  <button @click="show = false" aria-label="Dismiss" class="btn-close-alert">&times;</button>
</div>
```

## Files Changed

| File | Change |
|---|---|
| `templates/contribution_detail.templ` | Add `Flash` field render; add `submitting` state to Submit and Fix PR forms |
| `templates/contributions.templ` | Add `Flash` field render |
| `internal/web/handlers_contribute.go` | Read flash query param in detail + list handlers; append `?flash=...` in submit and resubmit redirects |

## Out of Scope

- Error flash messages (errors already render as HTTP error pages via Echo)
- Async submission / polling
- Any changes to the Save Draft button (synchronous, fast)
