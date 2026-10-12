#!/usr/bin/env bash
set -euo pipefail

repo_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
backend_dir="$repo_dir/backend"
binary="$backend_dir/bin/ai-gateway-linux-amd64"
required_go="$(sed -n 's/^go //p' "$backend_dir/go.mod")"

# 优先使用 go.mod 对应的已安装工具链，避免旧 GOROOT 干扰 mise shim。
if [[ -n "${GO_BIN:-}" ]]; then
  go_bin="$GO_BIN"
elif command -v mise >/dev/null 2>&1 && go_install="$(mise where "go@$required_go" 2>/dev/null)"; then
  go_bin="$go_install/bin/go"
else
  go_bin="$(command -v go)"
fi
go_root="$(env -u GOROOT GOENV=off "$go_bin" env GOROOT)"
export GOROOT="$go_root"
export GOTOOLCHAIN=local

version="$(sh "$backend_dir/scripts/resolve-version.sh")"
commit="$(git -C "$repo_dir" rev-parse --short=9 HEAD)"
if [[ -n "$(git -C "$repo_dir" status --porcelain)" ]]; then
  commit="$commit-dirty"
fi
build_date="$(date -u +%Y-%m-%dT%H:%M:%SZ)"

printf '构建 ai-gateway %s，源码 %s\n' "$version" "$commit"
"$go_bin" version
CI=true pnpm --dir "$repo_dir/frontend" install --frozen-lockfile --reporter=append-only
pnpm --dir "$repo_dir/frontend" run build

mkdir -p "$backend_dir/bin"
cd "$backend_dir"
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 "$go_bin" build \
  -tags=embed -trimpath \
  -ldflags="-s -w -X main.Version=$version -X main.Commit=$commit -X main.Date=$build_date -X main.BuildType=source" \
  -o "$binary.new" ./cmd/server
mv -f "$binary.new" "$binary"
shasum -a 256 "$binary"
