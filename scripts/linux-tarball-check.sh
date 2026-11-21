#!/usr/bin/env bash
# Runs the README's Linux install steps against a snapshot release, inside
# the Linux test container as the unprivileged user (make
# test-linux-tarball, after make release-snapshot). It takes the first sh
# block under "### Release binary on Linux" in README.md and runs its lines
# as written, except the ones that set VERSION, ARCH or BASE (taken from the
# snapshot instead) and the downloads and cosign verification (no network,
# and a snapshot is unsigned). Everything happens in a temporary folder that
# stands in for the home folder, so ~/.local/bin is inside it. Then the
# installed binary must report linux and this architecture, and scan a
# fixture home with --json.
set -euo pipefail

if [ "$(id -u)" -eq 0 ]; then
	echo "linux-tarball-check: refusing to run as root" >&2
	exit 1
fi
dist="${1:?usage: linux-tarball-check.sh <dist folder>}"
dist="$(cd "$dist" && pwd -P)"
readme="$(cd "$(dirname "$0")/.." && pwd -P)/README.md"
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
mkdir -p "$work/home" "$work/dl"
cp "$dist/$name" "$dist/checksums.txt" "$work/dl/"

# The README block, with backslash continuations joined.
steps="$(awk '/^### Release binary on Linux/{f=1} f&&/^```sh/{b=1; next} b&&/^```/{exit} b' "$readme" | sed -e ':a' -e '/\\$/N; s/\\\n//; ta')"
if [ -z "$steps" ]; then
	echo "FAIL  README.md has no sh block under \"### Release binary on Linux\"" >&2
	exit 1
fi
ran=0
while IFS= read -r line; do
	case "$line" in
	"" | "#"* | VERSION=* | ARCH=* | BASE=* | curl\ * | cosign\ *) continue ;;
	esac
	echo "run   $line"
	(cd "$work/dl" && HOME="$work/home" PATH="$work/home/.local/bin:/usr/bin:/bin" VERSION="$VERSION" ARCH="$ARCH" bash -euo pipefail -c "$line") >"$work/out" 2>&1 || {
		echo "FAIL  README step failed: $line" >&2
		cat "$work/out" >&2
		exit 1
	}
	ran=$((ran + 1))
done <<<"$steps"
last="$(tail -n 1 <<<"$steps")"
if [ "$ran" -lt 4 ] || [ "$last" != "kipple version" ]; then
	echo "FAIL  the README steps ran $ran commands and end with \"$last\", not kipple version" >&2
	exit 1
fi
if ! grep -q "^Platform: linux/$ARCH\$" "$work/out"; then
	echo "FAIL  kipple version does not print Platform: linux/$ARCH:" >&2
	cat "$work/out" >&2
	exit 1
fi
echo "ok    the README's Linux steps (checksum, unpack, install, kipple version) work; it prints Platform: linux/$ARCH"

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
