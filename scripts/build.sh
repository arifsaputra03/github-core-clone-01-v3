#!/bin/sh
# Build the forge binary: a statically linked Linux amd64 binary, stripped.
# Run from the repository root.
set -e
cd "$(dirname "$0")/.."
export CGO_ENABLED=0
GO="$HOME/workspace/go/go/bin/go"
if [ ! -x "$GO" ]; then GO=go; fi
"$GO" build -trimpath -ldflags "-s -w" -o forge ./cmd/forge
ls -l forge
