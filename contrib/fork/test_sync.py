"""sync.py 的测试：临时目录里一个「上游」仓库、一个 fork 克隆，真跑 git。

  python3 -m unittest contrib/fork/test_sync.py
"""
import json
import os
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

SYNC = Path(__file__).resolve().parent / "sync.py"
ENV = dict(os.environ, GIT_AUTHOR_NAME="t", GIT_AUTHOR_EMAIL="t@example.com", GIT_COMMITTER_NAME="t",
           GIT_COMMITTER_EMAIL="t@example.com", GIT_CONFIG_GLOBAL=os.devnull, GIT_CONFIG_NOSYSTEM="1")


class Rebase(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        root = Path(self.tmp.name)
        self.up, self.fork = root / "up", root / "fork"
        self.git(root, "init", "-q", "-b", "main", str(self.up))
        self.commit(self.up, "index.html", "<nav>\n<a>providers</a>\n<a>gateway</a>\n</nav>\n", "up: page")
        self.git(self.up, "tag", "v0.1.0")
        self.git(root, "clone", "-q", "-o", "upstream", str(self.up), str(self.fork))
        self.git(self.fork, "switch", "-q", "-c", "contrib-lanes")
        self.commit(self.fork, "stations.go", "package stations\n", "fork: stations")

    def tearDown(self):
        self.tmp.cleanup()

    def git(self, cwd, *args):
        return subprocess.run(["git", *args], cwd=cwd, env=ENV, check=True, capture_output=True, text=True).stdout.strip()

    def commit(self, repo, name, text, msg):
        (repo / name).write_text(text)
        self.git(repo, "add", name)
        self.git(repo, "commit", "-q", "-m", msg)
        return self.git(repo, "rev-parse", "HEAD")

    def sync(self, *args):
        r = subprocess.run([sys.executable, str(SYNC), *args], cwd=self.fork, env=ENV, capture_output=True, text=True)
        return r.returncode, (json.loads(r.stdout) if r.stdout.startswith("{") else None), r.stderr

    def fetch(self):
        self.git(self.fork, "fetch", "-q", "upstream")

    def test_current_does_nothing(self):
        head = self.git(self.fork, "rev-parse", "HEAD")
        code, rep, err = self.sync("rebase", "--onto", "upstream/main")
        self.assertEqual(code, 0, err)
        self.assertEqual((rep["status"], rep["after"], rep["upstream_new"]), ("current", head, 0))

    def test_rebases_and_says_the_version(self):
        self.commit(self.up, "other.go", "package other\n", "up: other")
        self.fetch()
        report = Path(self.tmp.name) / "r.json"
        code, rep, err = self.sync("rebase", "--onto", "upstream/main", "--report", str(report))
        self.assertEqual(code, 0, err)
        self.assertEqual(rep["status"], "rebased")
        self.assertEqual((rep["own_commits"], rep["upstream_new"]), (1, 1))
        # 上游的提交在 fork 的提交之下，fork 的提交只留一份
        self.assertEqual(self.git(self.fork, "rev-parse", "HEAD~1"), self.git(self.fork, "rev-parse", "upstream/main"))
        self.assertEqual(self.git(self.fork, "log", "-1", "--format=%s"), "fork: stations")
        self.assertRegex(rep["version"], r"^v0\.1\.0-2-g[0-9a-f]+$")
        self.assertEqual(json.loads(report.read_text())["after"], rep["after"])

    def test_conflict_aborts_and_names_the_files(self):
        self.commit(self.fork, "index.html", "<nav>\n<a>providers</a>\n<a>gateway</a>\n<a>stations</a>\n</nav>\n", "fork: nav")
        self.commit(self.up, "index.html", "<nav>\n<a>providers</a>\n<a>gateway</a>\n<a>masking</a>\n</nav>\n", "up: masking")
        self.fetch()
        head = self.git(self.fork, "rev-parse", "HEAD")
        report = Path(self.tmp.name) / "r.json"
        code, rep, err = self.sync("rebase", "--onto", "upstream/main", "--report", str(report))
        self.assertEqual(code, 3, err)
        self.assertEqual((rep["status"], rep["files"], rep["stopped_subject"]), ("conflict", ["index.html"], "fork: nav"))
        # 什么都没留下：HEAD 原样，没有进行中的 rebase，工作区干净
        self.assertEqual(self.git(self.fork, "rev-parse", "HEAD"), head)
        self.assertFalse((self.fork / ".git" / "rebase-merge").exists())
        self.assertEqual(self.git(self.fork, "status", "--porcelain"), "")
        body = subprocess.run([sys.executable, str(SYNC), "issue", str(report)], env=ENV, capture_output=True, text=True, check=True).stdout
        self.assertIn("- `index.html`", body)
        self.assertIn("fork: nav", body)
        self.assertIn("什么都没推", body)

    def test_a_version_that_reads_as_released_is_refused(self):
        # 没有 tag：describe 只剩哈希，客户端会把它当正式版
        self.git(self.up, "tag", "-d", "v0.1.0")
        self.git(self.fork, "tag", "-d", "v0.1.0")
        self.commit(self.up, "other.go", "package other\n", "up: other")
        self.fetch()
        code, rep, err = self.sync("rebase", "--onto", "upstream/main")
        self.assertEqual(code, 4, err)
        self.assertEqual(rep["status"], "bad-version")

    def test_the_forks_own_release_tag_is_not_the_version(self):
        # 前一次发布打在 fork 提交上的 tag 比上游的 v tag 近，版本号仍按 v tag 算
        self.git(self.fork, "tag", "stations-v0.1.0-1-gdeadbee")
        self.commit(self.fork, "more.go", "package more\n", "fork: more")
        code, rep, err = self.sync("rebase", "--onto", "upstream/main")
        self.assertEqual(code, 0, err)
        self.assertRegex(rep["version"], r"^v0\.1\.0-2-g[0-9a-f]+$")

    def test_refuses_a_dirty_worktree(self):
        (self.fork / "stations.go").write_text("package stations // edited\n")
        code, rep, err = self.sync("rebase", "--onto", "upstream/main")
        self.assertEqual(code, 2)
        self.assertIn("未提交", err)


if __name__ == "__main__":
    unittest.main()
