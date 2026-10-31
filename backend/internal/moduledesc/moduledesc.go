// Package moduledesc 根据后端与内核的实时状态生成并写回模块描述（module.prop 的
// description 行），让 KernelSU/Magisk 模块页不打开 WebUI 也能看到当前运行状态。
package moduledesc

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"mproxy/internal/config"
	"mproxy/internal/core"
)

// maxDescriptionBytes 是描述长度上限（字节）。管理器与面板都按行展示，过长的描述
// 会被截断；这里主动限制在 UTF-8 完整边界内，避免写坏多字节字符。
const maxDescriptionBytes = 150

// 状态与模式文案（中文优先、单行）。
const (
	stateRunning      = "运行中"
	stateStopped      = "已停止"
	stateNotInstalled = "内核未安装"
	modeTUN           = "TUN"
	modeMixed         = "Mixed"
)

// Build 生成一行模块描述，例如：
//
//	MihomoProxy Evolution | 运行中 · TUN · 混合端口 7890 · 节点 3 · 规则 12 | 内核 v1.19.33
//
// 状态为运行中/已停止/内核未安装；节点与规则数只在已安装内核时展示；
// 空版本、缺失配置摘要等空值会被优雅跳过。appVersion 在拿不到内核版本时作为
// 兜底版本展示，两者都没有时省略版本段。返回值保证无换行且不超过 ~150 字节。
func Build(appVersion string, st core.Status, summary config.Summary) string {
	state := stateStopped
	switch {
	case !st.Installed:
		state = stateNotInstalled
	case st.Running:
		state = stateRunning
	}
	parts := []string{state}
	if mode := displayMode(summary); mode != "" {
		parts = append(parts, mode)
	}
	if summary.MixedPort > 0 {
		parts = append(parts, fmt.Sprintf("混合端口 %d", summary.MixedPort))
	}
	// 节点/规则数只在内核已安装时展示：未安装时配置摘要不代表实际能力。
	if st.Installed {
		if summary.ProxyCount > 0 {
			parts = append(parts, fmt.Sprintf("节点 %d", summary.ProxyCount))
		}
		if summary.RuleCount > 0 {
			parts = append(parts, fmt.Sprintf("规则 %d", summary.RuleCount))
		}
	}
	desc := "MihomoProxy Evolution | " + strings.Join(parts, " · ")
	if ver := versionSegment(appVersion, st); ver != "" {
		desc += " | " + ver
	}
	return clampDescription(desc)
}

// displayMode 返回 TUN/Mixed 模式文案；配置不可读（摘要为空）时返回空串，
// 不猜测也不展示空值。
func displayMode(summary config.Summary) string {
	switch {
	case summary.TunEnabled || strings.EqualFold(strings.TrimSpace(summary.ProxyMode), "tun"):
		return modeTUN
	case strings.EqualFold(strings.TrimSpace(summary.ProxyMode), "mixed"):
		return modeMixed
	default:
		return strings.TrimSpace(summary.ProxyMode)
	}
}

// versionSegment 优先展示内核版本；内核版本缺失时用应用版本兜底，都为空则不展示。
func versionSegment(appVersion string, st core.Status) string {
	if v := normalizeVersion(st.Version); v != "" {
		return "内核 " + v
	}
	if v := normalizeVersion(appVersion); v != "" {
		return "版本 " + v
	}
	return ""
}

// normalizeVersion 去掉首尾空白并补上 v 前缀（空值返回空串）。
func normalizeVersion(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return ""
	}
	if !strings.HasPrefix(v, "v") {
		v = "v" + v
	}
	return v
}

// clampDescription 去掉换行/CR 并把长度限制在 maxDescriptionBytes 内（按 UTF-8 边界截断）。
func clampDescription(s string) string {
	s = strings.Join(strings.FieldsFunc(s, func(r rune) bool { return r == '\n' || r == '\r' }), " ")
	s = strings.TrimSpace(s)
	if len(s) <= maxDescriptionBytes {
		return s
	}
	limit := maxDescriptionBytes - len("…")
	cut := 0
	for i := range s {
		if i > limit {
			break
		}
		cut = i
	}
	return strings.TrimSpace(s[:cut]) + "…"
}

// ErrModulePropNotFound 表示模块目录下没有 module.prop（桌面开发环境属正常情况）。
// 调用方可用 errors.Is 判断并静默跳过。
var ErrModulePropNotFound = errors.New("module.prop 不存在")

// writeMu 串行化写操作：状态回调与面板轮询可能并发刷新同一文件。
var writeMu sync.Mutex

// WriteDescription 更新 <moduleDir>/module.prop 的 description 行：
//   - 只替换 description= 单行，保留其它行内容与顺序，绝不改 id/name/version；
//   - 输出强制 LF、UTF-8 无 BOM、权限 0644，原子 tmp+rename 写入；
//   - 内容完全相同时不重写；描述含换行/CR 时拒绝；
//   - module.prop 不存在时返回 ErrModulePropNotFound（开发环境可忽略）。
func WriteDescription(moduleDir, description string) error {
	if strings.ContainsAny(description, "\r\n") {
		return fmt.Errorf("模块描述不能包含换行")
	}
	description = strings.TrimSpace(description)
	if strings.TrimSpace(moduleDir) == "" {
		return ErrModulePropNotFound
	}
	path := filepath.Join(moduleDir, "module.prop")

	writeMu.Lock()
	defer writeMu.Unlock()

	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("%w: %s", ErrModulePropNotFound, path)
		}
		return fmt.Errorf("读取 module.prop 失败: %w", err)
	}
	// 去掉可能存在的 UTF-8 BOM，保证输出无 BOM；同时把 CRLF 统一成 LF。
	content := string(bytes.TrimPrefix(raw, []byte{0xEF, 0xBB, 0xBF}))
	content = strings.ReplaceAll(content, "\r\n", "\n")
	lines := strings.Split(content, "\n")
	// 去掉末尾因换行产生的空元素，稍后统一补一个换行；避免追加 description 前多出空行。
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}

	out := make([]string, 0, len(lines)+1)
	replaced := false
	for _, line := range lines {
		if strings.HasPrefix(line, "description=") {
			if replaced {
				// 理论上只有一行；出现重复时收敛为一行，避免写出非法 module.prop。
				continue
			}
			out = append(out, "description="+description)
			replaced = true
			continue
		}
		out = append(out, line)
	}
	if !replaced {
		out = append(out, "description="+description)
	}
	// 去掉末尾空行后统一补一个换行，保证 LF 且幂等。
	for len(out) > 0 && strings.TrimSpace(out[len(out)-1]) == "" {
		out = out[:len(out)-1]
	}
	next := []byte(strings.Join(out, "\n") + "\n")
	if bytes.Equal(next, raw) {
		return nil
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, next, 0o644); err != nil {
		return fmt.Errorf("写入 module.prop 临时文件失败: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("保存 module.prop 失败: %w", err)
	}
	return nil
}
