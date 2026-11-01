package config

import (
	"fmt"
	"net"
	"strings"
)

// Imported 描述一次导入的内容规模。
type Imported struct {
	Proxies   int    `json:"proxies"`
	Groups    int    `json:"groups"`
	Rules     int    `json:"rules"`
	Providers int    `json:"providers"`
	Kind      string `json:"kind"`
}

// DetectKind 粗判配置类型（clash / sing-box / unknown）。
func DetectKind(d *Doc) string {
	if d.Has("outbounds") || d.Has("inbounds") {
		return "sing-box"
	}
	for _, key := range []string{"proxies", "proxy-groups", "proxy-providers", "rules", "proxy-provider"} {
		if d.Has(key) {
			return "clash"
		}
	}
	return "unknown"
}

func countImported(d *Doc) Imported {
	imp := Imported{Kind: DetectKind(d)}
	imp.Proxies = d.listLen("proxies")
	imp.Groups = d.listLen("proxy-groups")
	imp.Rules = d.listLen("rules")
	imp.Providers = d.mapLen("proxy-providers")
	return imp
}

// Import 把订阅/完整配置合并或替换进当前配置；merge 保留现有节点、分组与 x-mproxy。
func Import(current *Doc, data []byte, mode string) (*Doc, Imported, []string, error) {
	imported, err := Parse(data)
	if err != nil {
		return nil, Imported{}, nil, err
	}
	if kind := DetectKind(imported); kind == "sing-box" {
		return nil, Imported{}, nil, fmt.Errorf("检测到 sing-box 配置，v0.1 仅支持 Clash/mihomo YAML")
	}
	imp := countImported(imported)
	warnings := []string{}
	var out *Doc
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", "merge":
		out = current.Clone()
		mergeInto(out, imported, &warnings)
		ensureProxyGroup(out, imported, &warnings)
	case "replace":
		x, _ := current.Get("x-mproxy")
		ctrlAddr, _ := current.Get("external-controller")
		secret, _ := current.Get("secret")
		out = imported.Clone()
		if x != nil {
			out.SetTop("x-mproxy", deepCopyValue(x))
		}
		if !out.Has("external-controller") && ctrlAddr != nil {
			out.SetTop("external-controller", ctrlAddr)
			warnings = append(warnings, "导入配置缺少 external-controller，已沿用当前控制器地址")
		}
		if !out.Has("secret") && secret != nil {
			out.SetTop("secret", secret)
			warnings = append(warnings, "导入配置缺少 secret，已沿用当前密钥")
		}
		out.EnsureDefaults("")
	default:
		return nil, Imported{}, nil, fmt.Errorf("未知的导入模式: %s", mode)
	}
	if verr := out.ValidateStructure(); verr != nil {
		warnings = append(warnings, "导入后的配置仍有告警: "+verr.Error())
	}
	// 导入后强制 tun.enable 与 x-mproxy.proxy-mode 一致（以模块设置为准）。
	if fixed, warning := ensureProxyModeConsistency(out); fixed {
		warnings = append(warnings, warning)
	}
	// allow-lan 暴露 + 空 secret 是高危组合，只告警不阻断。
	if warning := unsafeExposureWarning(out); warning != "" {
		warnings = append(warnings, warning)
	}
	return out, imp, warnings, nil
}

// ensureProxyModeConsistency 修正 tun.enable 与 x-mproxy.proxy-mode 的显式不一致。
// 以 x-mproxy.proxy-mode 为准（模块设置优先）；返回是否发生修正与中文警告。
func ensureProxyModeConsistency(d *Doc) (bool, string) {
	if d.ValidateConsistency() == nil {
		return false, ""
	}
	mode := strings.ToLower(strings.TrimSpace(d.ProxyMode()))
	wantTun := mode == ProxyModeTun
	d.Set([]string{"tun", "enable"}, wantTun)
	return true, fmt.Sprintf("导入配置中 tun.enable 与 x-mproxy.proxy-mode=%s 不一致，已自动修正为 tun.enable=%v", mode, wantTun)
}

// unsafeExposureWarning 检查“allow-lan + 非回环控制器 + 空 secret”的高危组合。
func unsafeExposureWarning(d *Doc) string {
	if !d.GetBool(false, "allow-lan") {
		return ""
	}
	if strings.TrimSpace(d.GetString("", "secret")) != "" {
		return ""
	}
	addr := d.GetString("", "external-controller")
	if isLoopbackController(addr) {
		return ""
	}
	return fmt.Sprintf("配置开启了 allow-lan，external-controller=%q 监听非回环地址且 secret 为空，同网段任何人都能控制内核；请设置 secret 或改回 127.0.0.1", addr)
}

// isLoopbackController 判断 external-controller 是否只监听回环地址。
func isLoopbackController(addr string) bool {
	addr = strings.TrimSpace(addr)
	addr = strings.TrimPrefix(addr, "http://")
	addr = strings.TrimPrefix(addr, "https://")
	host := addr
	if h, _, err := net.SplitHostPort(addr); err == nil {
		host = h
	}
	host = strings.Trim(strings.TrimSpace(host), "[]")
	if host == "" {
		// 空地址沿用默认回环控制器；":9090" 这类只写端口的形式会监听所有网卡。
		return !strings.HasPrefix(addr, ":")
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

// importMergeKeys 是 merge 模式允许从导入配置合并的顶层键（与导入规范一致）。
// 其余顶层键（尤其是 allow-lan / external-controller / secret / 端口 / tun / dns 等）
// 一律忽略，防止订阅内容改写本机安全敏感设置。
var importMergeKeys = map[string]bool{
	"proxies":         true,
	"proxy-groups":    true,
	"listeners":       true,
	"proxy-providers": true,
	"rule-providers":  true,
	"rules":           true,
	"sub-rules":       true,
}

// importSensitiveKeys 是 merge 模式显式忽略的安全敏感顶层键；命中时追加中文警告。
var importSensitiveKeys = []string{
	"allow-lan",
	"external-controller",
	"external-controller-tls",
	"secret",
	"port",
	"socks-port",
	"mixed-port",
	"bind-address",
	"authentication",
	"tun",
	"dns",
}

func mergeInto(dst, src *Doc, warnings *[]string) {
	if src.root == nil {
		return
	}
	for key, val := range src.root {
		if !importMergeKeys[key] {
			// 非白名单键一律忽略（x-mproxy 也在此列：模块设置永远以 MProxyEvolution 为准）
			continue
		}
		switch key {
		case "proxies", "proxy-groups", "listeners":
			dl, _ := dst.Get(key)
			dstList, _ := dl.([]any)
			srcList, _ := val.([]any)
			dst.SetTop(key, mergeNamedList(dstList, srcList, key, warnings))
		case "proxy-providers", "rule-providers":
			dm, _ := dst.Get(key)
			dstMap, _ := dm.(map[string]any)
			srcMap, _ := val.(map[string]any)
			dst.SetTop(key, mergeMap(deepCopyMap(dstMap), srcMap))
		case "rules", "sub-rules":
			// 规则顺序敏感：导入内容整体替换
			if l, ok := val.([]any); ok && len(l) > 0 {
				dst.SetTop(key, deepCopyValue(l))
			}
		default:
			if sm, ok := val.(map[string]any); ok {
				dm, _ := dst.Get(key)
				existing, _ := dm.(map[string]any)
				dst.SetTop(key, mergeMap(deepCopyMap(existing), sm))
				continue
			}
			dst.SetTop(key, deepCopyValue(val))
		}
	}
	// 明确告诉用户哪些安全敏感字段被忽略（固定顺序，输出稳定）。
	for _, key := range importSensitiveKeys {
		if src.Has(key) {
			*warnings = append(*warnings, "已忽略导入配置中的 "+key+"：该字段属于安全敏感设置，继续使用当前配置")
		}
	}
}

// mergeNamedList 按 name 合并列表：同名覆盖，新条目追加。
func mergeNamedList(dst, src []any, label string, warnings *[]string) []any {
	out := make([]any, 0, len(dst)+len(src))
	out = append(out, dst...)
	index := map[string]int{}
	for i, item := range out {
		if name := itemName(item); name != "" {
			index[name] = i
		}
	}
	replaced := 0
	for _, item := range src {
		name := itemName(item)
		if name == "" {
			out = append(out, deepCopyValue(item))
			continue
		}
		if i, exists := index[name]; exists {
			out[i] = deepCopyValue(item)
			replaced++
			continue
		}
		index[name] = len(out)
		out = append(out, deepCopyValue(item))
	}
	if replaced > 0 && label == "proxies" {
		*warnings = append(*warnings, fmt.Sprintf("已覆盖 %d 个同名节点", replaced))
	}
	return out
}

func itemName(v any) string {
	m, ok := v.(map[string]any)
	if !ok {
		return ""
	}
	name, _ := m["name"].(string)
	return strings.TrimSpace(name)
}

// ensureProxyGroup 把导入的节点/provider 追加到 PROXY 组，避免 MATCH,PROXY 无可选项。
func ensureProxyGroup(dst, imported *Doc, warnings *[]string) {
	var proxyNames []string
	if v, ok := imported.Get("proxies"); ok {
		if list, ok := v.([]any); ok {
			for _, item := range list {
				if name := itemName(item); name != "" {
					proxyNames = append(proxyNames, name)
				}
			}
		}
	}
	var providerNames []string
	if v, ok := imported.Get("proxy-providers"); ok {
		if m, ok := v.(map[string]any); ok {
			for name := range m {
				providerNames = append(providerNames, name)
			}
		}
	}
	if len(proxyNames) == 0 && len(providerNames) == 0 {
		return
	}
	groupsVal, ok := dst.Get("proxy-groups")
	if !ok {
		groupsVal = []any{}
	}
	groups, ok := groupsVal.([]any)
	if !ok {
		return
	}
	proxySet := map[string]bool{}
	for _, n := range proxyNames {
		proxySet[n] = true
	}
	for i, g := range groups {
		gm, ok := g.(map[string]any)
		if !ok {
			continue
		}
		name, _ := gm["name"].(string)
		if name != "PROXY" {
			continue
		}
		typ, _ := gm["type"].(string)
		if typ == "" {
			typ = "select"
		}
		members := toStringSlice(gm["proxies"])
		memberSet := map[string]bool{}
		for _, m := range members {
			memberSet[m] = true
		}
		for _, n := range proxyNames {
			if !memberSet[n] {
				members = append(members, n)
				memberSet[n] = true
			}
		}
		if len(providerNames) > 0 && len(members) == 1 && members[0] == "DIRECT" {
			members = nil
		}
		if len(members) > 0 {
			gm["proxies"] = toStringAnySlice(members)
		} else {
			delete(gm, "proxies")
		}
		if len(providerNames) > 0 {
			uses := toStringSlice(gm["use"])
			useSet := map[string]bool{}
			for _, u := range uses {
				useSet[u] = true
			}
			for _, p := range providerNames {
				if !useSet[p] {
					uses = append(uses, p)
					useSet[p] = true
				}
			}
			gm["use"] = toStringAnySlice(uses)
		}
		groups[i] = gm
		return
	}
	// 没有 PROXY 组则创建一个
	newGroup := map[string]any{
		"name": "PROXY",
		"type": "select",
	}
	if len(proxyNames) == 0 && len(providerNames) > 0 {
		// 只有 provider 时直接用 provider 里的节点，避免默认落到 DIRECT。
		newGroup["use"] = toStringAnySlice(providerNames)
	} else {
		newGroup["proxies"] = toStringAnySlice(append([]string{"DIRECT"}, proxyNames...))
		if len(providerNames) > 0 {
			newGroup["use"] = toStringAnySlice(providerNames)
		}
	}
	dst.SetTop("proxy-groups", append(groups, newGroup))
	*warnings = append(*warnings, "导入配置没有 PROXY 组，已自动创建")
	dst.EnsureProviderRouting()
}
