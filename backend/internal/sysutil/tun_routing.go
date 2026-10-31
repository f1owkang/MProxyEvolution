package sysutil

import (
	"context"
	"strings"
	"time"
)

// mihomo/sing-tun 在 Android 上默认使用策略路由表 2022、规则优先级 9000/9001/9010。
// 上一次异常退出（或内核启动到一半失败）可能留下规则/路由；新内核再添加路由时可能
// 报 "add route ...: network is unreachable" 或 "file exists"。参考 akashaProxy /
// tunclash 的做法，启动前清理这批已知的 mihomo 默认状态。
var tunRuleCleanupPriorities = []string{"9000", "9001", "9010"}

const tunRouteTable = "2022"

// CleanupTUNRouting 清理 mihomo 默认策略路由规则与路由表；仅 Android、尽力而为。
// 规则不存在时 ip rule del 必然报错，属正常情况；只有 route flush 失败才返回错误。
func CleanupTUNRouting() (bool, error) {
	if !IsAndroid() {
		return false, nil
	}
	for _, pref := range tunRuleCleanupPriorities {
		_, _ = runCommand(context.Background(), 3*time.Second, ipBinary(), "rule", "del", "pref", pref)
	}
	if _, err := runCommand(context.Background(), 3*time.Second, ipBinary(), "route", "flush", "table", tunRouteTable); err != nil {
		return true, err
	}
	return true, nil
}

// TUNRoutingSnapshot 采集策略路由/网卡快照，TUN 启动失败时写入日志，便于与参考
// 模块的 ip rule / ip route 输出对照排查。输出做长度限制，避免日志膨胀。
func TUNRoutingSnapshot() string {
	if !IsAndroid() {
		return "（非 Android，无策略路由快照）"
	}
	var b strings.Builder
	for _, args := range [][]string{
		{"rule", "show"},
		{"route", "show", "table", "all"},
		{"link", "show"},
	} {
		out, err := runCommand(context.Background(), 3*time.Second, ipBinary(), args...)
		out = strings.TrimSpace(out)
		if len(out) > 2000 {
			out = out[:2000] + "…（截断）"
		}
		b.WriteString("ip ")
		b.WriteString(strings.Join(args, " "))
		b.WriteString(":\n")
		if err != nil {
			b.WriteString("  (执行失败: " + err.Error() + ")\n")
			continue
		}
		if out == "" {
			b.WriteString("  (空)\n")
			continue
		}
		b.WriteString(out)
		b.WriteString("\n")
	}
	return b.String()
}
