#!/usr/bin/env bash
#
# Render a `go test -json` stream into something a human reads.
#
# Usage: scripts/report-test-json.sh <go-test-json-file>
#
# The test job captures JSON rather than plain output because the zero-skip gate
# needs the machine-readable form (scripts/check-no-skips.sh). Raw JSON in a CI
# log is unreadable, and `-v` output for a suite this size is worse, so this
# prints the two things a failing run actually needs:
#
#   - the full output of every FAILED test, and nothing else's output
#   - a per-package wall clock, slowest first
#
# The second is not decoration. This suite is slow and unevenly slow — a handful
# of Docker-backed packages dominate — and a CI job whose duration nobody can
# attribute is one nobody can make faster. Printing the distribution every run
# makes the cost visible where the decision to change it gets made.
set -euo pipefail

json_file=${1:-}
if [ -z "$json_file" ] || [ ! -s "$json_file" ]; then
  echo "usage: $0 <go-test-json-file> (file must exist and be non-empty)" >&2
  exit 2
fi
if ! command -v jq >/dev/null 2>&1; then
  echo "jq is required" >&2
  exit 2
fi

events=$(jq -R 'fromjson? // empty' "$json_file")

failed=$(jq -rs '[.[] | select(.Action == "fail" and (.Test // "") != "")
                 | "\(.Package)\t\(.Test)"] | .[]' <<<"$events")

if [ -n "$failed" ]; then
  echo "=============================== FAILURES ==============================="
  while IFS=$'\t' read -r pkg test; do
    echo
    echo "--- FAIL: $pkg $test"
    jq -rs --arg pkg "$pkg" --arg test "$test" \
      '[.[] | select(.Action == "output" and .Package == $pkg and (.Test // "") == $test) | .Output]
       | join("")' <<<"$events"
  done <<<"$failed"
  echo
  echo "======================================================================="
  echo
fi

# Build failures and package-level panics carry no .Test, so they would be
# invisible above. Surface them separately rather than letting a red run print
# an empty failure section.
pkg_failures=$(jq -rs '[.[] | select(.Action == "fail" and (.Test // "") == "") | .Package] | unique | .[]' <<<"$events")
tested_failures=$(jq -rs '[.[] | select(.Action == "fail" and (.Test // "") != "") | .Package] | unique | .[]' <<<"$events")
for pkg in $pkg_failures; do
  if ! grep -qxF "$pkg" <<<"$tested_failures"; then
    echo "--- FAIL (no failing test — build error, panic, or timeout): $pkg"
    jq -rs --arg pkg "$pkg" \
      '[.[] | select(.Action == "output" and .Package == $pkg) | .Output] | join("")' <<<"$events"
    echo
  fi
done

echo "======================== PACKAGE WALL CLOCK (s) ========================"
jq -rs '[.[] | select((.Test // "") == "" and (.Elapsed // 0) > 0)
        | {pkg: .Package, s: .Elapsed}]
        | sort_by(-.s) | .[] | "\(.s | tostring | .[0:7] | (" " * (7 - length)) + .)  \(.pkg)"' <<<"$events"
echo "-----------------------------------------------------------------------"
jq -rs '[.[] | select((.Test // "") == "" and (.Elapsed // 0) > 0) | .Elapsed] | add
        | "sum of package times: \(. | floor)s (wall clock is lower — packages run in parallel)"' <<<"$events"
echo "======================================================================="
