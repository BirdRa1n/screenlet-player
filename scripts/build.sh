#!/usr/bin/env bash
# Builds the full release matrix locally into ./dist — mirrors
# .github/workflows/release.yml so a release build can be sanity-checked
# before tagging.
#
#   ./scripts/build.sh v0.1.0
#
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."

VERSION="${1:-dev}"
COMMIT="$(git rev-parse --short HEAD 2>/dev/null || echo none)"
BUILD_DATE="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
LDFLAGS="-X github.com/BirdRa1n/screenlet-player/pkg/version.Version=${VERSION} -X github.com/BirdRa1n/screenlet-player/pkg/version.Commit=${COMMIT} -X github.com/BirdRa1n/screenlet-player/pkg/version.BuildDate=${BUILD_DATE}"

names=(linux-amd64 linux-arm64 linux-armv7 darwin-arm64)
goos_list=(linux linux linux darwin)
goarch_list=(amd64 arm64 arm arm64)
goarm_list=("" "" 7 "")

mkdir -p dist
rm -f dist/screenlet-player-*

for i in "${!names[@]}"; do
  name="${names[$i]}"
  echo "==> Building ${name} (version ${VERSION})"
  GOOS="${goos_list[$i]}" GOARCH="${goarch_list[$i]}" GOARM="${goarm_list[$i]}" CGO_ENABLED=0 \
    go build -trimpath -ldflags "${LDFLAGS}" -o "dist/screenlet-player-${name}" ./cmd/screenlet-player
  ( cd dist && sha256sum "screenlet-player-${name}" > "screenlet-player-${name}.sha256" )
done

echo "==> Done. Binaries in ./dist:"
ls -la dist/
