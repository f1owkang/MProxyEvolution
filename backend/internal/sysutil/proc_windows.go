//go:build windows

package sysutil

import (
	"os/exec"
	"syscall"
	"unsafe"
)

var (
	kernel32               = syscall.NewLazyDLL("kernel32.dll")
	procOpenProcess        = kernel32.NewProc("OpenProcess")
	procGetExitCodeProcess = kernel32.NewProc("GetExitCodeProcess")
	procCloseHandle        = kernel32.NewProc("CloseHandle")
	procTerminateProcess   = kernel32.NewProc("TerminateProcess")
)

const (
	processQueryLimitedInformation = 0x1000
	processTerminate               = 0x0001
	stillActive                    = 259

	createNewProcessGroup = 0x00000200
	detachedProcess       = 0x00000008
)

// SetDetached 让子进程脱离当前控制台（Windows 无 setsid，用 DETACHED_PROCESS）。
func SetDetached(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: createNewProcessGroup | detachedProcess,
	}
}

// ProcessCmdline Windows 无法从 /proc 读取进程命令行，返回 ErrNotSupported，
// 由 ProcessMatches 退化为进程存活检查。
func ProcessCmdline(pid int) (string, error) {
	return "", ErrNotSupported
}

// InstallCandidatePath 返回新内核安装前的候选路径。
// Windows 只有 .exe 后缀的文件才能被 CreateProcess 直接执行，因此候选文件名也带 .exe，
// 便于安装前运行 `<候选> -v` 校验版本。
func InstallCandidatePath(target string) string { return target + ".new.exe" }

func openProcess(pid int, access uint32) (syscall.Handle, error) {
	r, _, err := procOpenProcess.Call(uintptr(access), 0, uintptr(pid))
	if r == 0 {
		return 0, err
	}
	return syscall.Handle(r), nil
}

// IsProcessAlive 判断 pid 对应进程是否存活。
func IsProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	h, err := openProcess(pid, processQueryLimitedInformation)
	if err != nil {
		return false
	}
	defer procCloseHandle.Call(uintptr(h))
	var code uint32
	r, _, _ := procGetExitCodeProcess.Call(uintptr(h), uintptr(unsafe.Pointer(&code)))
	if r == 0 {
		return false
	}
	return code == stillActive
}

// Terminate 结束进程；Windows 没有可用的 SIGTERM，忽略 force 直接 TerminateProcess。
func Terminate(pid int, force bool) error {
	h, err := openProcess(pid, processTerminate)
	if err != nil {
		if !force {
			return nil
		}
		return err
	}
	defer procCloseHandle.Call(uintptr(h))
	r, _, err := procTerminateProcess.Call(uintptr(h), 1)
	if r == 0 {
		return err
	}
	return nil
}
