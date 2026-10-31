package sysutil

import (
	"testing"
	"time"
)

func TestDetectManager(t *testing.T) {
	switch got := DetectManager(); got {
	case "kernelsu", "apatch", "magisk", "unknown":
	default:
		t.Fatalf("DetectManager 返回未知取值: %q", got)
	}
}

func TestTUNDeviceExistsValidation(t *testing.T) {
	if TUNDeviceExists("") {
		t.Fatal("空设备名不应返回 true")
	}
	if !IsAndroid() && TUNDeviceExists("Meta") {
		t.Fatal("非 Android 平台不应检测 TUN 设备")
	}
}

func TestWaitForNetworkBackoffZeroTimeout(t *testing.T) {
	// 超时为 0 时不应睡眠，必须立即返回，避免调用方被卡住。
	start := time.Now()
	_ = WaitForNetworkBackoff(0)
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("零超时等待耗时过长: %s", elapsed)
	}
}
