#!/usr/bin/env bash
# Checks a snapshot release inside the Linux test container as the
# unprivileged user (make test-linux-tarball, after make release-snapshot):
# the archive for this machine is in checksums.txt and no Linux GUI
# artifact is, the README's Linux install steps work as written
# (scripts/linux-tarball-steps.sh, in a temporary folder that stands in for
# the home folder), and the installed binary reports linux and this
# architecture and scans a fixture home with --json.
set -euo pipefail

if [ "$(id -u)" -eq 0 ]; then
	echo "linux-tarball-check: refusing to run as root" >&2
	exit 1
fi
dist="${1:?usage: linux-tarball-check.sh <dist folder>}"
dist="$(cd "$dist" && pwd -P)"
case "$(uname -m)" in
x86_64) ARCH=amd64 ;;
aarch64 | arm64) ARCH=arm64 ;;
*)
	echo "linux-tarball-check: unknown machine $(uname -m)" >&2
	exit 1
	;;
esac
shopt -s nullglob
tarballs=("$dist"/kipple_*_linux_"$ARCH".tar.gz)
if [ "${#tarballs[@]}" -ne 1 ] || [ ! -f "$dist/checksums.txt" ]; then
	echo "linux-tarball-check: want one kipple_*_linux_$ARCH.tar.gz and checksums.txt in $dist (run make release-snapshot)" >&2
	exit 1
fi
name="$(basename "${tarballs[0]}")"
VERSION="${name#kipple_}"
VERSION="${VERSION%_linux_"$ARCH".tar.gz}"
if grep -q "_linux_.*kipple-gui\|kipple-gui_.*_linux" "$dist/checksums.txt"; then
	echo "FAIL  checksums.txt lists a Linux GUI artifact" >&2
	exit 1
fi
grep -q "  $name\$" "$dist/checksums.txt" || {
	echo "FAIL  $name is not in checksums.txt" >&2
	exit 1
}
echo "ok    $name is listed in checksums.txt"

work="$(mktemp -d "${TMPDIR:-/tmp}/kipple-tarball.XXXXXX")"
trap 'rm -rf "$work"' EXIT
# The README's steps, as written (shared with the BusyBox run in Alpine).
sh "$(dirname "$0")/linux-tarball-steps.sh" "$dist" "$work"

k="$work/home/.local/bin/kipple"
"$k" version --json | jq -e --arg a "$ARCH" '.os == "linux" and .arch == $a' >/dev/null
echo "ok    kipple version --json: os linux, arch $ARCH"
fx="$work/fx"
mkdir -p "$fx/.cache/pip/http"
head -c 4096 /dev/zero >"$fx/.cache/pip/http/blob"
find "$fx" -exec touch -h -d "40 days ago" {} +
KIPPLE_HOME="$fx" KIPPLE_STATE_DIR="$work/state" "$k" scan --json >"$work/plan.json"
jq -e --arg h "$fx" '.home == $h and ([.items[] | select(.rule == "pip.cache" and .selected)] | length) == 1' "$work/plan.json" >/dev/null
echo "ok    the installed binary scans a fixture home with --json (pip.cache selected)"
echo "linux-tarball-check: OK"
