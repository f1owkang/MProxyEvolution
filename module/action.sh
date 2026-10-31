#!/system/bin/sh
# MihomoProxy Evolution 模块操作按钮（KernelSU / Magisk 模块页的「执行」按钮）
# 行为：切换后端与内核的运行状态；启动时阻塞等待内核真正就绪（控制器可达、TUN 状态符合预期），
# 失败时当场输出原因与内核/后端日志尾部，不再只留下一条“已启动”。
# 输出约定：除标题与分隔线外，每行固定为「[标签] 内容」；中英双语跟随安装时选择的语言；
# 面板地址只出现一次，诊断输出里的 token 一律脱敏。

MPROXY_DATA="/data/adb/mproxy"
MPROXY_BIN="$MPROXY_DATA/bin/mproxy"
CORE_BIN="$MPROXY_DATA/bin/mihomo"
STATE_DIR="$MPROXY_DATA/state"
LOG_DIR="$MPROXY_DATA/logs"
CONFIG_FILE="$MPROXY_DATA/config.yaml"

# 模块目录：优先取脚本所在目录，兼容从模块目录内执行（$0 可能是相对路径）。
case "$0" in
  */*) MODDIR="${0%/*}" ;;
  *) MODDIR="." ;;
esac
[ -f "$MODDIR/module.prop" ] || MODDIR="/data/adb/modules/MProxyEvolution"

say() { printf '%s\n' "$*"; }
rule() { say "------------------------------------------------------------"; }
L() { if [ "$T" = en ]; then printf '%s' "$2"; else printf '%s' "$1"; fi; }

# 语言：沿用安装时的选择，没有记录时跟随系统，最后默认中文。
T=zh
oldlang=`cat "$MODDIR/data/lang" 2>/dev/null | tr -d '\r\n'`
case "$oldlang" in
  zh*) T=zh ;;
  en*) T=en ;;
  *)
    for key in persist.sys.locale ro.product.locale persist.sys.language; do
      value=`getprop "$key" 2>/dev/null`
      if [ -n "$value" ]; then
        case "$value" in
          zh*) T=zh ;;
          *) T=en ;;
        esac
        break
      fi
    done
    ;;
esac

module_version() {
  ver=`sed -n 's/^version=//p' "$MODDIR/module.prop" 2>/dev/null | head -n1 | tr -d '\r\n'`
  [ -n "$ver" ] || ver="unknown"
  printf '%s' "$ver"
}

# pid 文件可能残留或被复用：必须同时校验进程存在且 cmdline 匹配。
pid_matches() {
  pid_file="$1"
  match="$2"
  [ -f "$pid_file" ] || return 1
  pid=`cat "$pid_file" 2>/dev/null | tr -d '\r\n'`
  case "$pid" in
    ''|*[!0-9]*) return 1 ;;
  esac
  [ -d "/proc/$pid" ] || return 1
  cmdline=`tr '\0' ' ' < "/proc/$pid/cmdline" 2>/dev/null`
  case "$cmdline" in
    *"$match"*) return 0 ;;
    *) return 1 ;;
  esac
}

backend_running() {
  "$MPROXY_BIN" status >/dev/null 2>&1
}

backend_pid() {
  pid=`cat "$STATE_DIR/backend.pid" 2>/dev/null | tr -d '\r\n'`
  case "$pid" in
    ''|*[!0-9]*) return 1 ;;
  esac
  pid_matches "$STATE_DIR/backend.pid" mproxy || return 1
  printf '%s' "$pid"
}

kernel_line() {
  if [ ! -x "$CORE_BIN" ]; then
    L "未安装" "not installed"
    return
  fi
  if pid_matches "$STATE_DIR/core.pid" mihomo; then
    kernel_ver=`"$CORE_BIN" -v 2>/dev/null | sed -n 's/^Mihomo Meta \([^ ]*\).*/\1/p' | head -n1`
    if [ -n "$kernel_ver" ]; then
      L "$kernel_ver 运行中" "$kernel_ver running"
    else
      L "运行中" "running"
    fi
  else
    L "已安装，未运行" "installed, stopped"
  fi
}

mkdir -p "$STATE_DIR" 2>/dev/null
TMP_OUT="$STATE_DIR/.action.out"
trap 'rm -f "$TMP_OUT" 2>/dev/null' 0

say "MihomoProxy Evolution $(module_version)"
rule

if [ ! -x "$MPROXY_BIN" ]; then
  say "$(L "[错误] 未找到可执行文件：$MPROXY_BIN" "[error] executable not found: $MPROXY_BIN")"
  say "$(L "[提示] 请重新刷入 MihomoProxy Evolution 模块并重启设备。" "[hint] re-flash MihomoProxy Evolution and reboot.")"
  rule
  exit 1
fi

if backend_running; then
  was_running=1
  toggle_target="$(L "运行中 → 停止" "running → stop")"
else
  was_running=0
  toggle_target="$(L "已停止 → 启动" "stopped → start")"
fi
say "$(L "[操作] 切换服务状态：$toggle_target" "[action] toggle service: $toggle_target")"

# action 子命令在启动路径会阻塞等待内核就绪，最坏约 2 分钟；外层 timeout 兜底防止卡死。
if command -v timeout >/dev/null 2>&1; then
  timeout 180 "$MPROXY_BIN" action > "$TMP_OUT" 2>&1
else
  "$MPROXY_BIN" action > "$TMP_OUT" 2>&1
fi
rc=$?
if [ "$rc" -ne 0 ]; then
  say "$(L "[错误] 操作失败（退出码 $rc）" "[error] action failed (exit $rc)")"
  if [ -s "$TMP_OUT" ]; then
    say "$(L "[详情] 最近输出：" "[detail] last output:")"
    sed -e 's/token=[0-9A-Za-z]*/token=***/g' "$TMP_OUT" | tail -n 20 | sed 's/^/  /'
  fi
  say "$(L "[提示] 日志：$LOG_DIR/" "[hint] logs: $LOG_DIR/")"
  rule
  exit "$rc"
fi

if [ "$was_running" -eq 1 ]; then
  say "$(L "[结果] 后端与内核已停止。" "[result] backend and kernel stopped.")"
else
  say "$(L "[结果] 内核已启动并就绪。" "[result] kernel started and ready.")"
fi

# 切换后做一次状态校验，避免退出码为 0 但服务实际未就绪。
if [ "$was_running" -eq 0 ] && ! backend_running; then
  say "$(L "[警告] 后端未按预期启动，请检查日志。" "[warning] backend did not start as expected, check logs.")"
  say "$(L "[提示] 日志：$LOG_DIR/" "[hint] logs: $LOG_DIR/")"
  rule
  exit 1
fi
if [ "$was_running" -eq 1 ] && backend_running; then
  say "$(L "[警告] 后端未按预期停止，请检查日志。" "[warning] backend did not stop as expected, check logs.")"
  say "$(L "[提示] 日志：$LOG_DIR/" "[hint] logs: $LOG_DIR/")"
  rule
  exit 1
fi

kernel="$(kernel_line)"
if [ "$was_running" -eq 0 ]; then
  pid="$(backend_pid)"
  if [ -n "$pid" ]; then
    say "$(L "[状态] 后端运行中（pid $pid）" "[status] backend running (pid $pid)")"
  else
    say "$(L "[状态] 后端运行中" "[status] backend running")"
  fi
  say "$(L "[内核] $kernel" "[kernel] $kernel")"
  if [ -f "$CONFIG_FILE" ]; then
    say "$(L "[配置] $CONFIG_FILE" "[config] $CONFIG_FILE")"
  else
    say "$(L "[配置] 尚未生成（首次启动时创建）" "[config] not created yet (created on first start)")"
  fi
  panel_url=`"$MPROXY_BIN" url 2>/dev/null | head -n1`
  if [ -n "$panel_url" ]; then
    say "$(L "[面板] $panel_url" "[panel] $panel_url")"
  fi
  say "$(L "[提示] 面板地址含访问令牌，请勿外泄；日志：$LOG_DIR/" "[hint] the panel URL contains the access token, keep it private; logs: $LOG_DIR/")"
else
  say "$(L "[状态] 后端未运行" "[status] backend not running")"
  say "$(L "[内核] $kernel" "[kernel] $kernel")"
  say "$(L "[提示] 再次点击「执行」可重新启动；打开 WebUI 请使用模块页「打开」按钮。" "[hint] tap Execute again to start; open the WebUI from the module page's Open button.")"
fi

rule
exit 0
