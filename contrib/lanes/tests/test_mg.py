"""mg 的单元测试：magpie / Claude Code / Codex 都是替身，记下自己被怎么起的。

全部在临时 HOME 下跑：不读本机 mg.toml、~/.claude、~/.codex，不起真客户端，
不碰任何在跑的网关。运行: python3 -m unittest discover -s contrib/lanes/tests -p 'test_mg*.py'
"""

import json
import os
import subprocess
import sys
import tempfile
import textwrap
import time
import unittest
import uuid
from pathlib import Path

MG = Path(__file__).resolve().parent.parent / "mg"

# magpie 替身：`station ls --json` 回 listing.json；其余子命令记进 calls.jsonl。
FAKE_MAGPIE = textwrap.dedent("""\
    #!%(py)s
    import json, os, sys
    here = os.path.dirname(os.path.abspath(__file__))
    a = sys.argv[1:]
    with open(os.path.join(here, "calls.jsonl"), "a") as fh:
        fh.write(json.dumps({"args": a, "stations": os.environ.get("MAGPIE_STATIONS")}) + "\\n")
    if a[:3] == ["station", "ls", "--json"]:
        print(open(os.path.join(here, "listing.json")).read())
    elif a[:2] == ["station", "url"]:
        print("http://127.0.0.1:3946/?k=page-key")
    """)

# 客户端替身：把 argv 和环境写进 ran-<名>.json。
FAKE_CLIENT = textwrap.dedent("""\
    #!%(py)s
    import json, os, sys
    here = os.path.dirname(os.path.abspath(__file__))
    with open(os.path.join(here, "ran-" + os.path.basename(sys.argv[0]) + ".json"), "w") as fh:
        json.dump({"argv": sys.argv[1:], "env": dict(os.environ)}, fh)
    """)

ROUTING_ENV = {"ANTHROPIC_API_KEY": "", "HTTP_PROXY": "", "HTTPS_PROXY": "", "ALL_PROXY": "",
               "NO_PROXY": "127.0.0.1,localhost,::1"}


class MgTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.root = Path(self.tmp.name)
        self.bin = self.root / "bin"
        self.bin.mkdir()
        for name, body in (("magpie", FAKE_MAGPIE), ("cc-stand-in", FAKE_CLIENT), ("cx-stand-in", FAKE_CLIENT)):
            p = self.bin / name
            p.write_text(body % {"py": sys.executable})
            p.chmod(0o755)
        self.stations_root = self.root / "stations"
        self.home = self.root / "home"
        (self.home / ".codex").mkdir(parents=True)
        self.station = {
            "linxi": {"name": "linxi", "port": 3945, "webPort": 3946, "protected": True, "state": "up", "pid": 101,
                      "baseURL": "http://127.0.0.1:3945", "webURL": "http://127.0.0.1:3946",
                      "folder": str(self.stations_root / "linxi")},
            "ar": {"name": "ar", "port": 3965, "webPort": 3966, "protected": True, "state": "up", "pid": 102,
                   "baseURL": "http://127.0.0.1:3965", "webURL": "http://127.0.0.1:3966",
                   "folder": str(self.stations_root / "ar")},
            "spare": {"name": "spare", "port": 3985, "webPort": 3986, "protected": False, "state": "down",
                      "baseURL": "http://127.0.0.1:3985", "webURL": "http://127.0.0.1:3986",
                      "folder": str(self.stations_root / "spare")},
        }
        self.write_listing()
        # linxi 的两个调用 key，各一个 profile；另有一个打给别处的 profile
        (self.stations_root / "linxi" / "magpie").mkdir(parents=True)
        (self.stations_root / "linxi" / "magpie" / "caller-keys.json").write_text(json.dumps([
            {"id": "1", "name": "linxi-s1", "secret": "sk-linxi-one-0000000000"},
            {"id": "2", "name": "linxi-s2", "secret": "sk-linxi-two-0000000000"}]))
        self.profiles = self.stations_root / "profiles"
        self.profiles.mkdir()
        self.profile("linxi-s1", "http://127.0.0.1:3945", "sk-linxi-one-0000000000")
        self.profile("linxi-s2", "http://127.0.0.1:3945", "sk-linxi-two-0000000000")
        self.profile("baibei-s3", "http://127.0.0.1:3955", "sk-baibei-0000000000000")
        (self.root / "mg.toml").write_text(textwrap.dedent("""\
            [claude]
            launcher = "%s"
            [codex]
            launcher = "%s"
            provider = "custom"
            """) % (self.bin / "cc-stand-in", self.bin / "cx-stand-in"))
        self.codex_config('model_provider = "custom"\n[model_providers.custom]\nbase_url = "https://up.example/v1"\n')

    def tearDown(self):
        self.tmp.cleanup()

    # ------------------------------------------------------------ 夹具
    def write_listing(self):
        (self.bin / "listing.json").write_text(json.dumps({
            "path": str(self.root / "stations.yml"), "root": str(self.stations_root),
            "runner": "/official/magpie", "stations": list(self.station.values())}))

    def profile(self, name, base, token, drop=(), **extra):
        env = dict(ROUTING_ENV, ANTHROPIC_BASE_URL=base, ANTHROPIC_AUTH_TOKEN=token, **extra)
        for k in drop:
            env.pop(k)
        (self.profiles / (name + ".json")).write_text(json.dumps({"env": env, "model": "x"}))

    def codex_config(self, text):
        (self.home / ".codex" / "config.toml").write_text(text)

    def mg(self, *args, env=None):
        e = dict(os.environ)
        for k in ("CLAUDE_CONFIG_DIR", "CODEX_HOME", "CODEX_BIN", "MG_CLAUDE", "CX_DISABLE_COMPUTER_USE", "_MG_REEXEC"):
            e.pop(k, None)
        e.update({"HOME": str(self.home), "MG_CONFIG": str(self.root / "mg.toml"),
                  "MG_MAGPIE": str(self.bin / "magpie"), "MAGPIE_STATIONS": str(self.root / "stations.yml"),
                  "NO_PROXY": "example.com", "no_proxy": "example.com"})
        e.update(env or {})
        return subprocess.run([sys.executable, str(MG), *args], env=e, capture_output=True, text=True, timeout=60)

    def ran(self, who):
        p = self.bin / ("ran-%s.json" % who)
        return json.loads(p.read_text()) if p.exists() else None

    def calls(self):
        p = self.bin / "calls.jsonl"
        return [json.loads(l) for l in p.read_text().splitlines()] if p.exists() else []

    def hold(self, argv_tail, open_path=None):
        """一个在跑的进程：参数里带 argv_tail，可选地开着一个文件。"""
        code = "import sys,time\n"
        if open_path:
            code += "f=open(%r)\nprint('open',flush=True)\n" % str(open_path)
        code += "time.sleep(60)\n"
        p = subprocess.Popen([sys.executable, "-c", code, *argv_tail], stdout=subprocess.PIPE, text=True)

        def end():
            p.kill()
            p.wait()
            p.stdout.close()
        self.addCleanup(end)
        if open_path:
            p.stdout.readline()
        else:
            time.sleep(0.3)
        return p


class ClaudeCode(MgTest):
    def test_two_keys_must_be_named(self):
        r = self.mg("cc", "linxi")
        self.assertEqual(r.returncode, 1, r.stderr)
        self.assertIn("linxi-s1, linxi-s2", r.stderr)
        self.assertIsNone(self.ran("cc-stand-in"))

    def test_runs_the_profile_through_the_launcher(self):
        sid = str(uuid.uuid4())
        r = self.mg("cc", "linxi", "-k", "linxi-s2", "-r", sid, "--", "--model", "m")
        self.assertEqual(r.returncode, 0, r.stderr)
        got = self.ran("cc-stand-in")
        self.assertEqual(got["argv"], ["--settings", str(self.profiles / "linxi-s2.json"), "--resume", sid, "--model", "m"])
        self.assertEqual(got["env"]["NO_PROXY"], "127.0.0.1,localhost,::1,example.com")
        self.assertEqual(got["env"]["no_proxy"], "127.0.0.1,localhost,::1,example.com")
        self.assertIn("调用 key linxi-s2", r.stderr)
        self.assertIn("没找到会话", r.stderr)  # 临时 HOME 里没有这个会话的记录
        self.assertNotIn("sk-linxi", r.stdout + r.stderr)

    def test_one_profile_is_taken_without_k(self):
        (self.profiles / "linxi-s2.json").unlink()
        # 站点页不列的也不自动选：隐藏的、_模板、-k 收不了的名字
        for name in (".linxi-old", "_template", "linxi copy"):
            self.profile(name, "http://127.0.0.1:3945", "sk-linxi-one-0000000000")
        r = self.mg("cc", "linxi")
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertEqual(self.ran("cc-stand-in")["argv"], ["--settings", str(self.profiles / "linxi-s1.json")])

    def test_refuses_what_would_change_url_or_key(self):
        cases = {
            "baibei-s3": "不是 linxi 的网关",                                  # 打给别的网关
            "foreign": "不是 linxi 的调用 key",                                 # 对的网关，别家的 key
            "leaky": "缺 HTTPS_PROXY",                                         # 代理键没写，会从 settings.json 渗进来
            "proxied": "NO_PROXY 里没有 127.0.0.1",                            # 设了代理却没绕开回环
        }
        self.profile("foreign", "http://127.0.0.1:3945", "sk-someone-else-000000000")
        self.profile("leaky", "http://127.0.0.1:3945", "sk-linxi-one-0000000000", drop=("HTTPS_PROXY",))
        self.profile("proxied", "http://127.0.0.1:3945", "sk-linxi-one-0000000000",
                     HTTPS_PROXY="http://127.0.0.1:7897", NO_PROXY="localhost")
        for key, why in cases.items():
            with self.subTest(key):
                r = self.mg("cc", "linxi", "-k", key)
                self.assertEqual(r.returncode, 1, r.stderr)
                self.assertIn(why, r.stderr)
                self.assertIn("什么都没起", r.stderr)
                self.assertIsNone(self.ran("cc-stand-in"))
        r = self.mg("cc", "linxi", "-k", "../linxi-s1")
        self.assertEqual(r.returncode, 1)
        self.assertIn("不收路径", r.stderr)

    def test_refuses_a_session_resumed_elsewhere(self):
        sid = str(uuid.uuid4())
        holder = self.hold(["--resume", sid])
        r = self.mg("cc", "linxi", "-k", "linxi-s1", "-r", sid)
        self.assertEqual(r.returncode, 1, r.stderr)
        self.assertIn(str(holder.pid), r.stderr)
        self.assertIsNone(self.ran("cc-stand-in"))
        # 它退出之后就能续
        holder.kill()
        holder.wait()
        self.assertEqual(self.mg("cc", "linxi", "-k", "linxi-s1", "-r", sid).returncode, 0)

    def test_refuses_a_station_that_is_down_and_a_bad_id(self):
        r = self.mg("cc", "spare")
        self.assertEqual(r.returncode, 1)
        self.assertIn("mg up spare", r.stderr)
        r = self.mg("cc", "linxi", "-k", "linxi-s1", "-r", "not-a-session; rm -rf ~")
        self.assertEqual(r.returncode, 1)
        self.assertIn("会话 id", r.stderr)
        self.assertIsNone(self.ran("cc-stand-in"))

    def test_warns_only_of_what_settings_json_would_leak(self):
        (self.home / ".claude").mkdir()
        (self.home / ".claude" / "settings.json").write_text(json.dumps(
            {"env": {"CLAUDE_CODE_AUTO_COMPACT_WINDOW": "400000", "ANTHROPIC_BASE_URL": "https://elsewhere"}}))
        r = self.mg("cc", "linxi", "-k", "linxi-s1", "--print")
        self.assertEqual(r.returncode, 0, r.stderr)
        warned = [l for l in r.stderr.splitlines() if "settings.json 的值" in l]
        self.assertEqual(len(warned), 1, r.stderr)
        self.assertIn("CLAUDE_CODE_AUTO_COMPACT_WINDOW", warned[0])
        self.assertNotIn("ANTHROPIC_BETAS", warned[0])

    def test_print_runs_nothing(self):
        r = self.mg("cc", "linxi", "-k", "linxi-s1", "--print")
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertIn("--settings", r.stdout)
        self.assertIn("NO_PROXY=127.0.0.1,localhost,::1,example.com", r.stdout)
        self.assertIsNone(self.ran("cc-stand-in"))


class Codex(MgTest):
    def test_points_the_provider_at_the_gateway(self):
        r = self.mg("cx", "ar", "-r", "019a0000-1111-2222-3333-444455556666", "--", "--search")
        self.assertEqual(r.returncode, 0, r.stderr)
        got = self.ran("cx-stand-in")
        self.assertEqual(got["argv"], ["-c", 'model_providers.custom.base_url="http://127.0.0.1:3965/v1"',
                                       "--search", "resume", "019a0000-1111-2222-3333-444455556666"])
        self.assertEqual(got["env"]["NO_PROXY"], "127.0.0.1,localhost,::1,example.com")
        # config.toml 一个字节没动
        self.assertIn('base_url = "https://up.example/v1"', (self.home / ".codex" / "config.toml").read_text())

    def test_refuses_when_the_override_would_not_take(self):
        self.codex_config('model_provider = "openai"\n[model_providers.custom]\nbase_url = "x"\n')
        r = self.mg("cx", "ar")
        self.assertEqual(r.returncode, 1, r.stderr)
        self.assertIn("绕过网关", r.stderr)
        self.assertIsNone(self.ran("cx-stand-in"))

    def test_refuses_a_rollout_held_open(self):
        sid = "019a0000-aaaa-bbbb-cccc-ddddeeeeffff"
        day = self.home / ".codex" / "sessions" / "2026" / "10" / "26"
        day.mkdir(parents=True)
        rollout = day / ("rollout-2026-10-26T06-00-00-%s.jsonl" % sid)
        rollout.write_text("{}\n")
        holder = self.hold([], open_path=rollout)
        r = self.mg("cx", "ar", "-r", sid)
        self.assertEqual(r.returncode, 1, r.stderr)
        self.assertIn(str(holder.pid), r.stderr)
        self.assertIsNone(self.ran("cx-stand-in"))

    def test_computer_use_switch_is_kept(self):
        r = self.mg("cx", "ar", env={"CX_DISABLE_COMPUTER_USE": "1"})
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertEqual(self.ran("cx-stand-in")["argv"][:2], ["--disable", "computer_use"])


class Stations(MgTest):
    def test_up_down_new_go_through_magpie_station(self):
        self.assertEqual(self.mg("up", "--all").returncode, 0)
        self.assertEqual(self.mg("down", "spare").returncode, 0)
        self.assertEqual(self.mg("down", "linxi", "--force").returncode, 0)
        self.assertEqual(self.mg("new", "fresh", "--port", "3995", "--note", "n").returncode, 0)
        got = [c["args"] for c in self.calls() if c["args"][1] != "ls"]
        self.assertEqual(got, [["station", "start", "linxi"], ["station", "start", "ar"], ["station", "start", "spare"],
                               ["station", "stop", "spare"], ["station", "stop", "linxi", "--force"],
                               ["station", "new", "fresh", "--port", "3995", "--note", "n"]])
        # 每次都告诉 magpie 读哪份 stations.yml
        self.assertEqual({c["stations"] for c in self.calls()}, {str(self.root / "stations.yml")})
        # down 没有 --all
        self.assertEqual(self.mg("down", "--all").returncode, 2)

    def test_ls_env_web_logs(self):
        r = self.mg("ls")
        self.assertEqual(r.returncode, 0, r.stderr)
        for want in ("linxi", "spare", "down", "受保护", str(self.root / "stations.yml")):
            self.assertIn(want, r.stdout)
        r = self.mg("env", "ar")
        self.assertIn("export ANTHROPIC_BASE_URL=http://127.0.0.1:3965", r.stdout)
        self.assertIn("export OPENAI_BASE_URL=http://127.0.0.1:3965/v1", r.stdout)
        r = self.mg("web", "linxi", "--print")
        self.assertEqual(r.stdout.strip(), "http://127.0.0.1:3946/?k=page-key")
        (self.stations_root / "linxi" / "serve.log").write_text(
            "open http://127.0.0.1:3946/?k=secretkey123\nkey sk-abcdefghijklmnopqrstuv\nplain\n")
        r = self.mg("logs", "linxi", "-n", "3")
        self.assertNotIn("secretkey123", r.stdout)
        self.assertNotIn("sk-abcdefghijklmnopqrstuv", r.stdout)
        self.assertIn("plain", r.stdout)

    def test_the_window_s_stations_yml_by_default(self):
        """没写明 stations.yml 时，读客户端双击时读的那份：app 的 LSEnvironment。"""
        app = self.root / "magpie-stations.app" / "Contents"
        (app / "MacOS").mkdir(parents=True)
        (app / "MacOS" / "magpie").write_text((self.bin / "magpie").read_text())
        (app / "MacOS" / "magpie").chmod(0o755)
        (app / "MacOS" / "listing.json").write_text((self.bin / "listing.json").read_text())
        import plistlib
        view = self.root / "view"
        with open(app / "Info.plist", "wb") as fh:
            plistlib.dump({"LSEnvironment": {"XDG_CONFIG_HOME": str(view), "MAGPIE_ADDR": "127.0.0.1:3395"}}, fh)
        r = self.mg("ls", env={"MG_MAGPIE": str(app / "MacOS" / "magpie"), "MAGPIE_STATIONS": ""})
        self.assertEqual(r.returncode, 0, r.stderr)
        calls = [json.loads(l) for l in (app / "MacOS" / "calls.jsonl").read_text().splitlines()]
        self.assertEqual({c["stations"] for c in calls}, {str(view / "magpie" / "stations.yml")})

    def test_a_mistyped_config_is_refused(self):
        with open(self.root / "mg.toml", "a") as fh:
            fh.write("[codx]\nprovider = \"custom\"\n")
        r = self.mg("ls")
        self.assertEqual(r.returncode, 1)
        self.assertIn("不认识 [codx]", r.stderr)


if __name__ == "__main__":
    unittest.main()
