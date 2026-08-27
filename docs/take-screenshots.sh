#!/bin/sh
# Regenerates the README screenshots docs/file.png and docs/docs.png.
# Run via `make screenshots` from the repository root.
#
# Uses playwright-cli. When .playwright/cli.config.json declares a
# cdpEndpoint, playwright-cli attaches to that browser (required inside the
# Claude Code sandbox, which cannot launch one) and only the tab it opened is
# closed. Without the config it launches its own headless browser and closes
# it at the end.
set -eu

cd "$(dirname "$0")/.."
PORT="${PORT:-8642}"
URL="http://127.0.0.1:$PORT"

if curl -sf -o /dev/null --max-time 1 "$URL/"; then
    echo "port $PORT is already serving; stop it or set PORT" >&2
    exit 1
fi

# Serve from a throwaway HOME so the real ~/.mdfmt registry is untouched.
# Repo-local (gitignored) because system temp dirs are not writable when
# running inside the Claude Code sandbox.
TMP_HOME="$PWD/.screenshots-tmp"
rm -rf "$TMP_HOME"
mkdir -p "$TMP_HOME"
HOME="$TMP_HOME" ./mdfmt serve --port "$PORT" --path-token none . &
SERVER_PID=$!
cleanup() {
    kill "$SERVER_PID" 2>/dev/null || true
    rm -rf "$TMP_HOME"
}
trap cleanup EXIT

for _ in $(seq 1 20); do
    curl -sf -o /dev/null "$URL/" && break
    sleep 0.25
done

playwright-cli open "$URL/docs/test.md"
playwright-cli resize 1440 968
playwright-cli screenshot --hires --filename "$PWD/docs/file.png"
playwright-cli goto "$URL/"
playwright-cli resize 1440 520
playwright-cli screenshot --hires --filename "$PWD/docs/docs.png"

if grep -qs cdpEndpoint .playwright/cli.config.json; then
    playwright-cli tab-close
else
    playwright-cli close
fi
