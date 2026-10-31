//go:build !windows

package sysutil

// moveProcessToRootCgroup 在非 Windows 平台尝试写入 cgroup v2 根控制文件。
// Android 内核普遍支持 cgroup v2；桌面 Linux 开发环境可能只读或使用 cgroup v1，
// 此时返回描述性错误，由调用方记录日志后继续。
func moveProcessToRootCgroup(pid int, path string) error {
	return moveToRootCgroup(pid, path)
}
