#!/usr/bin/env bash
# Runs kipple's checks inside the Linux test container (test/linux/Dockerfile)
# as the unprivileged user kipple. It refuses to run as root: kipple refuses
# euid 0, and permission tests prove nothing as root. It proves hard rule 1
# of AGENTS.md on Linux: a listing of $HOME taken before the run must match
# the listing taken after it, or the run fails naming what changed.
#
# With arguments, it runs them as the command instead of the default suite
# (used to check the harness itself); the root refusal, the preflight and the
# home snapshot still apply.
set -euo pipefail

if [ "$(id -u)" -eq 0 ]; then
	echo "test/linux/run.sh: refusing to run as root (uid 0); kipple and its tests must run unprivileged" >&2
	exit 1
fi
: "${HOME:?HOME must be set}"
: "${TMPDIR:=/tmp}"
export TMPDIR

# Preflight: apply tags every item with a user extended attribute, so the
# temp folder the tests use must support them (tmpfs from Linux 6.6 on).
probe="$(mktemp "$TMPDIR/kipple-xattr.XXXXXX")"
if ! setfattr -n user.kipple.preflight -v 1 "$probe" 2>/dev/null; then
	rm -f "$probe"
	echo "test/linux/run.sh: $TMPDIR does not support user extended attributes (tmpfs needs Linux 6.6 or later; this kernel is $(uname -r))" >&2
	exit 1
fi
rm -f "$probe"
if [ ! -d /xdev ] || [ ! -w /xdev ]; then
	echo "test/linux/run.sh: /xdev (a second writable file system for cross-device tests) is missing" >&2
	exit 1
fi

snapshot() {
	find "$HOME" -printf '%p %s %T@ %i\n' | LC_ALL=C sort
}
before="$(mktemp "$TMPDIR/kipple-home-before.XXXXXX")"
after="$(mktemp "$TMPDIR/kipple-home-after.XXXXXX")"
snapshot >"$before"

status=0
# step runs one check, recording a failure without stopping the others.
step() {
	echo "== $*"
	if ! "$@"; then
		echo "== FAILED: $*" >&2
		status=1
	fi
}
if [ "$#" -gt 0 ]; then
	step "$@"
else
	step go vet ./...
	step go test -race -count=1 ./...
	step go run ./tools/checkhome .
	step scripts/agent-flow-check.sh
fi

snapshot >"$after"
if ! cmp -s "$before" "$after"; then
	echo "test/linux/run.sh: FAIL: the container home $HOME changed during the run (hard rule 1):" >&2
	diff "$before" "$after" >&2 || true
	status=1
fi
rm -f "$before" "$after"
if [ "$status" -eq 0 ]; then
	echo "test/linux/run.sh: OK (home $HOME unchanged)"
else
	echo "test/linux/run.sh: FAILED (status $status)" >&2
fi
exit "$status"
