package config

import (
	"fmt"
	"sort"
	"strings"
)

// 合法 proxy-mode 值。
const (
	ProxyModeTun    = "tun"
	ProxyModeMixed  = "mixed"
	ProxyModeTProxy = "tproxy"
)

// 合法 update-channel 值。
const (
	ChannelStable = "stable"
	ChannelAlpha  = "alpha"
)

// 合法 private-dns 值：auto 表示 TUN 运行时临时关闭系统 Private DNS，keep 表示不修改。
const (
	PrivateDNSAuto = "auto"
	PrivateDNSKeep = "keep"
)

// Settings 对应配置中的 x-mproxy 段。
type Settings struct {
	Version       int    `json:"version"`
	AutoStart     bool   `json:"autoStart"`
	ProxyMode     string `json:"proxyMode"`
	UpdateChannel string `json:"updateChannel"`
	Ghproxy       string `json:"ghproxy"`
	Onboarded     bool   `json:"onboarded"`
	PrivateDNS    string `json:"privateDNS"`
	IPv6          bool   `json:"ipv6"`
}

// SettingsPatch 是 PATCH /api/mproxy/settings 的请求体（指针区分“未提供”）。
type SettingsPatch struct {
	Version       *int    `json:"version,omitempty"`
	AutoStart     *bool   `json:"autoStart,omitempty"`
	ProxyMode     *string `json:"proxyMode,omitempty"`
	UpdateChannel *string `json:"updateChannel,omitempty"`
	Ghproxy       *string `json:"ghproxy,omitempty"`
	Onboarded     *bool   `json:"onboarded,omitempty"`
	TunEnabled    *bool   `json:"tunEnabled,omitempty"`
	PrivateDNS    *string `json:"privateDNS,omitempty"`
	IPv6          *bool   `json:"ipv6,omitempty"`
}

// DefaultSettings 返回 x-mproxy 的默认设置。
func DefaultSettings() Settings {
	return Settings{
		Version:       1,
		AutoStart:     true,
		ProxyMode:     ProxyModeTun,
		UpdateChannel: ChannelStable,
		Ghproxy:       "",
		Onboarded:     false,
		PrivateDNS:    PrivateDNSAuto,
		IPv6:          false,
	}
}

// Settings 读取 x-mproxy 段，缺失字段使用默认值。
func (d *Doc) Settings() Settings {
	s := DefaultSettings()
	// 兼容旧配置：x-mproxy 缺失（或没有 ipv6 字段）时沿用顶层 ipv6。
	s.IPv6 = d.GetBool(false, "ipv6")
	if _, ok := d.Get("x-mproxy"); !ok {
		return s
	}
	s.Version = d.GetInt(s.Version, "x-mproxy", "version")
	s.AutoStart = d.GetBool(s.AutoStart, "x-mproxy", "auto-start")
	if v := d.GetString(s.ProxyMode, "x-mproxy", "proxy-mode"); v != "" {
		s.ProxyMode = v
	}
	if v := d.GetString(s.UpdateChannel, "x-mproxy", "update-channel"); v != "" {
		s.UpdateChannel = v
	}
	s.Ghproxy = d.GetString("", "x-mproxy", "ghproxy")
	s.Onboarded = d.GetBool(false, "x-mproxy", "onboarded")
	s.PrivateDNS = normalizePrivateDNS(d.GetString(s.PrivateDNS, "x-mproxy", "private-dns"))
	s.IPv6 = d.GetBool(s.IPv6, "x-mproxy", "ipv6")
	return s
}

// normalizePrivateDNS 归一化 private-dns：合法值转小写，缺失或未知值回退 auto。
// 读取旧配置时保持宽松，校验交给 ValidatePrivateDNS。
func normalizePrivateDNS(v string) string {
	if strings.EqualFold(strings.TrimSpace(v), PrivateDNSKeep) {
		return PrivateDNSKeep
	}
	return PrivateDNSAuto
}

// ValidateProxyMode 校验 proxy-mode（v0.1 拒绝 tproxy）。
func ValidateProxyMode(mode string) error {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case ProxyModeTun, ProxyModeMixed:
		return nil
	case ProxyModeTProxy:
		return fmt.Errorf("v0.1 暂不支持 tproxy 模式")
	case "":
		return fmt.Errorf("proxy-mode 不能为空")
	default:
		return fmt.Errorf("未知的 proxy-mode: %s", mode)
	}
}

// ValidateChannel 校验更新通道。
func ValidateChannel(channel string) error {
	switch strings.ToLower(strings.TrimSpace(channel)) {
	case ChannelStable, ChannelAlpha:
		return nil
	default:
		return fmt.Errorf("未知的更新通道: %s", channel)
	}
}

// ValidatePrivateDNS 校验 private-dns（大小写不敏感的 auto / keep）。
func ValidatePrivateDNS(v string) error {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case PrivateDNSAuto, PrivateDNSKeep:
		return nil
	default:
		return fmt.Errorf("private-dns 只能是 auto 或 keep")
	}
}

// ApplySettings 把 PATCH 子集写回 x-mproxy 与 tun.enable，返回是否有变更。
func (d *Doc) ApplySettings(p SettingsPatch) (bool, error) {
	changed := false
	if _, ok := d.Get("x-mproxy"); !ok {
		d.SetTop("x-mproxy", map[string]any{
			"version":        DefaultSettings().Version,
			"auto-start":     DefaultSettings().AutoStart,
			"proxy-mode":     DefaultSettings().ProxyMode,
			"update-channel": DefaultSettings().UpdateChannel,
			"ghproxy":        "",
			"onboarded":      false,
			"private-dns":    DefaultSettings().PrivateDNS,
			"ipv6":           DefaultSettings().IPv6,
		})
	}
	if p.Version != nil && *p.Version != d.GetInt(1, "x-mproxy", "version") {
		d.Set([]string{"x-mproxy", "version"}, *p.Version)
		changed = true
	}
	if p.AutoStart != nil && *p.AutoStart != d.GetBool(true, "x-mproxy", "auto-start") {
		d.Set([]string{"x-mproxy", "auto-start"}, *p.AutoStart)
		changed = true
	}
	if p.ProxyMode != nil {
		mode := strings.ToLower(strings.TrimSpace(*p.ProxyMode))
		if err := ValidateProxyMode(mode); err != nil {
			return false, err
		}
		if mode != d.GetString(ProxyModeTun, "x-mproxy", "proxy-mode") {
			d.Set([]string{"x-mproxy", "proxy-mode"}, mode)
			changed = true
		}
		// tun.enable 与 proxy-mode 保持一致
		wantTun := mode == ProxyModeTun
		if d.GetBool(true, "tun", "enable") != wantTun {
			d.Set([]string{"tun", "enable"}, wantTun)
			changed = true
		}
	}
	if p.TunEnabled != nil {
		if d.GetBool(true, "tun", "enable") != *p.TunEnabled {
			d.Set([]string{"tun", "enable"}, *p.TunEnabled)
			changed = true
		}
		mode := ProxyModeMixed
		if *p.TunEnabled {
			mode = ProxyModeTun
		}
		if d.GetString(ProxyModeTun, "x-mproxy", "proxy-mode") != mode {
			d.Set([]string{"x-mproxy", "proxy-mode"}, mode)
			changed = true
		}
	}
	if p.UpdateChannel != nil {
		ch := strings.ToLower(strings.TrimSpace(*p.UpdateChannel))
		if err := ValidateChannel(ch); err != nil {
			return false, err
		}
		if ch != d.GetString(ChannelStable, "x-mproxy", "update-channel") {
			d.Set([]string{"x-mproxy", "update-channel"}, ch)
			changed = true
		}
	}
	if p.Ghproxy != nil {
		gh := strings.TrimSpace(*p.Ghproxy)
		if gh != "" && !strings.HasPrefix(gh, "http://") && !strings.HasPrefix(gh, "https://") {
			return false, fmt.Errorf("ghproxy 必须是 http(s) 地址")
		}
		if gh != "" && !strings.HasSuffix(gh, "/") {
			gh += "/"
		}
		if gh != d.GetString("", "x-mproxy", "ghproxy") {
			d.Set([]string{"x-mproxy", "ghproxy"}, gh)
			changed = true
		}
	}
	if p.Onboarded != nil && *p.Onboarded != d.GetBool(false, "x-mproxy", "onboarded") {
		d.Set([]string{"x-mproxy", "onboarded"}, *p.Onboarded)
		changed = true
	}
	if p.PrivateDNS != nil {
		val := strings.ToLower(strings.TrimSpace(*p.PrivateDNS))
		if err := ValidatePrivateDNS(val); err != nil {
			return false, err
		}
		if val != d.GetString(PrivateDNSAuto, "x-mproxy", "private-dns") {
			d.Set([]string{"x-mproxy", "private-dns"}, val)
			changed = true
		}
	}
	if p.IPv6 != nil {
		// x-mproxy.ipv6 是唯一开关：同步写入 mihomo 的顶层 ipv6 与 dns.ipv6。
		val := *p.IPv6
		if d.GetBool(false, "x-mproxy", "ipv6") != val {
			d.Set([]string{"x-mproxy", "ipv6"}, val)
			changed = true
		}
		if d.GetBool(false, "ipv6") != val {
			d.Set([]string{"ipv6"}, val)
			changed = true
		}
		if d.GetBool(false, "dns", "ipv6") != val {
			d.Set([]string{"dns", "ipv6"}, val)
			changed = true
		}
	}
	return changed, nil
}

// AppsMode 是应用分流模式。
type AppsMode string

const (
	AppsAll       AppsMode = "all"
	AppsWhitelist AppsMode = "whitelist"
	AppsBlacklist AppsMode = "blacklist"
)

// Apps 返回当前应用分流模式与应用列表。
func (d *Doc) Apps() (AppsMode, []string) {
	include := d.GetStringSlice("tun", "include-package")
	exclude := d.GetStringSlice("tun", "exclude-package")
	mode := AppsAll
	if len(include) > 0 {
		mode = AppsWhitelist
	} else if len(exclude) > 0 {
		mode = AppsBlacklist
	}
	all := append(append([]string{}, include...), exclude...)
	return mode, sortStrings(all)
}

// SetApps 按模式写入 tun.include-package / tun.exclude-package。
func (d *Doc) SetApps(mode AppsMode, apps []string) error {
	apps = sortStrings(apps)
	switch mode {
	case AppsAll:
		d.Delete("tun", "include-package")
		d.Delete("tun", "exclude-package")
	case AppsWhitelist:
		d.Delete("tun", "exclude-package")
		if len(apps) == 0 {
			return fmt.Errorf("白名单模式至少需要一个应用")
		}
		d.Set([]string{"tun", "include-package"}, toStringAnySlice(apps))
	case AppsBlacklist:
		d.Delete("tun", "include-package")
		if len(apps) == 0 {
			return fmt.Errorf("黑名单模式至少需要一个应用")
		}
		d.Set([]string{"tun", "exclude-package"}, toStringAnySlice(apps))
	default:
		return fmt.Errorf("未知的应用分流模式: %s", mode)
	}
	// 确保 tun mapping 存在（mihomo 要求 tun.enable 是布尔）
	if mode != AppsAll {
		if !d.Has("tun", "enable") {
			d.Set([]string{"tun", "enable"}, true)
		}
	}
	return nil
}

// ProviderInfo 描述一个 proxy-provider。
type ProviderInfo struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	URL      string `json:"url"`
	Path     string `json:"path"`
	Interval int    `json:"interval"`
}

// ProviderSpec 是新增 proxy-provider 的请求。
type ProviderSpec struct {
	Name     string `json:"name"`
	URL      string `json:"url"`
	Interval int    `json:"interval,omitempty"`
	Path     string `json:"path,omitempty"`
}

// Providers 返回全部 proxy-provider（按名称排序）。
func (d *Doc) Providers() []ProviderInfo {
	v, ok := d.Get("proxy-providers")
	if !ok {
		return []ProviderInfo{}
	}
	m, ok := v.(map[string]any)
	if !ok {
		return []ProviderInfo{}
	}
	out := make([]ProviderInfo, 0, len(m))
	for name, raw := range m {
		info := ProviderInfo{Name: name}
		if pm, ok := raw.(map[string]any); ok {
			info.Type, _ = pm["type"].(string)
			info.URL, _ = pm["url"].(string)
			info.Path, _ = pm["path"].(string)
			switch iv := pm["interval"].(type) {
			case int:
				info.Interval = iv
			case int64:
				info.Interval = int(iv)
			case float64:
				info.Interval = int(iv)
			}
		}
		out = append(out, info)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// AddProvider 新增（或覆盖）proxy-provider。
func (d *Doc) AddProvider(spec ProviderSpec) error {
	name := strings.TrimSpace(spec.Name)
	if name == "" {
		return fmt.Errorf("provider 名称不能为空")
	}
	if strings.ContainsAny(name, "/\\") {
		return fmt.Errorf("provider 名称不能包含路径分隔符")
	}
	url := strings.TrimSpace(spec.URL)
	if url == "" {
		return fmt.Errorf("provider URL 不能为空")
	}
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		return fmt.Errorf("provider URL 必须是 http(s) 地址")
	}
	interval := spec.Interval
	if interval <= 0 {
		interval = 86400
	}
	path := strings.TrimSpace(spec.Path)
	if path == "" {
		path = fmt.Sprintf("./providers/%s.yaml", name)
	}
	entry := map[string]any{
		"type":     "http",
		"url":      url,
		"interval": interval,
		"path":     path,
		"health-check": map[string]any{
			"enable":   true,
			"url":      "https://www.gstatic.com/generate_204",
			"interval": 300,
		},
	}
	providers := map[string]any{}
	if v, ok := d.Get("proxy-providers"); ok {
		if m, ok := v.(map[string]any); ok {
			providers = m
		}
	}
	providers[name] = entry
	d.SetTop("proxy-providers", providers)
	// 开箱即用：新增 provider 后自动挂到 PROXY 组，用户不必再手工建组/加节点。
	d.attachProviderToProxyGroup(name)
	return nil
}

// attachProviderToProxyGroup 把新 provider 加入 PROXY 组的 use 列表；如果 PROXY
// 还是默认的“只有 DIRECT”，改为引用自动选择组，避免默认落到订阅里的占位节点
// （部分机场第一个节点是 server: 0.0.0.0 的流量信息节点）。
func (d *Doc) attachProviderToProxyGroup(name string) {
	d.EnsureProviderRouting()
}

// EnsureQUICFallback 在规则末尾插入“拒绝 UDP 443”，强制浏览器从 QUIC 回退 TCP。
// 部分机场 TCP 可用但 UDP 转发不通，浏览器会长时间卡在 QUIC 上。
func (d *Doc) EnsureQUICFallback() bool {
	rules := d.GetStringSlice("rules")
	for _, r := range rules {
		compact := strings.ReplaceAll(strings.ToUpper(r), " ", "")
		if strings.Contains(compact, "NETWORK,UDP") && strings.Contains(compact, "DST-PORT,443") {
			return false
		}
	}
	const quicRule = "AND,((NETWORK,udp),(DST-PORT,443)),REJECT"
	idx := len(rules)
	for i, r := range rules {
		if strings.HasPrefix(strings.ToUpper(strings.TrimSpace(r)), "MATCH,") {
			idx = i
			break
		}
	}
	rules = append(rules, "")
	copy(rules[idx+1:], rules[idx:])
	rules[idx] = quicRule
	d.SetTop("rules", toStringAnySlice(rules))
	return true
}

// EnsureProviderRouting 保证“有 proxy-provider 就能直接用”：
// PROXY 组会引用 provider，且在 PROXY 没有显式节点时使用自动选择组。
func (d *Doc) EnsureProviderRouting() bool {
	providerNames := d.providerNames()
	if len(providerNames) == 0 {
		return false
	}
	groupsVal, ok := d.Get("proxy-groups")
	groups, ok := groupsVal.([]any)
	if !ok {
		groups = []any{}
	}
	changed := false
	needAuto := false
	for i, g := range groups {
		gm, ok := g.(map[string]any)
		if !ok {
			continue
		}
		if groupName, _ := gm["name"].(string); groupName != "PROXY" {
			continue
		}
		uses := toStringSlice(gm["use"])
		useSet := map[string]bool{}
		for _, u := range uses {
			useSet[u] = true
		}
		for _, p := range providerNames {
			if !useSet[p] {
				uses = append(uses, p)
				useSet[p] = true
				changed = true
			}
		}
		gm["use"] = toStringAnySlice(uses)
		members := toStringSlice(gm["proxies"])
		if len(members) == 0 || (len(members) == 1 && members[0] == "DIRECT") {
			gm["proxies"] = toStringAnySlice([]string{"自动选择"})
			needAuto = true
			changed = true
		}
		groups[i] = gm
		d.SetTop("proxy-groups", groups)
		if needAuto {
			d.ensureAutoSelectGroup(providerNames)
		}
		return changed
	}
	d.SetTop("proxy-groups", append(groups, map[string]any{
		"name":    "PROXY",
		"type":    "select",
		"proxies": toStringAnySlice([]string{"自动选择"}),
		"use":     toStringAnySlice(providerNames),
	}))
	d.ensureAutoSelectGroup(providerNames)
	return true
}

// ensureAutoSelectGroup 创建/更新顶层 url-test 组，用 provider 的健康检查自动选节点。
func (d *Doc) ensureAutoSelectGroup(providerNames []string) string {
	const autoName = "自动选择"
	groupsVal, _ := d.Get("proxy-groups")
	groups, _ := groupsVal.([]any)
	for i, g := range groups {
		gm, ok := g.(map[string]any)
		if !ok {
			continue
		}
		if groupName, _ := gm["name"].(string); groupName != autoName {
			continue
		}
		uses := toStringSlice(gm["use"])
		useSet := map[string]bool{}
		for _, u := range uses {
			useSet[u] = true
		}
		added := false
		for _, p := range providerNames {
			if !useSet[p] {
				uses = append(uses, p)
				useSet[p] = true
				added = true
			}
		}
		if added {
			gm["use"] = toStringAnySlice(uses)
			groups[i] = gm
			d.SetTop("proxy-groups", groups)
		}
		return autoName
	}
	d.SetTop("proxy-groups", append(groups, map[string]any{
		"name":      autoName,
		"type":      "url-test",
		"url":       "https://www.gstatic.com/generate_204",
		"interval":  300,
		"tolerance": 50,
		"use":       toStringAnySlice(providerNames),
	}))
	return autoName
}

// providerNames 返回已配置 proxy-provider 的名称（排序保证输出稳定）。
func (d *Doc) providerNames() []string {
	v, ok := d.Get("proxy-providers")
	if !ok {
		return nil
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	names := make([]string, 0, len(m))
	for name := range m {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// DeleteProvider 删除 proxy-provider，返回是否存在。
func (d *Doc) DeleteProvider(name string) bool {
	v, ok := d.Get("proxy-providers")
	if !ok {
		return false
	}
	m, ok := v.(map[string]any)
	if !ok {
		return false
	}
	if _, exists := m[name]; !exists {
		return false
	}
	delete(m, name)
	d.SetTop("proxy-providers", m)
	return true
}

// EnsureDefaults 补齐必要的默认键，保证 mihomo 与后端都能工作。
func (d *Doc) EnsureDefaults(secret string) {
	if !d.Has("x-mproxy") {
		d.SetTop("x-mproxy", map[string]any{
			"version":        1,
			"auto-start":     true,
			"proxy-mode":     ProxyModeTun,
			"update-channel": ChannelStable,
			"ghproxy":        "",
			"onboarded":      false,
			"private-dns":    PrivateDNSAuto,
			"ipv6":           false,
		})
	}
	if s := d.GetString("", "secret"); s == "" && secret != "" {
		d.SetTop("secret", secret)
	}
	if !d.Has("external-controller") {
		d.SetTop("external-controller", "127.0.0.1:9090")
	}
	if !d.Has("mode") {
		d.SetTop("mode", "rule")
	}
	if !d.Has("log-level") {
		d.SetTop("log-level", "info")
	}
}

// Controller 返回 external-controller 与 secret，controller 为空时使用默认地址。
func (d *Doc) Controller() (addr, secret string) {
	addr = strings.TrimSpace(d.GetString("", "external-controller"))
	if addr == "" {
		addr = "127.0.0.1:9090"
	}
	addr = strings.TrimPrefix(addr, "http://")
	addr = strings.TrimPrefix(addr, "https://")
	addr = strings.TrimSuffix(addr, "/")
	secret = d.GetString("", "secret")
	return addr, secret
}
