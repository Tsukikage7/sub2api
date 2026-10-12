#!/usr/bin/env bash
# shellcheck disable=SC2029
# SSH 命令中的发布批次和路径需要在本地展开。
set -euo pipefail

repo_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
binary="$repo_dir/backend/bin/ai-gateway-linux-amd64"
deploy_host="${DEPLOY_HOST:-ai-gateway}"
ssh_options=(-o BatchMode=yes -o ConnectTimeout=10)

if [[ ! -s "$binary" ]]; then
  printf '缺少发布程序，请先运行 task build。\n' >&2
  exit 1
fi
expected_hash="$(shasum -a 256 "$binary" | cut -d ' ' -f 1)"
release_id="$(date -u +%Y%m%dT%H%M%SZ)-${expected_hash:0:12}"
release_dir="/opt/sub2api/backups/ai-gateway-$release_id"

printf '上传发布程序到 %s，批次 %s\n' "$deploy_host" "$release_id"
ssh "${ssh_options[@]}" "$deploy_host" "install -d -m 0700 '$release_dir'"
gzip -c "$binary" | ssh "${ssh_options[@]}" "$deploy_host" \
  "umask 077; gzip -dc > '$release_dir/ai-gateway'"

# 参数由本地时间与 SHA-256 构成；远端脚本只操作现有原生服务。
ssh "${ssh_options[@]}" "$deploy_host" \
  "bash -s -- '$release_id' '$expected_hash'" < "$repo_dir/deploy/release-native-remote.sh"
