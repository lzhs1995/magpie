# mg 与 magpie-lanes

## mg：用哪个网关、开或续哪个客户端，一条命令

```sh
mg ls                          # 全部网关：状态、pid、钉着几个客户端、网关/管理台地址
mg up <站>                     # 起它（已在服务就不动）；mg up --all
mg down <站>                   # 停它；受保护的要 --force
mg cc <站> -k <key>            # Claude Code 经这个网关开新会话
mg cc <站> -k <key> -r <id>    # 续会话
mg cx <站>                     # Codex 经这个网关开新会话
mg cx <站> -r <id>             # 续会话
mg env <站>                    # 给别的工具用的 export 行
mg web <站>                    # 浏览器里开管理台（带口令登录）
mg new <站>                    # 新网关：端口、目录、口令都给好
mg logs <站> -n 60
```

`cc` / `cx` / `web` 都可加 `--print`：只打印要起的命令，什么都不起。`--` 之后的
参数原样交给客户端（`mg cc linxi -k linxi-s1 -- --model opus`）。

**网关清单只有一份：stations.yml**，就是客户端「网关站点」页那份。mg 不自己解析
它，也不自己拼网关的启动环境，全部经 fork 的 `magpie station …`（同一份 Go 代码）。
所以 mg 起的网关和页面「启动」起的、和 `magpie serve --station` 起的是同一个。

### 装

```sh
ln -s "$PWD/contrib/lanes/mg" ~/.local/bin/mg          # 或别的 PATH 目录
cp contrib/lanes/mg.toml.example ~/.config/magpie-stations/mg.toml   # 可选，本机设置
```

要 fork 客户端（magpie-stations.app，带 `magpie station`）和 Python 3.11+。
stations.yml 默认读客户端双击时读的那份（app 的 LSEnvironment），不用配。

### mg 替你守的规矩

- **只起新的客户端进程。** 不改 `~/.claude/settings.json`、`~/.codex/config.toml`，
  不碰任何网关的 `providers.json`：上游 URL 和上游 key 一个不换。
- **NO_PROXY 必带回环。** `.zshrc` 把 `HTTP(S)_PROXY` 指向 7897，codex 会把
  127.0.0.1 也交给它（502）。mg 把 `127.0.0.1,localhost,::1` 放最前，原有的不丢。
- **Claude Code 走 `--settings <profile>`，不走 export。** 本机 `settings.json`
  的 env 块会盖掉 shell 里的 `ANTHROPIC_BASE_URL`。profile 里没写的键会从
  `settings.json` 逐键渗进来，所以 `mg cc` 起之前核：
  - profile 打给的就是这个网关；
  - 带的是这个网关自己的调用 key（`caller-keys.json` 里有；站点目录里没有这个
    文件时只提醒）；
  - `ANTHROPIC_BASE_URL`、`ANTHROPIC_AUTH_TOKEN`、`ANTHROPIC_API_KEY`、
    `HTTP_PROXY`、`HTTPS_PROXY`、`ALL_PROXY`、`NO_PROXY` 自己写明了。
  任一条不满足就拒绝，什么都不起。压缩窗口、`ANTHROPIC_BETAS` 这类不改请求
  打给谁的键，profile 没写而 `settings.json` 写了时只提醒。
- **Codex 只覆盖 base_url。** `-c model_providers.custom.base_url=…/v1`。
  `config.toml` 的 `model_provider` 不是 `custom` 时覆盖不生效，请求会绕过网关，
  所以 mg 拒绝起。
- **续会话前先查占用。** Claude 的会话已有进程在 `--resume`，或 Codex 的 rollout
  被别的进程开着，就拒绝。先在那个窗口退出再续。

### 加第 N 个网关

```sh
mg new work                      # 端口从 3500 起每次 +10，页面端口 = 网关 +1
# 管理台里加上游 provider 和 key：
mg web work
mg up work
# Claude Code 要一个 profile：网关的 Gateway keys 页建一个调用 key，
# 复制现有 profile，ANTHROPIC_BASE_URL 改成新网关、ANTHROPIC_AUTH_TOKEN 改成那个 key
mg cc work -k work-s1 --print    # 先看一眼
```

### 跟 magpie-lanes 的关系

`mg` 接手了起停和接客户端。`magpie-lanes` 的 `routing` / `keys` / `edit` / `open`
照旧可用，读的仍是 lanes.toml。有两处不一致，用 mg：

- lanes.toml 的 `no_proxy` 是全局一个值，linxi / baibei 实际跑的是带
  `k40.shengqainbang.cn` 的。`magpie-lanes up` 起它们时，环境会和现在在跑的不同。
  stations.yml 里每站各写各的，`mg up` 读它。
- `magpie-lanes use` 的 Claude Code 写法是 export `ANTHROPIC_BASE_URL`，本机不生效
  （见上）。用 `mg cc`。

---

# magpie-lanes —— 并行通道监督器

一条命令管多条 magpie 通道。magpie-lanes 本身不改 magpie。上面的 `mg` 要用 fork 的
`magpie station`。

## 为什么需要它

一条 lane = 一个 magpie 进程 + 它专属的一组 key + 故障转移只在这组 key 内部发生，
**永不越界到别的 lane**。

magpie 没有「通道」这个一等概念：

- 一个进程只有一个 `settings.GatewayAddr()`（`internal/gateway/gateway.go`），
  所以一个进程只能有一个网关地址。
- `internal/gateway/affinity.go` 里写明，会话黏在答过它的那个 key 上，
  **但一旦那个 key 歇着或快用完，路由就重新接手**。也就是说 key A 耗尽时，
  会话会被转进 key B 的额度。

两条合起来：**并行隔离只能在进程级别做**，在一个进程里靠配置做不到。
所以要 N 条互不越界的通道，就得起 N 个 magpie，各自独立的 `XDG_CONFIG_HOME`。

手动管这些进程很烦——每条一个 start 脚本、一个 stop 脚本，端口、站点目录、
凭据路径散在脚本里。这个工具把它们收成一份声明式清单。

## 装

```sh
# 1. 建配置（别放仓库里，里面有本机路径）
mkdir -p ~/.config/magpie-stations
cp contrib/lanes/lanes.toml.example ~/.config/magpie-stations/lanes.toml
chmod 600 ~/.config/magpie-stations/lanes.toml
$EDITOR ~/.config/magpie-stations/lanes.toml

# 2. 跑
contrib/lanes/magpie-lanes status
```

配置查找顺序：`$MAGPIE_LANES_CONFIG` → `~/.config/magpie-stations/lanes.toml`
→ 脚本旁边的 `lanes.toml`。要 Python 3.11+（`tomllib`）；系统 Python 太旧时
会自动找一个够新的重新 exec。

## 从零建一条通道：完整五步

```sh
# 1. 建。目录、凭据、providers 骨架、配置条目，一条命令全齐。
#    端口自动找空闲对（3495 起，每次 +10），也可以 --gateway-port 自己指定。
magpie-lanes new mylane --client codex
#    → 打印站点目录、管理台口令、下一步提示

# 2. 填上游地址和 key。改完最多 1 秒生效，不用重启。
magpie-lanes edit mylane
#    没设 $EDITOR 就: magpie-lanes edit mylane --print --show-secrets
#    然后直接改它打印的那个路径

# 3. 核一下填对了没有
magpie-lanes keys mylane
#    列出 provider、上游地址、故障转移链、限额、每个 key 的末 4 位

# 4. 起它
magpie-lanes up mylane

# 5. 把客户端接进去
magpie-lanes use mylane          # 打印完整命令
magpie-lanes use mylane --run    # 直接起
```

## 日常

```sh
magpie-lanes status              # 全部通道一屏。只读，不碰任何进程
magpie-lanes routing mylane      # 终端里看路由：谁答的、几毫秒、重试几次
magpie-lanes open mylane         # 带 key 的管理台 URL（--launch 直接开浏览器）
magpie-lanes logs mylane -n 60
magpie-lanes up --all            # 把没在跑的都拉起来，在跑的一个不碰
magpie-lanes down mylane         # protected 的要 --force
```

### 看路由

`routing` 直接读 magpie 自己写的 `routing/<日期>.jsonl`，不用开浏览器：

```
=== agentrouter-a  （网关 :3465）
    时刻       状态     key                    毫秒      重试     token     会话/模型
    18:14:34 200    c7df209111             24969          110749    01a159b4/gpt-6.1-sol
    18:14:24 200    271757d18c             63071   1      147323    01a0fefd/gpt-6.1-sol
    18:14:54 200    264689afd1             33768          121136    01a0eb4c/gpt-6.1-sol

    这 5 条里各 key 的分担（本 lane 内部，不会越界到别的 lane）:
      agentrouter#c7df209111     2 次
      agentrouter#264689afd1     2 次
      agentrouter#271757d18c     2 次
    当前黏性（affinity.json，4 条）:
      会话 01a0eb4c  → 264689afd1               第 2 轮  18:15:27
```

「重试」那列非空就是**本 lane 内部的故障转移在发生**。各 key 的分担让你一眼看出
某个 key 是不是在扛全部流量。日志能到几 MB，所以只读尾部，不整文件进内存。

### status 的状态值

| 状态 | 含义 |
|---|---|
| `UP` | 在监听且能握手 |
| `LISTENING_NOT_CONNECTABLE` | 进程还在，端口在听，但连不上 |
| `DOWN` | 没在听，也没有客户端钉着它 |
| `STRANDED` | **没在听，但有客户端进程还在往它的端口打** |

退出码：`0` 全部正常 · `1` 有该跑的 lane 没跑 · `2` 有 `STRANDED`。
标了 `optional = true` 的 lane 单纯 `DOWN` 不计入退出码（常态停着的自检 lane
用它），但它变成 `STRANDED` 时照样升到 `2` —— 那是真故障，不能被标记藏住。

`STRANDED` 是最值得盯的一个。客户端把网关地址写死在启动参数里
（codex 的 `-c model_providers.custom.base_url=...`），启动后不再重新解析。
网关进程一死，客户端不知道，每个请求都 connection refused，表现为
**对端完全沉默**——看起来像对方忙或卡住，其实上游物理不可达。
magpie 的 failover 在这一层帮不上：它只在进程内部的 key 之间转移，
进程没了就没有转移者。

实测过一次：2026-10-24 15:09:36 magpie 自更新换掉二进制，把四条 lane 一起带走；
两条 42 秒后回来，另两条等了 41 分钟，期间 5 个 codex 进程一直钉在死端口上。
`status` 正是为了让这 41 分钟一眼可见。

## 改配置改哪里

| 想改什么 | 改哪 | 怎么生效 |
|---|---|---|
| 上游地址、key、路由顺序、故障转移链、限额 | `magpie-lanes edit <lane>`（即 `<站点>/magpie/providers.json`） | **最多 1 秒，不用重启** |
| 端口、client 类型、protected、lane 名 | `~/.config/magpie-stations/lanes.toml` | 下次 `up` 生效 |
| 管理台口令 | `<站点>/CREDENTIALS.json` | 下次 `up` 生效 |

`providers.json` 不用重启这件事是从源码读出来的，不是猜的：
`internal/provider/hold.go:183` 的 `heldOf` 第一句是
`if held.holds == 0 { return build() }`（没有活动 hold 时每次都重读文件）；
有 hold 时 `heldFor = time.Second`（`:109`），且 `heldFreshLocked`（`:56`）按
`stampOf(providers.json)` = `(ModTime, Size)`（`:44`）比对，文件一变就清缓存。
仓库里没有 fsnotify（全仓 0 命中），所以靠的是这个「每次读 + 1 秒缓存 + 戳比对」。

`providers.json` 的字段有个坑：**`baseAPI` 存的是 wire 格式名**（`responses` /
`anthropic`），**真 URL 在以那个名字命名的字段里**。实测：

```json
{ "baseAPI": "responses", "responses": "http://127.0.0.1:18765/v1" }
{ "baseAPI": "anthropic", "anthropic": "https://k40.shengqainbang.cn" }
```

`keys` 子命令已经按这个规则解析，`new` 生成的骨架也照这个形状。

## 安全设计

- **`protected = true` 的 lane 拒绝 `down`**，要 `--force`。生产通道标上它。
- **`up` 对已在监听的 lane 是 no-op**，检查在最前面短路，所以 `up --all`
  不可能扰动正在跑的进程。
- **起之前把前置条件查完**：二进制、站点目录、`providers.json`、web 口占用、
  凭据可读。缺任何一项就拒启，不让进程跑起来再 502。
- **凭据只读**。绝不创建、绝不覆盖 `CREDENTIALS.json`。
- **代理必须剥干净**。少了这步，到 `127.0.0.1` 的请求会被交给 shell 里的
  `HTTP(S)_PROXY`，表现为 502 Bad Gateway。实测：不带 `NO_PROXY` 时
  29/29 次采样全连代理、0 次连网关。
- **输出抹密**。管理台 URL 的 `?k=` 和 `sk-` 前缀的串都会被替换掉。
- **不回传 argv**。探测被钉住的客户端时只取匹配到的端口号——codex 的 argv
  里有 bearer token。

## 自检

```sh
contrib/lanes/tests/test_lifecycle.sh
```

只动名为 `sandbox` 的 lane。每一步前后快照所有 `protected` lane 的 pid，
任何变化即 FAIL。**26 项断言**，含 `STRANDED` 检测的正负对照、退出码升级验证，
以及 `keys`/`use`/`open`/`routing`/`edit` 五个只读子命令。
端口与 lane 名都从配置现读，不写死。

## 跟上游的关系

全部文件都在 `contrib/lanes/` 这个新目录下，`internal/` 一行没动。
所以 `git diff upstream/main --stat` 只会显示新增文件，
`git merge upstream/main` 冲突数按构造为零。

验证：

```sh
git diff upstream/main --stat -- ':!contrib'   # 必须为空
```
