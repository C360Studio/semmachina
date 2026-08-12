#!/usr/bin/env bash
set -euo pipefail

directory=${1:-}
output=${2:-}
if [[ -z "$directory" || -z "$output" ]]; then
	printf 'usage: %s <lane-json-directory> <union-json>\n' "$0" >&2
	exit 2
fi

lanes=(unit integration pipeline recovery acceptance e2e)
status=0
inputs=()
for lane in "${lanes[@]}"; do
	file=$directory/gotest-$lane.json
	if [[ ! -s "$file" ]]; then
		printf 'FAIL: required test-lane artifact is missing or empty: %s\n' "$file" >&2
		status=1
		continue
	fi
	inputs+=("$file")
done
if [[ "$status" -ne 0 ]]; then
	exit "$status"
fi

cat "${inputs[@]}" >"$output"
bash scripts/check-no-skips.sh "$output"
