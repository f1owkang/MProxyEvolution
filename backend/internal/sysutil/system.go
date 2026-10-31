package sysutil

import (
	"bufio"
	"context"
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

// HasDefaultRoute 检查当前是否已有默认路由（TUN auto-route 需要）。
func HasDefaultRoute() bool {
	ip := "ip"
	if _, err := os.Stat("/system/bin/ip"); err == nil {
		ip = "/system/bin/ip"
	}
	out, err := runCommand(context.Background(), 3*time.Second, ip, "route")
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
	ip := "ip"
	if _, err := os.Stat("/system/bin/ip"); err == nil {
		ip = "/system/bin/ip"
	}
	out, _ := runCommand(context.Background(), 2*time.Second, ip, "route")
	return parseDefaultInterface(string(proc), out)
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
