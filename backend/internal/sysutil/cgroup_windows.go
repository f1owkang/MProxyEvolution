//go:build windows

package sysutil

// moveProcessToRootCgroup Windows 没有 cgroup，明确返回 ErrNotSupported，
// 调用方据此静默跳过（不当作故障告警）。
func moveProcessToRootCgroup(pid int, path string) error {
	return ErrNotSupported
}
