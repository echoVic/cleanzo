#!/bin/sh
set -eu

# The sidebar/favicon SVG is the single source for the desktop icon.
# Install librsvg (rsvg-convert) to regenerate; MyGo creates ICNS/ICO at build time.
task_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
if ! command -v rsvg-convert >/dev/null 2>&1; then
  echo "rsvg-convert is required (macOS: brew install librsvg)" >&2
  exit 1
fi
rsvg-convert --width 2048 --height 2048 \
  --output "$task_root/resources/icon.png" "$task_root/public/favicon.svg"
echo "Generated resources/icon.png from public/favicon.svg"
