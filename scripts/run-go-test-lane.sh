#!/usr/bin/env bash
set -euo pipefail

# Run one checked Go test lane and apply the same JSON reporting and no-skip
# policy locally and in CI. Package membership always comes from the validated
# source manifest; callers never maintain a second package list.

lane=${1:-}
json=${2:-}

if [[ -z "$lane" || -z "$json" ]]; then
	printf 'usage: %s {unit|integration|pipeline|recovery|acceptance|e2e|all} <go-test-json>\n' "$0" >&2
	exit 2
fi

case "$lane" in
	unit)
		bash scripts/check-test-tiers_fixture_test.sh
		bash scripts/check-test-tiers.sh
		args=(-race -p 2 -timeout=10m -count=1 ./...)
		minimum=1500
		;;
	integration|pipeline|recovery|acceptance|e2e)
		bash scripts/check-test-tiers.sh
		packages=()
		while IFS= read -r package; do
			[[ -n "$package" ]] && packages+=("$package")
		done < <(bash scripts/check-test-tiers.sh packages "$lane")
		if [[ ${#packages[@]} -eq 0 ]]; then
			printf 'FAIL: the checked manifest selected no packages for %s\n' "$lane" >&2
			exit 1
		fi
		case "$lane" in
			integration) args=(-tags=integration -race -p 2 -timeout=20m -count=1 "${packages[@]}") ;;
			pipeline|recovery) args=(-tags=integration -race -p 1 -timeout=20m -count=1 "${packages[@]}") ;;
			acceptance) args=(-tags=acceptance -p 1 -timeout=20m -count=1 "${packages[@]}") ;;
			e2e) args=(-tags=e2e -p 1 -timeout=30m -count=1 "${packages[@]}") ;;
		esac
		minimum=1
		;;
	all)
		bash scripts/check-test-tiers_fixture_test.sh
		bash scripts/check-test-tiers.sh
		args=(-tags=integration,acceptance,e2e -race -p 2 -timeout=30m -count=1 ./...)
		minimum=1500
		;;
	*)
		printf 'usage: %s {unit|integration|pipeline|recovery|acceptance|e2e|all} <go-test-json>\n' "$0" >&2
		exit 2
		;;
esac

mkdir -p "$(dirname -- "$json")"
go test -json "${args[@]}" >"$json" && status=0 || status=$?
bash scripts/report-test-json.sh "$json" || echo "(could not render the test report)"
bash scripts/check-no-skips.sh "$json" "$minimum" && gate=0 || gate=$?

if [[ "$status" -ne 0 ]]; then
	echo "go test exited $status"
	exit "$status"
fi
exit "$gate"
