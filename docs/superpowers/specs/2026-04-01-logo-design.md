# BluForge Logo System — Design Spec

**Date:** 2026-04-01
**Status:** Approved

## Overview

BluForge gets a full product identity: an SVG icon mark plus a two-tone wordmark, usable at every scale from 16px favicon to app icon tile. The identity is "Ember Ring" — a Blu-ray disc whose ring transitions from cool blue at the top to molten amber/orange at the bottom, referencing both the Blu-ray medium and the forge-craftsmanship implied by the name.

## Visual Design

### Icon Mark

An SVG disc composed of three elements:

1. **Disc body** — filled circle, deep navy radial gradient (`#1d4ed8` → `#172554`)
2. **Inner ring groove** — thin stroke circle at ~61% radius, `#1e3a6e`, suggests disc data tracks
3. **Center hole** — small circle punched out (`#0f1419`, matching page background)
4. **Ember ring** — the outer stroke of the disc; a `linearGradient` running top-to-bottom:
   - Top: `#3b82f6` (matches existing app accent blue)
   - Mid (~55%): `#f59e0b` (amber)
   - Bottom: `#f97316` (orange)
5. **Heat bloom** — a subtle radial ellipse glow below the disc (`#f97316`, low opacity), present at 32px and above, omitted at 20px and smaller for clarity

### Wordmark

- Font: system sans-serif stack (`-apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif`) — matches the app body font, no web font dependency
- Weight: 700
- Letter-spacing: -0.025em to -0.03em (tight, modern)
- Split coloring: **"Blu"** in `#e2e8f0` (primary text), **"Forge"** in `#f59e0b` (amber)

### Lockup

Horizontal: icon left, wordmark right, `gap: 10px` (at nav scale). The icon sits optically centered with the wordmark cap-height.

## Color Tokens

| Token | Hex | Usage |
|-------|-----|-------|
| Ring top | `#3b82f6` | Existing `--accent-blue`; ring gradient start |
| Ring mid | `#f59e0b` | Amber; wordmark "Forge" color |
| Ring bottom | `#f97316` | Orange; gradient end, heat bloom |
| Disc fill | `#172554` | Deep navy disc body |
| Wordmark "Blu" | `#e2e8f0` | Existing `--text-primary` |
| Wordmark "Forge" | `#f59e0b` | Same as ring mid |

No new CSS variables are required — `--accent-blue` and `--text-primary` already exist. `#f59e0b` and `#f97316` are used only within the SVG and the `.logo` element.

## Deliverables

### 1. `static/logo.svg`

A standalone SVG file at a 72×72 viewBox (icon only, no wordmark). Used for:
- `<link rel="icon">` (favicon, referencing the SVG directly for modern browsers)
- GitHub repo social preview / README `<img>` tag
- Docker Hub image icon

The SVG is self-contained with no external references.

### 2. Nav component update (`templates/components/nav.templ`)

Replace the current text-only `.logo` anchor with an inline SVG icon (22×22 render) followed by the two-tone wordmark span. The existing `.logo` CSS class is kept; a new `.logo-icon` utility class handles `display:flex; align-items:center; gap: 10px`.

### 3. Favicon (`<link rel="icon">` in `templates/layout.templ`)

Add `<link rel="icon" href="/static/logo.svg" type="image/svg+xml">` to the `<head>`. SVG favicons are supported in all modern browsers. No `.ico` file is required.

### 4. Static file serving

`logo.svg` is placed in `static/` alongside `style.css`. The existing Echo static file middleware already serves this directory at `/static/`, so no code changes are needed to serve the file.

## Sizes and Detail Levels

| Render size | Detail kept | Detail dropped |
|-------------|-------------|----------------|
| 48px+ | Body, inner ring, center hole, ember ring, heat bloom | — |
| 32px | Body, inner ring, center hole, ember ring, heat bloom | — |
| 20–22px (nav) | Body, center hole, ember ring | Inner ring, heat bloom |
| 16px | Body, center hole, ember ring (thicker stroke) | Inner ring, heat bloom |

The SVG viewBox is fixed at 72×72. Size variation is handled by the `width`/`height` attributes on the `<svg>` element. At 20px and below, the inner ring groove and heat bloom are omitted from the nav inline SVG to keep the icon readable.

## Monochrome Variant

For light-background contexts (README on GitHub in light mode, docs sites):
- Disc body: `#1e293b`
- Ring stroke: `#1e293b`
- Center hole: page background (`transparent` or `#f8fafc`)
- Wordmark: `#1e293b`, single color

This variant is not implemented in the app itself — it is documented here for future use in README/docs.

## Out of Scope

- Animated logo (loading state, etc.)
- Light-mode app theme
- PNG/ICO raster exports (SVG covers all in-app needs)
- OG/social sharing image (separate future work)
