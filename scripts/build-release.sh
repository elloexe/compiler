#!/usr/bin/env bash
# Builds ExeForge for Linux, Windows and macOS into dist/.
set -euo pipefail
cd "$(dirname "$0")/.."
VERSION="${VERSION:-$(git describe --tags --always 2>/dev/null || echo dev)}"
LDFLAGS="-s -w -X main.version=${VERSION}"
mkdir -p dist
for target in linux/amd64 linux/arm64 windows/amd64 windows/arm64 darwin/amd64 darwin/arm64; do
  os="${target%/*}"; arch="${target#*/}"
  out="dist/exeforge-${os}-${arch}"
  [ "$os" = windows ] && out="${out}.exe"
  echo "→ ${out}"
  CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" go build -trimpath -ldflags "$LDFLAGS" -o "$out" ./cmd/exeforge
done
echo "Done. Linux users: chmod +x dist/exeforge-linux-amd64 && ./dist/exeforge-linux-amd64 install"
