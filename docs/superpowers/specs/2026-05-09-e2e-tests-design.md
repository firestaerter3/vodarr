# E2E Test Suite Design — Vodarr Sonarr/Radarr Integration

**Date:** 2026-05-09
**Branch:** `feature/download-mode`
**Status:** Approved

---

## Problem

Vodarr has solid unit tests for the Newznab handler (`internal/newznab/`) and the fake
qBit handler (`internal/qbit/`) in isolation, but no tests that exercise the full
Sonarr/Radarr workflow end-to-end. The two handlers must collaborate across an HTTP
boundary (qBit fetches a descriptor from Newznab, decodes it, then writes files), and
that collaboration is currently untested.

The `feature/download-mode` branch adds a second output mode — actual HTTP downloads via
`internal/download.Manager` — that also needs coverage before the branch can merge.

---

## Goals

1. Prove the complete Sonarr/Radarr flow works for **both output modes** (STRM and download).
2. Cover **both grab paths**: URL-based (Newznab `t=get` URL sent to qBit) and torrent
   file upload (Torznab multipart POST).
3. Keep tests hermetic, fast (<100ms total), and free of real network calls or port
   allocation.
4. Add no test-only hooks to production code.

---

## Non-Goals

- Testing the sync scheduler or TMDB enrichment pipeline.
- Testing the Vodarr web UI or arr auto-configure flow.
- Re-testing `download.Manager` internals (retry, throttle, stall detection) — those
  are covered by `internal/download/manager_test.go`.
- Testing real Xtream provider connectivity.

---

## Architecture

### Package

```
internal/e2e/
  harness_test.go   — shared test harness (servers, fixtures, helpers)
  strm_test.go      — STRM output mode tests (7 tests)
  download_test.go  — Download output mode tests (4 tests)
```

All files are `_test.go`. Nothing in this package is compiled into the production binary.
The package name is `e2e_test` (external test package) so it can only access exported
APIs, keeping the tests honest about the public surface.

### Component wiring

```
                  ┌─────────────────────────────────┐
                  │         newHarness(t, mode)       │
                  │                                   │
  index.Index ────┤──► newznab.Handler                │
                  │         │  httptest.Server         │
                  │         │  (newznabSrv)            │
                  │         │                          │
  qbit.Store  ────┤──► qbit.Handler                   │
  strm.Writer ────┤       newznabHost = newznabSrv     │
  dl.Manager  ────┤         │  httptest.Server         │
                  │         │  (qbitSrv)               │
                  │         │                          │
  mock Xtream ────┤◄────────┘ descriptorCli.Get()     │
  httptest.Server │           (in-process HTTP)        │
                  └─────────────────────────────────┘
```

The `descriptorCli` inside `qbit.Handler` calls back to the Newznab server when
processing a URL-based grab. Since both run as `httptest.Server`, this is fully
in-process with no real networking.

---

## Test Harness (`harness_test.go`)

### `newHarness(t *testing.T, outputMode string) *harness`

Creates and registers cleanup for:

1. **Index fixtures**
   - Movie: `{XtreamID:1, Name:"The Matrix", Year:"1999", IMDBId:"tt0133093",
     TMDBId:"603", Type:movie, ContainerExt:"mkv"}`
   - Series: `{XtreamID:2, Name:"Breaking Bad", Year:"2008", TVDBId:"81189",
     TMDBId:"1396", Type:series, Episodes:[S1E1 Pilot (ID:101), S1E2 Cat's in the Bag
     (ID:102), S2E1 Seven Thirty-Seven (ID:201)]}`
   - Unenriched movie: `{XtreamID:3, Name:"Unknown Film", Type:movie}` — no
     IMDB/TVDB/TMDB IDs
   - Error fixture: `{XtreamID:999, Name:"Banned Film", IMDBId:"tt9999999", Type:movie}`
     — mock Xtream returns 403 for stream ID 999

2. **Mock Xtream server** (`httptest.NewServer`)
   - `/movie/{user}/{pass}/{id}.{ext}` → 200, `Content-Type: video/x-matroska`,
     body = `strm.BuildMKVHeader(nil)` (~4 KB valid EBML header)
   - `/series/{user}/{pass}/{id}.{ext}` → same body
   - Any path containing `/999.` → 403 Forbidden
   - All other paths → 404

3. **`strm.Writer`** pointing to `t.TempDir()`

4. **`download.Manager`** with `MaxConcurrent:1, InterDelay:0` (no cooldown in tests)

5. **Newznab `httptest.Server`** — `newznab.NewHandler(idx, "", newznabSrv.URL, xtreamClient)`

6. **qBit `httptest.Server`** — `qbit.NewHandler(store, writer, xtreamClient, nilProber,
   tmpDir, "", "", newznabSrv.URL, outputMode, dlManager)`
   - `newznabHost` extracted from `newznabSrv.URL` so SSRF validation passes
   - `nilProber` — probe always returns `nil, nil` (no media metadata; mkv stubs use
     default 500MB sparse size)

7. **Web `httptest.Server`** — `web.NewHandler(idx, nil, writer, ...)` with a minimal
   config. Only the `/api/webhook` endpoint is exercised in E2E tests; the rest of the
   web UI is not tested here.

### Helper methods on `*harness`

| Method | Description |
|--------|-------------|
| `searchMovie(imdbID string) string` | GET `newznabSrv/api?t=movie&imdbid=<id>`, return RSS XML body |
| `searchSeries(tvdbID string) string` | GET `newznabSrv/api?t=tvsearch&tvdbid=<id>`, return RSS XML body |
| `searchText(q string) string` | GET `newznabSrv/api?t=search&q=<q>`, return RSS XML body |
| `extractDownloadURLs(rssXML string) []string` | Parse RSS, return all `<enclosure url=...>` values |
| `fetchTorrent(downloadURL string) []byte` | GET the `t=get` URL, return raw torrent bytes |
| `grabByURL(url, savePath, category string) string` | POST `torrents/add` with `urls=<url>`, return hash from subsequent `torrents/info` |
| `grabByTorrent(torrentBytes []byte, savePath, category string) string` | POST `torrents/add` as `multipart/form-data`, return hash |
| `waitForState(hash, target string, timeout time.Duration) error` | Poll `torrents/info` every 10ms until `state==target` or timeout |
| `torrentFiles(hash string) []string` | GET `torrents/files?hash=<hash>`, return file name list |
| `fileExists(relPath string) bool` | Check `tmpDir/relPath` exists on disk |
| `fileContent(relPath string) string` | Read and return file content |
| `fileSize(relPath string) int64` | Return file size in bytes |

---

## Test Cases

### `strm_test.go` — STRM output mode

All 6 tests call `newHarness(t, "strm")`.

**`TestMovieGrab_URL_Strm`**
Full movie flow via URL-based grab (Newznab path):
1. `searchMovie("tt0133093")` → parse RSS, get download URL
2. `grabByURL(downloadURL, tmpDir, "vodarr-movies")` → hash
3. `waitForState(hash, "pausedUP", 2s)`
4. `torrentFiles(hash)` → assert contains `The.Matrix.1999.WEB-DL.mkv`
5. Assert `.strm` file exists at `movies/The Matrix (1999)/The.Matrix.1999.WEB-DL.strm`
6. Assert `.strm` content starts with `xtreamSrv.URL + "/movie/"`
7. Assert `.mkv` stub exists alongside `.strm`

**`TestMovieGrab_Torrent_Strm`**
Same movie, Torznab (torrent upload) path:
1. `searchMovie("tt0133093")` → extract download URL
2. `fetchTorrent(downloadURL)` → raw `.torrent` bytes
3. `grabByTorrent(torrentBytes, tmpDir, "vodarr-movies")` → hash
4. `waitForState(hash, "pausedUP", 2s)`
5. Same file assertions as above

**`TestSeriesGrab_URL_Strm`**
Full series, all episodes:
1. `searchSeries("81189")` → RSS, get download URL
2. `grabByURL(downloadURL, tmpDir, "vodarr-tv")` → hash
3. `waitForState(hash, "pausedUP", 2s)`
4. `torrentFiles(hash)` → assert 3 entries
5. Assert 3 `.strm` files:
   - `tv/Breaking Bad/Season 01/Breaking.Bad.S01E01.Pilot.WEB-DL.strm`
   - `tv/Breaking Bad/Season 01/Breaking.Bad.S01E02.Cats.in.the.Bag.WEB-DL.strm`
   - `tv/Breaking Bad/Season 02/Breaking.Bad.S02E01.Seven.Thirty-Seven.WEB-DL.strm`
6. Assert each `.strm` content contains `/series/` Xtream URL

**`TestSeriesGrab_SingleEpisode_Strm`**
Sonarr single-episode grab via `episode_id`:
1. Newznab `t=tvsearch&tvdbid=81189` → extract URL, append `&episode_id=101`
2. `grabByURL(url, ...)` → hash
3. `waitForState(hash, "pausedUP", 2s)`
4. `torrentFiles(hash)` → assert exactly 1 entry (S01E01 only)
5. Assert only 1 `.strm` created

**`TestUnenrichedMovie_Strm`**
Unenriched item (no external IDs) reachable via text search:
1. `searchText("Unknown Film")` → RSS, extract download URL
2. `grabByURL(downloadURL, ...)` → hash
3. `waitForState(hash, "pausedUP", 2s)`
4. Assert `.strm` created (name derived from raw Xtream title)

**`TestMovieGrab_AuthRequired_Strm`**
qBit with credentials (`username:"admin", password:"secret"`):
1. `grabByURL(url, ...)` without SID → assert 403 response
2. POST `auth/login` with correct credentials → get SID cookie
3. `grabByURL(url, ...)` with SID → assert "Ok." response
4. `waitForState(hash, "pausedUP", 2s)` → assert `.strm` created

**`TestWebhookStubCleanup_Strm`**
Post-import webhook deletes `.mkv` stub, preserves `.strm`:
1. Run full movie grab flow (same as `TestMovieGrab_URL_Strm`) → `.strm` + `.mkv` both on disk
2. `torrentFiles(hash)` → extract the `.mkv` path reported by qBit
3. POST `MovieFileImported` webhook to web server with source path = `.mkv` path
4. Assert `.mkv` stub no longer exists on disk
5. Assert `.strm` still exists and content is unchanged

---

### `download_test.go` — Download output mode

All 4 tests call `newHarness(t, "download")`.

**`TestMovieGrab_URL_Download`**
Full movie download flow:
1. `searchMovie("tt0133093")` → download URL
2. `grabByURL(downloadURL, tmpDir, "vodarr-movies")` → hash
3. `waitForState(hash, "pausedUP", 2s)`
4. `torrentFiles(hash)` → assert contains `The.Matrix.1999.mkv` (no `.strm`)
5. Assert video file exists at `writer.MovieFilePath("The Matrix", "1999", "mkv")`
6. Assert `fileSize(...)` == len(mock Xtream payload) (byte-complete)
7. Assert no `.strm` file exists (download mode writes real file, not pointer)

**`TestSeriesGrab_URL_Download`**
All 3 episodes downloaded:
1. `searchSeries("81189")` → download URL
2. `grabByURL(...)` → hash
3. `waitForState(hash, "pausedUP", 2s)`
4. Assert 3 video files on disk at paths from `writer.EpisodeFilePath(...)`, each with correct byte count
5. `torrentFiles(hash)` → 3 entries, all `.mkv` paths

**`TestProgressTracking_Download`**
Progress updates visible via `torrents/info`:
1. `grabByURL(...)` → hash
2. Poll `torrents/info` in a loop; collect `progress` field values
3. `waitForState(hash, "pausedUP", 2s)`
4. Assert at least one intermediate progress sample where `0 < progress < 1`
   (proves `store.UpdateProgress` is wired through the HTTP response)

**`TestProviderError_Download`**
Xtream returns 403 for the error fixture:
1. Seed grab for `XtreamID:999` (mock Xtream returns 403)
2. `grabByURL(errorFixtureURL, ...)` → hash
3. `waitForState(hash, "pausedUP", 3s)` → assert returns timeout error
4. `torrentFiles(hash)` → assert `progress < 1.0` (download never completed)
5. Assert no video file exists on disk

---

## Key Implementation Notes

**SSRF validation:** `qbit.Handler` validates that descriptor URLs point to its own
Newznab server. `newHarness` sets `newznabURL = newznabSrv.URL` so the host check passes
automatically in tests. No production code change needed.

**Hash extraction:** After `grabByURL` / `grabByTorrent`, the helper immediately calls
`torrents/info` (no filter) to find the newly-added hash. Since tests run serially and
the store starts empty, the first torrent in the response is always the one just added.

**Async goroutine sync:** `waitForState` polls `torrents/info` every 10ms with a 2s
ceiling. With an in-process mock Xtream, STRM tests finish in <5ms; download tests with
~4KB payload finish in <20ms. No production code changes (no hooks, no channels injected).

**Download mode file paths:** In download mode `processDownload` writes to
`strm.Writer.DownloadPath(name, year, ext)` — a new method added on the feature branch.
The test asserts the file at that path, which can be computed independently using the
same `folderSafe`/`fileSafe` helpers the writer uses.

**`nilProber`:** A trivial `func Probe(ctx, url) (*probe.MediaInfo, error) { return nil, nil }`
defined in `harness_test.go`. Avoids spawning ffprobe in tests. MKV stubs get the
default 500MB sparse size, which is fine for all assertions.

**Mock Xtream payload:** `strm.BuildMKVHeader(nil)` is already exported and produces a
valid EBML header (~hundreds of bytes). The mock server returns this as the entire
response body with `Content-Length` set. Download-mode tests assert `fileSize == len(payload)`.

---

## Test Matrix Summary

| Test | Mode | Media | Grab path | Asserts |
|------|------|-------|-----------|---------|
| `TestMovieGrab_URL_Strm` | strm | movie | URL | .strm content, .mkv stub exists |
| `TestMovieGrab_Torrent_Strm` | strm | movie | torrent upload | .strm content, .mkv stub exists |
| `TestSeriesGrab_URL_Strm` | strm | series (3 ep) | URL | 3 .strm files in correct Season dirs |
| `TestSeriesGrab_SingleEpisode_Strm` | strm | series (1 ep) | URL+episode_id | exactly 1 .strm |
| `TestUnenrichedMovie_Strm` | strm | movie (no IDs) | URL | .strm created from raw title |
| `TestMovieGrab_AuthRequired_Strm` | strm | movie | URL | 403 without SID, success with SID |
| `TestWebhookStubCleanup_Strm` | strm | movie | URL | .mkv deleted after webhook, .strm preserved |
| `TestMovieGrab_URL_Download` | download | movie | URL | real file on disk, correct byte count |
| `TestSeriesGrab_URL_Download` | download | series (3 ep) | URL | 3 files, each byte-complete |
| `TestProgressTracking_Download` | download | movie | URL | progress 0→intermediate→1.0 |
| `TestProviderError_Download` | download | movie (403) | URL | timeout, no file on disk |

**Total: 11 tests, 3 files, 0 production code changes.**
