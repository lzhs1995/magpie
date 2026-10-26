"""mg upgrade 的单元测试：临时 .app、file:// 上的 release、假的 open/codesign/lsregister。

管理台是一个小 Python HTTP 服务（替身），在临时端口上监听；lsof 与 ps 用真的。
不碰本机的 magpie-stations.app、LaunchServices 和任何网关。
运行: python3 -m unittest contrib/lanes/tests/test_mg_upgrade.py
"""

import hashlib
import json
import os
import plistlib
import signal
import socket
import subprocess
import sys
import tempfile
import textwrap
import threading
import time
import unittest
import urllib.error
import urllib.request
from pathlib import Path

MG = Path(__file__).resolve().parent.parent / "mg"

# 管理台替身：--version 印版本；否则在 MAGPIE_ADDR 上回 GET /（状态码写死在脚本里），
# SIGTERM 后过 exit_delay 秒才退。pid 记进 pids.txt，tearDown 清理。
FAKE_CONSOLE = textwrap.dedent("""\
    #!%(py)s
    import http.server, os, signal, sys, threading
    VERSION, STATUS, EXIT_DELAY, PIDS = %(version)r, %(status)d, %(exit_delay)r, %(pids)r
    if sys.argv[1:] == ["--version"]:
        print("magpie " + VERSION)
        sys.exit(0)
    host, _, port = os.environ["MAGPIE_ADDR"].rpartition(":")
    class H(http.server.BaseHTTPRequestHandler):
        def do_GET(self):
            body = VERSION.encode()
            self.send_response(STATUS)
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)
        def log_message(self, *a):
            pass
    srv = http.server.ThreadingHTTPServer((host, int(port)), H)
    srv.daemon_threads = True
    signal.signal(signal.SIGTERM, lambda *_a: threading.Timer(EXIT_DELAY, lambda: os._exit(0)).start())
    with open(PIDS, "a") as fh:
        fh.write(str(os.getpid()) + "\\n")
    srv.serve_forever()
    """)

# open 替身：像 LaunchServices 那样，带 Info.plist 的 LSEnvironment 把包里的程序起成脱离的进程。
FAKE_OPEN = textwrap.dedent("""\
    #!%(py)s
    import json, os, plistlib, sys
    here = os.path.dirname(os.path.abspath(__file__))
    with open(os.path.join(here, "calls.jsonl"), "a") as fh:
        fh.write(json.dumps(["open"] + sys.argv[1:]) + "\\n")
    app = sys.argv[1]
    with open(os.path.join(app, "Contents", "Info.plist"), "rb") as fh:
        info = plistlib.load(fh)
    exe = os.path.join(app, "Contents", "MacOS", info["CFBundleExecutable"])
    env = dict(os.environ, **info.get("LSEnvironment", {}))
    if os.fork() == 0:
        os.setsid()
        if os.fork() == 0:
            # 输出进 console.log：起不来时测试失败信息里能看到为什么
            os.dup2(os.open(os.devnull, os.O_RDONLY), 0)
            log = os.open(os.path.join(here, "console.log"), os.O_WRONLY | os.O_CREAT | os.O_APPEND, 0o644)
            os.dup2(log, 1)
            os.dup2(log, 2)
            os.execve(exe, [exe], env)
        os._exit(0)
    os.wait()
    """)

# codesign / lsregister 替身：记下调用；同目录有 fail-<名> 就失败一次。
FAKE_TOOL = textwrap.dedent("""\
    #!%(py)s
    import json, os, sys
    here = os.path.dirname(os.path.abspath(__file__))
    name = os.path.basename(sys.argv[0])
    with open(os.path.join(here, "calls.jsonl"), "a") as fh:
        fh.write(json.dumps([name] + sys.argv[1:]) + "\\n")
    flag = os.path.join(here, "fail-" + name)
    if os.path.exists(flag):
        os.remove(flag)
        sys.stderr.write(name + " 替身按要求失败\\n")
        sys.exit(1)
    """)

OLD = "v0.1.1564-16-ga15f7a67"
NEW = "v0.1.1564-17-g9eea0efc"
PROXY_ENV = ("HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "http_proxy", "https_proxy", "all_proxy")


def free_port():
    with socket.socket() as s:
        s.bind(("127.0.0.1", 0))
        return s.getsockname()[1]


def served(port):
    """GET / → (状态码, 正文)；没在监听就 None。不走代理。"""
    plain = urllib.request.build_opener(urllib.request.ProxyHandler({}))
    try:
        with plain.open("http://127.0.0.1:%d/" % port, timeout=5) as resp:
            return resp.status, resp.read().decode()
    except urllib.error.HTTPError as exc:
        return exc.code, exc.read().decode()
    except OSError:
        return None


class UpgradeTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.root = Path(self.tmp.name)
        self.pids = self.root / "pids.txt"
        self.pids.touch()
        self.tools = self.root / "tools"
        self.tools.mkdir()
        for name, body in (("open", FAKE_OPEN), ("codesign", FAKE_TOOL), ("lsregister", FAKE_TOOL)):
            p = self.tools / name
            p.write_text(body % {"py": sys.executable})
            p.chmod(0o755)
        self.port = free_port()
        self.app = self.root / "Applications" / "magpie-stations.app"
        self.binary = self.app / "Contents" / "MacOS" / "magpie"
        self.binary.parent.mkdir(parents=True)
        self.old_body = self.console(OLD)
        self.binary.write_text(self.old_body)
        self.binary.chmod(0o755)
        self.plist = self.app / "Contents" / "Info.plist"
        self.write_info(CFBundleIdentifier="com.yetone.magpie.stations")
        self.home = self.root / "home"
        self.home.mkdir()
        self.state = self.root / "state"
        (self.root / "mg.toml").write_text("[upgrade]\nmin_free_gb = 0\n")
        self.release(NEW)

    def tearDown(self):
        for line in self.pids.read_text().split():
            try:
                os.kill(int(line), signal.SIGKILL)
            except (ProcessLookupError, ValueError):
                pass
        self.tmp.cleanup()

    # ------------------------------------------------------------ 夹具
    def console(self, version, status=200, exit_delay=0.0):
        return FAKE_CONSOLE % {"py": sys.executable, "version": version, "status": status,
                               "exit_delay": exit_delay, "pids": str(self.pids)}

    def write_info(self, **extra):
        info = {"CFBundleExecutable": "magpie", "CFBundleShortVersionString": OLD, "CFBundleVersion": OLD,
                "LSEnvironment": {"MAGPIE_ADDR": "127.0.0.1:%d" % self.port, "XDG_CONFIG_HOME": "/view"}}
        info.update(extra)
        with self.plist.open("wb") as fh:
            plistlib.dump(info, fh)

    def info(self):
        with self.plist.open("rb") as fh:
            return plistlib.load(fh)

    def release(self, version, tag=None, status=200, exit_delay=0.0, sha=None):
        """file:// 上的一个 release：tag stations-<tag 或 version>，二进制自报 version。"""
        rel = self.root / "release"
        rel.mkdir(exist_ok=True)
        body = self.console(version, status, exit_delay).encode()
        (rel / "magpie-darwin-arm64").write_bytes(body)
        digest = sha or hashlib.sha256(body).hexdigest()
        (rel / "SHA256SUMS").write_text("%s  magpie-darwin-arm64\n" % digest)
        (self.root / "releases.json").write_text(json.dumps([{
            "tag_name": "stations-" + (tag or version), "draft": False, "prerelease": False,
            "published_at": "2026-10-26T04:38:05Z",
            "assets": [{"name": n, "size": (rel / n).stat().st_size, "browser_download_url": (rel / n).as_uri()}
                       for n in ("magpie-darwin-arm64", "SHA256SUMS")]}]))

    def mg(self, *args, binary=None, tools=None):
        e = {k: v for k, v in os.environ.items() if k not in PROXY_ENV and k != "_MG_REEXEC"}
        e.update({"HOME": str(self.home), "MG_CONFIG": str(self.root / "mg.toml"),
                  "MG_MAGPIE": str(binary or self.binary), "MAGPIE_STATIONS": str(self.root / "stations.yml"),
                  "MG_TOOLS_DIR": str(tools or self.tools), "XDG_STATE_HOME": str(self.state),
                  "MG_RELEASES_API": (self.root / "releases.json").as_uri()})
        return subprocess.run([sys.executable, str(MG), "upgrade", *args], env=e,
                              capture_output=True, text=True, timeout=120)

    def wait_served(self, want, seconds=20):
        deadline = time.monotonic() + seconds
        while time.monotonic() < deadline:
            got = served(self.port)
            if got and got[1] == want:
                return got
            time.sleep(0.2)
        self.fail("端口 %d 在 %d 秒内没回 %s（最后: %r）\n%s" % (self.port, seconds, want, served(self.port),
                                                           self.diagnose()))

    def diagnose(self):
        """失败时附上：替身管理台的输出、端口上 lsof 看到的一切、记下的 pid 还活着没有。"""
        log = self.tools / "console.log"
        lsof = subprocess.run(["lsof", "-nP", "-iTCP:%d" % self.port], capture_output=True, text=True)
        alive = []
        for line in self.pids.read_text().split():
            try:
                os.kill(int(line), 0)
                alive.append(line + " 活着")
            except ProcessLookupError:
                alive.append(line + " 已退")
        return "console.log:\n%s\nlsof rc=%d:\n%s%s\npids: %s" % (
            log.read_text()[-3000:] if log.exists() else "（没有）", lsof.returncode,
            lsof.stdout[-1500:], lsof.stderr[-500:], ", ".join(alive) or "（没有）")

    def start_console(self):
        subprocess.run([str(self.tools / "open"), str(self.app)], check=True)
        self.wait_served(OLD)
        (self.tools / "calls.jsonl").unlink()
        return self.console_pid()

    def console_pid(self):
        out = subprocess.run(["lsof", "-nP", "-iTCP:%d" % self.port, "-sTCP:LISTEN", "-t"],
                             capture_output=True, text=True).stdout.split()
        return int(out[0]) if out else None

    def calls(self):
        p = self.tools / "calls.jsonl"
        return [json.loads(x)[0] for x in p.read_text().splitlines()] if p.exists() else []

    def backups(self):
        d = self.state / "magpie-stations" / "upgrades"
        return sorted(d.iterdir()) if d.is_dir() else []

    def assert_untouched(self, r, *why):
        self.assertEqual(r.returncode, 1, r.stdout + r.stderr)
        for w in why:
            self.assertIn(w, r.stderr)
        self.assertEqual(self.binary.read_text(), self.old_body)
        self.assertEqual(self.info()["CFBundleVersion"], OLD)
        self.assertEqual(self.calls(), [])
        self.assertEqual([p.name for p in self.backups()], [])

    # ------------------------------------------------------------ 测试
    def test_swaps_only_the_console_and_restarts_it_at_zero_connections(self):
        old_pid = self.start_console()
        self.write_info(CFBundleIdentifier="com.yetone.magpie.stations",
                        CFBundleURLTypes=[{"CFBundleURLSchemes": ["magpie"]}])
        r = self.mg()
        self.assertEqual(r.returncode, 0, r.stdout + r.stderr)
        self.assertEqual(served(self.port), (200, NEW))
        self.assertNotEqual(self.console_pid(), old_pid)
        with self.assertRaises(ProcessLookupError):
            os.kill(old_pid, 0)
        info = self.info()
        self.assertEqual((info["CFBundleShortVersionString"], info["CFBundleVersion"]), (NEW, NEW))
        self.assertEqual(info["LSEnvironment"], {"MAGPIE_ADDR": "127.0.0.1:%d" % self.port,
                                                 "XDG_CONFIG_HOME": "/view"})
        self.assertNotIn("CFBundleURLTypes", info)
        self.assertEqual(self.calls(), ["codesign", "codesign", "lsregister", "open"])
        [b] = self.backups()
        self.assertTrue(b.name.endswith("-" + OLD), b.name)
        self.assertEqual((b / "magpie").read_text(), self.old_body)
        with (b / "Info.plist").open("rb") as fh:
            self.assertEqual(plistlib.load(fh)["CFBundleVersion"], OLD)
        self.assertEqual(sorted(p.name for p in self.app.rglob("*")),
                         ["Contents", "Info.plist", "MacOS", "magpie"])

    def test_a_connected_client_means_nothing_is_touched(self):
        old_pid = self.start_console()
        with socket.create_connection(("127.0.0.1", self.port)):
            r = self.mg()
        # 要说出是谁连着（客户端 pid 就是本测试进程），用户才知道该关哪个
        self.assert_untouched(r, "1 个连接", "(%d)" % os.getpid(), "什么都没动")
        self.assertEqual(self.console_pid(), old_pid)
        self.assertFalse((self.state / "magpie-stations" / "upgrades").exists())

    def test_sha256_mismatch_is_refused_before_touching_the_app(self):
        old_pid = self.start_console()
        self.release(NEW, sha="0" * 64)
        r = self.mg()
        self.assert_untouched(r, "SHA256 对不上")
        self.assertEqual(self.console_pid(), old_pid)
        self.assertEqual(served(self.port), (200, OLD))

    def test_a_short_download_is_retried_then_refused(self):
        # release 列的大小比实际多 1 字节：每次下到的都「不完整」
        rels = json.loads((self.root / "releases.json").read_text())
        rels[0]["assets"][0]["size"] += 1
        (self.root / "releases.json").write_text(json.dumps(rels))
        r = self.mg()
        self.assert_untouched(r, "下载不完整")
        self.assertEqual(r.stdout.count("下载不完整"), 3, r.stdout)

    def test_a_release_whose_version_is_not_describe_shaped_is_refused(self):
        self.release("v0.1.1565")
        self.assert_untouched(self.mg(), "git describe")

    def test_a_binary_that_reports_another_version_is_refused(self):
        self.release(NEW, tag="v0.1.1564-18-gdeadbeef")
        self.assert_untouched(self.mg(), "自报 " + NEW)

    def test_already_current_does_nothing(self):
        self.release(OLD)
        r = self.mg()
        self.assertEqual(r.returncode, 0, r.stdout + r.stderr)
        self.assertIn("已是最新", r.stdout)
        self.assertEqual((self.calls(), self.backups()), ([], []))

    def test_an_older_release_is_not_installed(self):
        self.release("v0.1.1564-15-g0123abcd")
        self.assert_untouched(self.mg(), "不降级")

    def test_a_newer_upstream_beats_more_own_commits(self):
        self.release("v0.1.1565-2-g0123abcd")
        r = self.mg()
        self.assertEqual(r.returncode, 0, r.stdout + r.stderr)
        self.assertEqual(self.info()["CFBundleVersion"], "v0.1.1565-2-g0123abcd")

    def test_a_console_that_is_not_running_is_swapped_but_not_started(self):
        r = self.mg()
        self.assertEqual(r.returncode, 0, r.stdout + r.stderr)
        self.assertIn("没在跑", r.stdout)
        self.assertEqual(self.calls(), ["codesign", "codesign", "lsregister"])
        self.assertIsNone(served(self.port))
        self.assertIn(repr(NEW), self.binary.read_text())
        self.assertEqual(self.info()["CFBundleVersion"], NEW)

    def test_a_new_console_that_comes_up_broken_is_rolled_back(self):
        old_pid = self.start_console()
        self.release(NEW, status=500)
        r = self.mg()
        self.assertEqual(r.returncode, 1, r.stdout + r.stderr)
        self.assertIn("回 500", r.stderr)
        self.assertIn("已换回 " + OLD, r.stderr)
        self.assertEqual(served(self.port), (200, OLD))
        self.assertNotEqual(self.console_pid(), old_pid)
        self.assertEqual(self.binary.read_text(), self.old_body)
        self.assertEqual(self.info()["CFBundleVersion"], OLD)
        self.assertEqual(self.calls(), ["codesign", "codesign", "lsregister", "open"] * 2)

    def test_a_failed_resign_puts_the_old_binary_back(self):
        (self.tools / "fail-lsregister").touch()
        r = self.mg()
        self.assertEqual(r.returncode, 1, r.stdout + r.stderr)
        self.assertIn("lsregister 失败", r.stderr)
        self.assertIn("已换回 " + OLD, r.stderr)
        self.assertEqual(self.binary.read_text(), self.old_body)
        self.assertEqual(self.info()["CFBundleVersion"], OLD)
        self.assertEqual(self.calls(), ["codesign", "codesign", "lsregister"] * 2)
        self.assertIsNone(served(self.port))

    def test_a_slow_old_console_is_finished_in_the_background(self):
        self.old_body = self.console(OLD, exit_delay=6)
        self.binary.write_text(self.old_body)
        old_pid = self.start_console()
        r = self.mg("--wait", "1")
        self.assertEqual(r.returncode, 1, r.stdout + r.stderr)
        self.assertIn("没退出", r.stdout)
        self.assertEqual(served(self.port), (200, OLD))  # 没人 kill 它，旧的照常服务到自己退出
        self.assertNotIn("open", self.calls())
        self.assertIn(repr(NEW), self.binary.read_text())
        self.wait_served(NEW, seconds=30)
        self.assertNotEqual(self.console_pid(), old_pid)
        self.assertEqual(self.calls()[-1], "open")

    def test_check_only_reports(self):
        self.start_console()
        r = self.mg("--check")
        self.assertEqual(r.returncode, 0, r.stdout + r.stderr)
        for want in (OLD, NEW, "0 个连接"):
            self.assertIn(want, r.stdout)
        self.assertEqual(served(self.port), (200, OLD))
        self.assertEqual((self.calls(), self.backups()), ([], []))

    def test_not_enough_disk_is_refused(self):
        (self.root / "mg.toml").write_text("[upgrade]\nmin_free_gb = 1e12\n")
        self.assert_untouched(self.mg(), "磁盘只剩")

    def test_only_the_two_newest_backups_are_kept(self):
        d = self.state / "magpie-stations" / "upgrades"
        for name in ("20260101T000000Z-v0.1.1-1-gaaaaaaa", "20260102T000000Z-v0.1.1-2-gbbbbbbb",
                     "20260103T000000Z-v0.1.1-3-gccccccc"):
            (d / name).mkdir(parents=True)
        r = self.mg()
        self.assertEqual(r.returncode, 0, r.stdout + r.stderr)
        names = [p.name for p in self.backups()]
        self.assertEqual(len(names), 2, names)
        self.assertEqual(names[0], "20260103T000000Z-v0.1.1-3-gccccccc")
        self.assertTrue(names[1].endswith("-" + OLD), names)

    def test_a_port_held_by_another_program_is_left_alone(self):
        # 别的程序是单独的进程，退出就被收走：mg 万一 kill 了它，测试本身不会跟着挨 SIGTERM
        other_log = (self.root / "other.log").open("w")
        other = subprocess.Popen([sys.executable, "-m", "http.server", "--bind", "127.0.0.1", str(self.port)],
                                 cwd=self.home, stdout=other_log, stderr=subprocess.STDOUT)
        threading.Thread(target=other.wait, daemon=True).start()
        try:
            deadline = time.monotonic() + 20
            while self.console_pid() != other.pid and time.monotonic() < deadline:
                time.sleep(0.1)
            self.assertEqual(self.console_pid(), other.pid, "served=%r other.log=%r\n%s" % (
                served(self.port), (self.root / "other.log").read_text()[-1500:], self.diagnose()))
            self.assert_untouched(self.mg(), "被别的程序占着")
            self.assertIsNone(other.poll(), "mg 动了占着端口的别的程序")
        finally:
            other.kill()
            other_log.close()

    def test_the_official_bundle_is_never_upgraded(self):
        self.write_info(CFBundleIdentifier="com.yetone.magpie")
        r = self.mg()
        self.assertEqual(r.returncode, 1, r.stdout + r.stderr)
        self.assertIn("官方 magpie.app", r.stderr)
        self.assertEqual((self.binary.read_text(), self.calls(), self.backups()), (self.old_body, [], []))

    def test_a_binary_outside_an_app_is_refused(self):
        bare = self.root / "magpie"
        bare.write_text(self.old_body)
        bare.chmod(0o755)
        r = self.mg(binary=bare)
        self.assertEqual(r.returncode, 1, r.stdout + r.stderr)
        self.assertIn("不在 .app 里", r.stderr)

    def test_a_missing_tool_is_refused_up_front(self):
        (self.tools / "lsregister").unlink()
        self.assert_untouched(self.mg(), "找不到 lsregister")


if __name__ == "__main__":
    unittest.main()
