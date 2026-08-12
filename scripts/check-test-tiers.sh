#!/usr/bin/env bash
set -euo pipefail

# The checked manifest is deliberately simpler than Go build expressions: one
# exact tier and one test file per line. Keeping the grammar this small makes it
# possible to prove that a file cannot silently join two expensive lanes.

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
default_root=$(CDPATH= cd -- "$script_dir/.." && pwd)
root=${TEST_TIERS_ROOT:-$default_root}
manifest=${TEST_TIERS_MANIFEST:-$root/scripts/test-tiers.tsv}
command=${1:-check}
tier_arg=${2:-}

tmp=$(mktemp -d "${TMPDIR:-/tmp}/semmachina-test-tiers.XXXXXX")
trap 'rm -rf "$tmp"' EXIT
errors=$tmp/errors
entries=$tmp/entries
: >"$errors"
: >"$entries"

error() {
	printf 'ERROR: %s\n' "$*" >>"$errors"
}

valid_tier() {
	case "$1" in
		integration|pipeline|recovery|acceptance|e2e) return 0 ;;
		*) return 1 ;;
	esac
}

build_tag() {
	case "$1" in
		integration|pipeline|recovery) printf 'integration\n' ;;
		acceptance|e2e) printf '%s\n' "$1" ;;
	esac
}

read_manifest() {
	if [[ ! -f "$manifest" ]]; then
		error "test tier manifest does not exist: $manifest"
		return
	fi

	local line=0 tier file extra
	while IFS=$'\t' read -r tier file extra || [[ -n "${tier}${file}${extra}" ]]; do
		line=$((line + 1))
		[[ -z "$tier" || "$tier" == \#* ]] && continue
		if [[ -n "${extra:-}" || -z "${file:-}" ]]; then
			error "$manifest:$line must contain exactly: tier<TAB>test-file"
			continue
		fi
		if ! valid_tier "$tier"; then
			error "$manifest:$line names unsupported tier '$tier'"
			continue
		fi
		if [[ "$file" = /* || "$file" == *..* || "$file" != *_test.go ]]; then
			error "$manifest:$line has unsafe or non-test path '$file'"
			continue
		fi
		printf '%s\t%s\n' "$tier" "$file" >>"$entries"
	done <"$manifest"
}

is_classified() {
	awk -F '\t' -v file="$1" '$2 == file { found = 1 } END { exit !found }' "$entries"
}

check_manifest() {
	read_manifest

	cut -f2 "$entries" | LC_ALL=C sort | uniq -d | while IFS= read -r duplicate; do
		[[ -n "$duplicate" ]] && error "test file is classified more than once: $duplicate"
	done

	awk -F '\t' '
		{
			package = $2
			sub("/[^/]+$", "", package)
			key = package SUBSEP $1
			seen[key] = 1
		}
		END {
			for (key in seen) {
				split(key, parts, SUBSEP)
				package = parts[1]
				lane = parts[2]
				lanes[package] = lanes[package] " " lane
				counts[package]++
			}
			for (package in counts) {
				if (counts[package] > 1) print package "\t" lanes[package]
			}
		}
	' "$entries" | while IFS=$'\t' read -r package lanes; do
		[[ -n "$package" ]] && error "test package is assigned to multiple execution lanes:$lanes ($package)"
	done

	while IFS=$'\t' read -r tier file; do
		[[ -z "$tier" ]] && continue
		path=$root/$file
		if [[ ! -f "$path" ]]; then
			error "classified test file does not exist: $file"
			continue
		fi
		tag=$(build_tag "$tier")
		first=$(sed -n '1p' "$path")
		second=$(sed -n '2p' "$path")
		if [[ "$first" != "//go:build $tag" || -n "$second" ]]; then
			error "$file must start with the exact tag '//go:build $tag' followed by a blank line"
		fi
	done <"$entries"

	while IFS= read -r path; do
		rel=${path#"$root"/}
		first=$(sed -n '1p' "$path")
		if [[ "$first" == '//go:build '* ]] && ! is_classified "$rel"; then
			error "tagged test file is omitted from the manifest: $rel ($first)"
		fi
	done < <(find "$root" -path "$root/.git" -prune -o -type f -name '*_test.go' -print | LC_ALL=C sort)

	while IFS= read -r path; do
		rel=${path#"$root"/}
		if ! is_classified "$rel"; then
			error "integration-named test file is omitted from the manifest: $rel"
		fi
	done < <(find "$root" -path "$root/.git" -prune -o -type f -name '*_integration_test.go' -print | LC_ALL=C sort)

	docker_pattern='testcontainers\.NewDockerProvider[[:space:]]*\(|natsclient\.NewSharedTestClient[[:space:]]*\(|testinfra\.RunTests[[:space:]]*\('
	while IFS= read -r path; do
		[[ -z "$path" ]] && continue
		rel=${path#"$root"/}
		if ! is_classified "$rel"; then
			error "Docker-starting test file is omitted from the manifest: $rel"
		fi
	done < <(rg -l --glob '*_test.go' "$docker_pattern" "$root" || true)

	while IFS= read -r path; do
		[[ -z "$path" ]] && continue
		rel=${path#"$root"/}
		if ! is_classified "$rel" && rg -q "$docker_pattern|startBroker[[:space:]]*\(" "$path"; then
			error "untagged TestMain can start Docker: $rel"
		fi
	done < <(rg -l --glob '*_test.go' 'func TestMain[[:space:]]*\(' "$root" || true)

	if [[ -s "$errors" ]]; then
		LC_ALL=C sort -u "$errors" >&2
		return 1
	fi
	printf 'OK: %s classified test files are exact, exhaustive, and Docker-safe by default\n' "$(wc -l <"$entries" | tr -d ' ')"
}

case "$command" in
	check)
		check_manifest
		;;
	packages)
		if ! valid_tier "$tier_arg"; then
			printf 'usage: %s packages {integration|pipeline|recovery|acceptance|e2e}\n' "$0" >&2
			exit 2
		fi
		check_manifest >/dev/null
		awk -F '\t' -v tier="$tier_arg" '$1 == tier {
			file = $2
			sub("/[^/]+$", "", file)
			print "./" file
		}' "$entries" | LC_ALL=C sort -u
		;;
	*)
		printf 'usage: %s [check | packages {integration|pipeline|recovery|acceptance|e2e}]\n' "$0" >&2
		exit 2
		;;
esac
