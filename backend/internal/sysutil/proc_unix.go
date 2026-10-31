//go:build !windows

package sysutil

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

// SetDetached 让子进程脱离当前会话（用于常驻 serve/内核进程）。
func SetDetached(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}

// ProcessCmdline 读取 /proc/<pid>/cmdline，并把 NUL 分隔的参数用空格连接。
// 用于进程身份校验（pid 文件可能被复用）。
func ProcessCmdline(pid int) (string, error) {
	if pid <= 0 {
		return "", fmt.Errorf("非法 pid: %d", pid)
	}
	raw, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/cmdline")
	if err != nil {
		return "", err
	}
	return strings.ReplaceAll(string(raw), "\x00", " "), nil
}

// InstallCandidatePath 返回新内核安装前的候选路径（unix 直接追加 .new）。
func InstallCandidatePath(target string) string { return target + ".new" }

// IsProcessAlive 判断 pid 对应进程是否存活。
func IsProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}

// Terminate 向进程发送 SIGTERM；force 时发送 SIGKILL。
func Terminate(pid int, force bool) error {
	sig := syscall.SIGTERM
	if force {
		sig = syscall.SIGKILL
	}
	err := syscall.Kill(pid, sig)
	if err == syscall.ESRCH {
		return nil
	}
	return err
}
