package sysutil

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestNormalizeDNSMode(t *testing.T) {
	cases := map[string]string{
		"":                "",
		"off":             "",
		"OFF":             "",
		"null":            "",
		" opportunistic ": "opportunistic",
		"hostname":        "hostname",
		"Hostname":        "hostname",
	}
	for input, want := range cases {
		if got := normalizeDNSMode(input); got != want {
			t.Fatalf("normalizeDNSMode(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestDisablePrivateDNSWithFake(t *testing.T) {
	stateFile := filepath.Join(t.TempDir(), "private_dns")
	var putModes []string
	put := func(_ context.Context, mode string) error {
		putModes = append(putModes, mode)
		return nil
	}
	mode := "opportunistic"
	get := func(context.Context) (string, error) { return mode, nil }

	disabled, err := disablePrivateDNS(context.Background(), stateFile, get, put)
	if err != nil || !disabled {
		t.Fatalf("开启中的 Private DNS 应被关闭: disabled=%v err=%v", disabled, err)
	}
	b, err := os.ReadFile(stateFile)
	if err != nil || strings.TrimSpace(string(b)) != "opportunistic" {
		t.Fatalf("应保存原模式: %q err=%v", string(b), err)
	}
	if len(putModes) != 1 || putModes[0] != "off" {
		t.Fatalf("应写入 off: %v", putModes)
	}
	// 状态文件权限应为 0600（Windows 上忽略权限位）。
	if st, serr := os.Stat(stateFile); serr != nil {
		t.Fatalf("状态文件应存在: %v", serr)
	} else if runtime.GOOS != "windows" && st.Mode().Perm()&0o077 != 0 {
		t.Fatalf("状态文件权限应为 0600，实际 %v", st.Mode().Perm())
	}

	// 设备已是 off：不再写入、不覆盖已保存的原模式。
	mode = "off"
	disabled, err = disablePrivateDNS(context.Background(), stateFile, get, put)
	if err != nil || disabled {
		t.Fatalf("已关闭时不应重复处理: disabled=%v err=%v", disabled, err)
	}
	if len(putModes) != 1 {
		t.Fatalf("已关闭时不应再次 put: %v", putModes)
	}

	// 状态文件已存在、用户又手动开启 hostname：必须沿用第一次保存的原模式，
	// 不能把 hostname 写成新的“原值”。
	mode = "hostname"
	disabled, err = disablePrivateDNS(context.Background(), stateFile, get, put)
	if err != nil || !disabled {
		t.Fatalf("重新开启后应再次关闭: disabled=%v err=%v", disabled, err)
	}
	if len(putModes) != 2 || putModes[1] != "off" {
		t.Fatalf("应再次写入 off: %v", putModes)
	}
	b, _ = os.ReadFile(stateFile)
	if strings.TrimSpace(string(b)) != "opportunistic" {
		t.Fatalf("不应覆盖已保存的原模式: %q", string(b))
	}
}

func TestDisablePrivateDNSRollbackOnPutFailure(t *testing.T) {
	stateFile := filepath.Join(t.TempDir(), "private_dns")
	get := func(context.Context) (string, error) { return "hostname", nil }
	put := func(context.Context, string) error { return errors.New("settings 不可用") }
	if _, err := disablePrivateDNS(context.Background(), stateFile, get, put); err == nil {
		t.Fatal("settings put 失败时应返回错误")
	}
	if _, err := os.Stat(stateFile); !os.IsNotExist(err) {
		t.Fatalf("写入失败时应回滚本次新建的状态文件: %v", err)
	}
}

func TestRestorePrivateDNSWithFake(t *testing.T) {
	stateFile := filepath.Join(t.TempDir(), "private_dns")
	var putModes []string
	put := func(_ context.Context, mode string) error {
		putModes = append(putModes, mode)
		return nil
	}

	// 状态文件不存在：安全空操作（幂等）。
	if err := restorePrivateDNS(context.Background(), stateFile, put); err != nil {
		t.Fatalf("无状态文件时应空操作: %v", err)
	}
	if len(putModes) != 0 {
		t.Fatalf("无状态文件时不应写入: %v", putModes)
	}

	if err := writeDNSStateFile(stateFile, "opportunistic"); err != nil {
		t.Fatalf("写状态文件失败: %v", err)
	}
	if err := restorePrivateDNS(context.Background(), stateFile, put); err != nil {
		t.Fatalf("恢复失败: %v", err)
	}
	if len(putModes) != 1 || putModes[0] != "opportunistic" {
		t.Fatalf("应恢复原模式: %v", putModes)
	}
	if _, err := os.Stat(stateFile); !os.IsNotExist(err) {
		t.Fatalf("恢复后应删除状态文件: %v", err)
	}
	// 再次恢复：幂等，不应再写入。
	if err := restorePrivateDNS(context.Background(), stateFile, put); err != nil {
		t.Fatalf("重复恢复应空操作: %v", err)
	}
	if len(putModes) != 1 {
		t.Fatalf("重复恢复不应再次写入: %v", putModes)
	}
}

func TestPrivateDNSNonAndroid(t *testing.T) {
	if IsAndroid() {
		t.Skip("Android 设备上真实 settings 行为需真机验证")
	}
	if _, err := DNSMode(context.Background()); !errors.Is(err, ErrNotSupported) {
		t.Fatalf("非 Android 的 DNSMode 应返回 ErrNotSupported: %v", err)
	}
	stateFile := filepath.Join(t.TempDir(), "private_dns")
	if disabled, err := DisablePrivateDNS(stateFile); err != nil || disabled {
		t.Fatalf("非 Android 的 DisablePrivateDNS 应安全空操作: disabled=%v err=%v", disabled, err)
	}
	if err := RestorePrivateDNS(stateFile); err != nil {
		t.Fatalf("非 Android 的 RestorePrivateDNS 应安全空操作: %v", err)
	}
	if mode := PrivateDNSMode(); mode != "" {
		t.Fatalf("非 Android 的 PrivateDNSMode 应返回空串: %q", mode)
	}
}
