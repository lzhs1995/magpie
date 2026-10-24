#!/bin/zsh
# magpie-lanes 全周期自检。只动名为 sandbox 的那条 lane。
#
# 生产 lane 一个都不碰：每一步前后都快照四条生产网关的 pid，任何变化即 FAIL。
# 用法: tests/test_lifecycle.sh
set -uo pipefail

HERE=${0:A:h}
ROOT=${HERE:h}
CLI="$ROOT/magpie-lanes"
PY=${MAGPIE_LANES_PYTHON:-python3}
PASS=0; FAIL=0

# 受保护的 lane 和它们的端口从配置里现读，不写死 —— 换一台机器也能跑。
PROD_PORTS=(${(f)"$("$CLI" status --json 2>/dev/null | $PY -c '
import json,sys
for r in json.load(sys.stdin):
    if r["protected"]:
        print(r["gateway_port"])
')"})
PROTECTED_LANE=$("$CLI" status --json 2>/dev/null | $PY -c '
import json,sys
for r in json.load(sys.stdin):
    if r["protected"]:
        print(r["lane"]); break
')
if [[ -z "$PROTECTED_LANE" ]]; then
  print -- "配置里没有 protected lane —— 第 3 项无法验证。"
  print -- "给至少一条生产 lane 标上 protected = true 再跑。"
  exit 2
fi

say() { print -- "$@" }
ok()   { PASS=$((PASS+1)); say "  PASS  $1" }
bad()  { FAIL=$((FAIL+1)); say "  FAIL  $1" }

prod_snapshot() {
  local out="" p pid
  for p in $PROD_PORTS; do
    pid=$(lsof -nP -a -iTCP:$p -sTCP:LISTEN 2>/dev/null | awk 'NR==2{print $2}')
    out+="$p=${pid:-NONE} "
  done
  print -- "$out"
}

lane_state() {
  "$CLI" status --json 2>/dev/null | $PY -c "
import json,sys
want=sys.argv[1]
for r in json.load(sys.stdin):
    if r['lane']==want:
        print(r['state']); break
else:
    print('MISSING')
" "$1"
}

SANDBOX_PORT=$("$CLI" status --json 2>/dev/null | $PY -c '
import json,sys
for r in json.load(sys.stdin):
    if r["lane"]=="sandbox":
        print(r["gateway_port"]); break
')
if [[ -z "$SANDBOX_PORT" ]]; then
  print -- "配置里没有名为 sandbox 的 lane —— 本自检只动它，没有就跑不了。"
  exit 2
fi

BASE=$(prod_snapshot)
say "生产基线: $BASE"
say "sandbox 网关口: $SANDBOX_PORT  / protected lane 样本: $PROTECTED_LANE"
say ""

# ---- 1. 起 sandbox ----
say "1. up sandbox"
"$CLI" up sandbox >/dev/null 2>&1
[[ "$(lane_state sandbox)" == "UP" ]] && ok "sandbox 变成 UP" || bad "sandbox 没到 UP（实为 $(lane_state sandbox)）"
[[ "$(prod_snapshot)" == "$BASE" ]] && ok "生产 pid 未变" || bad "生产 pid 变了: $(prod_snapshot)"

# ---- 2. 已在跑时 up 必须 no-op ----
say "2. up sandbox 再来一次（必须 no-op）"
before=$(lsof -nP -a -iTCP:$SANDBOX_PORT -sTCP:LISTEN 2>/dev/null | awk 'NR==2{print $2}')
out=$("$CLI" up sandbox 2>&1)
after=$(lsof -nP -a -iTCP:$SANDBOX_PORT -sTCP:LISTEN 2>/dev/null | awk 'NR==2{print $2}')
[[ "$before" == "$after" && -n "$before" ]] && ok "pid 不变（$before）" || bad "pid 变了 $before -> $after"
[[ "$out" == *"不动它"* ]] && ok "明确报了 no-op" || bad "没报 no-op: $out"

# ---- 3. protected lane 拒绝 down ----
say "3. down 一条 protected 生产 lane（必须拒绝）: $PROTECTED_LANE"
out=$("$CLI" down "$PROTECTED_LANE" 2>&1); rc=$?
[[ $rc -ne 0 ]] && ok "返回码非零（$rc）" || bad "居然返回 0"
[[ "$out" == *"protected"* ]] && ok "报了 protected" || bad "没报 protected: $out"
[[ "$(prod_snapshot)" == "$BASE" ]] && ok "生产 pid 未变" || bad "生产 pid 变了: $(prod_snapshot)"

# ---- 4. STRANDED：网关死了但客户端还钉着 ----
say "4. STRANDED 检测"
nohup $PY -c 'import time; time.sleep(120)' --pinned-to=http://127.0.0.1:$SANDBOX_PORT/v1 >/dev/null 2>&1 &
FAKE=$!
sleep 1
if ps -p $FAKE >/dev/null 2>&1; then
  ok "假客户端活着（pid=$FAKE，正对照）"
  "$CLI" down sandbox >/dev/null 2>&1
  st=$(lane_state sandbox)
  [[ "$st" == "STRANDED" ]] && ok "报 STRANDED" || bad "应为 STRANDED，实为 $st"
  # 先接住输出再匹配。直接管道给 grep 会在 pipefail 下取 status 的退出码，
  # 而 STRANDED 时它故意返回 2 —— 那样匹配成功也会被判失败。
  sout=$("$CLI" status 2>&1)
  [[ "$sout" == *"拉回来"* ]] && ok "给出了修复命令" || bad "没给修复命令"
  # optional 的 lane 单纯 DOWN 不计入退出码，但 STRANDED 必须照样升级到 2，
  # 否则这个标记会把「客户端钉在死网关上」这种真故障藏起来。
  # 注意：别用管道取退出码 —— pipefail 下拿到的是管道末端那个命令的。
  "$CLI" status >/dev/null 2>&1; rc=$?
  [[ $rc -eq 2 ]] && ok "STRANDED 时退出码升到 2（optional 没藏住它）" \
                  || bad "STRANDED 时退出码应为 2，实为 $rc"

  kill $FAKE 2>/dev/null
  sleep 1
  st=$(lane_state sandbox)
  [[ "$st" == "DOWN" ]] && ok "客户端走后降为 DOWN（负对照）" || bad "应为 DOWN，实为 $st"
  "$CLI" status >/dev/null 2>&1; rc=$?
  [[ $rc -eq 0 ]] && ok "optional 且仅 DOWN 时退出码回 0" || bad "应为 0，实为 $rc"
else
  bad "假客户端没活下来 —— 本项无效，不计为通过"
fi

# ---- 5. 收尾 ----
say "5. 收尾"
"$CLI" down sandbox >/dev/null 2>&1
[[ "$(lane_state sandbox)" == "DOWN" ]] && ok "sandbox 已停" || bad "sandbox 还在: $(lane_state sandbox)"
[[ "$(prod_snapshot)" == "$BASE" ]] && ok "全程生产 pid 未变" || bad "生产 pid 变了: $(prod_snapshot)"

say ""
say "PASS=$PASS FAIL=$FAIL"
[[ $FAIL -eq 0 ]] || exit 1
