package config

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// ValidationResult 是配置校验结果（ok + mihomo -t 输出）。
type ValidationResult struct {
	OK     bool   `json:"ok"`
	Output string `json:"output"`
}

// ValidateText 做纯语法/结构校验，不依赖内核。
func ValidateText(data []byte) ValidationResult {
	doc, err := Parse(data)
	if err != nil {
		return ValidationResult{OK: false, Output: err.Error()}
	}
	if err := doc.ValidateStructure(); err != nil {
		return ValidationResult{OK: false, Output: err.Error()}
	}
	return ValidationResult{OK: true, Output: ""}
}

// ValidateStructure 校验后端关心的关键字段类型与取值。
func (d *Doc) ValidateStructure() error {
	var problems []string
	checkType := func(key string, want string) {
		v, ok := d.Get(key)
		if !ok || v == nil {
			return
		}
		bad := false
		switch want {
		case "string":
			if _, isStr := v.(string); !isStr {
				bad = true
			}
		case "bool":
			if _, isBool := v.(bool); !isBool {
				bad = true
			}
		case "int":
			switch v.(type) {
			case int, int64, float64:
			default:
				bad = true
			}
		}
		if bad {
			problems = append(problems, fmt.Sprintf("%s 类型应为 %s", key, want))
		}
	}
	checkType("external-controller", "string")
	checkType("secret", "string")
	checkType("mixed-port", "int")
	checkType("socks-port", "int")
	checkType("port", "int")
	checkType("allow-lan", "bool")
	checkType("ipv6", "bool")
	checkType("mode", "string")
	checkType("log-level", "string")

	for _, key := range []string{"mixed-port", "socks-port", "port"} {
		if p := d.GetInt(0, key); p < 0 || p > 65535 {
			problems = append(problems, fmt.Sprintf("%s 超出范围 0-65535", key))
		}
	}
	if mode := d.GetString("rule", "mode"); mode != "" {
		switch mode {
		case "rule", "global", "direct":
		default:
			problems = append(problems, fmt.Sprintf("未知的 mode: %s", mode))
		}
	}
	if ll := d.GetString("info", "log-level"); ll != "" {
		switch ll {
		case "silent", "error", "warning", "info", "debug":
		default:
			problems = append(problems, fmt.Sprintf("未知的 log-level: %s", ll))
		}
	}
	if _, ok := d.Get("tun", "enable"); ok {
		checkType("tun.enable", "bool")
	}
	if err := ValidateProxyMode(d.ProxyMode()); err != nil {
		problems = append(problems, err.Error())
	}
	if err := ValidateChannel(d.Settings().UpdateChannel); err != nil {
		problems = append(problems, err.Error())
	}
	if v, ok := d.Get("proxy-groups"); ok {
		if groups, ok := v.([]any); ok {
			for i, g := range groups {
				gm, ok := g.(map[string]any)
				if !ok {
					problems = append(problems, fmt.Sprintf("proxy-groups[%d] 不是 mapping", i))
					continue
				}
				if name, _ := gm["name"].(string); strings.TrimSpace(name) == "" {
					problems = append(problems, fmt.Sprintf("proxy-groups[%d] 缺少 name", i))
				}
				if typ, _ := gm["type"].(string); strings.TrimSpace(typ) == "" {
					problems = append(problems, fmt.Sprintf("proxy-groups[%d] 缺少 type", i))
				}
			}
		} else {
			problems = append(problems, "proxy-groups 应为列表")
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("%s", strings.Join(problems, "；"))
	}
	return nil
}

// ValidateWithCore 调用 `mihomo -t` 校验配置文本；内核不存在时直接通过并输出说明。
func ValidateWithCore(binary, dataDir string, data []byte) ValidationResult {
	if strings.TrimSpace(binary) == "" {
		return ValidationResult{OK: true, Output: "内核未安装，已跳过 mihomo -t 校验"}
	}
	stateDir := filepath.Join(dataDir, "state")
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return ValidationResult{OK: false, Output: "创建校验临时目录失败: " + err.Error()}
	}
	f, err := os.CreateTemp(stateDir, "validate-*.yaml")
	if err != nil {
		return ValidationResult{OK: false, Output: "创建校验临时文件失败: " + err.Error()}
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return ValidationResult{OK: false, Output: "写入校验临时文件失败: " + err.Error()}
	}
	_ = f.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "-t", "-d", dataDir, "-f", tmp)
	out, err := cmd.CombinedOutput()
	text := strings.TrimSpace(string(out))
	if ctx.Err() == context.DeadlineExceeded {
		return ValidationResult{OK: false, Output: "mihomo -t 校验超时（45s）"}
	}
	if err != nil {
		if text == "" {
			text = err.Error()
		}
		return ValidationResult{OK: false, Output: "mihomo -t 校验失败: " + text}
	}
	return ValidationResult{OK: true, Output: text}
}

// ValidateConsistency 校验 tun.enable 与 x-mproxy.proxy-mode 是否一致。
// 字段缺失视为一致（默认配置模板一定带 tun.enable；加载旧配置保持宽松），
// 只有显式写反（tun ↔ mixed）才报错。写入路径由 server.validateDocBytes 统一调用。
func (d *Doc) ValidateConsistency() error {
	mode := strings.ToLower(strings.TrimSpace(d.ProxyMode()))
	switch mode {
	case ProxyModeTun, ProxyModeMixed:
	default:
		// proxy-mode 的合法性问题由 ValidateProxyMode 负责
		return nil
	}
	wantTun := mode == ProxyModeTun
	if got := d.GetBool(wantTun, "tun", "enable"); got != wantTun {
		return fmt.Errorf("tun.enable=%v 与 x-mproxy.proxy-mode=%s 不一致：%s 模式要求 tun.enable=%v", got, mode, mode, wantTun)
	}
	return nil
}

// CriticalChanges 比较两份配置中需要重启内核才能生效的字段。
func CriticalChanges(oldDoc, newDoc *Doc) []string {
	var changed []string
	compare := func(label string, path ...string) {
		ov, _ := oldDoc.Get(path...)
		nv, _ := newDoc.Get(path...)
		if !valueEqual(ov, nv) {
			changed = append(changed, label)
		}
	}
	compare("external-controller", "external-controller")
	compare("secret", "secret")
	compare("mixed-port", "mixed-port")
	compare("socks-port", "socks-port")
	compare("port", "port")
	compare("allow-lan", "allow-lan")
	compare("tun.enable", "tun", "enable")
	compare("tun.stack", "tun", "stack")
	compare("proxy-mode", "x-mproxy", "proxy-mode")
	compare("mode", "mode")
	return changed
}

func valueEqual(a, b any) bool {
	switch av := a.(type) {
	case nil:
		return b == nil
	case string:
		bv, ok := b.(string)
		return ok && strings.TrimSpace(av) == strings.TrimSpace(bv)
	case bool:
		bv, ok := b.(bool)
		return ok && av == bv
	case int:
		return toInt(b) == int64(av)
	case int64:
		return toInt(b) == av
	case float64:
		return toFloat(b) == av
	}
	return fmt.Sprintf("%v", a) == fmt.Sprintf("%v", b)
}

func toInt(v any) int64 {
	switch t := v.(type) {
	case int:
		return int64(t)
	case int64:
		return t
	case float64:
		return int64(t)
	}
	return 0
}

func toFloat(v any) float64 {
	switch t := v.(type) {
	case int:
		return float64(t)
	case int64:
		return float64(t)
	case float64:
		return t
	}
	return 0
}
