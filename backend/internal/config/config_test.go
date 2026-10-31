package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mproxy/internal/sysutil"
)

func testPaths(t *testing.T) sysutil.Paths {
	t.Helper()
	dir := t.TempDir()
	return sysutil.Paths{DataDir: dir, ModuleDir: dir}
}

func TestDefaultConfigValid(t *testing.T) {
	doc, err := NewDefault("test-secret")
	if err != nil {
		t.Fatalf("生成默认配置失败: %v", err)
	}
	if err := doc.ValidateStructure(); err != nil {
		t.Fatalf("默认配置结构校验失败: %v", err)
	}
	if got := doc.GetString("", "secret"); got != "test-secret" {
		t.Fatalf("secret = %q, want test-secret", got)
	}
	summary := doc.Summary()
	if summary.ProxyMode != ProxyModeTun || !summary.AutoStart || !summary.TunEnabled {
		t.Fatalf("x-mproxy 默认值不正确: %+v", summary)
	}
	if summary.RuleCount == 0 || summary.GroupCount == 0 {
		t.Fatalf("默认配置应包含规则与 PROXY 分组: %+v", summary)
	}
	if mode, _ := doc.Apps(); mode != AppsAll {
		t.Fatalf("默认应用分流应为 all，得到 %s", mode)
	}
}

func TestParseAndSummary(t *testing.T) {
	yaml := `
mixed-port: 7890
socks-port: 7891
port: 8080
allow-lan: true
ipv6: true
mode: global
log-level: debug
tun:
  enable: false
  stack: system
proxies:
  - {name: a, type: socks5, server: 127.0.0.1, port: 1080}
  - {name: b, type: socks5, server: 127.0.0.1, port: 1081}
proxy-groups:
  - {name: PROXY, type: select, proxies: [a, b]}
proxy-providers:
  sub:
    type: http
    url: https://example.com/sub
rules:
  - MATCH,PROXY
x-mproxy:
  version: 1
  auto-start: false
  proxy-mode: mixed
`
	doc, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	s := doc.Summary()
	if s.MixedPort != 7890 || s.SocksPort != 7891 || s.Port != 8080 {
		t.Fatalf("端口摘要错误: %+v", s)
	}
	if !s.AllowLan || !s.IPv6 || s.Mode != "global" || s.LogLevel != "debug" {
		t.Fatalf("基础字段摘要错误: %+v", s)
	}
	if s.ProxyMode != ProxyModeMixed || s.AutoStart {
		t.Fatalf("x-mproxy 摘要错误: %+v", s)
	}
	if s.ProxyCount != 2 || s.GroupCount != 1 || s.RuleCount != 1 || s.ProviderCount != 1 {
		t.Fatalf("数量摘要错误: %+v", s)
	}
}

func TestValidateStructureErrors(t *testing.T) {
	cases := []string{
		"mixed-port: 70000",
		"mode: weird",
		"log-level: verbose",
		"x-mproxy: {proxy-mode: tproxy}",
		"proxy-groups:\n  - type: select\n",
	}
	for _, yaml := range cases {
		doc, err := Parse([]byte(yaml))
		if err != nil {
			t.Fatalf("解析 %q 失败: %v", yaml, err)
		}
		if err := doc.ValidateStructure(); err == nil {
			t.Fatalf("期望 %q 校验失败，实际通过", yaml)
		}
	}
	if res := ValidateText([]byte("not: [valid")); res.OK {
		t.Fatal("非法 YAML 应校验失败")
	}
}

func TestMergePatch(t *testing.T) {
	doc, err := NewDefault("s")
	if err != nil {
		t.Fatal(err)
	}
	doc.MergePatch(map[string]any{
		"mixed-port": int64(7899),
		"dns":        map[string]any{"enable": false, "enhanced-mode": "redir-host"},
	})
	s := doc.Summary()
	if s.MixedPort != 7899 {
		t.Fatalf("patch 后 mixed-port = %d", s.MixedPort)
	}
	if doc.GetBool(true, "dns", "enable") {
		t.Fatal("patch 后 dns.enable 应为 false")
	}
	if got := doc.GetString("", "dns", "enhanced-mode"); got != "redir-host" {
		t.Fatalf("dns.enhanced-mode = %q", got)
	}
	// 未被 patch 的嵌套字段应保留
	if got := doc.GetString("", "dns", "fake-ip-range"); got == "" {
		t.Fatal("patch 不应清空 dns 其他字段")
	}
	if _, ok := doc.Get("dns", "fallback"); !ok {
		t.Fatal("patch 不应删除 dns.fallback")
	}
}

func TestCriticalChanges(t *testing.T) {
	oldDoc, _ := NewDefault("s")
	newDoc := oldDoc.Clone()
	if changes := CriticalChanges(oldDoc, newDoc); len(changes) != 0 {
		t.Fatalf("相同配置不应有关键变化: %v", changes)
	}
	newDoc.Set([]string{"mixed-port"}, 7999)
	changes := CriticalChanges(oldDoc, newDoc)
	found := false
	for _, c := range changes {
		if c == "mixed-port" {
			found = true
		}
	}
	if !found {
		t.Fatalf("应检测到 mixed-port 变化: %v", changes)
	}
	newDoc.Set([]string{"dns", "enable"}, false)
	if changes := CriticalChanges(oldDoc, newDoc); len(changes) != 1 {
		t.Fatalf("dns 变化不应触发重启: %v", changes)
	}
}

const importedYAML = `
proxies:
  - {name: 导入节点, type: socks5, server: 1.2.3.4, port: 1080}
  - {name: 导入节点2, type: socks5, server: 1.2.3.5, port: 1080}
proxy-groups:
  - {name: PROXY, type: select, proxies: [导入节点, 导入节点2]}
rules:
  - DOMAIN-SUFFIX,example.com,PROXY
  - MATCH,PROXY
proxy-providers:
  imported-provider:
    type: http
    url: https://example.com/sub.yaml
`

func TestImportMerge(t *testing.T) {
	current, _ := NewDefault("s")
	out, imported, warnings, err := Import(current, []byte(importedYAML), "merge")
	if err != nil {
		t.Fatalf("merge 导入失败: %v", err)
	}
	if imported.Proxies != 2 || imported.Groups != 1 || imported.Rules != 2 || imported.Providers != 1 {
		t.Fatalf("导入计数错误: %+v", imported)
	}
	if imported.Kind != "clash" {
		t.Fatalf("kind = %q", imported.Kind)
	}
	s := out.Summary()
	if s.ProxyCount != 2 {
		t.Fatalf("合并后节点数 = %d，期望 2", s.ProxyCount)
	}
	if s.RuleCount != 2 {
		t.Fatalf("规则应被导入替换，实际 %d", s.RuleCount)
	}
	if !out.Has("x-mproxy") {
		t.Fatal("merge 导入必须保留 x-mproxy")
	}
	if got := out.GetString("", "x-mproxy", "proxy-mode"); got != ProxyModeTun {
		t.Fatalf("x-mproxy.proxy-mode 被改动: %q", got)
	}
	groups, _ := out.Get("proxy-groups")
	list, _ := groups.([]any)
	if len(list) != 1 {
		t.Fatalf("分组数 = %d", len(list))
	}
	gm, _ := list[0].(map[string]any)
	members := toStringSlice(gm["proxies"])
	if len(members) != 2 || members[0] != "导入节点" {
		t.Fatalf("PROXY 组成员错误: %v", members)
	}
	if len(warnings) != 0 {
		t.Logf("合并警告: %v", warnings)
	}
}

func TestImportReplaceKeepsXMPRoxy(t *testing.T) {
	current, _ := NewDefault("keep-secret")
	current.Set([]string{"x-mproxy", "onboarded"}, true)
	out, imported, _, err := Import(current, []byte(importedYAML), "replace")
	if err != nil {
		t.Fatalf("replace 导入失败: %v", err)
	}
	if imported.Proxies != 2 {
		t.Fatalf("导入计数错误: %+v", imported)
	}
	if got := out.GetBool(false, "x-mproxy", "onboarded"); !got {
		t.Fatal("replace 导入必须保留 x-mproxy")
	}
	if got := out.GetString("", "secret"); got != "keep-secret" {
		t.Fatalf("replace 导入应沿用原 secret，得到 %q", got)
	}
	if got := out.GetString("", "external-controller"); got != "127.0.0.1:9090" {
		t.Fatalf("replace 导入应补齐 external-controller，得到 %q", got)
	}
	if !out.Has("mixed-port") {
		// 导入内容没有端口时不应报错，但配置仍应可保存
		t.Log("导入配置未包含端口")
	}
	if err := out.ValidateStructure(); err != nil {
		t.Fatalf("replace 后配置不合法: %v", err)
	}
}

func TestImportSingBoxRejected(t *testing.T) {
	current, _ := NewDefault("s")
	_, _, _, err := Import(current, []byte("outbounds:\n  - {type: direct, tag: direct}\n"), "merge")
	if err == nil {
		t.Fatal("sing-box 配置应被拒绝")
	}
}

func TestValidateConsistency(t *testing.T) {
	doc, _ := NewDefault("s")
	if err := doc.ValidateConsistency(); err != nil {
		t.Fatalf("默认配置应一致: %v", err)
	}
	// proxy-mode=tun 但 tun.enable=false
	doc.Set([]string{"tun", "enable"}, false)
	if err := doc.ValidateConsistency(); err == nil {
		t.Fatal("tun.enable=false + proxy-mode=tun 应校验失败")
	}
	// proxy-mode=mixed 但 tun.enable=true
	doc.Set([]string{"tun", "enable"}, true)
	doc.Set([]string{"x-mproxy", "proxy-mode"}, ProxyModeMixed)
	if err := doc.ValidateConsistency(); err == nil {
		t.Fatal("tun.enable=true + proxy-mode=mixed 应校验失败")
	}
	// 缺失 tun.enable 视为一致（旧配置保持宽松）
	old, err := Parse([]byte("mixed-port: 7890\nx-mproxy: {proxy-mode: tun}\n"))
	if err != nil {
		t.Fatal(err)
	}
	if err := old.ValidateConsistency(); err != nil {
		t.Fatalf("缺失 tun.enable 不应报不一致: %v", err)
	}
}

func TestImportMergeIgnoresSensitiveKeys(t *testing.T) {
	current, err := NewDefault("keep-secret")
	if err != nil {
		t.Fatal(err)
	}
	content := `
allow-lan: true
external-controller: 0.0.0.0:9999
external-controller-tls: 0.0.0.0:9443
secret: hijacked
port: 1111
socks-port: 1112
mixed-port: 1113
bind-address: 0.0.0.0
authentication:
  - user:pass
tun:
  enable: false
dns:
  enable: false
listeners:
  - {name: l1, type: socks, port: 1234}
proxies:
  - {name: evil-node, type: socks5, server: 1.1.1.1, port: 1080}
rules:
  - MATCH,PROXY
`
	out, imported, warnings, err := Import(current, []byte(content), "merge")
	if err != nil {
		t.Fatalf("merge 导入失败: %v", err)
	}
	// 白名单内容仍然合并
	if imported.Proxies != 1 || imported.Rules != 1 {
		t.Fatalf("导入计数错误: %+v", imported)
	}
	if s := out.Summary(); s.ProxyCount != 1 || s.RuleCount != 1 {
		t.Fatalf("白名单内容未合并: %+v", s)
	}
	listeners, ok := out.Get("listeners")
	if !ok {
		t.Fatal("listeners 属于白名单，应被合并")
	}
	if list, _ := listeners.([]any); len(list) != 1 {
		t.Fatalf("listeners = %v", listeners)
	}
	// 安全敏感字段必须保持当前配置
	if out.GetBool(false, "allow-lan") {
		t.Fatal("allow-lan 不应被导入覆盖")
	}
	if got := out.GetString("", "external-controller"); got != "127.0.0.1:9090" {
		t.Fatalf("external-controller 被覆盖: %q", got)
	}
	if out.Has("external-controller-tls") {
		t.Fatal("external-controller-tls 不应被导入")
	}
	if got := out.GetString("", "secret"); got != "keep-secret" {
		t.Fatalf("secret 被覆盖: %q", got)
	}
	if got := out.GetInt(0, "mixed-port"); got != 7890 {
		t.Fatalf("mixed-port 被覆盖: %d", got)
	}
	if got := out.GetInt(0, "socks-port"); got != 7891 {
		t.Fatalf("socks-port 被覆盖: %d", got)
	}
	if got := out.GetInt(0, "port"); got != 0 {
		t.Fatalf("port 被覆盖: %d", got)
	}
	if got := out.GetString("", "bind-address"); got != "*" {
		t.Fatalf("bind-address 被覆盖: %q", got)
	}
	if out.Has("authentication") {
		t.Fatal("authentication 不应被导入")
	}
	if !out.GetBool(true, "tun", "enable") {
		t.Fatal("tun.enable 不应被导入覆盖")
	}
	if !out.GetBool(true, "dns", "enable") {
		t.Fatal("dns.enable 不应被导入覆盖")
	}
	// 每个被忽略的安全敏感键都必须有中文警告
	joined := strings.Join(warnings, "\n")
	for _, key := range importSensitiveKeys {
		if !strings.Contains(joined, key) {
			t.Fatalf("缺少 %s 的忽略警告，实际警告:\n%s", key, joined)
		}
	}
}

func TestImportReplaceWarnsUnsafeAndFixesProxyMode(t *testing.T) {
	current, err := NewDefault("keep-secret")
	if err != nil {
		t.Fatal(err)
	}
	// replace 模式：导入内容显式把 secret 置空、打开 allow-lan、控制器监听所有网卡，
	// 且 tun.enable 与当前 x-mproxy.proxy-mode=tun 相反。
	content := `
allow-lan: true
external-controller: 0.0.0.0:9090
secret: ""
tun:
  enable: false
proxy-groups:
  - {name: PROXY, type: select, proxies: [DIRECT]}
rules:
  - MATCH,PROXY
`
	out, _, warnings, err := Import(current, []byte(content), "replace")
	if err != nil {
		t.Fatalf("replace 导入失败: %v", err)
	}
	if got := out.GetString("", "secret"); got != "" {
		t.Fatalf("replace 模式应保留导入内容中的空 secret，得到 %q", got)
	}
	if !out.GetBool(false, "allow-lan") {
		t.Fatal("replace 模式应保留导入的 allow-lan")
	}
	// tun.enable 应被修正为与 proxy-mode=tun 一致
	if !out.GetBool(false, "tun", "enable") {
		t.Fatal("tun.enable 应被修正为 true")
	}
	if out.ProxyMode() != ProxyModeTun {
		t.Fatalf("proxy-mode = %q", out.ProxyMode())
	}
	if err := out.ValidateConsistency(); err != nil {
		t.Fatalf("修正后应一致: %v", err)
	}
	if err := out.ValidateStructure(); err != nil {
		t.Fatalf("replace 后配置不合法: %v", err)
	}
	joined := strings.Join(warnings, "\n")
	if !strings.Contains(joined, "tun.enable") {
		t.Fatalf("缺少 tun.enable 自动修正警告:\n%s", joined)
	}
	if !strings.Contains(joined, "allow-lan") || !strings.Contains(joined, "secret") {
		t.Fatalf("缺少 allow-lan/空 secret 安全警告:\n%s", joined)
	}
}

func TestOpenLenientOnTunProxyModeMismatch(t *testing.T) {
	paths := testPaths(t)
	if err := paths.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	// 已有的用户配置可能不一致；加载必须宽松，不能因为一致性检查失败而拒绝启动。
	yaml := `
x-mproxy: {version: 1, proxy-mode: mixed}
tun:
  enable: true
mixed-port: 7890
mode: rule
log-level: info
`
	if err := os.WriteFile(paths.ConfigPath(), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := Open(paths)
	if err != nil {
		t.Fatalf("打开已有配置失败: %v", err)
	}
	if m.LoadError() != nil {
		t.Fatalf("加载旧配置不应因 tun/proxy-mode 不一致报错: %v", m.LoadError())
	}
	doc, derr := m.Doc()
	if derr != nil {
		t.Fatalf("读取配置失败: %v", derr)
	}
	if err := doc.ValidateConsistency(); err == nil {
		t.Fatal("这份配置确实不一致，ValidateConsistency 应报错")
	}
}

func TestSaveBackupAndReload(t *testing.T) {
	paths := testPaths(t)
	if err := paths.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	m, err := Open(paths)
	if err != nil {
		t.Fatalf("打开配置失败: %v", err)
	}
	if _, err := os.Stat(paths.ConfigPath()); err != nil {
		t.Fatalf("默认配置应写入磁盘: %v", err)
	}
	doc, _ := m.Doc()
	doc.Set([]string{"mixed-port"}, 7000)
	if _, err := m.Save(doc); err != nil {
		t.Fatalf("保存失败: %v", err)
	}
	data, err := os.ReadFile(paths.ConfigPath())
	if err != nil || !strings.Contains(string(data), "7000") {
		t.Fatalf("保存后文件内容不正确: %v", err)
	}
	if _, err := os.Stat(paths.BackupPath()); err != nil {
		t.Fatalf("保存后应生成备份: %v", err)
	}
	backup, _ := os.ReadFile(paths.BackupPath())
	if strings.Contains(string(backup), "mixed-port: 7000") {
		t.Fatal("备份应是旧配置")
	}
	// 再保存一次，备份应更新为上一份
	doc2, _ := m.Doc()
	doc2.Set([]string{"mixed-port"}, 7001)
	if _, err := m.Save(doc2); err != nil {
		t.Fatal(err)
	}
	backup, _ = os.ReadFile(paths.BackupPath())
	if !strings.Contains(string(backup), "mixed-port: 7000") {
		t.Fatalf("第二次保存的备份应为上一份配置，实际:\n%s", backup)
	}
	// 外部修改后被 ReloadFromDisk 读取
	raw, _, _, _ := m.Raw()
	if err := os.WriteFile(paths.ConfigPath(), append(raw, []byte("\n# external edit\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := m.ReloadFromDisk(); err != nil {
		t.Fatalf("重载失败: %v", err)
	}
	if m.LoadError() != nil {
		t.Fatalf("重载后不应有加载错误: %v", m.LoadError())
	}
}

func TestInvalidConfigKeepsRaw(t *testing.T) {
	paths := testPaths(t)
	if err := paths.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	bad := []byte("mixed-port: [oops\n")
	if err := os.WriteFile(paths.ConfigPath(), bad, 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := Open(paths)
	if err != nil {
		t.Fatalf("打开无效配置不应返回致命错误: %v", err)
	}
	if m.LoadError() == nil {
		t.Fatal("应记录配置加载错误")
	}
	raw, _, exists, rerr := m.Raw()
	if rerr != nil {
		t.Fatalf("Raw 应仍可返回原始文本: %v", rerr)
	}
	if !exists || string(raw) != string(bad) {
		t.Fatal("原始文本应保留")
	}
	if _, derr := m.Doc(); derr == nil {
		t.Fatal("无效配置取 Doc 应报错")
	}
}

func TestSettingsAppsProviders(t *testing.T) {
	doc, _ := NewDefault("s")
	// settings
	auto := false
	mode := ProxyModeMixed
	gh := "https://ghfast.top"
	onboarded := true
	changed, err := doc.ApplySettings(SettingsPatch{
		AutoStart: &auto, ProxyMode: &mode, Ghproxy: &gh, Onboarded: &onboarded,
	})
	if err != nil || !changed {
		t.Fatalf("ApplySettings changed=%v err=%v", changed, err)
	}
	s := doc.Settings()
	if s.AutoStart || s.ProxyMode != ProxyModeMixed || s.Ghproxy != gh+"/" || !s.Onboarded {
		t.Fatalf("settings 写回错误: %+v", s)
	}
	if doc.GetBool(true, "tun", "enable") {
		t.Fatal("proxy-mode=mixed 应同步 tun.enable=false")
	}
	tproxy := "tproxy"
	if _, err := doc.ApplySettings(SettingsPatch{ProxyMode: &tproxy}); err == nil {
		t.Fatal("tproxy 应被拒绝")
	}
	// apps
	if err := doc.SetApps(AppsWhitelist, []string{"com.a", "com.b"}); err != nil {
		t.Fatal(err)
	}
	m, apps := doc.Apps()
	if m != AppsWhitelist || len(apps) != 2 {
		t.Fatalf("apps = %s %v", m, apps)
	}
	if got := doc.GetStringSlice("tun", "include-package"); len(got) != 2 {
		t.Fatalf("include-package = %v", got)
	}
	if err := doc.SetApps(AppsAll, nil); err != nil {
		t.Fatal(err)
	}
	if doc.Has("tun", "include-package") || doc.Has("tun", "exclude-package") {
		t.Fatal("all 模式应清空应用列表")
	}
	// providers
	if err := doc.AddProvider(ProviderSpec{Name: "sub", URL: "https://example.com/sub", Interval: 3600}); err != nil {
		t.Fatal(err)
	}
	providers := doc.Providers()
	if len(providers) != 1 || providers[0].Name != "sub" || providers[0].Interval != 3600 {
		t.Fatalf("providers = %+v", providers)
	}
	if !doc.DeleteProvider("sub") || len(doc.Providers()) != 0 {
		t.Fatal("删除 provider 失败")
	}
	if err := doc.AddProvider(ProviderSpec{Name: "bad", URL: "ftp://x"}); err == nil {
		t.Fatal("非法 provider URL 应被拒绝")
	}
}

func TestManagerSaveRejectsInvalid(t *testing.T) {
	paths := testPaths(t)
	m, err := Open(paths)
	if err != nil {
		t.Fatal(err)
	}
	bad, _ := Parse([]byte("mixed-port: 99999"))
	if _, err := m.Save(bad); err == nil {
		t.Fatal("结构非法的配置不应被保存")
	}
	if _, err := os.Stat(filepath.Join(paths.DataDir, "config.yaml.tmp")); !os.IsNotExist(err) {
		t.Fatal("保存失败后不应残留临时文件")
	}
}

func TestPrivateDNSSetting(t *testing.T) {
	doc, err := NewDefault("s")
	if err != nil {
		t.Fatal(err)
	}
	// 默认值为 auto，默认模板里带 private-dns: auto
	if got := doc.Settings().PrivateDNS; got != PrivateDNSAuto {
		t.Fatalf("默认 privateDNS = %q，期望 auto", got)
	}
	if !strings.Contains(defaultTemplate, "private-dns: auto") {
		t.Fatal("默认模板应包含 private-dns: auto")
	}
	if got := DefaultSettings().PrivateDNS; got != PrivateDNSAuto {
		t.Fatalf("DefaultSettings().PrivateDNS = %q", got)
	}
	// keep：大小写不敏感，写入时归一化为小写
	keep := "KEEP"
	changed, err := doc.ApplySettings(SettingsPatch{PrivateDNS: &keep})
	if err != nil || !changed {
		t.Fatalf("ApplySettings(keep) changed=%v err=%v", changed, err)
	}
	if got := doc.Settings().PrivateDNS; got != PrivateDNSKeep {
		t.Fatalf("privateDNS = %q，期望 keep", got)
	}
	if got := doc.GetString("", "x-mproxy", "private-dns"); got != PrivateDNSKeep {
		t.Fatalf("磁盘字段应归一化为小写，得到 %q", got)
	}
	// 相同值不产生变更
	keepAgain := "keep"
	if changed, err := doc.ApplySettings(SettingsPatch{PrivateDNS: &keepAgain}); err != nil || changed {
		t.Fatalf("重复设置 keep 不应变更: changed=%v err=%v", changed, err)
	}
	// auto：恢复默认
	auto := "AUTO"
	changed, err = doc.ApplySettings(SettingsPatch{PrivateDNS: &auto})
	if err != nil || !changed {
		t.Fatalf("ApplySettings(auto) changed=%v err=%v", changed, err)
	}
	if got := doc.Settings().PrivateDNS; got != PrivateDNSAuto {
		t.Fatalf("privateDNS = %q，期望 auto", got)
	}
	// 非法值拒绝且不写回
	for _, bad := range []string{"off", "", "strict ", "opportunistic"} {
		v := bad
		_, err := doc.ApplySettings(SettingsPatch{PrivateDNS: &v})
		if err == nil {
			t.Fatalf("private-dns=%q 应被拒绝", bad)
		}
		if !strings.Contains(err.Error(), "private-dns 只能是 auto 或 keep") {
			t.Fatalf("错误信息不符合预期: %v", err)
		}
	}
	if got := doc.Settings().PrivateDNS; got != PrivateDNSAuto {
		t.Fatalf("非法值不应写回，privateDNS = %q", got)
	}
	// 读取旧配置里的未知值时宽松回退 auto
	doc.Set([]string{"x-mproxy", "private-dns"}, "bogus")
	if got := doc.Settings().PrivateDNS; got != PrivateDNSAuto {
		t.Fatalf("未知值应宽松回退 auto，得到 %q", got)
	}
	// clone 独立，Summary 不受新字段影响
	doc.Set([]string{"x-mproxy", "private-dns"}, PrivateDNSAuto)
	clone := doc.Clone()
	keepClone := "keep"
	if _, err := clone.ApplySettings(SettingsPatch{PrivateDNS: &keepClone}); err != nil {
		t.Fatal(err)
	}
	if got := doc.Settings().PrivateDNS; got != PrivateDNSAuto {
		t.Fatalf("clone 修改不应影响原 doc，得到 %q", got)
	}
	if got := clone.Settings().PrivateDNS; got != PrivateDNSKeep {
		t.Fatalf("clone.privateDNS = %q", got)
	}
	summaryBefore := doc.Summary()
	doc.Set([]string{"x-mproxy", "private-dns"}, PrivateDNSKeep)
	if summaryAfter := doc.Summary(); summaryAfter != summaryBefore {
		t.Fatalf("Summary 不应包含 private-dns: %+v vs %+v", summaryBefore, summaryAfter)
	}
}

func TestIPv6Setting(t *testing.T) {
	doc, err := NewDefault("s")
	if err != nil {
		t.Fatal(err)
	}
	// 默认关闭；默认模板里 x-mproxy 与顶层、dns 的 ipv6 都是 false
	if got := doc.Settings().IPv6; got {
		t.Fatal("默认 x-mproxy.ipv6 应为 false")
	}
	if got := DefaultSettings().IPv6; got {
		t.Fatal("DefaultSettings().IPv6 应为 false")
	}
	if n := strings.Count(defaultTemplate, "ipv6: false"); n < 3 {
		t.Fatalf("默认模板应在 x-mproxy / 顶层 / dns 中都写 ipv6: false，实际出现 %d 次", n)
	}
	// 开启：x-mproxy.ipv6、顶层 ipv6、dns.ipv6 三处同步为 true
	on := true
	changed, err := doc.ApplySettings(SettingsPatch{IPv6: &on})
	if err != nil || !changed {
		t.Fatalf("ApplySettings(ipv6=true) changed=%v err=%v", changed, err)
	}
	if !doc.Settings().IPv6 {
		t.Fatal("Settings().IPv6 应为 true")
	}
	if !doc.GetBool(false, "x-mproxy", "ipv6") {
		t.Fatal("x-mproxy.ipv6 应为 true")
	}
	if !doc.GetBool(false, "ipv6") {
		t.Fatal("顶层 ipv6 应为 true")
	}
	if !doc.GetBool(false, "dns", "ipv6") {
		t.Fatal("dns.ipv6 应为 true")
	}
	if !doc.Summary().IPv6 {
		t.Fatal("Summary.IPv6 应反映顶层 ipv6=true")
	}
	// 相同值不产生变更
	if changed, err := doc.ApplySettings(SettingsPatch{IPv6: &on}); err != nil || changed {
		t.Fatalf("重复开启不应变更: changed=%v err=%v", changed, err)
	}
	// 关闭：三处同步为 false
	off := false
	changed, err = doc.ApplySettings(SettingsPatch{IPv6: &off})
	if err != nil || !changed {
		t.Fatalf("ApplySettings(ipv6=false) changed=%v err=%v", changed, err)
	}
	if doc.Settings().IPv6 || doc.GetBool(true, "ipv6") || doc.GetBool(true, "dns", "ipv6") {
		t.Fatal("关闭后 ipv6 相关字段都应为 false")
	}
	if doc.Summary().IPv6 {
		t.Fatal("Summary.IPv6 应为 false")
	}
}

func TestIPv6CompatRead(t *testing.T) {
	// x-mproxy 缺失：兼容读取顶层 ipv6
	doc, err := Parse([]byte("ipv6: true\nmode: rule\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !doc.Settings().IPv6 {
		t.Fatal("x-mproxy 缺失时应沿用顶层 ipv6=true")
	}
	// x-mproxy 存在但没有 ipv6 字段（旧配置）：同样沿用顶层 ipv6
	oldDoc, err := Parse([]byte("ipv6: true\nx-mproxy:\n  version: 1\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !oldDoc.Settings().IPv6 {
		t.Fatal("旧配置缺少 x-mproxy.ipv6 时应沿用顶层 ipv6=true")
	}
	// 两者都缺失：回退 false
	empty, err := Parse([]byte("mode: rule\n"))
	if err != nil {
		t.Fatal(err)
	}
	if empty.Settings().IPv6 {
		t.Fatal("缺少 ipv6 字段时应回退 false")
	}
}

func TestEnsureDefaultsIPv6(t *testing.T) {
	doc, err := Parse([]byte("mixed-port: 7890\n"))
	if err != nil {
		t.Fatal(err)
	}
	doc.EnsureDefaults("")
	v, ok := doc.Get("x-mproxy", "ipv6")
	if !ok {
		t.Fatal("EnsureDefaults 应在 x-mproxy 中补 ipv6")
	}
	if b, _ := v.(bool); b {
		t.Fatalf("EnsureDefaults 的 ipv6 应为 false，得到 %v", v)
	}
	if doc.Settings().IPv6 {
		t.Fatal("EnsureDefaults 后 Settings().IPv6 应为 false")
	}
}

func TestCriticalChangesIPv6(t *testing.T) {
	oldDoc, err := NewDefault("s")
	if err != nil {
		t.Fatal(err)
	}
	if changes := CriticalChanges(oldDoc, oldDoc.Clone()); len(changes) != 0 {
		t.Fatalf("相同配置不应有关键变化: %v", changes)
	}
	newDoc := oldDoc.Clone()
	newDoc.Set([]string{"ipv6"}, true)
	found := false
	for _, c := range CriticalChanges(oldDoc, newDoc) {
		if c == "ipv6" {
			found = true
		}
	}
	if !found {
		t.Fatalf("顶层 ipv6 变化应触发关键变化: %v", CriticalChanges(oldDoc, newDoc))
	}
}
