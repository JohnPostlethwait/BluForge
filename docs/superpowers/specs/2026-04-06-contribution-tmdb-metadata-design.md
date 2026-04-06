# Contribution: TMDB Metadata, tmdb.json, and Cover Images

**Date:** 2026-04-06  
**Status:** Approved

## Problem

BluForge contributions (e.g., [PR #216](https://github.com/TheDiscDb/data/pull/216)) are missing the files that TheDiscDB reviewers expect and that the data importer requires:

- `metadata.json` — title-level metadata (TMDB/IMDb IDs, plot, runtime, release date)
- `tmdb.json` — raw TMDB API response (~3000–9500 lines)
- `cover.jpg` — movie poster at the title level
- `front.jpg` — disc art at the release level
- `release.json` is also missing the `ImageUrl` field

Working contributions (PRs #95, #98, #206, #207, #210) include all of these. Until they are included, BluForge PRs will continue to be rejected or require manual intervention.

## Intended Outcome

Every BluForge contribution PR includes the same file set as manually-created working contributions. TMDB data is fetched at submit time using the already-configured API key. Images use the TMDB poster as a proxy for cover art (appropriate since we cannot photograph physical discs). When no poster is available the submission proceeds without image files.

## File Structure

```
data/movie/Willow (1988)/
├── metadata.json          ← NEW: title-level metadata
├── tmdb.json              ← NEW: raw TMDB API response
├── cover.jpg              ← NEW: TMDB poster (binary)
└── 1988-4k/
    ├── release.json       ← UPDATED: add ImageUrl field
    ├── front.jpg          ← NEW: same TMDB poster (binary)
    ├── disc01.json
    ├── disc01-summary.txt
    └── disc01.txt
```

## metadata.json Schema

```json
{
  "Title": "Willow",
  "FullTitle": "Willow",
  "SortTitle": "Willow",
  "Slug": "willow-1988",
  "Type": "Movie",
  "Year": 1988,
  "ImageUrl": "Movie/willow-1988/cover.jpg",
  "ExternalIds": {
    "Tmdb": "14160",
    "Imdb": "tt0096071"
  },
  "Groups": [],
  "Plot": "...",
  "Tagline": "...",
  "RuntimeMinutes": 126,
  "ReleaseDate": "1988-05-20T00:00:00Z",
  "DateAdded": "2026-04-06T..."
}
```

- `Slug` = `slugify(title, year)` (reuses existing `slugify` helper)
- `Type` is Title-cased mediaType: `"Movie"` or `"Series"`
- `ImageUrl` = `"{Type}/{slug}/cover.jpg"` (PascalCase, matches working PRs)
- `ReleaseDate` is from TMDB's `release_date` (movies) or `first_air_date` (TV), formatted as RFC3339
- `ImdbID` from `imdb_id` field (movies) or `external_ids.imdb_id` (TV)

## release.json Change

Add `ImageUrl` field using pattern: `"{Type}/{titleSlug}/{releaseSlug}.jpg"`.

Example: `"Movie/willow-1988/1988-4k.jpg"`

## Component Changes

### 1. `internal/tmdb/client.go`

Add `Fetcher` interface:
```go
type Fetcher interface {
    GetDetails(ctx context.Context, id int, mediaType string) (json.RawMessage, *MediaDetails, error)
    DownloadImage(ctx context.Context, posterPath, size string) ([]byte, error)
}
```

Add `MediaDetails` struct:
```go
type MediaDetails struct {
    ID             int
    Title          string
    Overview       string
    Tagline        string
    RuntimeMinutes int
    ReleaseDate    string  // YYYY-MM-DD from TMDB
    PosterPath     string
    ImdbID         string
}
```

`GetDetails`:
- Movie: `GET /3/movie/{id}?append_to_response=external_ids`
- TV: `GET /3/tv/{id}?append_to_response=external_ids`
- Returns `(json.RawMessage, *MediaDetails, error)` — raw bytes for tmdb.json, parsed fields for metadata.json

`DownloadImage`:
- Fetches `https://image.tmdb.org/t/p/{size}{posterPath}`
- Returns `([]byte, error)` — raw image bytes
- Caller: use `"original"` size

### 2. `internal/github/client.go`

Extend `FileEntry` to support binary content:
```go
type FileEntry struct {
    Path    string
    Content string // text; mutually exclusive with Blob
    Blob    []byte // binary; nil = use Content
}
```

Add `CreateBlob(ctx, owner, repo string, data []byte) (string, error)`:
- POSTs to GitHub Blobs API with base64 encoding
- Returns SHA for use in tree entries

Update `CommitFiles`:
- For each entry: if `Blob != nil`, call `CreateBlob` first and use SHA in the tree entry
- Otherwise, use `Content` (existing behavior, no breaking change)

### 3. `internal/contribute/types.go`

Add:
```go
type MetadataJSON struct { ... }   // see schema above
type ExternalIdsJSON struct { Tmdb string; Imdb string }
```

Update `ReleaseJSON`:
- Add `ImageUrl string \`json:"ImageUrl,omitempty"\``

### 4. `internal/contribute/filegen.go`

Add:
```go
func GenerateMetadataJSON(details *tmdb.MediaDetails, mediaType, mediaTitle string, mediaYear int) string
```

Update:
```go
func GenerateReleaseJSON(ri ReleaseInfo, githubUser, imageURL string) string
```
- Callers pass in a pre-computed `imageURL` (title slug + release slug).

### 5. `internal/contribute/service.go`

Add `TMDBFetcher` interface:
```go
type TMDBFetcher interface {
    GetDetails(ctx context.Context, id int, mediaType string) (json.RawMessage, *tmdb.MediaDetails, error)
    DownloadImage(ctx context.Context, posterPath, size string) ([]byte, error)
}
```

Update `Service` struct and constructor:
```go
type Service struct {
    store  Store
    github GitHubClient
    tmdb   TMDBFetcher
}
func NewService(store Store, github GitHubClient, tmdb TMDBFetcher) *Service
```

Update `Submit()` — after parsing stored JSON, before building `files`:
1. Parse `c.TmdbID` as int via `strconv.Atoi`
2. Call `s.tmdb.GetDetails(ctx, tmdbID, mediaType)` → hard error on failure
3. Call `s.tmdb.DownloadImage(ctx, details.PosterPath, "original")` → soft failure (log warning, omit images)
4. Compute `titleSlug = slugify(mediaTitle, mediaYear)`, `releaseImageURL`, `titleImageURL`
5. Append to `files`:
   - `mediaDir/metadata.json` (text)
   - `mediaDir/tmdb.json` (text, raw API response)
   - `mediaDir/cover.jpg` (binary, only if image downloaded)
   - `releaseDir/front.jpg` (binary, same bytes, only if image downloaded)
6. Pass `releaseImageURL` to `GenerateReleaseJSON`

Apply same additions to `Resubmit()`.

### 6. `internal/web/handlers_contribute.go`

In both `handleContributionSubmit` and `handleContributionResubmit`:
```go
opts := []tmdb.Option{}
if s.tmdbBaseURL != "" {
    opts = append(opts, tmdb.WithBaseURL(s.tmdbBaseURL))
}
tmdbClient := tmdb.NewClient(cfg.TMDBApiKey, opts...)
svc := contribute.NewService(s.store, ghClient, tmdbClient)
```

## Error Handling

| Scenario | Behavior |
|----------|----------|
| TMDB API key not set | Fail at submit with clear message (already validated before submit) |
| `GetDetails` returns error | Hard fail — metadata.json is required for acceptance |
| Poster path empty | Skip cover.jpg and front.jpg, log at info level |
| Image download fails | Skip cover.jpg and front.jpg, log warning; submit proceeds |
| TMDB ID non-numeric in DB | Hard fail with descriptive error |

## Testing

- `internal/tmdb/client_test.go` — add `GetDetails` tests (movie + TV) using httptest server; add `DownloadImage` tests (success + empty poster path)
- `internal/contribute/filegen_test.go` — add `GenerateMetadataJSON` tests; update `GenerateReleaseJSON` tests to pass `imageURL`
- `internal/contribute/service_test.go` — add `TMDBFetcher` mock; assert metadata.json, tmdb.json, cover.jpg, front.jpg are included in `CommitFiles` call; assert correct behaviour on image download failure
- `internal/github/client_test.go` — add `CreateBlob` test; add `CommitFiles` test with binary entry (asserts blob created, SHA used in tree)

## Verification

1. Run `go test ./...` — all tests pass
2. Run `go vet ./...` — no issues
3. Run `templ generate` — no template changes needed
4. Build: `go build -o bluforge .`
5. Manual: submit a test contribution and verify the PR contains all 8 files with the correct content
