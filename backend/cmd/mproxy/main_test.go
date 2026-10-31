package main

import (
	"encoding/json"
	"testing"

	"mproxy/internal/core"
)

// TestStatusResponseParsesStartupFields 锁定 /api/status 中 action 子命令依赖的字段，
// 避免后端 JSON 契约变更导致「执行」按钮误判内核已就绪。
func TestStatusResponseParsesStartupFields(t *testing.T) {
	raw := `{
		"core": {
			"installed": true,
			"running": true,
			"pid": 2163,
			"version": "v1.19.33",
			"restarts": 2,
			"lastExit": "time=2026-10-31T14:00:00Z pid=1 err=exit status 1",
			"controller": {
				"host": "127.0.0.1",
				"port": 9090,
				"reachable": true,
				"tunEnabled": true
			}
		},
		"config": {
			"summary": {
				"tunEnabled": true
			}
		}
	}`
	var st statusResponse
	if err := json.Unmarshal([]byte(raw), &st); err != nil {
		t.Fatalf("解析状态失败: %v", err)
	}
	if !st.Core.Installed || !st.Core.Running {
		t.Fatalf("内核状态解析错误: %+v", st.Core)
	}
	if st.Core.PID != 2163 || st.Core.Version != "v1.19.33" {
		t.Fatalf("内核 pid/版本解析错误: %+v", st.Core)
	}
	if st.Core.Restarts != 2 || st.Core.LastExit == "" {
		t.Fatalf("重启次数/上次退出解析错误: %+v", st.Core)
	}
	if !st.Core.Controller.Reachable || !st.Core.Controller.TunEnabled {
		t.Fatalf("控制器可访问性/TUN 状态解析错误: %+v", st.Core.Controller)
	}
	if !st.Config.Summary.TunEnabled {
		t.Fatalf("配置 TUN 状态解析错误: %+v", st.Config.Summary)
	}
}

func TestTunStartFailed(t *testing.T) {
	cases := []struct {
		name     string
		expect   bool
		modify   func(*core.Status)
		expected bool
	}{
		{
			name:   "TUN 开启但控制器报告未生效",
			expect: true,
			modify: func(st *core.Status) {
				st.Installed, st.Running = true, true
				st.Controller.Reachable, st.Controller.TunEnabled = true, false
			},
			expected: true,
		},
		{
			name:   "TUN 开启且控制器报告已生效",
			expect: true,
			modify: func(st *core.Status) {
				st.Installed, st.Running = true, true
				st.Controller.Reachable, st.Controller.TunEnabled = true, true
			},
			expected: false,
		},
		{
			name:   "TUN 关闭时不关心控制器 TUN 字段",
			expect: false,
			modify: func(st *core.Status) {
				st.Installed, st.Running = true, true
				st.Controller.Reachable, st.Controller.TunEnabled = true, false
			},
			expected: false,
		},
		{
			name:   "控制器不可达无法判定",
			expect: true,
			modify: func(st *core.Status) {
				st.Installed, st.Running = true, true
				st.Controller.Reachable, st.Controller.TunEnabled = false, false
			},
			expected: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var st core.Status
			tc.modify(&st)
			if got := tunStartFailed(st, tc.expect); got != tc.expected {
				t.Fatalf("tunStartFailed=%v，期望 %v", got, tc.expected)
			}
		})
	}
	if len(autoStartBackoffs) != 4 {
		t.Fatalf("5 次尝试应有 4 个退避间隔，实际 %d", len(autoStartBackoffs))
	}
}
