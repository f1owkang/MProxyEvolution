package sysutil

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestMoveToRootCgroupWritesPID(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cgroup.procs")
	// 模拟真实 cgroup.procs（内核保证该文件存在）；实现本身不做 O_CREATE，
	// 以免在不支持 cgroup v2 的 /sys 上创建普通文件。
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatalf("准备临时 cgroup.procs 失败: %v", err)
	}
	if err := moveToRootCgroup(4321, path); err != nil {
		t.Fatalf("写入 pid 失败: %v", err)
	}
	// 第二次必须追加而不是截断（cgroup.procs 不支持截断语义）。
	if err := moveToRootCgroup(8765, path); err != nil {
		t.Fatalf("追加写入 pid 失败: %v", err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取 cgroup.procs 失败: %v", err)
	}
	fields := strings.Fields(string(b))
	if len(fields) != 2 || fields[0] != "4321" || fields[1] != "8765" {
		t.Fatalf("cgroup.procs 内容错误: %q", string(b))
	}
}

func TestMoveToRootCgroupInvalidPID(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cgroup.procs")
	if err := moveToRootCgroup(0, path); err == nil {
		t.Fatal("非法 pid 应返回错误")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("非法 pid 不应创建文件: %v", err)
	}
}

func TestMoveToRootCgroupMissingPath(t *testing.T) {
	// 路径不存在（cgroup v1 或未挂载）时必须返回带路径的明确错误，供调用方记录日志。
	path := filepath.Join(t.TempDir(), "missing", "cgroup.procs")
	err := moveToRootCgroup(1234, path)
	if err == nil {
		t.Fatal("路径不存在时应返回错误")
	}
	if !strings.Contains(err.Error(), "cgroup.procs") {
		t.Fatalf("错误信息应包含路径: %v", err)
	}
}

func TestMoveProcessToRootCgroupPlatform(t *testing.T) {
	if runtime.GOOS == "windows" {
		if err := MoveProcessToRootCgroup(os.Getpid()); !errors.Is(err, ErrNotSupported) {
			t.Fatalf("Windows 应返回 ErrNotSupported: %v", err)
		}
		return
	}
	// 非 Windows 只验证参数防御；不写真实 /sys（开发机可能只读或使用 cgroup v1）。
	if err := MoveProcessToRootCgroup(0); err == nil {
		t.Fatal("非法 pid 应返回错误")
	}
}
