package sysutil

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestValidTUNDeviceName(t *testing.T) {
	valid := []string{
		"Meta",
		"meta0",
		"tun_0",
		"a.b",
		"a-b",
		strings.Repeat("a", 15),
	}
	for _, name := range valid {
		if !ValidTUNDeviceName(name) {
			t.Errorf("设备名 %q 应合法", name)
		}
	}
	invalid := []string{
		"",
		" ",
		"a b",
		"a/b",
		"tun@0",
		"设备",
		strings.Repeat("a", 16),
	}
	for _, name := range invalid {
		if ValidTUNDeviceName(name) {
			t.Errorf("设备名 %q 应非法", name)
		}
	}
}

func TestDecideTUNCleanup(t *testing.T) {
	base := TUNCleanupState{
		Android:      true,
		TunEnabled:   true,
		AbnormalExit: true,
		DeviceExists: true,
	}
	withDevice := func(name string) TUNCleanupState {
		s := base
		s.DeviceName = name
		return s
	}
	cases := []struct {
		name  string
		state TUNCleanupState
		stale bool
		clean bool
	}{
		{"默认设备残留允许自动清理", withDevice("Meta"), true, true},
		{"空设备名按默认 Meta 处理", withDevice(""), true, true},
		{"自定义设备只标记不清理", withDevice("utun9"), true, false},
		{"非法设备名只标记不清理", withDevice("bad name"), true, false},
		{"上次正常退出不清理", func() TUNCleanupState {
			s := base
			s.AbnormalExit = false
			return s
		}(), false, false},
		{"控制器仍可达不清理", func() TUNCleanupState {
			s := base
			s.ControllerReachable = true
			return s
		}(), false, false},
		{"设备不存在不清理", func() TUNCleanupState {
			s := base
			s.DeviceExists = false
			return s
		}(), false, false},
		{"TUN 未启用不清理", func() TUNCleanupState {
			s := base
			s.TunEnabled = false
			return s
		}(), false, false},
		{"非 Android 不清理", func() TUNCleanupState {
			s := base
			s.Android = false
			return s
		}(), false, false},
	}
	for _, tc := range cases {
		got := DecideTUNCleanup(tc.state)
		if got.Stale != tc.stale || got.Clean != tc.clean {
			t.Errorf("%s: 决策错误 Stale=%v Clean=%v，期望 Stale=%v Clean=%v（reason=%q）",
				tc.name, got.Stale, got.Clean, tc.stale, tc.clean, got.Reason)
		}
		if got.Stale && !got.Clean && got.Reason == "" {
			t.Errorf("%s: 跳过清理时必须给出原因", tc.name)
		}
	}
}

func TestDeleteLinkWithFakeRunner(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows 上不覆盖 ip 命令路径")
	}
	var gotBinary string
	var gotArgs []string
	var gotTimeout time.Duration
	record := func(_ context.Context, timeout time.Duration, name string, args ...string) (string, error) {
		gotBinary, gotArgs, gotTimeout = name, args, timeout
		return "", nil
	}
	if err := deleteLinkWithRunner(record, "Meta"); err != nil {
		t.Fatalf("删除命令应成功: %v", err)
	}
	if gotBinary != ipBinary() {
		t.Fatalf("命令路径错误: %s", gotBinary)
	}
	if len(gotArgs) != 3 || gotArgs[0] != "link" || gotArgs[1] != "del" || gotArgs[2] != "Meta" {
		t.Fatalf("ip 命令参数错误: %v", gotArgs)
	}
	if gotTimeout <= 0 {
		t.Fatalf("命令超时应为正数: %v", gotTimeout)
	}

	// 非法设备名必须在执行命令前被拒绝
	called := false
	fake := func(_ context.Context, _ time.Duration, _ string, _ ...string) (string, error) {
		called = true
		return "", nil
	}
	if err := deleteLinkWithRunner(fake, "bad name"); err == nil {
		t.Fatal("非法设备名应报错")
	}
	if called {
		t.Fatal("非法设备名不应执行 ip 命令")
	}

	// 命令失败应带上下文并保留原始错误
	boom := errors.New("ip failed")
	fail := func(_ context.Context, _ time.Duration, _ string, _ ...string) (string, error) {
		return "", boom
	}
	if err := deleteLinkWithRunner(fail, "Meta"); err == nil || !errors.Is(err, boom) {
		t.Fatalf("命令失败应返回带上下文的错误: %v", err)
	}
}

func TestDeleteLinkNonAndroid(t *testing.T) {
	if IsAndroid() {
		t.Skip("Android 上 DeleteLink 会真实执行 ip 命令")
	}
	if err := DeleteLink("Meta"); !errors.Is(err, ErrNotSupported) {
		t.Fatalf("非 Android 平台应返回 ErrNotSupported: %v", err)
	}
}
