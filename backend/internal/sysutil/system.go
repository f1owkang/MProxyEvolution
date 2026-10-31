package sysutil

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// SystemInfo 是 /api/status 中的系统信息。
type SystemInfo struct {
	Arch      string  `json:"arch"`
	ABI       string  `json:"abi"`
	Android   string  `json:"android"`
	Model     string  `json:"model"`
	Kernel    string  `json:"kernel"`
	Manager   string  `json:"manager"`
	MemTotal  int64   `json:"memTotal"`
	MemFree   int64   `json:"memFree"`
	Load1     float64 `json:"load1"`
	UptimeSec int64   `json:"uptimeSec"`
}

// CollectSystemInfo 收集系统信息；Windows 开发环境下部分字段为空。
func CollectSystemInfo() SystemInfo {
	info := SystemInfo{
		Arch:    runtime.GOARCH,
		ABI:     ABI(),
		Android: GetProp("ro.build.version.release"),
		Model:   GetProp("ro.product.model"),
		Manager: DetectManager(),
	}
	if info.Model == "" {
		info.Model = GetProp("ro.product.marketname")
	}
	if IsAndroid() {
		info.Kernel = GetProp("ro.kernel.version")
	}
	if info.Kernel == "" && runtime.GOOS != "windows" {
		if out, err := runCommand(context.Background(), 2*time.Second, "uname", "-r"); err == nil {
			info.Kernel = strings.TrimSpace(out)
		}
	}
	readProcStatus(&info)
	return info
}

func readProcStatus(info *SystemInfo) {
	if f, err := os.Open("/proc/meminfo"); err == nil {
		defer f.Close()
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			line := sc.Text()
			switch {
			case strings.HasPrefix(line, "MemTotal:"):
				info.MemTotal = parseKB(line)
			case strings.HasPrefix(line, "MemAvailable:"):
				info.MemFree = parseKB(line)
			}
		}
	}
	if b, err := os.ReadFile("/proc/loadavg"); err == nil {
		if fields := strings.Fields(string(b)); len(fields) > 0 {
			info.Load1, _ = strconv.ParseFloat(fields[0], 64)
		}
	}
	if b, err := os.ReadFile("/proc/uptime"); err == nil {
		if fields := strings.Fields(string(b)); len(fields) > 0 {
			if v, err := strconv.ParseFloat(fields[0], 64); err == nil {
				info.UptimeSec = int64(v)
			}
		}
	}
}

func parseKB(line string) int64 {
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return 0
	}
	v, err := strconv.ParseInt(fields[1], 10, 64)
	if err != nil {
		return 0
	}
	return v * 1024
}

// GetProp 读取 Android 系统属性；非 Android 或命令失败时返回空串。
func GetProp(name string) string {
	if !IsAndroid() {
		return ""
	}
	out, err := runCommand(context.Background(), 2*time.Second, GetPropPath(), name)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// ipBinary 返回可用的 ip 命令路径（Android 优先 /system/bin/ip）。
func ipBinary() string {
	if _, err := os.Stat("/system/bin/ip"); err == nil {
		return "/system/bin/ip"
	}
	return "ip"
}

// HasDefaultRoute 检查当前是否已有默认路由（TUN auto-route 需要）。
func HasDefaultRoute() bool {
	out, err := runCommand(context.Background(), 3*time.Second, ipBinary(), "route")
	if err != nil {
		return false
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "default ") {
			return true
		}
	}
	return false
}

// TUNDeviceExists 检查指定网卡是否仍存在（诊断 TUN 残留时使用）。
// 只在 Android 上检测，其它平台返回 false，避免把桌面网卡误判成 TUN。
func TUNDeviceExists(name string) bool {
	if !IsAndroid() {
		return false
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return false
	}
	out, err := runCommand(context.Background(), 2*time.Second, ipBinary(), "link", "show", name)
	if err != nil {
		return false
	}
	return strings.TrimSpace(out) != ""
}

// EnableIPv6Forwarding 尽力开启 IPv6 转发（TUN 代理 IPv6 流量需要）。
// 同时写 all 与 default 两个 forwarding 开关；非 Android（桌面开发）安全跳过。
// 内核未编入 IPv6 或 /proc/sys 只读时返回错误，调用方记录日志后继续启动。
func EnableIPv6Forwarding() error {
	if !IsAndroid() {
		return nil
	}
	var firstErr error
	for _, path := range []string{
		"/proc/sys/net/ipv6/conf/all/forwarding",
		"/proc/sys/net/ipv6/conf/default/forwarding",
	} {
		if err := os.WriteFile(path, []byte("1\n"), 0o644); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("写入 %s 失败: %w", path, err)
		}
	}
	return firstErr
}

// WaitForNetwork 等待默认路由出现，最多 timeout；Android 开机后网络可能晚于
// sys.boot_completed 就绪，直接启动 TUN 会报 "network is unreachable"。
func WaitForNetwork(timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if HasDefaultRoute() {
			return true
		}
		time.Sleep(time.Second)
	}
	return HasDefaultRoute()
}

// WaitForNetworkBackoff 与 WaitForNetwork 语义相同，却按 1s→5s 逐步拉长探测间隔，
// 避免开机/飞行模式恢复期间每秒 fork 一次 ip 命令。用于 runServe 的 120s 开机等待。
func WaitForNetworkBackoff(timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	delay := time.Second
	for {
		if HasDefaultRoute() {
			return true
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return false
		}
		if remaining < delay {
			delay = remaining
		}
		time.Sleep(delay)
		if delay < 5*time.Second {
			delay += time.Second
			if delay > 5*time.Second {
				delay = 5 * time.Second
			}
		}
	}
}

// DetectManager 识别当前 root 管理器：优先 KernelSU，其次 APatch，最后 Magisk。
// 三者都通过各自在 /data/adb 下的特征目录判断，未识别时返回 "unknown"。
func DetectManager() string {
	candidates := []struct {
		path string
		name string
	}{
		{"/data/adb/ksu", "kernelsu"},
		{"/data/adb/apd", "apatch"},
		{"/data/adb/magisk", "magisk"},
	}
	for _, c := range candidates {
		if _, err := os.Stat(c.path); err == nil {
			return c.name
		}
	}
	return "unknown"
}

// runCommand 带超时执行命令并返回 stdout+stderr。
func runCommand(ctx context.Context, timeout time.Duration, name string, args ...string) (string, error) {
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	out, err := exec.CommandContext(runCtx, name, args...).CombinedOutput()
	return string(out), err
}

// DefaultInterface 读取当前默认路由的网卡名（Android 策略路由/主表都可）。
func DefaultInterface() string {
	proc, _ := os.ReadFile("/proc/net/route")
	out, _ := runCommand(context.Background(), 2*time.Second, ipBinary(), "route")
	if iface := parseDefaultInterface(string(proc), out); iface != "" {
		return iface
	}
	// Android 17 的默认路由常只存在于策略表（如 table wlan0），`ip route` 主表为空；
	// 用 `ip route get` 让内核按策略规则给出真实出口网卡（akashaProxy 也用这个思路）。
	get, _ := runCommand(context.Background(), 2*time.Second, ipBinary(), "route", "get", "1.1.1.1")
	return parseRouteGetInterface(get)
}

// parseRouteGetInterface 从 `ip route get 1.1.1.1` 输出中解析 `dev <iface>`。
func parseRouteGetInterface(out string) string {
	fields := strings.Fields(out)
	for i, f := range fields {
		if f == "dev" && i+1 < len(fields) {
			return fields[i+1]
		}
	}
	return ""
}

// parseDefaultInterface 从 /proc/net/route 与 `ip route` 输出解析默认网卡。
// 拆成纯函数便于测试 Android 策略路由的两种格式。
func parseDefaultInterface(procRoute, ipRoute string) string {
	for _, line := range strings.Split(procRoute, "\n")[1:] {
		fields := strings.Fields(line)
		if len(fields) < 4 || fields[1] != "00000000" || fields[0] == "" {
			continue
		}
		if flags, err := strconv.ParseUint(fields[3], 16, 32); err == nil && flags&0x1 != 0 {
			return fields[0]
		}
	}
	for _, line := range strings.Split(ipRoute, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || fields[0] != "default" {
			continue
		}
		for i, f := range fields {
			if f == "dev" && i+1 < len(fields) {
				return fields[i+1]
			}
		}
	}
	return ""
}
