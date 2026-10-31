package main

import (
	"encoding/json"
	"testing"
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
