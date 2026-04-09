# Reset PR State — Design Spec

**Date:** 2026-04-08

## Problem

When a user manually closes or deletes a PR on GitHub, BluForge has no way to detect that. The contribution remains in `submitted` state with a stale `pr_url`. On the next submit attempt, the service calls `pushToExisting`, which tries to amend the closed/deleted PR's branch — instead of opening a fresh one.

The user needs a way to reset a contribution's PR state so that the next submission goes through `submitFresh` and opens a new PR.

## Scope

- Reset PR state action (clears `pr_url`, sets `status` back to `"pending"`)
- Alpine.js confirmation modal shared by both Reset PR and the existing Delete action (replacing the native `confirm()` dialog)
- Both the contributions list page and the contribution detail page get the Reset PR button

Out of scope: detecting PR closure from GitHub automatically.

---

## Backend

### DB Method

```go
// ResetContributionPR clears the pr_url and sets status back to "pending".
func (s *Store) ResetContributionPR(id int64) error
```

SQL:
```sql
UPDATE contributions
SET status = 'pending', pr_url = '', updated_at = CURRENT_TIMESTAMP
WHERE id = ?
```

No schema migration required — reuses existing `status` and `pr_url` columns.

### Handler

`handleContributionResetPR` in `internal/web/handlers_contribute.go`:

1. Parse contribution ID from route param
2. Load contribution from DB
3. Guard: if `status != "submitted"`, return HTTP 400
4. Call `store.ResetContributionPR(id)`
5. Redirect to `/contributions` with flash: `"PR state reset — contribution is ready to resubmit"`

### Route

```
POST /contributions/:id/reset-pr
```

Registered alongside the other contribution routes in `internal/web/server.go`.

---

## Frontend

### Alpine.js Confirmation Modal

A single shared confirmation modal per page, driven by this state:

```js
confirmModal: {
  show: false,
  title: '',
  message: '',
  action: ''   // POST URL to submit when user confirms
}
```

The modal renders as a fixed overlay with:
- A title and message (set per-action)
- "Cancel" button — sets `show: false`
- "Confirm" button — submits a hidden `<form>` pointing to `action`

The modal includes a hidden `<form>` with a `_csrf` token; the Confirm button submits it. This ensures all destructive POSTs carry CSRF protection just like the existing forms.

### Contributions List Page (`contributions.templ`)

- The outer card `<div>` gains `x-data="{ confirmModal: { ... } }"`
- The modal markup is rendered once inside this scope
- The existing Delete button's `onsubmit="return confirm(...)"` is removed; a click handler sets `confirmModal` state and shows the modal
- New "Reset PR" button for `submitted` contributions: plain `<button>` that sets `confirmModal` state and shows the modal

Button layout for `submitted` rows (replaces current "View PR" / "Update PR" row):

```
[View PR]  [Update PR]  [Reset PR]
```

### Contribution Detail Page (`contribution_detail.templ`)

- The existing top-level `<div x-data>` expands to `x-data="{ confirmModal: { ... } }"`
- The modal markup is rendered once inside this scope
- In the sticky footer, a "Reset PR" button is added — visible only when `status == "submitted"`, positioned after the "View PR on GitHub" link

Sticky footer layout when `status == "submitted"`:

```
[Update PR]  [View PR on GitHub]  [Reset PR]
```

---

## Error Handling

| Condition | Response |
|-----------|----------|
| Contribution not found | HTTP 404 |
| Status is not `"submitted"` | HTTP 400 — "Cannot reset a contribution that has not been submitted." |
| DB error on reset | HTTP 500 |

---

## Testing

### `internal/db/contributions_test.go`

- Create a contribution, set status to `"submitted"` with a `pr_url`
- Call `ResetContributionPR`
- Assert `status == "pending"` and `pr_url == ""`

### `internal/web/handlers_contribute_test.go`

- `POST /contributions/:id/reset-pr` on a `pending` contribution → HTTP 400
- `POST /contributions/:id/reset-pr` on a `submitted` contribution → HTTP 303 redirect to `/contributions`
- After reset, verify DB state has `status == "pending"` and `pr_url == ""`
