#!/usr/bin/env bash
# Run the pinned repository linter without scanning generated dependency
# fixtures installed below web/node_modules. The exclusion names only that
# generated subtree; every Go file owned by this repository remains in ./....
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."

go tool revive \
  -config revive.toml \
  -formatter friendly \
  -exclude ./web/node_modules/... \
  ./...
