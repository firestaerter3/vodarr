#!/bin/bash
# Sonarr custom import script for VODarr .strm files.
#
# Sonarr v4 passes:  $1 = source path   $2 = destination path
# (also available as env vars sonarr_sourcepath / sonarr_destinationpath)
#
# VODarr writes TWO files per episode:
#   episode.WEB-DL.strm  — stream URL content (for Plex/Emby)
#   episode.WEB-DL.mkv   — empty stub (passes Sonarr's video-extension filter)
#
# Sonarr's import pipeline sees the .mkv stub, calls this script with the .mkv
# paths.  We detect the companion .strm, create a .strm symlink in the library
# for Plex/Emby, and also create the .mkv symlink Sonarr expects.
#
# Deploy to DUMB container:
#   scp scripts/sonarr-import.sh root@<host>:/path/to/sonarr-import.sh
#   chmod +x /path/to/sonarr-import.sh
#   Configure in Sonarr: Settings → Media Management → Custom Script

set -euo pipefail

SOURCE_FILE="${1:-}"
DEST_FILE="${2:-}"

if [ -z "$SOURCE_FILE" ] || [ -z "$DEST_FILE" ]; then
    echo "Usage: $0 <source> <dest>" >&2
    exit 1
fi

# VODarr companion detection:
# If the source is an empty .mkv stub with a real .strm alongside it, use the
# .strm as the actual source so library entries point to streaming URLs.
STRM_SOURCE="${SOURCE_FILE%.mkv}.strm"
if [ "${SOURCE_FILE%.mkv}" != "$SOURCE_FILE" ] && [ -f "$STRM_SOURCE" ]; then
    # Create a .strm symlink in the library alongside the .mkv Sonarr expects.
    # Plex/Emby will resolve the .strm to its URL and stream from there.
    DEST_STRM="${DEST_FILE%.mkv}.strm"
    mkdir -p "$(dirname "$DEST_STRM")"
    [ -e "$DEST_STRM" ] || [ -L "$DEST_STRM" ] && rm -f "$DEST_STRM"
    ln -sf "$STRM_SOURCE" "$DEST_STRM"

    SOURCE_FILE="$STRM_SOURCE"
fi

# Create the symlink at Sonarr's expected destination path.
mkdir -p "$(dirname "$DEST_FILE")"
[ -e "$DEST_FILE" ] || [ -L "$DEST_FILE" ] && rm -f "$DEST_FILE"
ln -sf "$SOURCE_FILE" "$DEST_FILE"

[ -L "$DEST_FILE" ] && exit 0 || exit 1
