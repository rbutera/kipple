#!/usr/bin/env bash
# Captures the GUI's screens from a fixture home, never the real one.
#
# Usage: scripts/gui-screenshots.sh <output-folder>
#
# It builds a realistic fixture home in a new temp folder with
# tools/mkfixture -gui, points KIPPLE_HOME and KIPPLE_STATE_DIR at it, and
# runs the gui-tagged TestScreenshots, which drives the real window through
# scan, review (light, dark, and the 760x520 minimum), confirm and done with
# Fyne's software renderer. No window opens, and the only items that move
# are the fixture's, into the fixture's own .Trash. The temp folder is
# removed afterwards.
set -euo pipefail

if [ "$#" -ne 1 ]; then
	echo "usage: $0 <output-folder>" >&2
	exit 2
fi
mkdir -p "$1"
out="$(cd "$1" && pwd -P)"

cd "$(dirname "$0")/.."
work="$(mktemp -d "${TMPDIR:-/tmp}/kipple-gui-shots.XXXXXX")"
work="$(cd "$work" && pwd -P)"
cleanup() { chmod -R u+w "$work" 2>/dev/null || true; command rm -rf "$work"; }
trap cleanup EXIT

home="$work/home"
mkdir -p "$home" "$work/state"

# Refuse a fixture that is, is inside, or contains the real home folder.
real="$(cd ~ && pwd -P)"
case "$home/" in "$real"/*) echo "refusing: $home is inside the real home folder" >&2; exit 1 ;; esac
case "$real/" in "$home"/*) echo "refusing: $home contains the real home folder" >&2; exit 1 ;; esac

GOTOOLCHAIN=local go run ./tools/mkfixture -gui "$home"

export KIPPLE_HOME="$home"
export KIPPLE_STATE_DIR="$work/state"
export KIPPLE_SCREENSHOT_HOME="$home"
export KIPPLE_SCREENSHOTS="$out"
GOTOOLCHAIN=local go test -tags gui -count=1 -run '^TestScreenshots$' ./cmd/kipple-gui

ls -1 "$out"/gui-*.png
