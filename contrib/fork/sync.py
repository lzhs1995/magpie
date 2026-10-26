#!/usr/bin/env python3
"""fork 跟进上游：把当前分支 rebase 到上游，冲突时说清是哪个提交、哪些文件。

  sync.py rebase --onto upstream/main [--report report.json]
  sync.py issue report.json          # 冲突报告写成 issue 正文（Markdown）

rebase 只动当前检出的工作区：成功就留在 rebase 后的 HEAD；冲突就 abort，HEAD 回到
原处，什么都不推。推送、打 tag、发布由 workflow 在另一个 job 里做（fork-sync.yml）。

退出码：0 已是最新或 rebase 成功；3 有冲突；4 rebase 后版本号不是 git describe
形状（客户端会把它当正式版去下官方更新）；2 用法或工作区不干净。
"""
import argparse
import json
import re
import subprocess
import sys
from pathlib import Path

# 与 internal/update 的 Released 判断一致：预发布段是 N-g<hash> 才算源码构建
DESCRIBE = re.compile(r"^v?\d+\.\d+\.\d+-\d+-g[0-9a-f]{7,}$")


class Usage(Exception):
    pass


def git(*args, check=True):
    r = subprocess.run(["git", *args], capture_output=True, text=True)
    if check and r.returncode:
        raise Usage("git %s: %s" % (" ".join(args), (r.stderr or r.stdout).strip()))
    return r.stdout.strip()


def describe():
    """只认上游的 v 版本 tag：fork 发布用的 stations-… tag 若被选中，版本号就不是
    describe 形状了。本机构建客户端也用同一条命令。"""
    return re.sub(r"[^A-Za-z0-9._+-]", "", git("describe", "--tags", "--match", "v[0-9]*", "--always", "--dirty"))


def rebase(onto):
    if git("status", "--porcelain", "--untracked-files=no"):
        raise Usage("工作区有未提交的改动，不 rebase")
    branch = git("rev-parse", "--abbrev-ref", "HEAD")
    if branch == "HEAD":
        raise Usage("HEAD 不在分支上")
    before = git("rev-parse", "HEAD")
    target = git("rev-parse", onto + "^{commit}")
    own = int(git("rev-list", "--count", "%s..HEAD" % onto))
    behind = int(git("rev-list", "--count", "HEAD..%s" % onto))
    rep = {"branch": branch, "onto": onto, "onto_sha": target, "before": before,
           "own_commits": own, "upstream_new": behind}
    if behind == 0:
        rep.update(status="current", after=before, version=describe())
        return rep, 0
    r = subprocess.run(["git", "-c", "core.editor=true", "rebase", onto], capture_output=True, text=True)
    if r.returncode:
        files = git("diff", "--name-only", "--diff-filter=U", check=False).splitlines()
        stopped = git("rev-parse", "--verify", "-q", "REBASE_HEAD", check=False)
        subject = git("log", "-1", "--format=%s", stopped, check=False) if stopped else ""
        git("rebase", "--abort", check=False)
        rep.update(status="conflict", after=git("rev-parse", "HEAD"), files=files,
                   stopped_at=stopped, stopped_subject=subject,
                   message=(r.stdout + r.stderr).strip()[-2000:])
        if rep["after"] != before:
            raise Usage("rebase --abort 后 HEAD 没回到 %s" % before)
        return rep, 3
    rep.update(status="rebased", after=git("rev-parse", "HEAD"), version=describe())
    if not DESCRIBE.match(rep["version"]):
        rep["status"] = "bad-version"
        return rep, 4
    return rep, 0


def issue_body(rep):
    lines = ["自动 rebase 到 `%s`（%s）停在了冲突上，什么都没推。" % (rep["onto"], rep["onto_sha"][:12]), "",
             "- 分支：`%s`，原 HEAD `%s`" % (rep["branch"], rep["before"][:12]),
             "- 停在：`%s` %s" % (rep.get("stopped_at", "")[:12], rep.get("stopped_subject", "")),
             "- fork 自有提交 %d 个，上游新增 %d 个" % (rep["own_commits"], rep["upstream_new"]), "",
             "冲突的文件："]
    lines += ["- `%s`" % f for f in rep.get("files") or ["（git 没列出，见下面的输出）"]]
    lines += ["", "本机解：", "```sh",
              "git fetch upstream && git switch %s && git rebase %s" % (rep["branch"], rep["onto"]),
              "```", "", "<details><summary>git 的输出</summary>", "", "```",
              rep.get("message", ""), "```", "</details>"]
    return "\n".join(lines) + "\n"


def main(argv):
    ap = argparse.ArgumentParser(prog="sync.py")
    sub = ap.add_subparsers(dest="cmd", required=True)
    rb = sub.add_parser("rebase")
    rb.add_argument("--onto", required=True)
    rb.add_argument("--report")
    iss = sub.add_parser("issue")
    iss.add_argument("report")
    a = ap.parse_args(argv)
    try:
        if a.cmd == "issue":
            sys.stdout.write(issue_body(json.loads(Path(a.report).read_text(encoding="utf-8"))))
            return 0
        rep, code = rebase(a.onto)
    except Usage as e:
        print("sync.py: %s" % e, file=sys.stderr)
        return 2
    text = json.dumps(rep, ensure_ascii=False, indent=2)
    if a.report:
        Path(a.report).write_text(text + "\n", encoding="utf-8")
    print(text)
    return code


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
