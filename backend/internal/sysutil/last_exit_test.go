package sysutil

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLastExitRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "last_exit")
	at := time.Date(2026, 10, 31, 12, 34, 56, 0, time.FixedZone("CST", 8*3600))
	info := LastExitInfo{
		Time:        at,
		PID:         4321,
		Err:         "signal: killed & unexpected <crash>",
		Intentional: true,
	}
	if err := WriteLastExit(path, info); err != nil {
		t.Fatalf("写入上次退出状态失败: %v", err)
	}
	got, err := ReadLastExit(path)
	if err != nil {
		t.Fatalf("读取上次退出状态失败: %v", err)
	}
	if !got.Time.Equal(at) || got.PID != 4321 || got.Err != info.Err || !got.Intentional {
		t.Fatalf("反序列化结果错误: %+v", got)
	}
	summary := got.Summary()
	if !strings.Contains(summary, "pid=4321") || !strings.Contains(summary, "主动停止") {
		t.Fatalf("摘要错误: %s", summary)
	}

	RemoveLastExit(path)
	if _, err := ReadLastExit(path); !os.IsNotExist(err) {
		t.Fatalf("删除后应返回 IsNotExist: %v", err)
	}
}

func TestReadLastExitCorrupt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "last_exit")
	if err := os.WriteFile(path, []byte("time=not-a-time pid=x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadLastExit(path); err == nil {
		t.Fatal("非法时间应返回错误")
	}
}
