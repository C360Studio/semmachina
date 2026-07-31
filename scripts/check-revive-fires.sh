#!/usr/bin/env bash
#
# Prove the lint gate can fail, and that the one exclusion in revive.toml is as
# narrow as it claims.
#
# `go tool revive -config revive.toml ./...` exiting 0 has two causes that look
# identical in a CI log: the module is clean, or revive examined nothing. The
# second is not hypothetical — the first revive run in this repo silently missed
# a file that existed on disk, and the miss was only visible because a later run
# found it. A gate whose only observed outcome is success has never been
# observed at all.
#
# The probes are written to a temp directory OUTSIDE the module tree, so `./...`
# never sees them and no committed file has to carry a deliberate defect. Paths
# under the temp root mimic the module layout, because the max-public-structs
# exclusion is a PATH filter and a probe that ignored paths could not test it.
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."

probe_dir=$(mktemp -d)
trap 'rm -rf "$probe_dir"' EXIT
mkdir -p "$probe_dir/internal/vocabulary" "$probe_dir/internal/other"

failures=0

# run <name> <want-status> <file> [expected-substrings...]
run() {
  local name=$1 want=$2 file=$3
  shift 3
  local out status
  out=$(go tool revive -config revive.toml -formatter default "$file" 2>&1) && status=0 || status=$?
  if [ "$status" -ne "$want" ]; then
    echo "FAIL [$name]: revive exited $status, want $want. Output was:"
    sed 's/^/       /' <<<"$out"
    failures=$((failures + 1))
    return
  fi
  local expected
  for expected in "$@"; do
    if ! grep -qF "$expected" <<<"$out"; then
      echo "FAIL [$name]: exit was right but the finding was not: expected \"$expected\". Output was:"
      sed 's/^/       /' <<<"$out"
      failures=$((failures + 1))
      return
    fi
  done
  echo "ok   [$name]: exit $status"
}

# -- 1. The configured rules fire at all. ------------------------------------
#
# The shadowed builtin is `len` rather than `max`: `max`/`min` only became
# builtins in Go 1.21, and revive decides that from the module's go version —
# which it cannot see for a file outside any module, so it silently does not
# flag them here. Measured, not assumed. A probe is only useful when the thing
# it probes is unconditional.
cat >"$probe_dir/violating.go" <<'GO'
package probe

func Shadow(len int) int {
	return len
}
GO
run "a violating file fails" 1 "$probe_dir/violating.go" \
  "redefinition of the built-in function len" \
  "should have a package comment" \
  "exported function Shadow should have comment"

# -- 2. A clean file passes. -------------------------------------------------
#
# Without this, "revive always fails" would satisfy every other case here and
# the gate would be useless in the opposite direction.
cat >"$probe_dir/clean.go" <<'GO'
// Package probe is a lint fixture.
package probe

// Fine is an exported function with a doc comment and no builtin shadow.
func Fine(n int) int {
	return n
}
GO
run "a clean file passes" 0 "$probe_dir/clean.go"

# -- 3/4. max-public-structs is EXCLUDED for internal/vocabulary only. -------
#
# revive.toml disables that rule for the closed-vocabulary package, where it has
# no signal (eleven `type X string` enums are eleven closed sets, not clutter).
# An exclusion is only defensible if it is narrow, and "narrow" is a claim about
# a path filter that nobody has ever run. These two probes are byte-identical
# and differ only in which directory they sit in.
vocabulary_shaped() {
  {
    echo "// Package probe is a lint fixture."
    echo "package probe"
    echo
    for i in $(seq 1 11); do
      echo "// T$i is a closed vocabulary."
      echo "type T$i string"
    done
  } >"$1"
}
vocabulary_shaped "$probe_dir/internal/vocabulary/enums.go"
vocabulary_shaped "$probe_dir/internal/other/enums.go"

run "max-public-structs fires outside internal/vocabulary" 1 "$probe_dir/internal/other/enums.go" \
  "you have exceeded the maximum number (10) of public struct declarations"
run "max-public-structs is excluded inside internal/vocabulary" 0 "$probe_dir/internal/vocabulary/enums.go"

if [ "$failures" -ne 0 ]; then
  echo
  echo "FAIL: $failures revive probe(s) behaved wrongly. The lint gate is not enforcing what revive.toml says."
  exit 1
fi
echo
echo "OK: revive fails on violations, passes clean code, and excludes max-public-structs by path"
