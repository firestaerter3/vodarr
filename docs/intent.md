# VODarr — Intent & Requirements

## Problem

Xtream Codes is the dominant protocol for IPTV providers. Providers
typically offer tens of thousands of VOD movies and TV series, but
there is no first-class way to browse and manage this content through
the *arr ecosystem (Sonarr, Radarr) or play it through Jellyfin with
proper metadata.

The Jellyfin Xtream plugin exists but loads the entire catalog on
startup, causing unbearable load times and providing no filtering.

**VODarr solves this by acting as a native *arr integration:**
- Sonarr/Radarr search VODarr as they would any Newznab indexer
- VODarr looks up the content in the Xtream catalog
- Sonarr/Radarr "download" via a fake qBittorrent client
- VODarr writes a `.strm` file pointing to the Xtream stream URL
- Sonarr/Radarr import the `.strm` into the media library
- Jellyfin plays the `.strm`, streaming directly from the provider

No actual video is downloaded. No disk space is consumed beyond the
tiny `.strm` text files.

---

## Goals

1. **Drop-in integration** — add to Sonarr/Radarr exactly like any
   other indexer + download client; no custom plugins required.

2. **ID-based matching** — resolve Xtream catalog items to IMDB/TVDB
   IDs via TMDB so Radarr's `imdbid=` and Sonarr's `tvdbid=` queries
   return exact matches, not just fuzzy title hits.

3. **Low friction setup** — single Docker container, one config file,
   no external database required.

4. **Non-destructive** — VODarr only writes `.strm` files. It does not
   modify the Xtream provider or the *arr libraries in any destructive
   way.

---

## Non-Goals

- **Transcoding or re-encoding** — streams are passed through as-is.
- **DRM bypass** — VODarr only works with providers that serve plain
  HTTP streams.
- **Download scheduling / queuing** — that is Sonarr/Radarr's job.
- **Subtitle or metadata scraping** — Jellyfin handles this via its
  own TMDB/TVDB scrapers once the `.strm` is imported.

---

## Architecture

```
┌─────────────────────────────────────────────────────────────────┐
│                           VODARR                                │
│                                                                 │
│  ┌──────────┐   ┌──────────┐   ┌──────────────────────────┐   │
│  │ Xtream   │──▶│ Content  │◀──│ TMDB Client              │   │
│  │ Client   │   │ Index    │   │ (ID cross-reference)     │   │
│  └──────────┘   └────┬─────┘   └──────────────────────────┘   │
│                      │                                          │
│         ┌────────────┼────────────┐                             │
│         ▼            ▼            ▼                             │
│  ┌──────────┐  ┌──────────┐  ┌──────────┐  ┌──────────┐      │
│  │ Newznab  │  │ qBit API │  │ STRM     │  │ Web UI   │      │
│  │ API      │  │ (fake)   │  │ Writer   │  │ (React)  │      │
│  │ :7878    │  │ :8080    │  │          │  │ :3000    │      │
│  └──────────┘  └──────────┘  └──────────┘  └──────────┘      │
└─────────────────────────────────────────────────────────────────┘
       ▲                ▲                           ▲
       │                │                           │
   Sonarr/Radarr    Sonarr/Radarr              Browser
   (search)         (download)
```

### Key Design Decisions

**In-memory index (no database)**
The full Xtream catalog is fetched and enriched at sync time, then
held in memory as a set of maps keyed by IMDB ID, TVDB ID, TMDB ID,
and normalised title. A full rebuild happens on every sync. This keeps
the code simple and startup fast; the trade-off is that a restart
requires a fresh sync (configurable via `sync.on_startup`).

**Fake qBittorrent API**
Sonarr and Radarr have native qBittorrent support with well-understood
behaviour. Impersonating qBit (returning `state: "pausedUP"`,
`progress: 1.0` immediately) is more reliable than implementing a
custom download client protocol, which would require a plugin in each
*arr application.

**Newznab `t=get` returns JSON, not NZB**
The standard `t=get` response is an NZB file. VODarr instead returns a
small JSON descriptor containing the Xtream stream ID and metadata.
The fake qBit handler fetches this descriptor when processing a
`torrents/add` request. This avoids implementing a full NZB parser and
keeps the grab→strm path simple.

**STRM file naming follows *arr conventions**
Radarr expects: `movies/{Movie Name (Year)}/{Movie.Name.Year.mkv}`
Sonarr expects: `tv/{Series Name}/Season NN/{Series.Name.SXXEYY.mkv}`
VODarr writes `.strm` files in exactly these layouts so that *arr's
import logic works without custom configuration.

---

## Data Flow

### Sync (background, runs every `sync.interval`)

```
Xtream GetVODStreams() ──────────────────────────────────────────┐
Xtream GetSeries()                                               │
                                                                 ▼
                                                    For each item with tmdb_id:
                                                      TMDB GetMovieExternalIDs()
                                                      TMDB GetTVExternalIDs()
                                                    For each item without tmdb_id:
                                                      TMDB SearchMovie() / SearchTV()
                                                      → GetExternalIDs()
                                                                 │
                                                                 ▼
                                                    index.Replace(enrichedItems)
```

### Search (Sonarr/Radarr → VODarr Newznab)

```
GET /api?t=movie&imdbid=tt0133093
  → index.SearchByIMDB("tt0133093")
  → build RSS 2.0 XML with newznab:attr elements
  → return to Sonarr/Radarr

GET /api?t=tvsearch&tvdbid=81189
  → index.SearchByTVDB("81189")
  → build RSS 2.0 XML
  → return to Sonarr/Radarr
```

### Grab (Sonarr/Radarr → VODarr qBit)

```
POST /api/v2/torrents/add  (urls=http://vodarr:7878/api?t=get&id=42&type=movie)
  → fetch JSON descriptor from Newznab t=get
  → store Torrent{hash, name, state:uploading} in memory
  → goroutine: xtream.StreamURL(42, "mkv") → strm.WriteMovie(...)
  → store.SetComplete(hash, ["/data/strm/movies/..."])

GET /api/v2/torrents/info
  → return [{state:"pausedUP", progress:1.0, ...}]
  → Sonarr/Radarr detect completion, trigger import
```

---

## Integration Contract

### Sonarr / Radarr — Indexer

| Setting | Value |
|---------|-------|
| Type | Newznab |
| URL | `http://<vodarr-host>:7878` |
| API Key | (configured in vodarr, or leave blank) |
| Movie categories | `2000` |
| TV categories | `5000` |

Supported search parameters:

| Parameter | Behaviour |
|-----------|-----------|
| `imdbid=tt1234567` | Exact lookup in IMDB index |
| `tvdbid=12345` | Exact lookup in TVDB index |
| `tmdbid=12345` | Exact lookup in TMDB index |
| `q=Title` | Trigram fuzzy match on normalised title |
| `year=2021` | Combined with `q=`, filters by year |

### Sonarr / Radarr — Download Client

| Setting | Value |
|---------|-------|
| Type | qBittorrent |
| Host | `<vodarr-host>` |
| Port | `8080` |
| Username | (leave blank) |
| Password | (leave blank) |
| Category | (optional, ignored by VODarr) |

VODarr reports every torrent as immediately complete
(`state: "pausedUP"`, `progress: 1.0`). *arr applications will
trigger an import check within their normal polling cycle.

### Jellyfin

No VODarr-specific configuration needed. Jellyfin plays `.strm`
files natively — point a library at the same path that Sonarr/Radarr
import to, and Jellyfin will pick up the files automatically.

---

## Configuration Reference

```yaml
xtream:
  url: "http://provider.example.com"  # Base URL, no trailing slash
  username: "user"
  password: "pass"

tmdb:
  api_key: "your-tmdb-api-key"        # Free at themoviedb.org/settings/api

output:
  path: "/data/strm"                  # Root for all .strm files
  movies_dir: "movies"                # → /data/strm/movies/
  series_dir: "tv"                    # → /data/strm/tv/

sync:
  interval: "6h"                      # Go duration: 1h, 6h, 24h, etc.
  on_startup: true                    # Sync immediately on start

server:
  newznab_port: 7878
  qbit_port: 8080
  web_port: 3000

logging:
  level: "info"                       # debug | info | warn | error
```

---

## STRM File Naming

### Movies
```
{output.path}/{output.movies_dir}/{Movie Name (Year)}/{Movie.Name.Year.WEB-DL.ext}
```
Example:
```
/data/strm/movies/Inception (2010)/Inception.2010.WEB-DL.mkv.strm
```

### TV Series
```
{output.path}/{output.series_dir}/{Series Name}/Season NN/{Series.Name.SXXEYY.Title.WEB-DL.ext}
```
Example:
```
/data/strm/tv/Breaking Bad/Season 03/Breaking.Bad.S03E10.Fly.WEB-DL.mkv.strm
```

`.strm` file content is a single line: the Xtream stream URL.
```
http://provider.example.com/movie/user/pass/12345.mkv
```

---

## Testing

### Unit tests (no credentials required)
```bash
go test ./...
```
Covers: index matching, STRM naming, Newznab XML generation,
Newznab HTTP handler, qBit state store.

### Manual smoke test
1. Copy `config.example.yml` to `config.yml`, fill in real credentials
2. `go run ./cmd/vodarr -config config.yml`
3. Open `http://localhost:3000` — dashboard should show sync progress
4. In Radarr: Settings → Indexers → Add → Newznab, URL `http://localhost:7878`
5. Search for a movie → results should appear
6. Grab a movie → `.strm` file should appear under `output.path`

### Docker
```bash
docker compose up --build
```

---

## Roadmap (Phase 3)

| Feature | Notes |
|---------|-------|
| SQLite persistence | Survive restarts without full re-sync |
| Multiple providers | Support >1 Xtream account |
| Quality selection | Prefer HD streams over SD duplicates |
| *arr webhook listener | Instant re-sync on library events |
| API key UI | Configure indexer API key from web UI |
| Health alerting | Notify when sync fails repeatedly |
