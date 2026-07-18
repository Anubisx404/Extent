#!/usr/bin/env bash
set -euo pipefail

if command -v goreleaser >/dev/null 2>&1 && command -v syft >/dev/null 2>&1; then
  goreleaser check
  goreleaser release --snapshot --clean --skip=publish
  exit 0
fi

command -v docker >/dev/null 2>&1 || {
  printf 'goreleaser and syft, or a running Docker daemon, are required\n' >&2
  exit 127
}
docker info >/dev/null
image='goreleaser/goreleaser:v2.12.7'
docker run --rm -v "$PWD:/workspace" -w /workspace "$image" check
docker run --rm -v "$PWD:/workspace" -w /workspace "$image" release --snapshot --clean --skip=publish
