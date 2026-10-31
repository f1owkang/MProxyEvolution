package sysutil

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// ReadPID 读取 pid 文件，不存在或内容非法时返回错误。
func ReadPID(path string) (int, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || pid <= 0 {
		return 0, fmt.Errorf("pid 文件内容非法: %s", path)
	}
	return pid, nil
}

// WritePIDFile 语义化别名：原子写入 pid 文件（内容为十进制 pid + 换行）。
func WritePIDFile(path string, pid int) error { return WritePID(path, pid) }

// RemovePIDIfMatch 比较后删除 pid 文件：只有文件内容确实等于 pid 时才删除，
// 避免后端/内核误删别的进程写下的 pid 文件（例如 pid 被复用后的新进程）。
// 返回是否执行了删除。
func RemovePIDIfMatch(path string, pid int) bool {
	if pid <= 0 {
		return false
	}
	cur, err := ReadPID(path)
	if err != nil || cur != pid {
		return false
	}
	if err := os.Remove(path); err != nil {
		return os.IsNotExist(err)
	}
	return true
}

// WritePID 原子写入 pid 文件。
func WritePID(path string, pid int) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(strconv.Itoa(pid)+"\n"), 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// RemovePID 删除 pid 文件（忽略不存在）。
func RemovePID(path string) {
	_ = os.Remove(path)
}

// StartDetached 以脱离当前进程组的方式启动命令，并把输出写入日志文件。
func StartDetached(cmd *exec.Cmd, logPath string) error {
	if logPath != "" {
		f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			return err
		}
		cmd.Stdout = f
		cmd.Stderr = f
		// 子进程继承 fd，父进程关闭自己的副本即可
		defer f.Close()
	}
	SetDetached(cmd)
	if err := cmd.Start(); err != nil {
		return err
	}
	return nil
}

// StopPID 先尝试优雅结束进程，超时后强制杀死；进程不存在时返回 nil。
func StopPID(pid int, timeout time.Duration) error {
	if pid <= 0 {
		return nil
	}
	if !IsProcessAlive(pid) {
		return nil
	}
	_ = Terminate(pid, false)
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !IsProcessAlive(pid) {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err := Terminate(pid, true); err != nil {
		return fmt.Errorf("结束进程 %d 失败: %w", pid, err)
	}
	for i := 0; i < 20; i++ {
		if !IsProcessAlive(pid) {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("进程 %d 在强制结束后仍未退出", pid)
}

// ErrNotRunning 表示进程未运行。
var ErrNotRunning = errors.New("not running")

// ErrNotSupported 表示当前平台不支持该操作。
var ErrNotSupported = errors.New("当前平台不支持该操作")

// ProcessMatches 判断 pid 对应进程的命令行是否包含 needle（不区分大小写）。
// 用于在接管/结束进程前确认身份，避免仅凭 pid 就误杀被复用的 pid。
// Windows 无法读取 /proc，拿不到命令行时退化为“进程是否存活”（保持原有行为）；
// 其他读取失败（例如进程已退出）返回 false。
func ProcessMatches(pid int, needle string) bool {
	if pid <= 0 {
		return false
	}
	cmdline, err := ProcessCmdline(pid)
	if err != nil {
		if errors.Is(err, ErrNotSupported) {
			return IsProcessAlive(pid)
		}
		return false
	}
	if needle == "" {
		return IsProcessAlive(pid)
	}
	return strings.Contains(strings.ToLower(cmdline), strings.ToLower(needle))
}
