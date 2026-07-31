#!/usr/bin/env bash
#
# Assert the revive that `go tool` runs is the revive go.mod pins.
#
# revive is pinned by go.mod's `tool` directive (Go 1.24+), so there is no
# install step and normally no way to drift. The case this catches is a build
# cache — GitHub's setup-go cache, or a stale local one — serving a binary built
# from a different version than the current go.mod requires. That drift is
# silent: lint still runs, still exits 0, and enforces a ruleset nobody chose.
#
# Borrowed from semstreams' CI (its gh#221 review), including the normalization:
# `revive --version` prints "version 1.15.0" when invoked one way and "v1.15.0"
# another, so both sides have the leading v stripped before comparison.
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."

# Match the require line shape `github.com/mgechev/revive vX.Y.Z`. The trailing
# `v` in the pattern disambiguates it from the `tool` directive line, which
# names the package with no version.
expected=$(awk '/github.com\/mgechev\/revive v/ {print $2}' go.mod | head -1)
if [ -z "$expected" ]; then
  echo "FAIL: go.mod has no 'github.com/mgechev/revive vX.Y.Z' require line."
  echo "      revive is supposed to be pinned via the tool directive; run:"
  echo "        go get -tool github.com/mgechev/revive@<version>"
  exit 1
fi

actual_raw=$(go tool revive --version 2>&1 | head -1 | awk '{print $NF}')
expected="${expected#v}"
actual="${actual_raw#v}"

if [ "$expected" != "$actual" ]; then
  echo "FAIL: revive version mismatch — go.mod pin: $expected, go tool raw: $actual_raw (normalized: $actual)"
  exit 1
fi
echo "OK: revive $actual matches the go.mod pin"
