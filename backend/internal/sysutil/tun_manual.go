package sysutil

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// akashaProxy 风格的手工 TUN 策略路由参数（与参考模块保持一致）：
// 打 fwmark 2025，查 table 2025，规则优先级 5000（早于 Android 的 11000+）。
const (
	manualTUNMark   = "2025"
	manualTUNTable  = "2025"
	manualTUNPref   = "5000"
	manualTUNPreCh  = "MPROXY_TUN_PRE"
	manualTUNOutCh  = "MPROXY_TUN_OUT"
	manualTUN6OutCh = "MPROXY_TUN6_OUT"
)

// manualTUNBypassCIDRs 是必须直连、不能进 TUN 的组播/广播/链路本地网段。
// LocalSend（224.0.0.167:53317）、小米互传、DLNA/SSDP 等依赖这些地址完成发现；
// 若被 mark 进 TUN，发现报文会被内核/mihomo 丢掉，表现为“局域网内看不到设备”。
var manualTUNBypassCIDRs = []string{
	"224.0.0.0/4",        // 组播（mDNS 224.0.0.251、SSDP 239.255.255.250、LocalSend 224.0.0.167）
	"255.255.255.255/32", // 本地广播
	"169.254.0.0/16",     // 链路本地
}

// manualTUNBypassList 返回组播/广播 + 本机直连网段（wlan0 的 192.168.x.0/24 等）。
func manualTUNBypassList() []string {
	seen := make(map[string]bool)
	var out []string
	for _, cidr := range manualTUNBypassCIDRs {
		if !seen[cidr] {
			seen[cidr] = true
			out = append(out, cidr)
		}
	}
	for _, cidr := range LocalIPv4CIDRs() {
		if !seen[cidr] {
			seen[cidr] = true
			out = append(out, cidr)
		}
	}
	return out
}

// iptablesBinary 返回 Android 上可用的 iptables 路径。
func iptablesBinary() string {
	for _, p := range []string{"/system/bin/iptables", "/system/xbin/iptables"} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return "iptables"
}

// ip6tablesBinary 返回 Android 上可用的 ip6tables 路径。
func ip6tablesBinary() string {
	for _, p := range []string{"/system/bin/ip6tables", "/system/xbin/ip6tables"} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return "ip6tables"
}

// WaitTUNDevice 等待内核创建 TUN 设备（mihomo 用 auto-route:false 启动时由它自己建卡）。
func WaitTUNDevice(name string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if TUNDeviceExists(name) {
			return true
		}
		time.Sleep(200 * time.Millisecond)
	}
	return TUNDeviceExists(name)
}

// parsePackagesUIDs 从 /data/system/packages.list 内容解析包名→UID。
// 纯函数便于测试；数字字符串直接当作 UID（兼容面板传入 uid）。
func parsePackagesUIDs(content string, packages []string) []string {
	want := make(map[string]string, len(packages))
	for _, p := range packages {
		p = strings.TrimSpace(p)
		if p != "" {
			want[p] = ""
		}
	}
	for _, line := range strings.Split(content, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		if _, ok := want[fields[0]]; ok {
			want[fields[0]] = fields[1]
		}
	}
	var uids []string
	for _, p := range packages {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if uid := want[p]; uid != "" {
			uids = append(uids, uid)
			continue
		}
		if isNumeric(p) {
			uids = append(uids, p)
		}
	}
	return uids
}

func isNumeric(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
}

// resolvePackageUIDs 读取 Android packages.list，把面板里的包名转换成 UID。
func resolvePackageUIDs(packages []string) []string {
	if len(packages) == 0 {
		return nil
	}
	raw, err := os.ReadFile("/data/system/packages.list")
	if err != nil {
		return nil
	}
	return parsePackagesUIDs(string(raw), packages)
}

// ManualTUNSetup 在 auto-route:false 时按 akashaProxy 的 tun.proxy 方式接管流量：
// mangle 标记 → fwmark 规则 → 默认路由指向 TUN，并保留应用分流/局域网回包排除。
// whitelist=true 时只标记 listed 包（apps 为包名或 UID），false 时排除 listed 包后标记其余。
func ManualTUNSetup(device string, whitelist bool, apps []string, ipv6 bool) error {
	if !IsAndroid() {
		return nil
	}
	device = strings.TrimSpace(device)
	if device == "" || !ValidTUNDeviceName(device) {
		return fmt.Errorf("TUN 设备名无效: %q", device)
	}
	ip := ipBinary()
	ipt := iptablesBinary()
	uids := resolvePackageUIDs(apps)
	if len(apps) > 0 && len(uids) == 0 {
		return fmt.Errorf("应用分流条目无法解析为 UID（packages.list 不可读）")
	}

	run := func(what string, name string, args ...string) error {
		if _, err := runCommand(context.Background(), 5*time.Second, name, args...); err != nil {
			return fmt.Errorf("%s: %w", what, err)
		}
		return nil
	}
	// Android 的 iptables 可能被其他模块/系统并发持有，统一 -w 100 等待锁。
	iptRun := func(what string, args ...string) error {
		return run(what, ipt, append([]string{"-w", "100"}, args...)...)
	}

	// 先清理旧规则，保证幂等。
	ManualTUNCleanup(device)

	// 1) PREROUTING：转发/热点流量打 mark（owner 匹配在 PREROUTING 不可用）。
	_ = iptRun("创建 PREROUTING 链", "-t", "mangle", "-N", manualTUNPreCh)
	if err := iptRun("清空 PREROUTING 链", "-t", "mangle", "-F", manualTUNPreCh); err != nil {
		return err
	}
	// DNS 先打 mark（即使目标是局域网 DNS 也走 TUN 的 dns-hijack），然后再放行直连网段。
	for _, proto := range []string{"udp", "tcp"} {
		if err := iptRun("PREROUTING DNS mark "+proto, "-t", "mangle", "-A", manualTUNPreCh,
			"-p", proto, "--dport", "53", "-j", "MARK", "--set-xmark", manualTUNMark); err != nil {
			return err
		}
	}
	for _, cidr := range manualTUNBypassList() {
		if err := iptRun("PREROUTING 放行直连 "+cidr, "-t", "mangle", "-A", manualTUNPreCh,
			"-d", cidr, "-j", "RETURN"); err != nil {
			return err
		}
	}
	if err := iptRun("PREROUTING 打 mark", "-t", "mangle", "-A", manualTUNPreCh,
		"-j", "MARK", "--set-xmark", manualTUNMark); err != nil {
		return err
	}
	if _, err := runCommand(context.Background(), 5*time.Second, ipt, "-w", "100", "-t", "mangle", "-C", "PREROUTING", "-j", manualTUNPreCh); err != nil {
		if err := iptRun("挂接 PREROUTING", "-t", "mangle", "-A", "PREROUTING", "-j", manualTUNPreCh); err != nil {
			return err
		}
	}

	// OUTPUT：排除 root（mihomo 与后端自身）及局域网入站回包，再打 mark。
	// 注意：mihomo 的 net_admin(3005) 只是附加组，iptables 的 --gid-owner 在部分
	// 内核上只看主组，曾导致 mihomo 出站被重新打 mark 进 TUN，规则集下载报 EOF。
	// 因此按 UID 0 整体排除（后端/内核自身流量不需要再被 TUN 代理）。
	_ = iptRun("创建 OUTPUT 链", "-t", "mangle", "-N", manualTUNOutCh)
	if err := iptRun("清空 OUTPUT 链", "-t", "mangle", "-F", manualTUNOutCh); err != nil {
		return err
	}
	if err := iptRun("排除内核自身", "-t", "mangle", "-A", manualTUNOutCh,
		"-m", "owner", "--uid-owner", "0", "-j", "RETURN"); err != nil {
		return err
	}
	// 手机上的 MCB/ADB/局域网服务回包属于 conntrack REPLY：不 mark，否则外部无法连入。
	if err := iptRun("排除局域网回包", "-t", "mangle", "-A", manualTUNOutCh,
		"-m", "conntrack", "--ctdir", "REPLY", "-j", "RETURN"); err != nil {
		return err
	}
	// DNS 先打 mark（局域网 DNS 也走 dns-hijack），随后直连网段/组播走正常路由。
	for _, proto := range []string{"udp", "tcp"} {
		if err := iptRun("OUTPUT DNS mark "+proto, "-t", "mangle", "-A", manualTUNOutCh,
			"-p", proto, "--dport", "53", "-j", "MARK", "--set-xmark", manualTUNMark); err != nil {
			return err
		}
	}
	for _, cidr := range manualTUNBypassList() {
		if err := iptRun("OUTPUT 放行直连 "+cidr, "-t", "mangle", "-A", manualTUNOutCh,
			"-d", cidr, "-j", "RETURN"); err != nil {
			return err
		}
	}
	for _, uid := range uids {
		if whitelist {
			if err := iptRun("白名单应用 "+uid, "-t", "mangle", "-A", manualTUNOutCh,
				"-m", "owner", "--uid-owner", uid, "-j", "MARK", "--set-xmark", manualTUNMark); err != nil {
				return err
			}
		} else if err := iptRun("黑名单应用 "+uid, "-t", "mangle", "-A", manualTUNOutCh,
			"-m", "owner", "--uid-owner", uid, "-j", "RETURN"); err != nil {
			return err
		}
	}
	if !whitelist {
		if err := iptRun("OUTPUT 打 mark", "-t", "mangle", "-A", manualTUNOutCh,
			"-j", "MARK", "--set-xmark", manualTUNMark); err != nil {
			return err
		}
	}
	if _, err := runCommand(context.Background(), 5*time.Second, ipt, "-w", "100", "-t", "mangle", "-C", "OUTPUT", "-j", manualTUNOutCh); err != nil {
		if err := iptRun("挂接 OUTPUT", "-t", "mangle", "-A", "OUTPUT", "-j", manualTUNOutCh); err != nil {
			return err
		}
	}

	// 3) fwmark 规则 + 指向 TUN 的默认路由（对应 tun.proxy 的 rule/route）。
	_, _ = runCommand(context.Background(), 5*time.Second, ip, "-4", "rule", "del", "fwmark", manualTUNMark, "table", manualTUNTable, "pref", manualTUNPref)
	if err := run("添加 fwmark 规则", ip, "-4", "rule", "add", "fwmark",
		manualTUNMark, "table", manualTUNTable, "pref", manualTUNPref); err != nil {
		return err
	}
	// TUN 刚创建时路由表可能尚未就绪，akashaProxy 用循环重试；这里有限次重试。
	for attempt := 0; attempt < 5; attempt++ {
		_, _ = runCommand(context.Background(), 5*time.Second, ip, "-4", "route", "del", "default", "dev", device, "table", manualTUNTable)
		if err := run("添加 TUN 默认路由", ip, "-4", "route", "add", "default", "dev", device, "table", manualTUNTable); err == nil {
			break
		} else if attempt == 4 {
			return err
		}
		time.Sleep(200 * time.Millisecond)
	}

	// 4) 放行 TUN 的转发；IPv6 关闭时禁用 TUN 网卡 IPv6（参考 akashaProxy）。
	for _, dir := range []string{"-o", "-i"} {
		if _, err := runCommand(context.Background(), 5*time.Second, ipt, "-w", "100", "-C", "FORWARD", dir, device, "-j", "ACCEPT"); err != nil {
			if err2 := iptRun("放行 FORWARD "+dir, "-A", "FORWARD", dir, device, "-j", "ACCEPT"); err2 != nil {
				return err2
			}
		}
	}
	if !ipv6 {
		_, _ = runCommand(context.Background(), 3*time.Second, ip, "-4", "route", "show", "table", manualTUNTable)
		_ = os.WriteFile("/proc/sys/net/ipv6/conf/"+device+"/disable_ipv6", []byte("1\n"), 0o644)
		// 手工 TUN 只接管 IPv4；系统 IPv6 默认路由仍然存在，应用会 Happy Eyeballs
		// 直连海外 IPv6，绕过代理后表现为“国内正常、海外全部超时”。关闭 IPv6 时
		// 用 ip6tables 拒绝应用侧 IPv6（放行 ::1/链路本地/组播与 root 自身），
		// 逼应用回退 IPv4 进入 TUN。
		setupIPv6Block()
		// 部分 ROM 的 ip6tables 会被系统链提前放行，单靠拒绝规则仍可能漏；
		// 同时按 x-mproxy.ipv6=false 临时关闭系统 IPv6，停止/重启时恢复原值。
		disableSystemIPv6()
	}
	return nil
}

const ipv6DisabledStatePath = "/data/adb/mproxy/state/ipv6_disabled"

// disableSystemIPv6 保存各接口原 disable_ipv6 值后关闭 IPv6；已有记录时保留第一次的原值。
func disableSystemIPv6() {
	if _, err := os.Stat(ipv6DisabledStatePath); err == nil {
		return
	}
	paths, _ := filepath.Glob("/proc/sys/net/ipv6/conf/*/disable_ipv6")
	var saved []string
	for _, p := range paths {
		iface := filepath.Base(filepath.Dir(p))
		old, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		saved = append(saved, iface+"="+strings.TrimSpace(string(old)))
		_ = os.WriteFile(p, []byte("1\n"), 0o644)
	}
	if len(saved) == 0 {
		return
	}
	_ = os.MkdirAll(filepath.Dir(ipv6DisabledStatePath), 0o700)
	_ = os.WriteFile(ipv6DisabledStatePath, []byte(strings.Join(saved, "\n")+"\n"), 0o600)
}

// restoreSystemIPv6 恢复 disableSystemIPv6 之前的接口状态。
func restoreSystemIPv6() {
	data, err := os.ReadFile(ipv6DisabledStatePath)
	if err != nil {
		return
	}
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 || parts[0] == "" {
			continue
		}
		_ = os.WriteFile(filepath.Join("/proc/sys/net/ipv6/conf", parts[0], "disable_ipv6"), []byte(parts[1]+"\n"), 0o644)
	}
	_ = os.Remove(ipv6DisabledStatePath)
}

// setupIPv6Block 拒绝应用侧 IPv6 出站，避免绕过手工 TUN 直连海外；幂等，失败不阻断启动。
func setupIPv6Block() {
	ip6 := ip6tablesBinary()
	run := func(args ...string) error {
		_, err := runCommand(context.Background(), 5*time.Second, ip6, append([]string{"-w", "100"}, args...)...)
		return err
	}
	_ = run("-t", "mangle", "-N", manualTUN6OutCh)
	_ = run("-t", "mangle", "-F", manualTUN6OutCh)
	for _, args := range [][]string{
		{"-d", "::1/128", "-j", "RETURN"},
		{"-d", "fe80::/10", "-j", "RETURN"},
		{"-d", "ff00::/8", "-j", "RETURN"},
		{"-m", "owner", "--uid-owner", "0", "-j", "RETURN"},
		{"-j", "REJECT"},
	} {
		_ = run(append([]string{"-t", "mangle", "-A", manualTUN6OutCh}, args...)...)
	}
	if _, err := runCommand(context.Background(), 5*time.Second, ip6, "-w", "100", "-t", "mangle", "-C", "OUTPUT", "-j", manualTUN6OutCh); err != nil {
		_ = run("-t", "mangle", "-A", "OUTPUT", "-j", manualTUN6OutCh)
	}
}

// ManualTUNCleanup 删除手工 TUN 的 iptables 链/规则与策略路由；幂等，Android 外为空操作。
func ManualTUNCleanup(device string) {
	if !IsAndroid() {
		return
	}
	ip := ipBinary()
	ipt := iptablesBinary()
	iptRun := func(args ...string) {
		_, _ = runCommand(context.Background(), 5*time.Second, ipt, append([]string{"-w", "100"}, args...)...)
	}
	for _, chain := range []struct{ parent, self string }{
		{"OUTPUT", manualTUNOutCh},
		{"PREROUTING", manualTUNPreCh},
	} {
		iptRun("-t", "mangle", "-D", chain.parent, "-j", chain.self)
		iptRun("-t", "mangle", "-F", chain.self)
		iptRun("-t", "mangle", "-X", chain.self)
	}
	if device != "" {
		for _, dir := range []string{"-o", "-i"} {
			iptRun("-D", "FORWARD", dir, device, "-j", "ACCEPT")
		}
	}
	_, _ = runCommand(context.Background(), 5*time.Second, ip, "-4", "rule", "del", "fwmark", manualTUNMark, "table", manualTUNTable, "pref", manualTUNPref)
	_, _ = runCommand(context.Background(), 5*time.Second, ip, "-4", "route", "flush", "table", manualTUNTable)

	ip6 := ip6tablesBinary()
	run6 := func(args ...string) {
		_, _ = runCommand(context.Background(), 5*time.Second, ip6, append([]string{"-w", "100"}, args...)...)
	}
	run6("-t", "mangle", "-D", "OUTPUT", "-j", manualTUN6OutCh)
	run6("-t", "mangle", "-F", manualTUN6OutCh)
	run6("-t", "mangle", "-X", manualTUN6OutCh)
	restoreSystemIPv6()
}
