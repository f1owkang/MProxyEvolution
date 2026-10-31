package sysutil

import "testing"

func TestTUNRuleCleanupPriorities(t *testing.T) {
	want := []string{"9000", "9001", "9010"}
	if len(tunRuleCleanupPriorities) != len(want) {
		t.Fatalf("策略路由清理优先级数量错误: %v", tunRuleCleanupPriorities)
	}
	for i, v := range want {
		if tunRuleCleanupPriorities[i] != v {
			t.Fatalf("策略路由清理优先级第 %d 项应为 %s，实际 %s", i, v, tunRuleCleanupPriorities[i])
		}
	}
	if tunRouteTable != "2022" {
		t.Fatalf("mihomo 默认路由表应为 2022，实际 %s", tunRouteTable)
	}
}

func TestCleanupTUNRoutingNoopOnDesktop(t *testing.T) {
	if IsAndroid() {
		t.Skip("Android 环境会实际执行 ip 命令")
	}
	ran, err := CleanupTUNRouting()
	if err != nil {
		t.Fatalf("桌面环境清理应为空操作: %v", err)
	}
	if ran {
		t.Fatal("桌面环境不应执行策略路由清理")
	}
}
