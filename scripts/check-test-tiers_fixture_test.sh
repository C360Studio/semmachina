#!/usr/bin/env bash
set -euo pipefail

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
checker=$script_dir/check-test-tiers.sh
tmp=$(mktemp -d "${TMPDIR:-/tmp}/semmachina-test-tier-fixtures.XXXXXX")
trap 'rm -rf "$tmp"' EXIT

# Exercise the checker with only its declared portable POSIX-ish toolchain.
# Constructing this PATH explicitly proves the fixtures do not accidentally
# inherit a developer-installed rg (or any other undeclared scanner).
portable_bin=$tmp/portable-bin
mkdir -p "$portable_bin"
for tool in bash awk cut sort uniq sed find grep wc tr mktemp rm dirname; do
	target=$(command -v "$tool")
	ln -s "$target" "$portable_bin/$tool"
done
if PATH=$portable_bin command -v rg >/dev/null 2>&1; then
	printf 'FAIL: portable checker PATH unexpectedly contains rg\n' >&2
	exit 1
fi

make_fixture() {
	local root=$1
	mkdir -p "$root/internal/focused" "$root/internal/pipeline" "$root/internal/boot" "$root/internal/e2e" "$root/scripts"
	printf '//go:build integration\n\npackage focused_test\n' >"$root/internal/focused/focused_integration_test.go"
	printf '//go:build integration\n\npackage pipeline_test\n' >"$root/internal/pipeline/pipeline_integration_test.go"
	printf '//go:build acceptance\n\npackage boot_test\n' >"$root/internal/boot/boot_integration_test.go"
	printf '//go:build e2e\n\npackage e2e_test\n' >"$root/internal/e2e/harness_test.go"
	printf 'package e2e_test\n' >"$root/internal/e2e/seeds_test.go"
	printf 'integration\tinternal/focused/focused_integration_test.go\npipeline\tinternal/pipeline/pipeline_integration_test.go\nacceptance\tinternal/boot/boot_integration_test.go\ne2e\tinternal/e2e/harness_test.go\n' >"$root/scripts/test-tiers.tsv"
}

run_checker() {
	local root=$1
	shift
	PATH=$portable_bin TEST_TIERS_ROOT=$root TEST_TIERS_MANIFEST=$root/scripts/test-tiers.tsv \
		"$portable_bin/bash" "$checker" "$@"
}

expect_failure() {
	local name=$1 pattern=$2 root=$3
	if run_checker "$root" check >"$root/out" 2>&1; then
		printf 'FAIL: %s fixture unexpectedly passed\n' "$name" >&2
		exit 1
	fi
	if ! grep -Fq "$pattern" "$root/out"; then
		printf 'FAIL: %s fixture did not report %q\n' "$name" "$pattern" >&2
		cat "$root/out" >&2
		exit 1
	fi
}

good=$tmp/good
make_fixture "$good"
run_checker "$good" check >/dev/null
packages=$(run_checker "$good" packages integration)
if [[ "$packages" != './internal/focused' ]]; then
	printf 'FAIL: package emission = %q, want ./internal/focused\n' "$packages" >&2
	exit 1
fi
packages=$(run_checker "$good" packages pipeline)
if [[ "$packages" != './internal/pipeline' ]]; then
	printf 'FAIL: pipeline package emission = %q, want ./internal/pipeline\n' "$packages" >&2
	exit 1
fi

lane_collision=$tmp/lane-collision
make_fixture "$lane_collision"
printf '//go:build integration\n\npackage focused_test\n' >"$lane_collision/internal/focused/also_pipeline_integration_test.go"
printf 'pipeline\tinternal/focused/also_pipeline_integration_test.go\n' >>"$lane_collision/scripts/test-tiers.tsv"
expect_failure lane-collision 'test package is assigned to multiple execution lanes' "$lane_collision"

duplicate=$tmp/duplicate
make_fixture "$duplicate"
printf 'e2e\tinternal/focused/focused_integration_test.go\n' >>"$duplicate/scripts/test-tiers.tsv"
expect_failure duplicate 'classified more than once' "$duplicate"

missing=$tmp/missing
make_fixture "$missing"
printf 'integration\tinternal/focused/missing_integration_test.go\n' >>"$missing/scripts/test-tiers.tsv"
expect_failure missing 'classified test file does not exist' "$missing"

wrong_tag=$tmp/wrong-tag
make_fixture "$wrong_tag"
printf '//go:build acceptance\n\npackage focused_test\n' >"$wrong_tag/internal/focused/focused_integration_test.go"
expect_failure wrong-tag 'must start with the exact tag' "$wrong_tag"

tagged_omission=$tmp/tagged-omission
make_fixture "$tagged_omission"
printf '//go:build e2e\n\npackage e2e_test\n' >"$tagged_omission/internal/e2e/delivery_test.go"
expect_failure tagged-omission 'tagged test file is omitted from the manifest' "$tagged_omission"

named_omission=$tmp/named-omission
make_fixture "$named_omission"
printf 'package focused_test\n' >"$named_omission/internal/focused/forgotten_integration_test.go"
expect_failure named-omission 'integration-named test file is omitted from the manifest' "$named_omission"

docker_omission=$tmp/docker-omission
make_fixture "$docker_omission"
printf 'package focused_test\n\nfunc helper() { natsclient.NewSharedTestClient() }\n' >"$docker_omission/internal/focused/broker_test.go"
expect_failure docker-omission 'Docker-starting test file is omitted from the manifest' "$docker_omission"

testmain=$tmp/testmain
make_fixture "$testmain"
printf 'package focused_test\n\nfunc TestMain(m *testing.M) { startBroker() }\n' >"$testmain/internal/focused/main_test.go"
expect_failure testmain 'untagged TestMain can start Docker' "$testmain"

printf 'OK: test-tier validator fixtures exercise every omission and exactness guard without rg\n'
