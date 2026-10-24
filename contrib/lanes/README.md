# magpie-lanes —— 并行通道监督器

一条命令管多条 magpie 通道。**magpie 本体零改动**，所以 `git merge upstream/main`
永远干净。

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

## 用

```sh
magpie-lanes status              # 只读。不起、不停、不碰任何进程
magpie-lanes status --json       # 同上，给脚本吃
magpie-lanes up <lane>           # 已在监听则 no-op
magpie-lanes up --all            # 把没在跑的都拉起来，在跑的一个不碰
magpie-lanes up <lane> --dry-run # 只打印会执行什么
magpie-lanes down <lane>         # protected 的要 --force
magpie-lanes logs <lane> -n 60
```

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
任何变化即 FAIL。15 项断言，含 `STRANDED` 检测的正负对照和退出码升级验证。
端口与 lane 名都从配置现读，不写死。

## 跟上游的关系

全部文件都在 `contrib/lanes/` 这个新目录下，`internal/` 一行没动。
所以 `git diff upstream/main --stat` 只会显示新增文件，
`git merge upstream/main` 冲突数按构造为零。

验证：

```sh
git diff upstream/main --stat -- ':!contrib'   # 必须为空
```
