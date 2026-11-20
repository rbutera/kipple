#!/usr/bin/env bash
# Wraps a kipple-gui binary into kipple.app, ad-hoc signs it and zips it.
#
# Usage: scripts/build-app.sh <kipple-gui binary> <version> <output .zip>
#
# goreleaser runs it on the universal (arm64 + amd64) kipple-gui binary; CI
# runs it on a host build. The bundle is ad-hoc signed, not signed with a
# Developer ID and not notarised, so macOS asks before the first launch (see
# the README). No fyne CLI is needed.
set -euo pipefail

if [ "$#" -ne 3 ]; then
	echo "usage: $0 <kipple-gui binary> <version> <output .zip>" >&2
	exit 2
fi
bin="$(cd "$(dirname "$1")" && pwd -P)/$(basename "$1")"
version="$2"
case "$3" in
/*) out="$3" ;;
*) out="$(pwd -P)/$3" ;;
esac
root="$(cd "$(dirname "$0")/.." && pwd -P)"

# CFBundleShortVersionString takes numbers only: 0.2.0-snapshot+abc -> 0.2.0.
short="$(printf '%s' "$version" | sed -E 's/^v//; s/[^0-9.].*$//')"
[ -n "$short" ] || short="0.0.0"

work="$(mktemp -d "${TMPDIR:-/tmp}/kipple-app.XXXXXX")"
trap 'command rm -rf "$work"' EXIT
app="$work/kipple.app"
mkdir -p "$app/Contents/MacOS" "$app/Contents/Resources"
command cp "$bin" "$app/Contents/MacOS/kipple"
command cp "$root/assets/brand/kipple-icon.icns" "$app/Contents/Resources/kipple.icns"
cat >"$app/Contents/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>CFBundleName</key><string>kipple</string>
	<key>CFBundleDisplayName</key><string>kipple</string>
	<key>CFBundleIdentifier</key><string>com.rbutera.kipple</string>
	<key>CFBundleExecutable</key><string>kipple</string>
	<key>CFBundleIconFile</key><string>kipple</string>
	<key>CFBundlePackageType</key><string>APPL</string>
	<key>CFBundleShortVersionString</key><string>${short}</string>
	<key>CFBundleVersion</key><string>${short}</string>
	<key>CFBundleInfoDictionaryVersion</key><string>6.0</string>
	<key>LSMinimumSystemVersion</key><string>11.0</string>
	<key>LSApplicationCategoryType</key><string>public.app-category.utilities</string>
	<key>NSHighResolutionCapable</key><true/>
	<key>NSHumanReadableCopyright</key><string>MIT licence. kipple only moves things to the Trash.</string>
</dict>
</plist>
PLIST
plutil -lint "$app/Contents/Info.plist" >/dev/null
codesign --force --deep --sign - "$app"
codesign --verify --deep --strict "$app"
mkdir -p "$(dirname "$out")"
command rm -f "$out"
ditto -c -k --norsrc --noextattr --noqtn --keepParent "$app" "$out"
echo "$out"
