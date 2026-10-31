#!/system/bin/sh
# MProxyEvolution 开机自启（Magisk late_start service / KernelSU service）
# 必须等待 sys.boot_completed=1 之后再拉起后端，否则 TUN / 网络栈尚未就绪。

MPROXY_DATA="/data/adb/mproxy"
MPROXY_BIN="$MPROXY_DATA/bin/mproxy"
LOG_FILE="$MPROXY_DATA/logs/service.log"

mkdir -p "$MPROXY_DATA/logs" "$MPROXY_DATA/state" 2>/dev/null

# 日志轮转：service.log 超过 1 MiB 时只保留最后 2000 行，避免长期开机累积占满空间。
MAX_LOG_BYTES=1048576
if [ -f "$LOG_FILE" ]; then
  log_size="$(wc -c < "$LOG_FILE" 2>/dev/null | tr -d ' ')"
  case "$log_size" in
    ''|*[!0-9]*) log_size=0 ;;
  esac
  if [ "$log_size" -gt "$MAX_LOG_BYTES" ]; then
    if tail -n 2000 "$LOG_FILE" > "$LOG_FILE.tmp" 2>/dev/null; then
      mv -f "$LOG_FILE.tmp" "$LOG_FILE" 2>/dev/null || rm -f "$LOG_FILE.tmp" 2>/dev/null
      echo "[$(date '+%Y-%m-%d %H:%M:%S')] service.log 超过 1 MiB，已截断保留最后 2000 行" >> "$LOG_FILE" 2>/dev/null
    fi
  fi
fi

log() {
  echo "[$(date '+%Y-%m-%d %H:%M:%S')] $*" >> "$LOG_FILE" 2>/dev/null
}

# 等待系统启动完成（最多约 120 秒）
tries=0
while [ "$(getprop sys.boot_completed 2>/dev/null)" != "1" ]; do
  tries=$((tries + 1))
  if [ "$tries" -ge 60 ]; then
    log "等待 sys.boot_completed=1 超时（120 秒），仍尝试启动 MProxyEvolution"
    break
  fi
  sleep 2
done

if [ ! -x "$MPROXY_BIN" ]; then
  log "未找到可执行的 $MPROXY_BIN，跳过开机启动"
  exit 0
fi

log "系统启动完成，执行 mproxy boot --boot"

# boot 会输出带 token 的面板地址；先落到 state/（0700）再脱敏后追加到 service.log，
# 避免 token 明文进入日志。退出码仍按 boot 命令本身记录。
BOOT_TMP="$MPROXY_DATA/state/.boot.out"
if command -v timeout >/dev/null 2>&1; then
  timeout 180 "$MPROXY_BIN" boot --boot > "$BOOT_TMP" 2>&1
else
  "$MPROXY_BIN" boot --boot > "$BOOT_TMP" 2>&1
fi
rc=$?
if [ -f "$BOOT_TMP" ]; then
  chmod 0600 "$BOOT_TMP" 2>/dev/null
  sed -e 's/token=[0-9A-Za-z]*/token=***/g' "$BOOT_TMP" >> "$LOG_FILE" 2>/dev/null
  rm -f "$BOOT_TMP" 2>/dev/null
fi
log "mproxy boot --boot 退出码：$rc"

exit 0
