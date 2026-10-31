package sysutil

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// DefaultTUNDeviceName 是 mihomo 默认的 TUN 设备名。
// 只有这个默认设备会被受控自动清理；自定义设备名可能是用户自己的网卡
// （其它 VPN、手工创建的 link 等），绝不能自动删除。
const DefaultTUNDeviceName = "Meta"

// tunDeviceNameRe 限制设备名范围：Linux/Android 网卡名最多 15 个字符，
// 只允许字母、数字、下划线、点和连字符。设备名会作为参数传给 ip 命令，
// 严格校验可避免把配置里的任意内容拼进命令行。
var tunDeviceNameRe = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,15}$`)

// ValidTUNDeviceName 判断设备名是否是安全的 Linux 网卡名（1..15 个字符）。
func ValidTUNDeviceName(name string) bool { return tunDeviceNameRe.MatchString(name) }

// commandRunner 与 runCommand 签名一致，测试可注入假实现，避免真的执行 ip 命令。
type commandRunner func(ctx context.Context, timeout time.Duration, name string, args ...string) (string, error)

// tunCommandRunner 是 TUN 清理使用的命令执行器；包级变量便于测试替换。
var tunCommandRunner commandRunner = runCommand

// DeleteLink 删除指定 TUN 网卡（仅 Android；其它平台返回 ErrNotSupported）。
// 调用方必须先确认这是 mihomo 自己残留的设备，绝不能删除用户自定义网卡。
func DeleteLink(name string) error {
	if !IsAndroid() {
		return ErrNotSupported
	}
	return deleteLinkWithRunner(tunCommandRunner, name)
}

// deleteLinkWithRunner 用注入的 runner 执行 `ip link del <name>`；不直接执行真实命令，
// 便于单元测试验证设备名校验与命令参数。
func deleteLinkWithRunner(run commandRunner, name string) error {
	name = strings.TrimSpace(name)
	if !ValidTUNDeviceName(name) {
		return fmt.Errorf("非法的 TUN 设备名: %q", name)
	}
	if run == nil {
		return fmt.Errorf("未配置 TUN 命令执行器")
	}
	if _, err := run(context.Background(), 3*time.Second, ipBinary(), "link", "del", name); err != nil {
		return fmt.Errorf("删除 TUN 设备 %s 失败: %w", name, err)
	}
	return nil
}

// TUNCleanupState 是判断“是否自动清理 TUN 残留”所需的全部输入状态。
type TUNCleanupState struct {
	// Android 表示当前运行在 Android 用户空间（非 Android 不做任何清理）。
	Android bool
	// TunEnabled 表示配置里 tun.enable 为 true。
	TunEnabled bool
	// AbnormalExit 表示上一次内核退出是异常退出（非主动停止、有错误原因）。
	AbnormalExit bool
	// DeviceExists 表示配置的 TUN 设备名当前仍存在于系统网卡列表。
	DeviceExists bool
	// ControllerReachable 表示 mihomo 控制器仍可访问（说明内核仍然活着）。
	ControllerReachable bool
	// DeviceName 是配置的 TUN 设备名（空串按默认 mihomo 设备名处理）。
	DeviceName string
}

// TUNCleanupDecision 是受控清理决策：Stale 表示检测到疑似残留，
// Clean 表示允许后端自动删除该设备；Reason 用于日志与诊断说明。
type TUNCleanupDecision struct {
	Stale  bool
	Clean  bool
	Reason string
}

// DecideTUNCleanup 以“安全优先”判定是否自动清理残留 TUN 网卡。
// 只有同时满足以下条件才允许清理：Android、TUN 开启、上次内核异常退出、
// 控制器不可达、设备确实存在、设备名合法且恰为 mihomo 默认名 "Meta"。
// 自定义设备名只标记为残留并给出跳过原因，交由用户手动处理。
func DecideTUNCleanup(s TUNCleanupState) TUNCleanupDecision {
	switch {
	case !s.Android, !s.TunEnabled, s.ControllerReachable, !s.AbnormalExit, !s.DeviceExists:
		return TUNCleanupDecision{}
	}
	name := strings.TrimSpace(s.DeviceName)
	if name == "" {
		name = DefaultTUNDeviceName
	}
	if !ValidTUNDeviceName(name) {
		return TUNCleanupDecision{
			Stale:  true,
			Reason: fmt.Sprintf("设备名 %q 非法，已跳过自动清理", name),
		}
	}
	if name != DefaultTUNDeviceName {
		return TUNCleanupDecision{
			Stale:  true,
			Reason: fmt.Sprintf("设备 %s 不是 mihomo 默认设备名 %s（可能是自定义网卡），需手动清理", name, DefaultTUNDeviceName),
		}
	}
	return TUNCleanupDecision{
		Stale:  true,
		Clean:  true,
		Reason: fmt.Sprintf("设备 %s 疑似上次内核异常退出残留", name),
	}
}
