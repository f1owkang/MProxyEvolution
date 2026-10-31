//go:build linux

package sysutil

import (
	"os"
	"os/exec"
	"syscall"
)

// aidNetAdmin 是 Android 的 AID_NET_ADMIN（AOSP 固定值）。
// akashaProxy / box4magisk 都以 root:net_admin 启动 mihomo；Android 17 上部分网络
// 操作（TUN 建链、策略路由）在缺少该补充组时会失败，因此在 Linux/Android 上为
// mihomo 子进程补充 net_admin 组。
const aidNetAdmin = 3005

// ApplyCoreNetAdmin 给内核子进程补充 net_admin 组；非 Android（桌面 Linux）不处理，
// 避免在没有该组的桌面环境里 setgroups 失败。
func ApplyCoreNetAdmin(cmd *exec.Cmd) {
	if !IsAndroid() || os.Geteuid() != 0 {
		return
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Credential = &syscall.Credential{
		Uid:    0,
		Gid:    0,
		Groups: []uint32{aidNetAdmin},
	}
}
