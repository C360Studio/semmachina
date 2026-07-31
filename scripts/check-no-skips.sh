#!/usr/bin/env bash
#
# Assert a `go test -json` run skipped nothing and actually ran something.
#
# Usage: scripts/check-no-skips.sh <go-test-json-file> [min-tests]
#
# WHY THIS IS A GATE AND NOT A CONVENTION
#
# Missing infrastructure FAILS in this module rather than skipping (see
# internal/testinfra.SkipEnv): a run without Docker is loud. That policy is only
# worth having if nothing quietly reintroduces the alternative, and the two ways
# it comes back are both invisible in a normal log:
#
#   - SEMMACHINA_SKIP_INTEGRATION gets set somewhere in the environment. Every
#     real-infrastructure test then skips, and `go test ./...` prints `ok` for
#     every package. A green run and a run that proved nothing look identical.
#   - Someone adds a t.Skip. `go test` does not print a skipped test's name
#     without -v, and a package whose tests all skipped still reports `ok`.
#
# So the count is load-bearing, and this reads it off the machine-readable
# stream rather than off a human summary that does not contain it.
#
# THREE SEPARATE ASSERTIONS, because each covers a different way to be green
# without proving anything:
#
#   1. zero skipped tests
#   2. at least min-tests tests actually ran (a selector that silently matched
#      nothing, or a build tag that excluded half the module, leaves a run with
#      no failures and no coverage)
#   3. SEMMACHINA_SKIP_INTEGRATION is unset in this environment
#
# NOTE ON THE FILTER SHAPE, which is the easiest thing here to get wrong:
# `go test -json` emits {"Action":"skip"} for a PACKAGE with no test files as
# well as for a skipped TEST, and this module has such packages. A filter that
# counted every "skip" action would fail every honest run; a filter that
# compensated by loosening would stop catching real skips. The discriminator is
# the presence of a non-empty .Test field — package-level events carry none.
set -euo pipefail

json_file=${1:-}

# The floor is a FLOOR: adding tests never trips it, and only a run that lost a
# large fraction of the suite does. Measured 2026-07-31 at 2048 test outcomes
# across 24 packages for `go test -race ./...`; 1500 leaves room for ordinary
# churn while still failing a run that dropped a quarter of the module.
min_tests=${2:-${MIN_TESTS:-1500}}

if [ -z "$json_file" ]; then
  echo "usage: $0 <go-test-json-file> [min-tests]" >&2
  exit 2
fi
if ! command -v jq >/dev/null 2>&1; then
  echo "FAIL: jq is required to read go test -json output." >&2
  echo "      Parsing it with grep would be the fragile filter this check exists to avoid." >&2
  exit 2
fi
if [ ! -s "$json_file" ]; then
  echo "FAIL: $json_file is missing or empty — the test run produced no JSON stream to check." >&2
  exit 1
fi

if [ -n "${SEMMACHINA_SKIP_INTEGRATION:-}" ]; then
  echo "FAIL: SEMMACHINA_SKIP_INTEGRATION is set (value: ${SEMMACHINA_SKIP_INTEGRATION})."
  echo "      That variable turns the real-infrastructure proofs OFF. It is a developer escape hatch"
  echo "      for a machine without Docker; a gate that sets it is a gate that certifies nothing."
  exit 1
fi

# `fromjson? // empty` drops any non-JSON line rather than aborting the whole
# read. Garbage in produces a zero count, which assertion 2 then catches — a
# quieter failure here would be a louder one there.
events=$(jq -R 'fromjson? // empty' "$json_file")

skipped=$(jq -rs '[.[] | select(.Action == "skip" and (.Test // "") != "")
                  | "\(.Package)\t\(.Test)"] | .[]' <<<"$events")
ran=$(jq -s '[.[] | select((.Action == "pass" or .Action == "fail" or .Action == "skip")
             and (.Test // "") != "")] | length' <<<"$events")
packages=$(jq -rs '[.[] | select((.Test // "") == "" and .Package != null) | .Package] | unique | length' <<<"$events")

skipped_count=0
if [ -n "$skipped" ]; then
  skipped_count=$(wc -l <<<"$skipped" | tr -d ' ')
fi

# `ran` counts pass+fail+skip so that a wholly-skipped suite still clears the
# floor and fails on the skip assertion instead, with the message that names the
# actual problem.
echo "test outcomes: ${ran} across ${packages} packages, ${skipped_count} skipped"

status=0
if [ "$skipped_count" -ne 0 ]; then
  echo "FAIL: ${skipped_count} test(s) were SKIPPED:"
  sed 's/^/    /' <<<"$skipped"
  echo "      A skipped test reports ok for work it did not do. Missing infrastructure is supposed to"
  echo "      FAIL in this module; if a prerequisite is genuinely absent, fix the prerequisite."
  status=1
fi
if [ "$ran" -lt "$min_tests" ]; then
  echo "FAIL: only ${ran} tests ran, expected at least ${min_tests}."
  echo "      The suite is much larger than that, so this run selected a fraction of it — a build tag,"
  echo "      a -run filter, or a package list that no longer covers the module."
  status=1
fi
if [ "$status" -eq 0 ]; then
  echo "OK: no skips, ${ran} tests ran (floor ${min_tests})"
fi
exit "$status"
