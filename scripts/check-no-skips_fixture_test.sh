#!/usr/bin/env bash
#
# Prove check-no-skips.sh catches what it claims and passes what it should.
#
# A gate whose only observed outcome is "OK" has never been observed. This runs
# the checker against synthetic `go test -json` streams covering the matrix that
# matters — including the one false-positive shape that would tempt someone to
# loosen the filter until it stopped catching anything.
#
# Runs in CI (fast, no Docker, no Go build) and via `task lint`.
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."
checker=scripts/check-no-skips.sh

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

failures=0

# expect <name> <want-status> <file> [min-tests]
expect() {
  local name=$1 want=$2 file=$3 min=${4:-1}
  local out status
  out=$(SEMMACHINA_SKIP_INTEGRATION= bash "$checker" "$file" "$min" 2>&1) && status=0 || status=$?
  if [ "$status" -ne "$want" ]; then
    echo "FAIL [$name]: exit $status, want $want"
    sed 's/^/       /' <<<"$out"
    failures=$((failures + 1))
  else
    echo "ok   [$name]: exit $status"
  fi
}

# ---------------------------------------------------------------- fixtures --
# A healthy run: three tests pass in one package.
cat >"$work/healthy.json" <<'JSON'
{"Time":"2026-07-31T12:00:00Z","Action":"run","Package":"m/internal/dice","Test":"TestA"}
{"Time":"2026-07-31T12:00:01Z","Action":"pass","Package":"m/internal/dice","Test":"TestA","Elapsed":0.01}
{"Time":"2026-07-31T12:00:01Z","Action":"pass","Package":"m/internal/dice","Test":"TestB","Elapsed":0.01}
{"Time":"2026-07-31T12:00:01Z","Action":"pass","Package":"m/internal/dice","Test":"TestC","Elapsed":0.01}
{"Time":"2026-07-31T12:00:02Z","Action":"pass","Package":"m/internal/dice","Elapsed":0.5}
JSON

# The shape that must NOT be a false positive: a package with no test files
# emits a package-level skip, carrying no .Test field. This module has such
# packages, so a checker that counted it would fail every honest run — and the
# obvious "fix" for that is to loosen the filter until it catches nothing.
cat >"$work/package-level-skip.json" <<'JSON'
{"Time":"2026-07-31T12:00:01Z","Action":"pass","Package":"m/internal/dice","Test":"TestA","Elapsed":0.01}
{"Time":"2026-07-31T12:00:01Z","Action":"pass","Package":"m/internal/dice","Test":"TestB","Elapsed":0.01}
{"Time":"2026-07-31T12:00:02Z","Action":"skip","Package":"m/internal/vocabulary","Elapsed":0}
{"Time":"2026-07-31T12:00:02Z","Action":"output","Package":"m/internal/vocabulary","Output":"?   \tm/internal/vocabulary\t[no test files]\n"}
JSON

# One skipped test hiding among passes — the regression this gate exists for.
cat >"$work/one-skipped-test.json" <<'JSON'
{"Time":"2026-07-31T12:00:01Z","Action":"pass","Package":"m/internal/dice","Test":"TestA","Elapsed":0.01}
{"Time":"2026-07-31T12:00:01Z","Action":"skip","Package":"m/internal/boot","Test":"TestBootsAgainstRealNATS","Elapsed":0}
{"Time":"2026-07-31T12:00:01Z","Action":"pass","Package":"m/internal/dice","Test":"TestB","Elapsed":0.01}
JSON

# A skipped SUBTEST, which is the same regression wearing a slash.
cat >"$work/skipped-subtest.json" <<'JSON'
{"Time":"2026-07-31T12:00:01Z","Action":"pass","Package":"m/internal/dice","Test":"TestA","Elapsed":0.01}
{"Time":"2026-07-31T12:00:01Z","Action":"skip","Package":"m/internal/dice","Test":"TestA/the_hard_case","Elapsed":0}
JSON

# A run that selected nothing: package events only, no tests at all.
cat >"$work/no-tests.json" <<'JSON'
{"Time":"2026-07-31T12:00:02Z","Action":"pass","Package":"m/internal/dice","Elapsed":0.5}
{"Time":"2026-07-31T12:00:02Z","Action":"pass","Package":"m/internal/boot","Elapsed":0.5}
JSON

# A failing run with no skips: the checker's business is coverage, not verdicts;
# the go test exit code owns failures. Reporting them here too would double-count
# and make a real failure look like a skip regression.
cat >"$work/failing.json" <<'JSON'
{"Time":"2026-07-31T12:00:01Z","Action":"fail","Package":"m/internal/dice","Test":"TestA","Elapsed":0.01}
{"Time":"2026-07-31T12:00:01Z","Action":"pass","Package":"m/internal/dice","Test":"TestB","Elapsed":0.01}
JSON

# Not JSON at all — a redirect that captured the wrong stream.
printf 'ok  \tm/internal/dice\t0.5s\nok  \tm/internal/boot\t76.0s\n' >"$work/not-json.txt"

: >"$work/empty.json"

# ------------------------------------------------------------------ matrix --
expect "healthy run passes"                    0 "$work/healthy.json"                3
expect "package-with-no-test-files is not a skip" 0 "$work/package-level-skip.json"  2
expect "a skipped test fails"                  1 "$work/one-skipped-test.json"       1
expect "a skipped subtest fails"               1 "$work/skipped-subtest.json"        1
expect "a run with no tests fails"             1 "$work/no-tests.json"               1
expect "a failing run is not reported as a skip regression" 0 "$work/failing.json"   2
expect "non-JSON output fails"                 1 "$work/not-json.txt"                1
expect "empty file fails"                      1 "$work/empty.json"                  1
expect "too few tests fails"                   1 "$work/healthy.json"                4

# The floor assertion must be reachable from the other side too, or "at least N"
# would be satisfiable by any N nobody checked.
expect "the floor is exactly met"              0 "$work/healthy.json"                3

# And the opt-out variable itself, which is the failure mode with no fixture:
# a healthy stream must still be refused when the environment says the proofs
# were turned off.
out=$(SEMMACHINA_SKIP_INTEGRATION=1 bash "$checker" "$work/healthy.json" 3 2>&1) && status=0 || status=$?
if [ "$status" -eq 0 ]; then
  echo "FAIL [opt-out env is refused]: exit 0, want 1"
  sed 's/^/       /' <<<"$out"
  failures=$((failures + 1))
else
  echo "ok   [opt-out env is refused]: exit $status"
fi

if [ "$failures" -ne 0 ]; then
  echo
  echo "FAIL: $failures case(s) in the skip-gate matrix behaved wrongly."
  exit 1
fi
echo
echo "OK: the skip gate behaves correctly on all $((10 + 1)) cases"
