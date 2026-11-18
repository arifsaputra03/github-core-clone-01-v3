#!/bin/sh
# Run the forge locally with the embedded file-based store.
# The server prints FORGE_PORT=<port> on stdout when it is listening.
# The bearer token for local testing is FORGE_TOKEN (default: dev-token).
set -e
cd "$(dirname "$0")/.."
if [ ! -x ./forge ]; then
  ./scripts/build.sh
fi
export FORGE_DATA_DIR="${FORGE_DATA_DIR:-$PWD/.data}"
export FORGE_TOKEN="${FORGE_TOKEN:-dev-token}"
export FORGE_PORT="${FORGE_PORT:-0}"
exec ./forge
