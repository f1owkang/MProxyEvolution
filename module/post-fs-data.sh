#!/system/bin/sh
# MProxyEvolution post-fs-data：尽早准备数据目录与 TUN 设备节点。
# 部分内核/设备只会暴露 /dev/tun，mihomo 默认使用 /dev/net/tun，这里做兼容软链。

MPROXY_DATA="/data/adb/mproxy"

mkdir -p "$MPROXY_DATA/bin" \
         "$MPROXY_DATA/state" \
         "$MPROXY_DATA/logs" \
         "$MPROXY_DATA/cache" \
         "$MPROXY_DATA/providers" \
         "$MPROXY_DATA/ruleset" 2>/dev/null

chmod 0755 "$MPROXY_DATA" 2>/dev/null
chmod 0755 "$MPROXY_DATA/bin" "$MPROXY_DATA/logs" "$MPROXY_DATA/cache" \
           "$MPROXY_DATA/providers" "$MPROXY_DATA/ruleset" 2>/dev/null
chmod 0700 "$MPROXY_DATA/state" 2>/dev/null

# 开启 IPv4 转发（TUN 模式需要；后端启动时也会再设置一次）
echo 1 > /proc/sys/net/ipv4/ip_forward 2>/dev/null

# 开启 IPv6 转发（x-mproxy.ipv6=true 时 TUN 需要；尽力而为，失败不阻断开机）
echo 1 > /proc/sys/net/ipv6/conf/all/forwarding 2>/dev/null || true
echo 1 > /proc/sys/net/ipv6/conf/default/forwarding 2>/dev/null || true

# /dev/net/tun 兼容处理：部分内核只暴露 /dev/tun。
mkdir -p /dev/net 2>/dev/null
# 先清理指向不存在目标的旧软链（[ -e ] 对 dangling symlink 为假，必须先按 -L 判断再删除）。
if [ -L /dev/net/tun ] && [ ! -e /dev/net/tun ]; then
  rm -f /dev/net/tun 2>/dev/null
fi
# /dev/tun 存在且 /dev/net/tun 缺失（或刚被清理）时创建软链；已存在的真实节点不覆盖。
if [ -e /dev/tun ] && [ ! -e /dev/net/tun ] && [ ! -L /dev/net/tun ]; then
  ln -s /dev/tun /dev/net/tun 2>/dev/null
fi
# 设备节点存在时放宽权限，保证 root 启动的内核可打开
chmod 0600 /dev/net/tun 2>/dev/null

exit 0
