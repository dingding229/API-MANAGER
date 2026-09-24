#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
source_root="${GAME_DISCOUNT_PROJECT_PATH:-"$repo_root/../game-discount-api"}"
output="$repo_root/integrations/game-discount/wasm/plugin.wasm"
if [[ ! -f "$source_root/cmd/wasm-plugin/main.go" ]]; then
  echo "找不到 $source_root/cmd/wasm-plugin/main.go" >&2
  exit 1
fi
mkdir -p "$(dirname "$output")"
(cd "$source_root" && GOOS=wasip1 GOARCH=wasm CGO_ENABLED=0 go build -buildmode=c-shared -trimpath -o "$output" ./cmd/wasm-plugin)
printf '已构建 WASM 插件：%s\n' "$output"
