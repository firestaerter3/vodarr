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
- Sonarr/Radarr search VODarr as they would any Torznab indexer
- VODarr looks up the content in the Xtream catalog
- Sonarr/Radarr "download" via a fake qBittorrent client
- VODarr writes a `.strm` file pointing to the Xtream stream URL
- Sonarr/Radarr import the `.strm` into the media library
- Jellyfin plays the `.strm`, streaming directly from the provider

No actual video is downloaded. No disk space is consumed beyond the
tiny `.strm` text files (plus ephemeral `.mkv` stubs used during
import, which are deleted via webhook after import completes).

---

## Goals

1. **Drop-in integration** — add to Sonarr/Radarr exactly like any
   other indexer + download client; no custom plugins required.

2. **ID-based matching** — resolve Xtream catalog items to IMDB/TVDB
   IDs via TMDB so Radarr's `imdbid=` and Sonarr's `tvdbid=` queries
   return exact matches, not just fuzzy title hits.

3. **Low friction setup** — single Docker container, one config file,
   no external database required. Arr instances can be auto-configured
   directly from the VODarr web UI.

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
┌─────────────────────────────────────────────────────────────────────┐
│                             VODARR                                  │
│                                                                     │
│  ┌──────────┐   ┌──────────┐   ┌──────────────┐  ┌─────────────┐  │
│  │ Xtream   │──▶│ Content  │◀──│ TMDB Client  │  │ TVDB Client │  │
│  │ Client   │   │ Index    │   │ (ID xref)    │  │ (optional)  │  │
│  └──────────┘   └────┬─────┘   └──────────────┘  └─────────────┘  │
│                      │ ◀── disk cache (.vodarr-cache.json)          │
│         ┌────────────┼────────────┬──────────────┐                 │
│         ▼            ▼            ▼              ▼                  │
│  ┌──────────┐  ┌──────────┐  ┌──────────┐  ┌──────────────────┐   │
│  │ Torznab  │  │ qBit API │  │ STRM     │  │ Web UI + Arr API │   │
│  │ API      │  │ (fake)   │  │ Writer   │  │ (React + webhook)│   │
│  │ :9091    │  │ :9092    │  │          │  │ :9090            │   │
│  └──────────┘  └──────────┘  └──────────┘  └──────────────────┘   │
└─────────────────────────────────────────────────────────────────────┘
       ▲                ▲                             ▲  │
       │                │                             │  │ (webhook POST
   Sonarr/Radarr    Sonarr/Radarr              Browser  │  on import)
   (search)         (download)                          ▼
                                                  Sonarr/Radarr
```

### Key Design Decisions

**In-memory index with disk cache**
The full Xtream catalog is fetched and enriched at sync time, then
held in memory as a set of maps keyed by IMDB ID, TVDB ID, TMDB ID,
and normalised title. After each sync the enriched index is written to
`.vodarr-cache.json` alongside the strm output. On startup, VODarr
loads the cache before scheduling the first sync — the index is
available immediately, with no cold-start delay. Only new or renamed
items hit TMDB on subsequent syncs (cached items with valid IDs are
skipped).

**Fake qBittorrent API**
Sonarr and Radarr have native qBittorrent support with well-understood
behaviour. Impersonating qBit (returning `state: "pausedUP"`,
`progress: 1.0` immediately) is more reliable than implementing a
custom download client protocol, which would require a plugin in each
*arr application.

**Torznab indexer (not Newznab)**
VODarr exposes a Torznab-compatible endpoint (a superset of Newznab).
Using Torznab ensures grabs are routed to the configured qBittorrent
download client rather than a SABnzbd/NZB client, matching VODarr's
fake qBit API.

**Newznab `t=get` returns JSON, not NZB/torrent**
The fake grab descriptor is a small JSON blob containing the Xtream
stream ID and metadata. The fake qBit handler fetches this descriptor
when processing a `torrents/add` request. This avoids implementing a
full NZB/torrent parser and keeps the grab→strm path simple.

**MKV stubs with real EBML/Matroska headers**
Sonarr and Radarr inspect grabbed files before import: they check file
size (must be ≥ a "sample" threshold) and probe the container with
ffprobe (must contain valid video/audio tracks). VODarr satisfies both
by writing a `.mkv` companion file alongside each `.strm`:

- The stub is a valid Matroska container with a real EBML header,
  probed directly from the Xtream HLS/MP4 stream URL at grab time.
- Video codec, audio codec, channel count, and duration are extracted
  from the live stream and embedded in the stub header.
- If the stream returns duration=0 (common with HLS), a minimum of
  45 minutes is used to avoid "sample" rejection.
- File size is estimated from bitrate × duration so Sonarr/Radarr's
  size checks pass even without a real download.

After import, the `.mkv` stub is deleted via the webhook (see below).
Only the `.strm` remains on disk.

**Language token injection**
Release names include a language token (e.g. `DUTCH`) derived from
the IPTV stream category prefix (e.g. `┃NL┃`). This lets Sonarr and
Radarr custom formats fire on language (e.g. score "Dutch" releases
higher), so the correct language audio track is preferred automatically.

**TMDB canonical titles in release names**
VODarr uses the official TMDB/TVDB canonical title (not the raw
provider name) when building Newznab release names. This ensures arr
can match the release back to the correct library entry, even when the
provider uses a translated or abbreviated title.

**Title cleanup patterns**
Provider names often contain noise (`(DUBBED)`, `[EXTENDED]`, country
suffixes). `sync.title_cleanup_patterns` accepts a list of regex
patterns stripped from stream names before TMDB search, improving
match rates without hardcoding provider-specific logic.

**Webhook-based stub cleanup**
After Sonarr/Radarr import a grabbed item, they POST an event to
`/api/webhook`. VODarr uses this to delete the ephemeral `.mkv` stub
file, leaving only the `.strm` in the library. This keeps the output
directory clean without requiring a separate cron job.

**Arr auto-configure**
`GET /api/arr/status` and `POST /api/arr/setup` allow the VODarr web
UI to register itself as an indexer and download client in each
configured arr instance in one click. The setup endpoint adds:
- A Torznab indexer pointing at the Newznab port
- A qBittorrent download client pointing at the qBit port
- A webhook connection pointing at the web port

This removes the need for users to manually add three separate entries
per arr instance.

**STRM file naming follows *arr conventions**
Radarr expects: `movies/{Movie Name (Year)}/{Movie.Name.Year.mkv}`
Sonarr expects: `tv/{Series Name}/Season NN/{Series.Name.SXXEYY.mkv}`
VODarr writes `.strm` files in exactly these layouts so that *arr's
import logic works without custom configuration.

---

## Data Flow

### Startup

```
Load .vodarr-cache.json (if exists)
  → index.Replace(cachedItems)         ← index ready immediately
Schedule first sync (immediate if sync.on_startup=true)
```

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
                                                    (skip if cached IDs already present)
                                                                 │
                                                                 ▼
                                                    index.Replace(enrichedItems)
                                                    cache.Save(.vodarr-cache.json)
```

### Search (Sonarr/Radarr → VODarr Torznab)

```
GET /api?t=movie&imdbid=tt0133093
  → index.SearchByIMDB("tt0133093")
  → build RSS 2.0 XML with newznab:attr elements
  → inject language token (e.g. DUTCH) in release name
  → return to Sonarr/Radarr

GET /api?t=tvsearch&tvdbid=81189
  → index.SearchByTVDB("81189")
  → build RSS 2.0 XML
  → return to Sonarr/Radarr
```

### Grab (Sonarr/Radarr → VODarr qBit)

```
POST /api/v2/torrents/add  (urls=http://vodarr:9091/api?t=get&id=42&type=movie)
  → fetch JSON descriptor from Torznab t=get
  → store Torrent{hash, name, state:uploading} in memory
  → goroutine:
      xtream.StreamURL(42, "mkv") → strm.WriteMovie(...)
      probe stream URL via HTTP HEAD → estimate file size
      probe stream URL via ffprobe → extract codec/duration
      write .mkv stub with real EBML header (video+audio tracks, duration)
  → store.SetComplete(hash, ["/data/strm/movies/..."])

GET /api/v2/torrents/info
  → return [{state:"pausedUP", progress:1.0, size:<estimated>, ...}]
  → Sonarr/Radarr detect completion, trigger import
```

### Post-import cleanup (arr → VODarr webhook)

```
POST /api/webhook  (arr sends on EpisodeFileImported / MovieFileImported)
  → parse event, extract source path
  → delete .mkv stub file at source path
  → log cleanup
```

### Arr auto-configure (browser → VODarr → arr)

```
GET /api/arr/status
  → for each configured arr instance: test API connection, check indexer+client+webhook presence
  → return per-instance status

POST /api/arr/setup  {instance: "Sonarr"}
  → POST /api/v3/indexers    (Torznab, VODarr URL)
  → POST /api/v3/downloadclients  (qBittorrent, VODarr qBit port)
  → POST /api/v3/notifications  (Webhook, VODarr /api/webhook)
```

---

## Integration Contract

### Sonarr / Radarr — Indexer

| Setting | Value |
|---------|-------|
| Type | Torznab |
| URL | `http://<vodarr-host>:9091` |
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
| Port | `9092` |
| Username | (optional, matches `server.qbit_username`) |
| Password | (optional, matches `server.qbit_password`) |
| Category | (optional, ignored by VODarr) |

VODarr reports every torrent as immediately complete
(`state: "pausedUP"`, `progress: 1.0`). *arr applications will
trigger an import check within their normal polling cycle.

### Sonarr / Radarr — Webhook

| Setting | Value |
|---------|-------|
| URL | `http://<vodarr-host>:9090/api/webhook` |
| Events | On Import / On Upgrade |

The webhook is unauthenticated (called by arr, not a user). VODarr
uses it to delete the `.mkv` stub file after import. If not
configured, stubs accumulate in the output directory.

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
  # tvdb_api_key: "your-tvdb-api-key" # Optional; enables TVDB fallback for unmatched series

output:
  path: "/data/strm"                  # Root for all .strm files
  movies_dir: "movies"                # → /data/strm/movies/
  series_dir: "tv"                    # → /data/strm/tv/

sync:
  interval: "6h"                      # Go duration: 1h, 6h, 24h, etc.
  on_startup: true                    # Sync immediately on start
  parallelism: 10                     # Concurrent TMDB enrichment workers (1–20)
  # title_cleanup_patterns:           # Regex patterns stripped before TMDB search
  #   - '\s*\(DUBBED\)'
  #   - '\s*\[EXTENDED\]'

server:
  newznab_port: 9091                  # Torznab/Newznab indexer API
  qbit_port: 9092                     # Fake qBittorrent API
  web_port: 9090                      # Web UI and webhook endpoint
  # Optional auth
  web_username: ""
  web_password: ""
  api_key: ""                         # Newznab API key; empty = no auth
  qbit_username: ""                   # qBittorrent auth (if required by arr)
  qbit_password: ""
  external_url: ""                    # Public base URL; used when generating webhook URLs for arr setup
                                      # e.g. "http://vodarr:9090" — defaults to request host if unset

logging:
  level: "info"                       # debug | info | warn | error

# arr instances — used by auto-configure (Settings → Arr Instances → Setup)
# arr:
#   instances:
#     - name: "Sonarr"
#       type: sonarr                  # "sonarr" or "radarr"
#       url: "http://<host>:8989"
#       api_key: "<api-key>"
#     - name: "Radarr"
#       type: radarr
#       url: "http://<host>:7878"
#       api_key: "<api-key>"
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

A `.mkv` stub with a valid Matroska header is written alongside each
`.strm` during the grab phase and deleted via webhook after import.

---

## Testing

### Unit tests (no credentials required)
```bash
go test -race ./...
```
Covers: index matching, STRM naming, Newznab XML generation,
Newznab HTTP handler, qBit state store, config load/save, web API.

### Manual smoke test
1. Copy `config.example.yml` to `config.yml`, fill in real credentials
2. `go run ./cmd/vodarr -config config.yml`
3. Open `http://localhost:9090` — dashboard should show sync progress
4. Settings → Arr Instances → click **Setup** (if arr instances configured), or:
   - In Radarr: Settings → Indexers → Add → Torznab, URL `http://localhost:9091`
   - In Radarr: Settings → Download Clients → Add → qBittorrent, Port `9092`
5. Search for a movie → results should appear with language and size
6. Grab a movie → `.strm` + `.mkv` stub appear under `output.path`; stub deleted after import via webhook

### Docker
```bash
docker compose up --build
```

---

## Roadmap

| Feature | Status | Notes |
|---------|--------|-------|
| *arr webhook listener | Done (v0.3.0) | Deletes .mkv stubs post-import |
| Arr auto-configure | Done (v0.3.0) | One-click indexer + client + webhook setup from web UI |
| Restart-free index | Done (v0.2.x) | Disk cache eliminates cold-start re-sync |
| Multiple providers | Planned | Support >1 Xtream account |
| Quality selection | Planned | Prefer HD streams over SD duplicates |
| SQLite persistence | Planned | Full schema persistence (cache covers most use cases) |
