#!/usr/bin/env bash
#
# Prove the OpenSpec gate uses the repository pin rather than executable or
# environment state, rejects a version mismatch before validation, and keeps
# the load-bearing validation arguments intact.
#
# This is entirely offline: a fake npx supplies both version and validation
# output, while a poisonous global openspec makes any PATH fallback fail loudly.
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."
checker=scripts/check-openspec.sh

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
fake_bin="$work/bin"
mkdir -p "$fake_bin"

cat >"$fake_bin/openspec" <<'FAKE'
#!/usr/bin/env bash
echo "global openspec was called" >>"${POISON_LOG:?}"
exit 97
FAKE

cat >"$fake_bin/npx" <<'FAKE'
#!/usr/bin/env bash
printf '%s\n' "$*" >>"${NPX_LOG:?}"

if [ "$#" -lt 3 ] || [ "$1" != "--yes" ] || [ "$2" != "@fission-ai/openspec@1.7.0" ]; then
  echo "fake npx received unexpected package arguments: $*" >&2
  exit 96
fi
shift 2

case "$1" in
  --version)
    if [ "$#" -ne 1 ]; then
      echo "fake npx received unexpected version arguments: $*" >&2
      exit 95
    fi
    printf '%s\n' "${FAKE_OPENSPEC_VERSION:?}"
    ;;
  validate)
    if [ "$#" -ne 3 ] || [ "$2" != "--all" ] || [ "$3" != "--strict" ]; then
      echo "fake npx received unexpected validation arguments: $*" >&2
      exit 94
    fi
    echo "Totals: 18 passed, 0 failed (18 items)"
    ;;
  *)
    echo "fake npx received unexpected command: $*" >&2
    exit 93
    ;;
esac
FAKE

chmod +x "$fake_bin/openspec" "$fake_bin/npx"

failures=0

run_checker() {
  local version=$1
  POISON_LOG="$work/poison.log" \
    NPX_LOG="$work/npx.log" \
    FAKE_OPENSPEC_VERSION="$version" \
    OPENSPEC_VERSION=9.9.9 \
    PATH="$fake_bin:/usr/bin:/bin" \
    bash "$checker" 2>&1
}

assert_eq() {
  local name=$1 got=$2 want=$3
  if [ "$got" != "$want" ]; then
    echo "FAIL [$name]: got '$got', want '$want'"
    failures=$((failures + 1))
  else
    echo "ok   [$name]"
  fi
}

# A mismatch must stop after the version probe. Validation may be expensive and
# a known-wrong binary must never be allowed to certify the repository.
: >"$work/npx.log"
: >"$work/poison.log"
out=$(run_checker 1.4.9) && status=0 || status=$?
if [ "$status" -eq 0 ]; then
  echo "FAIL [version mismatch fails]: exit 0, want non-zero"
  failures=$((failures + 1))
else
  echo "ok   [version mismatch fails]: exit $status"
fi
assert_eq "mismatch stops before validation" \
  "$(cat "$work/npx.log")" \
  "--yes @fission-ai/openspec@1.7.0 --version"
assert_eq "global openspec is ignored on mismatch" "$(cat "$work/poison.log")" ""

# Even a hostile environment override must not move the repository-owned pin.
# The healthy path must probe the version, print verification, then validate
# exactly the non-empty strict selection.
: >"$work/npx.log"
: >"$work/poison.log"
out=$(run_checker 1.7.0) && status=0 || status=$?
assert_eq "healthy pinned run exits zero" "$status" "0"
assert_eq "global openspec is ignored on success" "$(cat "$work/poison.log")" ""
assert_eq "pinned package and commands are exact" \
  "$(cat "$work/npx.log")" \
  $'--yes @fission-ai/openspec@1.7.0 --version\n--yes @fission-ai/openspec@1.7.0 validate --all --strict'

if ! grep -qF "OK: verified OpenSpec version 1.7.0" <<<"$out"; then
  echo "FAIL [verified version is printed]: output was:"
  sed 's/^/       /' <<<"$out"
  failures=$((failures + 1))
else
  echo "ok   [verified version is printed]"
fi
if ! grep -qF "OK: openspec validated 18 item(s) strictly" <<<"$out"; then
  echo "FAIL [18-item validation is observed]: output was:"
  sed 's/^/       /' <<<"$out"
  failures=$((failures + 1))
else
  echo "ok   [18-item validation is observed]"
fi

if [ "$failures" -ne 0 ]; then
  echo
  echo "FAIL: $failures OpenSpec fixture assertion(s) failed."
  exit 1
fi
echo
echo "OK: OpenSpec pin, version check, PATH isolation, and strict validation arguments are enforced"
