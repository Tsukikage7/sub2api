#!/usr/bin/env bash
set -euo pipefail
umask 077

release_id="${1:?缺少发布批次}"
expected_hash="${2:?缺少文件校验值}"
service="sub2api.service"
binary="/opt/sub2api/sub2api"
release_dir="/opt/sub2api/backups/ai-gateway-$release_id"

[[ "$release_id" =~ ^[0-9]{8}T[0-9]{6}Z-[a-f0-9]{12}$ ]]
[[ "$expected_hash" =~ ^[a-f0-9]{64}$ ]]
[[ "$(id -u)" == 0 && "$(uname -m)" == x86_64 ]]
[[ "$(systemctl show "$service" -p ExecStart --value)" == *"path=$binary ;"* ]]

exec 9> "/opt/sub2api/.deploy.lock"
flock -n 9 || { printf '已有发布正在运行。\n' >&2; exit 1; }

printf '%s  %s\n' "$expected_hash" "$release_dir/ai-gateway" | sha256sum -c -
cp -p "$binary" "$release_dir/previous-binary"
cp -p "/etc/sub2api/env" "$release_dir/env.backup"
if [[ -f "/opt/sub2api/data/config.yaml" ]]; then
  cp -p "/opt/sub2api/data/config.yaml" "$release_dir/config.yaml.backup"
fi

# 由 root 接收 pg_dump 的标准输出，兼容仅 root 可写的备份目录。
printf '备份数据库与旧程序：%s\n' "$release_dir"
runuser -u postgres -- pg_dump -Fc sub2api > "$release_dir/database.dump"
pg_restore -l "$release_dir/database.dump" >/dev/null
runuser -u postgres -- psql -d sub2api -Atqc \
  'SELECT filename FROM schema_migrations ORDER BY filename' > "$release_dir/migrations-before.txt"

install -o sub2api -g sub2api -m 0755 "$release_dir/ai-gateway" "$binary.new"
printf '%s  %s\n' "$expected_hash" "$binary.new" | sha256sum -c -
mv -f "$binary.new" "$binary"
printf '%s  %s\n' "$expected_hash" "$binary" | sha256sum -c -
systemctl restart "$service"

# 只检查切换后的进程与发布文件一致，不执行 HTTP、浏览器或业务验收。
sleep 3
systemctl is-active --quiet "$service"
main_pid="$(systemctl show "$service" -p MainPID --value)"
[[ "$main_pid" != 0 ]]
printf '%s  /proc/%s/exe\n' "$expected_hash" "$main_pid" | sha256sum -c -
"$binary" -version 2>&1
printf '发布完成：service=%s pid=%s backup=%s\n' "$service" "$main_pid" "$release_dir"
