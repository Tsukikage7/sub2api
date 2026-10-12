#!/usr/bin/env bash
set -euo pipefail

repo_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_dir"

fail() {
  printf '%s\n' "$1" >&2
  exit 1
}

branch="$(git symbolic-ref --quiet --short HEAD)" || fail '当前为 detached HEAD，请先切换到定制分支。'
for operation in MERGE_HEAD CHERRY_PICK_HEAD REVERT_HEAD rebase-merge rebase-apply; do
  operation_path="$(git rev-parse --git-path "$operation")"
  [[ ! -e "$operation_path" ]] || fail '存在未完成的 Git 操作，请先解决或中止，再同步上游。'
done
[[ -z "$(git status --porcelain --untracked-files=normal)" ]] || \
  fail '工作区有未提交或未跟踪的文件，请先整理并提交；同步命令不会自动 stash 或覆盖改动。'

# 使用原始 remote URL 检查上游来源；不依赖 origin 的分支跟踪设置。
upstream_url="$(git config --get-all remote.upstream.url)" || \
  fail '缺少 upstream 远端，请先添加 Fork 的源仓库。'
case "$upstream_url" in
  https://github.com/Wei-Shaw/sub2api|https://github.com/Wei-Shaw/sub2api.git|\
  git@github.com:Wei-Shaw/sub2api|git@github.com:Wei-Shaw/sub2api.git|\
  ssh://git@github.com/Wei-Shaw/sub2api|ssh://git@github.com/Wei-Shaw/sub2api.git) ;;
  *) fail 'upstream 未指向预期的 Fork 源仓库，已停止同步。' ;;
esac

printf '获取 Fork 上游更新，当前定制分支：%s\n' "$branch"
git fetch --quiet --prune upstream
target="refs/remotes/upstream/main"
git rev-parse --verify "$target^{commit}" >/dev/null || fail '上游 upstream/main 不存在。'
git merge-base HEAD "$target" >/dev/null || fail '当前分支与上游 main 没有共同历史，已停止同步。'

if git merge-base --is-ancestor "$target" HEAD; then
  printf '当前分支已包含上游 main 的全部更新。\n'
  exit 0
fi

printf '待合并上游提交数：%s\n' "$(git rev-list --count "HEAD..$target")"
if ! git -c merge.autoStash=false merge --no-edit --ff --commit "$target"; then
  printf '%s\n' \
    '上游合并未完成，已保留当前 Git 状态。' \
    '若有冲突：修改冲突文件，git add 对应文件，再执行 git merge --continue。' \
    '若要取消本次合并：git merge --abort。' >&2
  exit 1
fi

printf '同步完成：%s\n' "$(git rev-parse --short HEAD)"
printf '可运行 task build 检查构建，再运行 task deploy 发布；同步不会推送或部署。\n'
