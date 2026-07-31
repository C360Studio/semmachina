#!/usr/bin/env bash
#
# Validate every OpenSpec change and spec, strictly.
#
# The `--all` is load-bearing and is the reason this is a script rather than one
# line in the workflow: `openspec validate --strict` with no target validates
# NOTHING. It prints "Nothing to validate", suggests the flags, and exits 0 — a
# perfectly green gate that examined zero files. Measured, not assumed.
#
# So this passes --all and then asserts the reported item count is non-zero,
# which covers the other end of the same failure: a future CLI that changes its
# default, or a repo layout move that leaves --all with nothing to find.
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."

# Pinned rather than `openspec@latest`: a validator that can change under CI can
# turn a green branch red with no commit, which is the same version-drift
# problem the revive `tool` pin solves for lint.
OPENSPEC_VERSION=${OPENSPEC_VERSION:-1.5.0}

if command -v openspec >/dev/null 2>&1; then
  runner=(openspec)
else
  runner=(npx --yes "openspec@${OPENSPEC_VERSION}")
fi

output=$("${runner[@]}" validate --all --strict 2>&1) && status=0 || status=$?
echo "$output"

if [ "$status" -ne 0 ]; then
  echo "FAIL: openspec validation failed."
  exit "$status"
fi

# "Totals: 1 passed, 0 failed (1 items)"
items=$(sed -n 's/.*(\([0-9][0-9]*\) items).*/\1/p' <<<"$output" | tail -1)
if [ -z "$items" ]; then
  echo "FAIL: could not read an item count from the validator output."
  echo "      Its report format changed, so 'it validated something' is no longer checkable here."
  exit 1
fi
if [ "$items" -lt 1 ]; then
  echo "FAIL: openspec validated 0 items. A validator with nothing to validate is not a gate."
  exit 1
fi
echo "OK: openspec validated $items item(s) strictly"
