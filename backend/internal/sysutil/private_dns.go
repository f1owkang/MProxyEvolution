package sysutil

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// privateDNSModeKey 是 Android 全局设置中 Private DNS 的键名。
const privateDNSModeKey = "private_dns_mode"

// settingsRunner 执行 Android settings 命令，测试可替换以模拟不同设备状态。
// 默认走系统 /system/bin/settings（退回 PATH 中的 settings）。
var settingsRunner = func(ctx context.Context, args ...string) (string, error) {
	return runCommand(ctx, 3*time.Second, settingsBinaryPath(), args...)
}

// settingsBinaryPath 返回可用的 settings 可执行文件路径。
func settingsBinaryPath() string {
	if _, err := os.Stat("/system/bin/settings"); err == nil {
		return "/system/bin/settings"
	}
	return "settings"
}

// DNSMode 读取系统 Private DNS 模式（settings get global private_dns_mode）。
// 返回值为去掉首尾空白的原始模式：off / opportunistic / hostname；设备未启用时
// 常见为 "off" 或输出 "null"。仅 Android 支持，其它平台返回 ErrNotSupported。
func DNSMode(ctx context.Context) (string, error) {
	if !IsAndroid() {
		return "", ErrNotSupported
	}
	out, err := settingsRunner(ctx, "get", "global", privateDNSModeKey)
	if err != nil {
		return "", fmt.Errorf("读取系统 Private DNS 模式失败: %w", err)
	}
	return strings.TrimSpace(out), nil
}

// normalizeDNSMode 规范化 settings 输出：空、"null"、"off" 都视为“未启用”，
// 返回空串；其余返回小写模式名（opportunistic / hostname）。
func normalizeDNSMode(raw string) string {
	mode := strings.ToLower(strings.TrimSpace(raw))
	switch mode {
	case "", "null", "off":
		return ""
	default:
		return mode
	}
}

// putDNSMode 写入系统 Private DNS 模式。
func putDNSMode(ctx context.Context, mode string) error {
	if _, err := settingsRunner(ctx, "put", "global", privateDNSModeKey, mode); err != nil {
		return fmt.Errorf("执行 settings put global %s %s 失败: %w", privateDNSModeKey, mode, err)
	}
	return nil
}

// DisablePrivateDNS 在 TUN 启动前临时关闭系统 Private DNS，避免 DoT（853）绕过
// mihomo fake-ip 导致 DNS 泄漏/解析失败。
//
// 行为：
//   - 只在 Android 上执行，其它平台安全空操作；
//   - 当前模式为空/off/null 时不写入、返回 false；
//   - 原模式保存到 stateFile（0600，原子写入）：文件已存在时不覆盖，
//     避免连重启时把“off”当成用户原值保存；
//   - 写入 off 成功返回 true，失败返回错误（调用方记录日志后继续）。
func DisablePrivateDNS(stateFile string) (bool, error) {
	if !IsAndroid() {
		return false, nil
	}
	return disablePrivateDNS(context.Background(), stateFile, DNSMode, putDNSMode)
}

// disablePrivateDNS 是带可注入读取/写入函数的实现，便于单元测试。
func disablePrivateDNS(
	ctx context.Context,
	stateFile string,
	getMode func(context.Context) (string, error),
	putMode func(context.Context, string) error,
) (bool, error) {
	raw, err := getMode(ctx)
	if err != nil {
		return false, fmt.Errorf("读取系统 Private DNS 模式失败: %w", err)
	}
	mode := normalizeDNSMode(raw)
	if mode == "" {
		return false, nil
	}
	stateExists := false
	if _, statErr := os.Stat(stateFile); statErr == nil {
		stateExists = true
	} else if !os.IsNotExist(statErr) {
		return false, fmt.Errorf("检查 Private DNS 状态文件失败: %w", statErr)
	}
	if !stateExists {
		if err := writeDNSStateFile(stateFile, mode); err != nil {
			return false, err
		}
	}
	if err := putMode(ctx, "off"); err != nil {
		// 写入失败且状态文件是本次新建的：回滚，避免留下无意义的“原模式”记录。
		if !stateExists {
			_ = os.Remove(stateFile)
		}
		return false, err
	}
	return true, nil
}

// RestorePrivateDNS 恢复 DisablePrivateDNS 保存的原始模式，成功后删除状态文件；
// 状态文件不存在或内容为空时安全空操作（幂等）。
func RestorePrivateDNS(stateFile string) error {
	if !IsAndroid() {
		return nil
	}
	return restorePrivateDNS(context.Background(), stateFile, putDNSMode)
}

// restorePrivateDNS 是带可注入写入函数的实现，便于单元测试。
func restorePrivateDNS(
	ctx context.Context,
	stateFile string,
	putMode func(context.Context, string) error,
) error {
	raw, err := readDNSStateFile(stateFile)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	mode := strings.TrimSpace(raw)
	if mode == "" {
		// 状态文件损坏/为空：删掉，避免下次误恢复。
		if err := os.Remove(stateFile); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("删除空的 Private DNS 状态文件失败: %w", err)
		}
		return nil
	}
	if err := putMode(ctx, mode); err != nil {
		return err
	}
	if err := os.Remove(stateFile); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("删除 Private DNS 状态文件失败: %w", err)
	}
	return nil
}

// PrivateDNSMode 是面向诊断接口的最佳努力读取：非 Android 或读取失败返回空串；
// 已关闭（off/null）也返回空串，避免把“未启用”当成异常模式。
func PrivateDNSMode() string {
	if !IsAndroid() {
		return ""
	}
	mode, err := DNSMode(context.Background())
	if err != nil {
		return ""
	}
	return normalizeDNSMode(mode)
}

// writeDNSStateFile 原子写入 Private DNS 原模式（临时文件 + rename，权限 0600）。
func writeDNSStateFile(path, mode string) error {
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("创建 Private DNS 状态目录失败: %w", err)
		}
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(mode+"\n"), 0o600); err != nil {
		return fmt.Errorf("写入 Private DNS 状态文件失败: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("保存 Private DNS 状态文件失败: %w", err)
	}
	_ = os.Chmod(path, 0o600)
	return nil
}

// readDNSStateFile 读取保存的 Private DNS 原模式。
func readDNSStateFile(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
