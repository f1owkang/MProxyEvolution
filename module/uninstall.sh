#!/system/bin/sh
# MProxyEvolution 卸载脚本：停止所有进程，保留 /data/adb/mproxy 数据目录。
# 用户配置、订阅与规则集不会随模块卸载被删除。

MPROXY_DATA="/data/adb/mproxy"
MPROXY_BIN="$MPROXY_DATA/bin/mproxy"

echo "- 正在停止 MProxyEvolution…"

if [ -x "$MPROXY_BIN" ]; then
  "$MPROXY_BIN" stop >/dev/null 2>&1 || true
fi

# 兜底：按 pid 文件结束残留进程。
# pid 文件可能被复用/残留，先核对 /proc/<pid>/cmdline 确认是本模块的 mproxy/mihomo 再 kill。
for pid_file in "$MPROXY_DATA"/state/*.pid; do
  [ -f "$pid_file" ] || continue
  pid="$(cat "$pid_file" 2>/dev/null)"
  case "$pid" in
    ''|*[!0-9]*) continue ;;
  esac
  cmdline="$(tr '\0' ' ' < "/proc/$pid/cmdline" 2>/dev/null)"
  case "$cmdline" in
    *mproxy*|*mihomo*)
      kill "$pid" 2>/dev/null || true
      ;;
    *)
      echo "- 跳过非 MProxyEvolution 进程 PID $pid（$(basename "$pid_file")）"
      ;;
  esac
done

sleep 1

# 兜底：结束仍占用本模块二进制的进程（只匹配本模块路径，避免误杀）
pkill -f "$MPROXY_DATA/bin/mproxy serve" 2>/dev/null || true
pkill -f "$MPROXY_DATA/bin/mihomo " 2>/dev/null || true
sleep 1

echo "- MProxyEvolution 已停止。"
echo "- 数据目录已保留：$MPROXY_DATA"
echo "  如需彻底清理（会删除全部配置与订阅），请手动执行："
echo "    rm -rf $MPROXY_DATA"

exit 0
