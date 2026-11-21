#!/usr/bin/env bash
# Real-mount check for kipple on Linux (openspec linux-port task 2.7,
# "Real mounts checked"). make test-linux-mounts runs it as root in a
# throwaway --privileged container whose only mount is the read-only
# repository (the kipple binary, cross-compiled by make, is inside it under
# dist/). As root it builds a fixture home under /tmp/fx and creates real
# mounts inside the fixture only:
#   - a bind mount of the fixture's Documents at .cache/foo/docs
#   - a bind mount at .cache/bar itself
#   - a tmpfs at .cache/baz/t
#   - an overlay at .cache/ovl whose lower layer holds a cache item, and an
#     overlay as the pip.cache root (.cache/pip) whose lower layer holds the
#     item http
#   - a bind mount of the fixture's .ssh over the Trash's info folder (the
#     wave 1 red-team layout)
#   - a plain control item, .cache/plain
# then drops to uid 1000 with setpriv and runs kipple scan and clean --apply
# against KIPPLE_HOME=/tmp/fx. It checks that the items holding a mount are
# contains_mount, the mount points different_device, a Trash folder that is
# a mount refused with trash_access, that only the control item moved, and
# that nothing outside it changed (sha256 tree). It never touches a real
# home: the container's own root home is not used and the host's is not
# mounted.
set -euo pipefail

K="${1:?usage: linux-mount-check.sh <kipple binary>}"
if [ "$(id -u)" -ne 0 ]; then
	echo "linux-mount-check: must run as root inside the throwaway container (it creates mounts)" >&2
	exit 1
fi
if [ ! -x "$K" ]; then
	echo "linux-mount-check: $K is not an executable kipple binary" >&2
	exit 1
fi

fx=/tmp/fx
state=/tmp/state
work=/tmp/work
failures=0
fail() {
	echo "FAIL  $*"
	failures=$((failures + 1))
}
ok() { echo "ok    $*"; }

mkdir -p "$fx" "$state" "$work" /tmp/ovl/lower/item /tmp/ovl/upper /tmp/ovl/work
old="30 days ago"
mkfile() { mkdir -p "$(dirname "$1")" && printf '%s' "$2" >"$1"; }
mkfile "$fx/Documents/letter.txt" "dear"
mkfile "$fx/.ssh/id_ed25519" "private key"
mkfile "$fx/.cache/foo/blob" "foo"
mkdir -p "$fx/.cache/foo/docs"
mkfile "$fx/barsrc/blob" "bar"
mkdir -p "$fx/.cache/bar"
mkfile "$fx/.cache/baz/blob" "baz"
mkdir -p "$fx/.cache/baz/t"
mkdir -p "$fx/.cache/ovl"
mkfile /tmp/ovl/lower/item/blob "lower"
mkfile "$fx/.cache/plain/blob" "plain"
mkdir -p "$fx/.cache/pip" /tmp/ovl2/lower/http /tmp/ovl2/upper /tmp/ovl2/work
mkfile /tmp/ovl2/lower/http/blob "pip lower"
mkdir -p "$fx/.local/share/Trash/files" "$fx/.local/share/Trash/info"
chmod 700 "$fx/.local/share/Trash" "$fx/.local/share/Trash/files" "$fx/.local/share/Trash/info" "$fx/.ssh"
chown -R 1000:1000 "$fx" "$state" "$work" /tmp/ovl /tmp/ovl2
find "$fx" /tmp/ovl /tmp/ovl2 -exec touch -h -d "$old" {} +

mount --bind "$fx/Documents" "$fx/.cache/foo/docs"
mount --bind "$fx/barsrc" "$fx/.cache/bar"
mount -t tmpfs -o size=1m,mode=0755,uid=1000,gid=1000 tmpfs "$fx/.cache/baz/t"
mount -t overlay overlay -o lowerdir=/tmp/ovl/lower,upperdir=/tmp/ovl/upper,workdir=/tmp/ovl/work "$fx/.cache/ovl"
mount -t overlay overlay -o lowerdir=/tmp/ovl2/lower,upperdir=/tmp/ovl2/upper,workdir=/tmp/ovl2/work "$fx/.cache/pip"
cleanup() {
	for m in "$fx/.local/share/Trash/info" "$fx/.cache/pip" "$fx/.cache/ovl" "$fx/.cache/baz/t" "$fx/.cache/bar" "$fx/.cache/foo/docs"; do
		mountpoint -q "$m" && umount "$m"
	done
	return 0
}
trap cleanup EXIT
find "$fx/.cache" -maxdepth 1 -exec touch -h -d "$old" {} +

# kipple runs as the unprivileged user, against the fixture only.
as_user() {
	setpriv --reuid=1000 --regid=1000 --clear-groups --reset-env \
		env HOME=/home/kipple PATH=/usr/bin:/bin KIPPLE_HOME="$fx" KIPPLE_STATE_DIR="$state" "$@"
}
tree() { (cd "$1" && find . -xdev -path ./.local/share/Trash -prune -o -path ./.cache/plain -prune -o -type f -print0 | LC_ALL=C sort -z | xargs -0 sha256sum); }

# 1. The wave 1 red-team layout: ~/.ssh bind-mounted over the Trash's info
#    folder. kipple must refuse the Trash (trash_access) and write nothing
#    into .ssh.
mount --bind "$fx/.ssh" "$fx/.local/share/Trash/info"
ssh_before="$(cd "$fx/.ssh" && find . | LC_ALL=C sort)"
set +e
as_user "$K" clean --apply --json >"$work/trashinfo.json" 2>"$work/trashinfo.err"
code=$?
set -e
ssh_after="$(cd "$fx/.ssh" && find . | LC_ALL=C sort)"
if [ "$code" -eq 1 ] && jq -e '.error.code == "trash_access"' "$work/trashinfo.json" >/dev/null && [ "$ssh_before" = "$ssh_after" ]; then
	ok "a bind mount over Trash/info is refused with trash_access and nothing is written into it"
else
	fail "bind mount over Trash/info: exit $code, $(cat "$work/trashinfo.json"), .ssh before [$ssh_before] after [$ssh_after]"
fi
umount "$fx/.local/share/Trash/info"
if [ -e "$fx/.cache/plain/blob" ]; then ok "the control item did not move while the Trash was refused"; else fail "the control item moved"; fi

# 2. Scan and clean with the mounts in place.
before="$(tree "$fx")"
as_user "$K" scan --json >"$work/plan.json"
reasons() { jq -r --arg p "$fx/$1" '[.items[] | select(.path == $p) | .reasons[]] | join(",")' "$work/plan.json"; }
selected() { jq -r --arg p "$fx/$1" '[.items[] | select(.path == $p) | .selected][0]' "$work/plan.json"; }
idof() { jq -r --arg p "$fx/$1" '[.items[] | select(.path == $p) | .id][0] // empty' "$work/plan.json"; }

for it in .cache/foo .cache/baz; do
	case "$(reasons "$it")" in
	*contains_mount*) ok "scan: $it is contains_mount" ;;
	*) fail "scan: $it reasons [$(reasons "$it")], want contains_mount" ;;
	esac
done
for it in .cache/bar .cache/ovl .cache/pip/http; do
	r="$(reasons "$it")"
	if [ -z "$(idof "$it")" ] || [[ "$r" == *different_device* ]]; then ok "scan: $it is not offered (${r:-not listed})"; else fail "scan: $it reasons [$r], want different_device"; fi
done
if [ "$(selected .cache/plain)" = true ]; then ok "scan: the control item is selected"; else fail "scan: control item not selected"; fi

ids=""
for it in .cache/foo .cache/bar .cache/baz .cache/ovl .cache/pip/http .cache/plain; do
	id="$(idof "$it")"
	[ -n "$id" ] && ids="${ids:+$ids,}$id"
done
set +e
as_user "$K" clean --apply --json --only "$ids" >"$work/res.json" 2>"$work/res.err"
code=$?
set -e
moved="$(jq -r '[.moved[].path] | join(",")' "$work/res.json")"
if [ "$moved" = "$fx/.cache/plain" ]; then ok "clean --apply moved only the control item (exit $code)"; else fail "clean --apply moved [$moved] (exit $code): $(cat "$work/res.err")"; fi
# A same-device bind mount is contains_mount alone. A mount of another
# file system (the tmpfs) also stops the walk there, so the item's age
# cannot be proven and too_recent is named first; contains_mount is among
# the reasons in the detail.
for it in .cache/foo .cache/baz; do
	r="$(jq -r --arg p "$fx/$it" '[.skipped[] | select(.path == $p) | "\(.reason): \(.detail)"][0] // empty' "$work/res.json")"
	case "$r" in
	contains_mount:* | *contains_mount*) ok "clean: $it skipped ($r)" ;;
	*) fail "clean: $it skipped with [$r], want contains_mount" ;;
	esac
done
r="$(jq -r --arg p "$fx/.cache/foo" '[.skipped[] | select(.path == $p) | .reason][0] // empty' "$work/res.json")"
if [ "$r" = contains_mount ]; then ok "clean: the bind-mount holder's reason is contains_mount"; else fail "clean: .cache/foo reason [$r]"; fi
for it in .cache/bar .cache/ovl; do
	if [ -d "$fx/$it" ] && mountpoint -q "$fx/$it"; then ok "clean: $it is still mounted in place"; else fail "clean: $it moved"; fi
done
if [ -e "$fx/.cache/pip/http/blob" ] && mountpoint -q "$fx/.cache/pip"; then ok "clean: the overlay pip item is still in place"; else fail "clean: the overlay pip item moved"; fi
if [ -e "$fx/Documents/letter.txt" ] && [ -e "$fx/.cache/foo/docs/letter.txt" ]; then ok "Documents is intact and still mounted"; else fail "Documents changed"; fi
entries="$(find "$fx/.local/share/Trash/files" -mindepth 1 -maxdepth 1 | wc -l)"
if [ "$entries" -eq 1 ] && [ -e "$fx/.local/share/Trash/files/plain/blob" ]; then ok "the Trash holds only the control item"; else fail "the Trash holds $entries entries"; fi
after="$(tree "$fx")"
if [ "$before" = "$after" ]; then ok "nothing else in the fixture changed (sha256 tree)"; else fail "the fixture changed:"; diff <(echo "$before") <(echo "$after") || true; fi

# 3. A btrfs home (wave 2 red-team R5): stat's device is per subvolume and
#    never mountinfo's, which made every KIPPLE_HOME refused. A loop btrfs
#    image with a subvolume @home mounted at /bh stands in for the real
#    home (HOME=/bh/kh). Only the loop device attached here is detached.
btrfs_check() {
	local img=/tmp/btrfs.img lo
	truncate -s 300M "$img"
	mkfs.btrfs -q "$img" >/dev/null
	lo="$(losetup -f --show "$img")"
	mkdir -p /tmp/bt /tmp/bh
	mount "$lo" /tmp/bt
	btrfs -q subvolume create /tmp/bt/@home
	mkdir -p /tmp/bt/@home/kh
	mount -o subvol=@home "$lo" /tmp/bh
	btrfs -q subvolume create /tmp/bh/fx
	mkfile /tmp/bh/fx/.cache/a/blob "a"
	btrfs -q subvolume create /tmp/bh/fx/.cache/sub
	mkfile /tmp/bh/fx/.cache/sub/blob "s"
	mkdir -p /tmp/bh/fx/.local/share/Trash/files /tmp/bh/fx/.local/share/Trash/info /tmp/btfx /tmp/btstate
	mkfile /tmp/btfx/.cache/b/blob "b"
	chmod 700 /tmp/bh/fx/.local/share/Trash /tmp/bh/fx/.local/share/Trash/files /tmp/bh/fx/.local/share/Trash/info
	chown -R 1000:1000 /tmp/bt /tmp/bh /tmp/btfx /tmp/btstate
	find /tmp/bh/fx /tmp/btfx -exec touch -h -d "$old" {} +
	bt_user() {
		setpriv --reuid=1000 --regid=1000 --clear-groups --reset-env \
			env HOME="$1" PATH=/usr/bin:/bin KIPPLE_HOME="$2" KIPPLE_STATE_DIR=/tmp/btstate "${@:3}"
	}
	set +e
	bt_user /tmp/bh/kh /tmp/btfx "$K" scan --json >"$work/bt1.json" 2>"$work/bt1.err"
	c1=$?
	bt_user /tmp/bh/kh /tmp/bt/@home/kh "$K" scan --json >"$work/bt2.json" 2>"$work/bt2.err"
	c2=$?
	bt_user /tmp/btfx /tmp/bh/fx "$K" scan --json >"$work/bt3.json" 2>"$work/bt3.err"
	c3=$?
	bt_user /tmp/btfx /tmp/bh/fx "$K" clean --apply --json >"$work/bt4.json" 2>"$work/bt4.err"
	c4=$?
	set -e
	if [ "$c1" -eq 0 ]; then ok "btrfs home: a separate KIPPLE_HOME fixture is accepted"; else fail "btrfs home: fixture refused (exit $c1): $(cat "$work/bt1.err")"; fi
	if [ "$c2" -eq 1 ] && grep -q "real home" "$work/bt2.err"; then ok "btrfs home: the home through the top-level mount is refused"; else fail "btrfs home: alias accepted (exit $c2)"; fi
	r="$(jq -r '[.items[] | select(.path == "/tmp/bh/fx/.cache/sub") | .reasons[]] | join(",")' "$work/bt3.json")"
	if [ "$c3" -eq 0 ] && { [ -z "$r" ] || [[ "$r" == *different_device* ]]; }; then ok "btrfs fixture: a subvolume item is not offered (${r:-not listed})"; else fail "btrfs fixture: subvolume item reasons [$r] (exit $c3)"; fi
	if [ -e /tmp/bh/fx/.local/share/Trash/files/a/blob ] && [ -e /tmp/bh/fx/.cache/sub/blob ]; then ok "btrfs fixture: the plain item moved, the subvolume stayed (exit $c4)"; else fail "btrfs fixture: clean --apply exit $c4: $(cat "$work/bt4.err")"; fi
	umount /tmp/bh
	umount /tmp/bt
	losetup -d "$lo"
	rm -f "$img"
}
btrfs_check

# 4 and 5 build many-item fixtures: <root>/home with .ssh and N aged items
# .cache/cNNN/{blob,m/}, and a Trash.
many_fixture() {
	local root="$1" n="$2" i
	mkfile "$root/home/.ssh/id_ed25519" "private key"
	mkdir -p "$root/home/.local/share/Trash/files" "$root/home/.local/share/Trash/info" "$root/state" "$root/out"
	for i in $(seq -w 1 "$n"); do
		mkfile "$root/home/.cache/c$i/blob" "z"
		mkdir -p "$root/home/.cache/c$i/m"
	done
	chmod 700 "$root/home/.ssh" "$root/home/.local/share/Trash" "$root/home/.local/share/Trash/files" "$root/home/.local/share/Trash/info"
	chown -R 1000:1000 "$root"
	find "$root/home" -exec touch -h -d "$old" {} +
}
mounts_under() { awk '{print $5}' /proc/self/mountinfo | grep -c "^$1" || true; }
unmount_under() {
	local m
	for m in $(awk '{print $5}' /proc/self/mountinfo | grep "^$1" | sort -r || true); do umount -l "$m"; done
}
gcc -O2 -o /tmp/flip test/linux/flip.c
gcc -O2 -o /tmp/nostatx test/linux/nostatx.c
user_in() {
	local root="$1"
	shift
	setpriv --reuid=1000 --regid=1000 --clear-groups --reset-env \
		env HOME=/home/kipple PATH=/usr/bin:/bin KIPPLE_HOME="$root/home" KIPPLE_STATE_DIR="$root/state" "$@"
}

# 4. Wave 2 red-team R1: bind mounts of .ssh flip on and off inside every
#    item while clean --apply runs. A bind that rides into the Trash with an
#    item must be found after the rename and the item put back: no mount
#    may be left under the Trash.
race_check() {
	local r=/tmp/race n=150 i targets="" moved skipped left
	many_fixture "$r" "$n"
	user_in "$r" "$K" scan --json >"$r/out/plan.json"
	for i in $(seq -w 1 "$n"); do targets="$targets $r/home/.cache/c$i/m"; done
	rm -f /tmp/flipstop
	# shellcheck disable=SC2086
	/tmp/flip 60 "$r/home/.ssh" $targets 2>"$r/out/flip.err" &
	local fp=$!
	sleep 0.2
	set +e
	user_in "$r" "$K" clean --apply --json --plan "$r/out/plan.json" >"$r/out/res.json" 2>"$r/out/res.err"
	set -e
	touch /tmp/flipstop
	wait "$fp" || true
	rm -f /tmp/flipstop
	left="$(mounts_under "$r/home/.local/share/Trash")"
	moved="$(jq '.moved | length' "$r/out/res.json")"
	skipped="$(jq -r '[.skipped[]?.reason] | group_by(.) | map("\(.[0])=\(length)") | join(" ")' "$r/out/res.json")"
	if [ "$left" -eq 0 ] && [ -e "$r/home/.ssh/id_ed25519" ]; then
		ok "binds flipping during apply: no mount left in the Trash (moved $moved; skipped $skipped)"
	else
		fail "binds flipping during apply: $left mounts under the Trash (moved $moved; skipped $skipped)"
	fi
	unmount_under "$r/home"
}
race_check

# 5. Wave 2 red-team R3: with statx denied (kernels before 5.8), kipple
#    falls back to /proc/self/mountinfo. A bind present at scan is
#    contains_mount, and binds made while clean --apply runs must be seen
#    (the table is read again before each item and after each move).
nostatx_check() {
	local r=/tmp/nostatx-fx n=400 i mounted=0 left
	many_fixture "$r" "$n"
	mkdir -p "$r/p"
	for i in $(seq -w 1 "$n"); do mkfile "$r/p/$i/thesis.tex" "gold"; done
	chown -R 1000:1000 "$r/p"
	find "$r/p" -exec touch -h -d "$old" {} +
	mount --bind "$r/p/001" "$r/home/.cache/c001/m"
	/tmp/nostatx setpriv --reuid=1000 --regid=1000 --clear-groups --reset-env \
		env HOME=/home/kipple PATH=/usr/bin:/bin KIPPLE_HOME="$r/home" KIPPLE_STATE_DIR="$r/state" "$K" scan --json >"$r/out/plan.json"
	case "$(jq -r --arg p "$r/home/.cache/c001" '[.items[] | select(.path == $p) | .reasons[]] | join(",")' "$r/out/plan.json")" in
	*contains_mount*) ok "statx denied: a bind present at scan is contains_mount (mountinfo fallback)" ;;
	*) fail "statx denied: the bind in c001 was not seen" ;;
	esac
	(/tmp/nostatx setpriv --reuid=1000 --regid=1000 --clear-groups --reset-env \
		env HOME=/home/kipple PATH=/usr/bin:/bin KIPPLE_HOME="$r/home" KIPPLE_STATE_DIR="$r/state" \
		"$K" clean --apply --json --plan "$r/out/plan.json" >"$r/out/res.json" 2>"$r/out/res.err") &
	local kp=$!
	sleep 0.1
	for i in $(seq -w 200 "$n"); do mount --bind "$r/p/$i" "$r/home/.cache/c$i/m" 2>/dev/null && mounted=$((mounted + 1)); done
	wait "$kp" || true
	left="$(mounts_under "$r/home/.local/share/Trash")"
	if [ "$left" -eq 0 ]; then
		ok "statx denied, $mounted binds made during apply: none in the Trash (moved $(jq '.moved | length' "$r/out/res.json"), contains_mount $(jq '[.skipped[]? | select(.reason == "contains_mount")] | length' "$r/out/res.json"))"
	else
		fail "statx denied: $left binds made during apply are under the Trash"
	fi
	unmount_under "$r/home"
}
nostatx_check

if [ "$failures" -ne 0 ]; then
	echo "linux-mount-check: $failures failure(s)"
	exit 1
fi
echo "linux-mount-check: OK (Real mounts checked)"
