package sysutil

import "testing"

func TestParseDefaultInterface(t *testing.T) {
	proc := `Iface	Destination	Gateway 	Flags	RefCnt	Use	Metric	Mask		MTU	Window	IRTT
wlan0	00000000	0102A8C0	0003	0	0	0	00000000	0	0	0
rmnet_data0	00000000	00000000	0000	0	0	0	00000000	0	0	0
`
	ip := "default via 192.168.1.1 dev wlan0 proto dhcp src 192.168.1.20 metric 100"
	if got := parseDefaultInterface(proc, ip); got != "wlan0" {
		t.Fatalf("proc 默认路由解析 = %q，期望 wlan0", got)
	}

	ip2 := `10.0.0.0/8 dev rmnet_data0 proto kernel scope link
default via 10.0.0.1 dev rmnet_data0 proto static`
	if got := parseDefaultInterface("", ip2); got != "rmnet_data0" {
		t.Fatalf("ip route 默认路由解析 = %q，期望 rmnet_data0", got)
	}

	if got := parseDefaultInterface("", "default via 10.0.0.1"); got != "" {
		t.Fatalf("缺少 dev 时应返回空，实际 %q", got)
	}
	if got := parseDefaultInterface("", ""); got != "" {
		t.Fatalf("空输入应返回空，实际 %q", got)
	}
}

func TestDefaultInterfaceDoesNotPanic(t *testing.T) {
	// 只验证在非 Android 环境也不会 panic/死循环；真实网卡名由解析单测覆盖。
	_ = DefaultInterface()
}
