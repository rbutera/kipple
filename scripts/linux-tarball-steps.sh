#!/bin/sh
# Runs the README's "Release binary on Linux" steps as written against a
# snapshot release in dist/, as an unprivileged user. POSIX sh, so it runs
# on BusyBox (Alpine) as well as on GNU systems: make test-linux-tarball
# runs it in the Linux test image and in alpine:3.20.
#
# It takes the first sh block under "### Release binary on Linux" in
# README.md and runs its lines one by one, except those that set VERSION,
# ARCH or BASE (taken from the snapshot archive instead) and the downloads
# and cosign verification (no network, and a snapshot is unsigned). WORK
# stands in for the home folder, so ~/.local/bin is inside it. The block
# must end with kipple version, which must print Platform: linux/<arch>.
set -eu

if [ "$(id -u)" -eq 0 ]; then
	echo "linux-tarball-steps: refusing to run as root" >&2
	exit 1
fi
dist="${1:?usage: linux-tarball-steps.sh <dist folder> <work folder>}"
work="${2:?usage: linux-tarball-steps.sh <dist folder> <work folder>}"
readme="$(cd "$(dirname "$0")/.." && pwd -P)/README.md"
case "$(uname -m)" in
x86_64) ARCH=amd64 ;;
aarch64 | arm64) ARCH=arm64 ;;
*)
	echo "linux-tarball-steps: unknown machine $(uname -m)" >&2
	exit 1
	;;
esac
set -- "$dist"/kipple_*_linux_"$ARCH".tar.gz
if [ "$#" -ne 1 ] || [ ! -f "$1" ] || [ ! -f "$dist/checksums.txt" ]; then
	echo "linux-tarball-steps: want one kipple_*_linux_$ARCH.tar.gz and checksums.txt in $dist (run make release-snapshot)" >&2
	exit 1
fi
name="$(basename "$1")"
VERSION="${name#kipple_}"
VERSION="${VERSION%_linux_"$ARCH".tar.gz}"
mkdir -p "$work/home" "$work/dl"
cp "$1" "$dist/checksums.txt" "$work/dl/"

# The README block, with backslash continuations joined.
awk '/^### Release binary on Linux/{f=1} f&&/^```sh/{b=1; next} b&&/^```/{exit} b' "$readme" |
	sed -e ':a' -e '/\\$/N; s/\\\n//; ta' >"$work/steps"
if [ ! -s "$work/steps" ]; then
	echo "FAIL  README.md has no sh block under \"### Release binary on Linux\"" >&2
	exit 1
fi
ran=0
last=""
while IFS= read -r line; do
	case "$line" in
	"" | "#"* | VERSION=* | ARCH=* | BASE=* | curl\ * | cosign\ *) continue ;;
	esac
	echo "run   $line"
	if ! (cd "$work/dl" && HOME="$work/home" PATH="$work/home/.local/bin:/usr/bin:/bin" VERSION="$VERSION" ARCH="$ARCH" sh -eu -c "$line") >"$work/out" 2>&1; then
		echo "FAIL  README step failed: $line" >&2
		cat "$work/out" >&2
		exit 1
	fi
	ran=$((ran + 1))
	last="$line"
done <"$work/steps"
if [ "$ran" -lt 4 ] || [ "$last" != "kipple version" ]; then
	echo "FAIL  the README steps ran $ran commands and end with \"$last\", not kipple version" >&2
	exit 1
fi
if ! grep -q "^Platform: linux/$ARCH\$" "$work/out"; then
	echo "FAIL  kipple version does not print Platform: linux/$ARCH:" >&2
	cat "$work/out" >&2
	exit 1
fi
echo "ok    the README's Linux steps (checksum, unpack, install, kipple version) work here; it prints Platform: linux/$ARCH"
