#!/usr/bin/env python3
"""用临时 Git 仓库验证同步行为，不访问 GitHub 或生产环境。"""

import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest


SCRIPT = Path(__file__).resolve().parents[1] / "upstream-sync.sh"
UPSTREAM_URL = "https://github.com/Wei-Shaw/sub2api.git"


class UpstreamSyncTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="ai-gateway-sync-test-")
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.remote = self.root / "upstream.git"
        self.seed = self.root / "seed"
        self.work = self.root / "work"
        self.env = {
            **os.environ,
            "GIT_CONFIG_NOSYSTEM": "1",
            "GIT_CONFIG_GLOBAL": os.devnull,
            "GIT_TERMINAL_PROMPT": "0",
            "GIT_EDITOR": "true",
        }
        self.git(self.root, "init", "--bare", "--initial-branch=main", str(self.remote))
        self.git(self.root, "init", "--initial-branch=main", str(self.seed))
        self.identity(self.seed)
        (self.seed / "deploy").mkdir()
        shutil.copy2(SCRIPT, self.seed / "deploy/upstream-sync.sh")
        (self.seed / "shared.txt").write_text("base\n")
        self.commit(self.seed, "base")
        self.git(self.seed, "push", str(self.remote), "main")
        self.git(self.root, "clone", str(self.remote), str(self.work))
        self.identity(self.work)
        self.git(self.work, "remote", "add", "upstream", UPSTREAM_URL)
        # 原始 remote 保持预期的上游 URL，Git 的本地重写规则将传输导向测试仓库。
        self.git(self.work, "config", f"url.{self.remote.as_uri()}.insteadOf", UPSTREAM_URL)

    def git(self, directory, *args):
        return subprocess.run(
            ["git", "-C", str(directory), *args],
            env=self.env, text=True, capture_output=True, check=True,
        ).stdout.strip()

    def identity(self, directory):
        self.git(directory, "config", "user.name", "Sync Test")
        self.git(directory, "config", "user.email", "sync-test@example.invalid")

    def commit(self, directory, message):
        self.git(directory, "add", ".")
        self.git(directory, "commit", "-m", message)
        return self.git(directory, "rev-parse", "HEAD")

    def upstream_commit(self, filename, content):
        (self.seed / filename).write_text(content)
        commit = self.commit(self.seed, "upstream update")
        self.git(self.seed, "push", str(self.remote), "main")
        return commit

    def sync(self):
        return subprocess.run(
            ["bash", str(self.work / "deploy/upstream-sync.sh")],
            cwd=self.work, env=self.env, text=True, capture_output=True,
        )

    def test_no_change_keeps_head(self):
        before = self.git(self.work, "rev-parse", "HEAD")
        result = self.sync()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(self.git(self.work, "rev-parse", "HEAD"), before)
        self.assertEqual(self.git(self.work, "status", "--porcelain"), "")

    def test_fast_forward(self):
        expected = self.upstream_commit("upstream.txt", "new upstream feature\n")
        result = self.sync()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(self.git(self.work, "rev-parse", "HEAD"), expected)

    def test_merge_preserves_custom_and_upstream_commits(self):
        (self.work / "custom.txt").write_text("custom branding\n")
        custom = self.commit(self.work, "custom change")
        upstream = self.upstream_commit("upstream.txt", "new upstream feature\n")
        result = self.sync()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual((self.work / "custom.txt").read_text(), "custom branding\n")
        self.assertEqual((self.work / "upstream.txt").read_text(), "new upstream feature\n")
        self.git(self.work, "merge-base", "--is-ancestor", custom, "HEAD")
        self.git(self.work, "merge-base", "--is-ancestor", upstream, "HEAD")
        self.assertEqual(len(self.git(self.work, "show", "-s", "--format=%P").split()), 2)

    def test_dirty_tracked_or_untracked_files_are_preserved(self):
        before = self.git(self.work, "rev-parse", "HEAD")
        for filename in ("shared.txt", "untracked.txt"):
            with self.subTest(filename=filename):
                path = self.work / filename
                path.write_text("unfinished local work\n")
                result = self.sync()
                self.assertNotEqual(result.returncode, 0)
                self.assertIn("工作区", result.stderr)
                self.assertEqual(path.read_text(), "unfinished local work\n")
                self.assertEqual(self.git(self.work, "rev-parse", "HEAD"), before)
                if filename == "shared.txt":
                    path.write_text("base\n")
                else:
                    path.unlink()

    def test_conflict_is_left_for_manual_resolution(self):
        (self.work / "shared.txt").write_text("custom\n")
        before = self.commit(self.work, "custom conflict")
        self.upstream_commit("shared.txt", "upstream\n")
        result = self.sync()
        self.assertNotEqual(result.returncode, 0)
        self.assertTrue((self.work / ".git/MERGE_HEAD").exists())
        self.assertEqual(self.git(self.work, "rev-parse", "HEAD"), before)
        self.assertIn("<<<<<<<", (self.work / "shared.txt").read_text())
        repeated = self.sync()
        self.assertNotEqual(repeated.returncode, 0)
        self.assertIn("未完成的 Git 操作", repeated.stderr)

    def test_detached_head_is_rejected(self):
        self.git(self.work, "checkout", "--detach")
        result = self.sync()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("detached HEAD", result.stderr)

    def test_wrong_upstream_is_rejected(self):
        self.git(self.work, "remote", "set-url", "upstream", "https://github.com/example/other.git")
        result = self.sync()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("未指向预期的 Fork 源仓库", result.stderr)


if __name__ == "__main__":
    unittest.main(verbosity=2)
