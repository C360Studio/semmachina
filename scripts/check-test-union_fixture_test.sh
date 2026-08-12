#!/usr/bin/env bash
set -euo pipefail

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
checker=$script_dir/check-test-union.sh
tmp=$(mktemp -d "${TMPDIR:-/tmp}/semmachina-test-union-fixtures.XXXXXX")
trap 'rm -rf "$tmp"' EXIT

event='{"Action":"pass","Package":"example.test","Test":"TestProof"}'
for lane in unit integration pipeline recovery acceptance e2e; do
	printf '%s\n' "$event" >"$tmp/gotest-$lane.json"
done

MIN_TESTS=6 bash "$checker" "$tmp" "$tmp/union.json" >/dev/null
if [[ $(wc -l <"$tmp/union.json" | tr -d ' ') != 6 ]]; then
	echo 'FAIL: healthy union did not contain all six lane streams' >&2
	exit 1
fi

rm "$tmp/gotest-recovery.json"
if MIN_TESTS=1 bash "$checker" "$tmp" "$tmp/missing.json" >"$tmp/out" 2>&1; then
	echo 'FAIL: union without recovery artifact unexpectedly passed' >&2
	exit 1
fi
if ! grep -Fq 'gotest-recovery.json' "$tmp/out"; then
	echo 'FAIL: missing-lane diagnostic did not name the recovery artifact' >&2
	cat "$tmp/out" >&2
	exit 1
fi

printf 'OK: test-union fixtures require every one of the six lane artifacts\n'
