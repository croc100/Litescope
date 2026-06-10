#!/usr/bin/env bash
# Build Go binaries for all platforms and place them in vscode/bin/
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
BIN_DIR="$SCRIPT_DIR/../bin"

targets=(
  "darwin/amd64"
  "darwin/arm64"
  "linux/amd64"
  "linux/arm64"
  "windows/amd64"
  "windows/arm64"
)

for target in "${targets[@]}"; do
  os="${target%%/*}"
  arch="${target##*/}"
  out_dir="$BIN_DIR/${os}-${arch}"
  mkdir -p "$out_dir"

  if [[ "$os" == "windows" ]]; then
    exe="litescope.exe"
  else
    exe="litescope"
  fi

  echo "Building $os/$arch → bin/${os}-${arch}/$exe"
  GOOS="$os" GOARCH="$arch" CGO_ENABLED=0 \
    go build -ldflags="-s -w" -o "$out_dir/$exe" "$REPO_ROOT/cmd/litescope"
done

echo "Done."
