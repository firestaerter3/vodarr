# Design: TMDB Year-Conflict Fix

**Date**: 2026-03-09
**Status**: Approved

## Problem

Some provider-supplied TMDBIds point to the wrong movie — typically a remake or original that shares a title with the stream's intended film. For example:

- Stream: `┃NL┃ Ghostbusters - 2016 [DOLBY]`
- Provider TMDBId: 620 → Ghostbusters (1984)
- Correct TMDBId: 277834 → Ghostbusters: Answer the Call (2016)

Because `resolveByTitle` is skipped when a provider TMDBId is present, these mismatches pass through undetected and result in the wrong IMDB ID being stored. The existing fallback (retry title search when no IMDB ID is found) does not help because the wrong TMDBId yields a valid — just incorrect — IMDB ID.

## Affected Items

842 movies have duplicate TMDB IDs. Of those, ~9 distinct cases have a clear year conflict where the raw name contains an explicit year (`- YYYY`, `(YYYY)`, `[YYYY]`) that differs from the TMDB result year.

## Solution: Post-Details Year-Conflict Check (Approach A)

### New helper: `extractNameYear`

A new package-level function in `internal/sync/scheduler.go`:

```go
func extractNameYear(name string, patterns []*regexp.Regexp) string
```

It strips IPTV prefixes and quality/language markers (HEVC, 4K, DOLBY, NL GESPROKEN, user patterns) from the raw name — but does **not** strip year patterns — then calls the existing `extractTrailingYear` on the result. This correctly extracts `2016` from `Ghostbusters - 2016 [DOLBY]` while leaving `2049` alone in `Blade Runner 2049` (not a trailing pattern).

### Modification to `enrich`

Inside the `if item.TMDBId != ""` block, after the `GetMovieDetails` call sets `item.ReleaseDate` and `item.Year`:

1. Compute `nameYear := extractNameYear(item.Name, s.userPatterns)`
2. Compute `tmdbYear` from `item.ReleaseDate[:4]`
3. If `nameYear != ""` and `tmdbYear != ""` and `nameYear != tmdbYear` and `providerTMDBId != ""`:
   - Log the conflict at Debug level
   - Clear `item.TMDBId`, `item.CanonicalName`, `item.IMDBId`, `item.TVDBId`
   - Set `item.Year = nameYear`
   - Call `resolveByTitle(ctx, item)` — uses `nameYear` as the TMDB search hint; has built-in no-year retry fallback
   - If `resolveByTitle` found a new TMDBId, re-fetch external IDs

The guard `providerTMDBId != ""` ensures this path only triggers for provider-supplied IDs, not for IDs we found ourselves via title search.

### Trade-offs

- For conflicting items, `GetMovieExternalIDs` is called twice (once with wrong ID, once with correct ID). This is a one-time cost: after the first post-fix sync the corrected IDs are cached and the item takes the fast cache-hit path.
- Items where the name year matches the TMDB year are unaffected.
- Items with no trailing year in the name are unaffected.

## Files Changed

- `internal/sync/scheduler.go` — add `extractNameYear`, add year-conflict check in `enrich`
- `internal/sync/scheduler_test.go` — add test covering the year-conflict path

## Test Cases

| Raw name | Provider TMDBId | TMDB year | Name year | Expected outcome |
|---|---|---|---|---|
| `┃NL┃ Ghostbusters - 2016 [DOLBY]` | 620 (1984) | 1984 | 2016 | Retry → find 2016 film |
| `┃NL┃Aladdin - 2019 4K` | 812 (1992) | 1992 | 2019 | Retry → find 2019 film |
| `┃NL┃ Blade Runner 2049` | correct | 2017 | *(no trailing year)* | No change |
| `┃NL┃ Argentina, 1985` | correct | 2022 | *(no trailing year)* | No change |
| `┃NL┃ Fear Street: 1994 (2021)` | correct | 2021 | 2021 | No change (years match) |
