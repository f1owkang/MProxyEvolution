package sysutil

import (
	"fmt"
	"os"
)

// rootCgroupProcsPath 是 Linux cgroup v2 的根 cgroup 控制文件：写入进程 pid 即可把
// 该进程从管理器的子 cgroup 移回根 cgroup。Android 上 root 管理器（KernelSU/Magisk）
// 切后台或清理时会连带杀掉自己 cgroup 里的进程，移出后内核/后端才能长期存活。
const rootCgroupProcsPath = "/sys/fs/cgroup/cgroup.procs"

// MoveProcessToRootCgroup 把进程移入根 cgroup（cgroup v2）。Android/Linux 上写入
// /sys/fs/cgroup/cgroup.procs；不支持该能力的平台返回 ErrNotSupported，调用方只记录
// 日志、绝不因此让启动失败。失败信息包含路径与原因，便于真机排障。
func MoveProcessToRootCgroup(pid int) error {
	return moveProcessToRootCgroup(pid, rootCgroupProcsPath)
}

// moveToRootCgroup 是平台无关的写入实现，路径可注入以便测试。
// 注意：cgroup.procs 必须追加写入，绝不能截断（O_TRUNC 会破坏 cgroup 文件语义）。
func moveToRootCgroup(pid int, path string) error {
	if pid <= 0 {
		return fmt.Errorf("非法 pid: %d", pid)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		return fmt.Errorf("打开 %s 失败（cgroup v2 不可用或权限不足）: %w", path, err)
	}
	defer f.Close()
	if _, err := fmt.Fprintf(f, "%d\n", pid); err != nil {
		return fmt.Errorf("写入 %s 失败: %w", path, err)
	}
	return nil
}
