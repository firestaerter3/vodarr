# VODarr

Bridges an Xtream Codes IPTV provider with Sonarr and Radarr. Search your IPTV catalog through the standard \*arr interface, "download" content as `.strm` files, and play directly in Jellyfin — no video is ever downloaded to disk.

## How it works

1. **Sync** — VODarr fetches your full IPTV catalog from the Xtream provider, enriches each title with IMDB/TVDB IDs via TMDB, and holds everything in memory.
2. **Search** — Sonarr/Radarr query VODarr like any Newznab indexer (port 7878). Searches match by IMDB ID, TVDB ID, or fuzzy title.
3. **Grab** — Sonarr/Radarr send a "download" request to VODarr's fake qBittorrent client (port 8080). VODarr writes a tiny `.strm` file pointing to the stream URL and immediately reports the torrent as complete.
4. **Play** — Jellyfin imports the `.strm` file and streams directly from the IPTV provider on playback.

## Quick start

```bash
cp config.example.yml config.yml
# Edit config.yml with your Xtream and TMDB credentials
docker compose up -d
```

Open `http://<host>:3000` for the web UI.

## Sonarr / Radarr setup

**Indexer** (Newznab):

| Setting | Value |
|---------|-------|
| URL | `http://<vodarr-host>:7878` |
| Movie categories | `2000` |
| TV categories | `5000` |

**Download client** (qBittorrent):

| Setting | Value |
|---------|-------|
| Host | `<vodarr-host>` |
| Port | `8080` |

## Shared path requirement

VODarr writes `.strm` files to the configured `output.path`. Sonarr, Radarr, and Jellyfin all need read access to that same directory. When running via Docker, mount the same volume path in all containers.

## ⚠️ Security — credentials in `.strm` files

Every `.strm` file contains your Xtream username and password in the stream URL:

```
http://provider.example.com/movie/username/password/12345.mkv
```

This is a limitation of the Xtream Codes protocol — credentials are embedded in stream URLs by design and cannot be removed.

**What this means in practice:**

- Anyone with read access to your `.strm` output directory can see your Xtream credentials.
- Media players (Jellyfin, Plex, Emby) and library scanners will have access to these files in the normal course of operation.
- If your media server logs playback errors, the full URL (including credentials) may appear in logs.

**Recommended mitigations:**

- Restrict the output directory to the minimum set of users/processes that need it (`chmod 750`).
- Do not expose the output directory publicly or via an unauthenticated network share.
- If your IPTV provider supports sub-accounts or connection limits, create a dedicated account for VODarr with a unique password. That way, if credentials are ever exposed, you can rotate just that account without affecting other devices.
- Enable the VODarr web UI authentication (`server.web_username` / `server.web_password`) if the web port is accessible on your network.

## Configuration

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
  on_startup: true

server:
  newznab_port: 7878
  qbit_port: 8080
  web_port: 3000
  # Optional auth
  web_username: ""
  web_password: ""
  api_key: ""                         # Newznab API key; empty = no auth

logging:
  level: "info"                       # debug | info | warn | error
```

## Architecture & design decisions

See [`docs/intent.md`](docs/intent.md) for a detailed breakdown of the architecture, data flow, and the reasoning behind key design choices.
