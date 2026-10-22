#!/bin/sh
# The most common LANLeecher API calls. Needs curl and, for pretty output, jq.
#   export LANLEECHER_URL=https://lanleecher.example LANLEECHER_TOKEN=llk_…
#   sh curl.sh
set -eu
: "${LANLEECHER_URL:?set LANLEECHER_URL}"
: "${LANLEECHER_TOKEN:?set LANLEECHER_TOKEN}"

api() {
    curl -fsS -H "Authorization: Bearer $LANLEECHER_TOKEN" "$LANLEECHER_URL/api/v1/$1"
}
pretty() { if command -v jq >/dev/null; then jq "$@"; else cat; fi; }

echo "== Top 10 downloaders of the running event"
api "rankings/download?limit=10" | pretty -r '.entries[] | "\(.rank). \(.name)  \(.bytes_down / 1e9 | floor) GB down"'

echo "== Top 10 uploaders over all events, hidden entries included"
api "rankings/upload?event=all&limit=10&include_hidden=true" | pretty -r '.entries[] | "\(.rank). \(.name)  \(.bytes_up / 1e9 | floor) GB up\(if .hidden then " (hidden)" else "" end)"'

echo "== Events"
api "events" | pretty -r '.events[] | "\(.slug)  \(.name)  active=\(.active)"'

echo "== Search (needs read:clients)"
api "clients/search?q=desktop" | pretty -r '.results[] | "\(.mac)  \(.ip)  \(.hostname)"' || true
