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

# This is an internal repository pin, not configuration. A caller-provided
# version or a global executable would make the same commit validate under a
# different contract on different machines.
readonly OPENSPEC_VERSION=1.7.0
runner=(npx --yes "@fission-ai/openspec@${OPENSPEC_VERSION}")

reported_version=$("${runner[@]}" --version 2>&1) && version_status=0 || version_status=$?
if [ "$version_status" -ne 0 ]; then
  echo "$reported_version"
  echo "FAIL: could not verify the pinned OpenSpec version."
  exit "$version_status"
fi
if [ "$reported_version" != "$OPENSPEC_VERSION" ]; then
  echo "FAIL: OpenSpec reported version '$reported_version'; expected exactly '$OPENSPEC_VERSION'."
  exit 1
fi
echo "OK: verified OpenSpec version $reported_version"

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
