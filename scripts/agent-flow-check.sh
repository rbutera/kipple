#!/usr/bin/env bash
# Runs the README's "For agents" flow, command for command, against a fixture
# home in a fresh temporary folder, and checks every exit code against the
# documented table. It never touches your real home folder: KIPPLE_HOME and
# KIPPLE_STATE_DIR point into the temporary folder, which must not be under
# $HOME, and kipple itself refuses a KIPPLE_HOME that is or holds the real
# home. Needs go and jq. CI runs it on every push.
set -euo pipefail

cd "$(dirname "$0")/.."
work="$(mktemp -d "${TMPDIR:-/tmp}/kipple-agent-flow.XXXXXX")"
work="$(cd "$work" && pwd -P)"
cleanup() { chmod -R u+w "$work" 2>/dev/null || true; rm -rf "$work"; }
trap cleanup EXIT
home_real="$(cd "$HOME" && pwd -P)"
case "$work/" in
"$home_real"/* | "$HOME"/*)
	echo "agent-flow-check: refusing to work in $work, which is inside your home folder" >&2
	exit 1
	;;
esac

go build -o "$work/bin/kipple" ./cmd/kipple
go run ./tools/mkfixture "$work/home"
mkdir -p "$work/empty" "$work/run"
export KIPPLE_HOME="$work/home" KIPPLE_STATE_DIR="$work/state"
export PATH="$work/bin:$PATH"
cd "$work/run"

failures=0
# step <expected exit code> '<command line from the README>'
step() {
	local want="$1" line="$2" got
	set +e
	eval "$line" >"$work/stdout" 2>"$work/stderr"
	got=$?
	set -e
	if [ "$got" -ne "$want" ]; then
		echo "FAIL  exit $got, want $want:  $line"
		sed 's/^/      stderr: /' "$work/stderr"
		failures=$((failures + 1))
	else
		echo "ok    exit $got  $line"
	fi
	# Every kipple command here uses --json: its stdout (or the file it is
	# redirected to) must be non-empty and hold exactly one JSON document.
	case "$line" in kipple* | *" kipple "*) ;; *) return 0 ;; esac
	local out="$work/stdout" docs
	case "$line" in *" > "*) out="${line##* > }" ;; esac
	if [ ! -s "$out" ]; then
		echo "FAIL  stdout is empty:  $line"
		failures=$((failures + 1))
		return 0
	fi
	if ! jq -c . "$out" >"$work/docs" 2>/dev/null; then
		echo "FAIL  stdout is not JSON:  $line"
		failures=$((failures + 1))
		return 0
	fi
	docs=$(wc -l <"$work/docs" | tr -d ' ')
	if [ "$docs" -ne 1 ]; then
		echo "FAIL  stdout holds $docs JSON documents, want exactly 1:  $line"
		failures=$((failures + 1))
	fi
}

# The README flow, in order (a test checks each kipple line appears here).
step 0 'kipple scan --json > plan.json'
step 0 "jq -r '.items[] | [.id, .tier, .selected, .allocated_bytes, .path] | @tsv' plan.json"
ID=$(jq -r '[.items[] | select(.selected)][0].id' plan.json)
step 0 'kipple explain "$ID" --json'
IDS=$(jq -r '[.items[] | select(.selected)][0:2] | map(.id) | join(",")' plan.json)
step 0 'kipple clean --json --only "$IDS"'
step 0 'kipple clean --apply --json --only "$IDS" > result.json'
step 0 'kipple history --json'
RECEIPT=$(jq -r .receipt_id result.json)
step 0 'kipple restore "$RECEIPT" --json'
step 0 'kipple restore "$RECEIPT" --apply --json'
step 0 'kipple schema plan > plan.schema.json'

# The exit-code table.
INFO=$(jq -r '[.items[] | select(.tier == "info")][0].id' plan.json)
step 4 'kipple clean --apply --json --only "$INFO"'
step 4 'kipple restore "$RECEIPT" --apply --json'
step 3 'KIPPLE_HOME="$work/empty" kipple clean --apply --json'
step 2 'kipple scan --json --bogus'
step 1 'kipple restore 20990101T000000Z-aaaaaa --json'

# The fixture is back where it started: nothing moved but the restored items.
moved=$(jq '.moved | length' result.json)
left=$(find "$KIPPLE_HOME/.Trash" -mindepth 1 -maxdepth 1 | wc -l | tr -d ' ')
if [ "$moved" -ne 2 ] || [ "$left" -ne 0 ]; then
	echo "FAIL  moved $moved items (want 2), $left entries left in the fixture Trash (want 0)"
	failures=$((failures + 1))
fi

if [ "$failures" -ne 0 ]; then
	echo "agent-flow-check: $failures failure(s)"
	exit 1
fi
echo "agent-flow-check: OK"
