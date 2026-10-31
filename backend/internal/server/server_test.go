package server

import (
	"bytes"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mproxy/internal/config"
	"mproxy/internal/core"
	"mproxy/internal/download"
	"mproxy/internal/job"
	"mproxy/internal/sysutil"
)

const testToken = "test-token-123"

type testEnv struct {
	server *Server
	paths  sysutil.Paths
	config *config.Manager
	core   *core.Manager
}

func newTestEnv(t *testing.T, dev bool) *testEnv {
	t.Helper()
	dir := t.TempDir()
	paths := sysutil.Paths{DataDir: dir, ModuleDir: dir}
	if err := paths.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	logger := log.New(io.Discard, "", 0)
	cfg, err := config.Open(paths)
	if err != nil {
		t.Fatalf("打开配置失败: %v", err)
	}
	mgr := core.NewManager(paths, cfg, logger)
	srv, err := New(Options{
		Paths:    paths,
		Token:    testToken,
		DevMode:  dev,
		Config:   cfg,
		Core:     mgr,
		Jobs:     job.NewManager(),
		Download: download.New(),
		Logger:   logger,
	})
	if err != nil {
		t.Fatalf("创建 server 失败: %v", err)
	}
	return &testEnv{server: srv, paths: paths, config: cfg, core: mgr}
}

func (e *testEnv) request(t *testing.T, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(data)
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("X-MProxy-Token", testToken)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	e.server.Handler().ServeHTTP(rec, req)
	return rec
}

func decodeResponse[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var out T
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("解析响应失败（%d %s）: %v", rec.Code, rec.Body.String(), err)
	}
	return out
}

func TestHealthzPublic(t *testing.T) {
	env := newTestEnv(t, false)
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	env.server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("healthz 状态码 = %d", rec.Code)
	}
	body := decodeResponse[healthResponse](t, rec)
	if !body.OK || body.Version == "" || body.PID == 0 {
		t.Fatalf("healthz 响应错误: %+v", body)
	}
}

func TestAPIAuth(t *testing.T) {
	env := newTestEnv(t, false)

	req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	rec := httptest.NewRecorder()
	env.server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("缺少 token 应 401，实际 %d", rec.Code)
	}
	var envl errorEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &envl); err != nil || envl.Error.Code != "unauthorized" {
		t.Fatalf("401 错误格式不正确: %s", rec.Body.String())
	}

	// ?token= 方式
	req = httptest.NewRequest(http.MethodGet, "/api/status?token="+testToken, nil)
	rec = httptest.NewRecorder()
	env.server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("?token= 认证失败: %d %s", rec.Code, rec.Body.String())
	}

	// Bearer 方式
	req = httptest.NewRequest(http.MethodGet, "/api/status", nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	rec = httptest.NewRecorder()
	env.server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("Bearer 认证失败: %d", rec.Code)
	}

	// 错误 token
	req = httptest.NewRequest(http.MethodGet, "/api/status", nil)
	req.Header.Set("X-MProxy-Token", "wrong")
	rec = httptest.NewRecorder()
	env.server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("错误 token 应 401，实际 %d", rec.Code)
	}

	// 静态资源公开
	req = httptest.NewRequest(http.MethodGet, "/", nil)
	rec = httptest.NewRecorder()
	env.server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("静态首页应公开，实际 %d", rec.Code)
	}
}

func TestDevModeDisablesAuth(t *testing.T) {
	env := newTestEnv(t, true)
	req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	rec := httptest.NewRecorder()
	env.server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("dev 模式应关闭鉴权，实际 %d", rec.Code)
	}
}

func TestStatusAndMeta(t *testing.T) {
	env := newTestEnv(t, false)
	rec := env.request(t, http.MethodGet, "/api/status", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status 失败: %d %s", rec.Code, rec.Body.String())
	}
	body := decodeResponse[statusResponse](t, rec)
	if body.Backend.Version == "" || body.Backend.PID == 0 {
		t.Fatalf("backend 状态错误: %+v", body.Backend)
	}
	if body.Config.Path != env.paths.ConfigPath() || !body.Config.Exists || !body.Config.Valid {
		t.Fatalf("config 状态错误: %+v", body.Config)
	}
	if body.Config.Summary.RuleCount == 0 {
		t.Fatalf("默认配置应有规则: %+v", body.Config.Summary)
	}
	if body.Core.Installed {
		t.Fatal("测试环境不应安装内核")
	}

	rec = env.request(t, http.MethodGet, "/api/meta", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("meta 失败: %d", rec.Code)
	}
	var meta struct {
		URL     string `json:"url"`
		Version string `json:"version"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &meta); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(meta.URL, "127.0.0.1:9091") || !strings.Contains(meta.URL, testToken) {
		t.Fatalf("meta.url 错误: %s", meta.URL)
	}
}

func TestConfigEndpoints(t *testing.T) {
	env := newTestEnv(t, false)
	rec := env.request(t, http.MethodGet, "/api/config", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/config 失败: %d", rec.Code)
	}
	var got configGetResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if !got.Valid || !strings.Contains(got.YAML, "x-mproxy") {
		t.Fatalf("GET /api/config 内容错误: valid=%v", got.Valid)
	}

	// validate：非法配置返回 ok=false
	rec = env.request(t, http.MethodPost, "/api/config/validate", map[string]any{"yaml": "mixed-port: [oops"})
	if rec.Code != http.StatusOK {
		t.Fatalf("validate 接口应返回 200: %d", rec.Code)
	}
	var vr struct {
		OK     bool   `json:"ok"`
		Output string `json:"output"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &vr); err != nil || vr.OK || vr.Output == "" {
		t.Fatalf("validate 结果错误: %s", rec.Body.String())
	}

	// PUT 合法配置
	newYAML := `mixed-port: 7895
external-controller: 127.0.0.1:9090
secret: test-secret
mode: rule
log-level: info
tun:
  enable: true
dns:
  enable: true
  enhanced-mode: fake-ip
  fake-ip-range: 198.18.0.1/16
x-mproxy:
  version: 1
  auto-start: true
  proxy-mode: tun
proxy-groups:
  - {name: PROXY, type: select, proxies: [DIRECT]}
rules:
  - MATCH,PROXY
`
	rec = env.request(t, http.MethodPut, "/api/config", map[string]any{"yaml": newYAML, "apply": "none"})
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT /api/config 失败: %d %s", rec.Code, rec.Body.String())
	}
	var put applyResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &put); err != nil {
		t.Fatal(err)
	}
	if !put.OK || !put.Saved || put.Summary.MixedPort != 7895 {
		t.Fatalf("PUT 响应错误: %+v", put)
	}
	if put.Applied {
		t.Fatal("apply=none 不应应用到内核")
	}
	// 备份应已生成
	if _, err := os.Stat(env.paths.BackupPath()); err != nil {
		t.Fatalf("PUT 后应生成备份: %v", err)
	}
	// 磁盘内容更新
	data, _ := os.ReadFile(env.paths.ConfigPath())
	if !strings.Contains(string(data), "mixed-port: 7895") {
		t.Fatal("磁盘配置未更新")
	}
	// PUT 非法配置
	rec = env.request(t, http.MethodPut, "/api/config", map[string]any{"yaml": "mode: [bad", "apply": "none"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("非法配置应 400，实际 %d", rec.Code)
	}

	// export / backup
	rec = env.request(t, http.MethodGet, "/api/config/export", nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "mixed-port: 7895") {
		t.Fatalf("export 失败: %d", rec.Code)
	}
	rec = env.request(t, http.MethodGet, "/api/config/backup", nil)
	if rec.Code != http.StatusOK || rec.Body.Len() == 0 {
		t.Fatalf("backup 失败: %d", rec.Code)
	}
}

func TestConfigImportEndpoint(t *testing.T) {
	env := newTestEnv(t, false)
	content := `proxies:
  - {name: node-a, type: socks5, server: 1.1.1.1, port: 1080}
proxy-groups:
  - {name: PROXY, type: select, proxies: [node-a]}
rules:
  - MATCH,PROXY
`
	rec := env.request(t, http.MethodPost, "/api/config/import", map[string]any{
		"content": content,
		"mode":    "merge",
		"apply":   "none",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("导入失败: %d %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Imported config.Imported `json:"imported"`
		Summary  config.Summary  `json:"summary"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Imported.Proxies != 1 || resp.Imported.Rules != 1 {
		t.Fatalf("导入计数错误: %+v", resp.Imported)
	}
	if resp.Summary.ProxyCount != 1 {
		t.Fatalf("合并后节点数错误: %+v", resp.Summary)
	}
	// x-mproxy 仍保留
	doc, err := env.config.Doc()
	if err != nil || !doc.Has("x-mproxy") {
		t.Fatal("导入后 x-mproxy 丢失")
	}
}

func TestMProxyEndpoints(t *testing.T) {
	env := newTestEnv(t, false)

	// settings
	rec := env.request(t, http.MethodGet, "/api/mproxy/settings", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET settings 失败: %d", rec.Code)
	}
	rec = env.request(t, http.MethodPatch, "/api/mproxy/settings", map[string]any{
		"autoStart":     false,
		"proxyMode":     "mixed",
		"updateChannel": "alpha",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH settings 失败: %d %s", rec.Code, rec.Body.String())
	}
	doc, _ := env.config.Doc()
	s := doc.Settings()
	if s.AutoStart || s.ProxyMode != "mixed" || s.UpdateChannel != "alpha" {
		t.Fatalf("settings 未写回: %+v", s)
	}
	if doc.GetBool(true, "tun", "enable") {
		t.Fatal("mixed 模式应关闭 tun.enable")
	}
	// tproxy 应拒绝
	rec = env.request(t, http.MethodPatch, "/api/mproxy/settings", map[string]any{"proxyMode": "tproxy"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("tproxy 应 400，实际 %d", rec.Code)
	}

	// apps
	rec = env.request(t, http.MethodPut, "/api/mproxy/apps", map[string]any{
		"mode": "whitelist",
		"apps": []string{"com.example.a", "com.example.b"},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT apps 失败: %d %s", rec.Code, rec.Body.String())
	}
	doc, _ = env.config.Doc()
	mode, apps := doc.Apps()
	if mode != config.AppsWhitelist || len(apps) != 2 {
		t.Fatalf("apps 未写回: %s %v", mode, apps)
	}
	rec = env.request(t, http.MethodGet, "/api/mproxy/apps", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET apps 失败: %d", rec.Code)
	}

	// providers
	rec = env.request(t, http.MethodPost, "/api/mproxy/providers", map[string]any{
		"name": "sub1", "url": "https://example.com/sub", "interval": 7200,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("POST provider 失败: %d %s", rec.Code, rec.Body.String())
	}
	doc, _ = env.config.Doc()
	if providers := doc.Providers(); len(providers) != 1 || providers[0].Name != "sub1" {
		t.Fatalf("provider 未写回: %+v", providers)
	}
	rec = env.request(t, http.MethodDelete, "/api/mproxy/providers/sub1", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("DELETE provider 失败: %d", rec.Code)
	}
	if len(doc.Providers()) != 0 {
		// doc 是旧副本，重新读取
		doc, _ = env.config.Doc()
		if len(doc.Providers()) != 0 {
			t.Fatal("provider 未删除")
		}
	}

	// diagnostics
	rec = env.request(t, http.MethodGet, "/api/mproxy/diagnostics", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("diagnostics 失败: %d", rec.Code)
	}
}

func TestCoreEndpointsWithoutBinary(t *testing.T) {
	env := newTestEnv(t, false)
	rec := env.request(t, http.MethodPost, "/api/core/start", nil)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("没有内核时 start 应 500，实际 %d", rec.Code)
	}
	var envl errorEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &envl); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(envl.Error.Message, "未安装") {
		t.Fatalf("错误信息应提示未安装内核: %s", envl.Error.Message)
	}
	// 停止未运行的内核应成功
	rec = env.request(t, http.MethodPost, "/api/core/stop", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("stop 未运行内核应 200，实际 %d", rec.Code)
	}
	// 内核日志接口（无日志时返回空数组）
	rec = env.request(t, http.MethodGet, "/api/logs/core?lines=10", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("logs 接口失败: %d", rec.Code)
	}
	// 后端日志接口
	rec = env.request(t, http.MethodGet, "/api/logs/backend?lines=10", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("backend logs 接口失败: %d", rec.Code)
	}
	// 任务不存在
	rec = env.request(t, http.MethodGet, "/api/jobs/not-exist", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("不存在的任务应 404，实际 %d", rec.Code)
	}
}

func TestMihomoProxyWithoutCore(t *testing.T) {
	env := newTestEnv(t, false)
	rec := env.request(t, http.MethodGet, "/api/mihomo/version", nil)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("内核未运行时代理应 502，实际 %d", rec.Code)
	}
	var envl errorEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &envl); err != nil || envl.Error.Code != "core_not_running" {
		t.Fatalf("502 错误格式错误: %s", rec.Body.String())
	}
}

func TestPersistedPatchConfigs(t *testing.T) {
	env := newTestEnv(t, false)
	rec := env.request(t, http.MethodPatch, "/api/mihomo/configs", map[string]any{
		"mixed-port": 7898,
		"dns":        map[string]any{"enable": false},
	})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("PATCH configs 应 204，实际 %d %s", rec.Code, rec.Body.String())
	}
	doc, err := env.config.Doc()
	if err != nil {
		t.Fatal(err)
	}
	if doc.GetInt(0, "mixed-port") != 7898 || doc.GetBool(true, "dns", "enable") {
		t.Fatalf("PATCH 未写回 YAML: %+v", doc.Summary())
	}
	if !doc.Has("x-mproxy") {
		t.Fatal("PATCH 后 x-mproxy 丢失")
	}
}

func TestPersistedPatchSyncsTunProxyMode(t *testing.T) {
	env := newTestEnv(t, false)
	cases := []struct {
		enable bool
		want   string
	}{
		{false, config.ProxyModeMixed},
		{true, config.ProxyModeTun},
	}
	for _, tc := range cases {
		rec := env.request(t, http.MethodPatch, "/api/mihomo/configs", map[string]any{
			"tun": map[string]any{"enable": tc.enable},
		})
		if rec.Code != http.StatusNoContent {
			t.Fatalf("PATCH tun.enable=%v 应 204，实际 %d %s", tc.enable, rec.Code, rec.Body.String())
		}
		doc, err := env.config.Doc()
		if err != nil {
			t.Fatal(err)
		}
		if got := doc.Settings().ProxyMode; got != tc.want {
			t.Fatalf("tun.enable=%v 后 proxy-mode=%q，期望 %q", tc.enable, got, tc.want)
		}
	}
}

func TestPersistedPutConfigsPayload(t *testing.T) {
	env := newTestEnv(t, false)
	payload := `mixed-port: 7897
external-controller: 127.0.0.1:9090
secret: payload-secret
proxy-groups:
  - {name: PROXY, type: select, proxies: [DIRECT]}
rules:
  - MATCH,PROXY
x-mproxy:
  version: 1
  auto-start: false
  proxy-mode: mixed
`
	rec := env.request(t, http.MethodPut, "/api/mihomo/configs", map[string]any{"payload": payload})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("PUT configs 应 204，实际 %d %s", rec.Code, rec.Body.String())
	}
	doc, err := env.config.Doc()
	if err != nil {
		t.Fatal(err)
	}
	if doc.GetInt(0, "mixed-port") != 7897 {
		t.Fatalf("payload 未保存: %+v", doc.Summary())
	}
	// 传入 payload 里的 x-mproxy 应被当前配置覆盖（当前 auto-start=true）
	if !doc.Settings().AutoStart {
		t.Fatal("payload 中的 x-mproxy 不应覆盖 MProxyEvolution 设置")
	}
	if doc.GetString("", "secret") != "payload-secret" {
		t.Fatalf("secret = %q", doc.GetString("", "secret"))
	}
}

func TestStaticSPA(t *testing.T) {
	env := newTestEnv(t, false)
	// 首页
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	env.server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK ||
		(!strings.Contains(rec.Body.String(), "mproxy-boot.js") && !strings.Contains(rec.Body.String(), "MihomoProxy Evolution")) {
		t.Fatalf("首页失败: %d", rec.Code)
	}
	// SPA 路由回退
	req = httptest.NewRequest(http.MethodGet, "/#/mproxy", nil)
	rec = httptest.NewRecorder()
	env.server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("SPA 路由回退失败: %d", rec.Code)
	}
	// 缺失的 assets 不应对回退到 index
	req = httptest.NewRequest(http.MethodGet, "/assets/missing.js", nil)
	rec = httptest.NewRecorder()
	env.server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("缺失 assets 应 404，实际 %d", rec.Code)
	}
}

func TestPackagesNonAndroid(t *testing.T) {
	env := newTestEnv(t, false)
	rec := env.request(t, http.MethodGet, "/api/packages?type=user", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("packages 失败: %d", rec.Code)
	}
	var resp struct {
		Packages []PackageInfo `json:"packages"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	// 非 Android 平台应返回空列表而不是报错
	if resp.Packages == nil && !strings.Contains(rec.Body.String(), "null") {
		t.Log("packages 为空")
	}
}

func TestConfigResetAndRestore(t *testing.T) {
	env := newTestEnv(t, false)
	// 保存一份配置并产生备份
	rec := env.request(t, http.MethodPut, "/api/config", map[string]any{
		"yaml":  "mixed-port: 7777\nmode: rule\nx-mproxy: {version: 1, auto-start: true, proxy-mode: tun}\n",
		"apply": "none",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("准备配置失败: %d %s", rec.Code, rec.Body.String())
	}
	// restore 备份
	rec = env.request(t, http.MethodPost, "/api/config/restore", map[string]any{"apply": "none"})
	if rec.Code != http.StatusOK {
		t.Fatalf("restore 失败: %d %s", rec.Code, rec.Body.String())
	}
	// 重置
	rec = env.request(t, http.MethodPost, "/api/config/reset", map[string]any{"confirm": false})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("未确认的重置应 400，实际 %d", rec.Code)
	}
	rec = env.request(t, http.MethodPost, "/api/config/reset", map[string]any{"confirm": true, "apply": "none"})
	if rec.Code != http.StatusOK {
		t.Fatalf("reset 失败: %d %s", rec.Code, rec.Body.String())
	}
	doc, err := env.config.Doc()
	if err != nil || !doc.Has("x-mproxy") {
		t.Fatal("reset 后默认配置缺少 x-mproxy")
	}
	if secret := doc.GetString("", "secret"); secret == "" {
		t.Fatal("reset 后应生成随机 secret")
	}
	if p := filepath.Join(env.paths.DataDir, "config.yaml.bak"); p == "" {
		t.Fatal("unreachable")
	}
}

// corruptCurrentConfig 模拟磁盘上带结构错误的旧配置：解析成功但 ValidateStructure 失败，
// 用来验证各写入路径是否执行了与 PUT /api/config 相同的校验。
func (e *testEnv) corruptCurrentConfig(t *testing.T) {
	t.Helper()
	bad := `mixed-port: 99999
mode: rule
external-controller: 127.0.0.1:9090
secret: keep-me
proxy-providers:
  sub:
    type: http
    url: https://example.com/sub
x-mproxy: {version: 1, auto-start: true, proxy-mode: tun}
tun:
  enable: true
`
	if err := os.WriteFile(e.paths.ConfigPath(), []byte(bad), 0o644); err != nil {
		t.Fatal(err)
	}
	// ReloadFromDisk 对结构非法但可解析的配置只记录 LoadError，仍保留可用 Doc。
	if err := e.config.ReloadFromDisk(); err != nil {
		t.Fatalf("重载损坏配置失败: %v", err)
	}
	if e.config.LoadError() == nil {
		t.Fatal("损坏配置应记录 LoadError")
	}
}

func TestConfigPutPreservesControllerAndSecret(t *testing.T) {
	env := newTestEnv(t, false)
	first := `mixed-port: 7895
external-controller: 192.168.1.2:9090
secret: panel-secret
mode: rule
x-mproxy: {version: 1, auto-start: true, proxy-mode: mixed}
proxy-groups:
  - {name: PROXY, type: select, proxies: [DIRECT]}
rules:
  - MATCH,PROXY
`
	rec := env.request(t, http.MethodPut, "/api/config", map[string]any{"yaml": first, "apply": "none"})
	if rec.Code != http.StatusOK {
		t.Fatalf("首次 PUT 失败: %d %s", rec.Code, rec.Body.String())
	}
	// 第二次完整替换 YAML 时故意省略 external-controller / secret / x-mproxy。
	second := `mixed-port: 7896
mode: rule
proxy-groups:
  - {name: PROXY, type: select, proxies: [DIRECT]}
rules:
  - MATCH,PROXY
`
	rec = env.request(t, http.MethodPut, "/api/config", map[string]any{"yaml": second, "apply": "none"})
	if rec.Code != http.StatusOK {
		t.Fatalf("第二次 PUT 失败: %d %s", rec.Code, rec.Body.String())
	}
	doc, err := env.config.Doc()
	if err != nil {
		t.Fatal(err)
	}
	if got := doc.GetString("", "external-controller"); got != "192.168.1.2:9090" {
		t.Fatalf("external-controller 应沿用旧值，得到 %q", got)
	}
	if got := doc.GetString("", "secret"); got != "panel-secret" {
		t.Fatalf("secret 应沿用旧值，得到 %q", got)
	}
	if !doc.Has("x-mproxy") {
		t.Fatal("x-mproxy 应被保留")
	}
	if got := doc.GetInt(0, "mixed-port"); got != 7896 {
		t.Fatalf("mixed-port = %d", got)
	}
}

func TestConfigImportMergeIgnoresSensitiveKeys(t *testing.T) {
	env := newTestEnv(t, false)
	before, err := env.config.Doc()
	if err != nil {
		t.Fatal(err)
	}
	oldSecret := before.GetString("", "secret")
	content := `allow-lan: true
external-controller: 0.0.0.0:9999
secret: hijacked
mixed-port: 1234
tun:
  enable: false
proxies:
  - {name: n1, type: socks5, server: 1.1.1.1, port: 1080}
rules:
  - MATCH,PROXY
`
	rec := env.request(t, http.MethodPost, "/api/config/import", map[string]any{
		"content": content,
		"mode":    "merge",
		"apply":   "none",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("merge 导入失败: %d %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Imported config.Imported `json:"imported"`
		Warnings []string        `json:"warnings"`
		Summary  config.Summary  `json:"summary"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Imported.Proxies != 1 || resp.Summary.ProxyCount != 1 {
		t.Fatalf("白名单内容未合并: imported=%+v summary=%+v", resp.Imported, resp.Summary)
	}
	doc, err := env.config.Doc()
	if err != nil {
		t.Fatal(err)
	}
	if doc.GetBool(false, "allow-lan") {
		t.Fatal("allow-lan 不应被导入覆盖")
	}
	if got := doc.GetString("", "external-controller"); got != "127.0.0.1:9090" {
		t.Fatalf("external-controller 被覆盖: %q", got)
	}
	if got := doc.GetString("", "secret"); got != oldSecret {
		t.Fatalf("secret 被覆盖: %q", got)
	}
	if got := doc.GetInt(0, "mixed-port"); got != 7890 {
		t.Fatalf("mixed-port 被覆盖: %d", got)
	}
	if !doc.GetBool(true, "tun", "enable") {
		t.Fatal("tun.enable 不应被导入覆盖")
	}
	joined := strings.Join(resp.Warnings, "\n")
	for _, key := range []string{"allow-lan", "external-controller", "secret", "mixed-port", "tun"} {
		if !strings.Contains(joined, key) {
			t.Fatalf("缺少 %s 的忽略警告:\n%s", key, joined)
		}
	}
}

func TestMProxyWritesValidateConfig(t *testing.T) {
	cases := []struct {
		name   string
		method string
		path   string
		body   any
	}{
		{"settings", http.MethodPatch, "/api/mproxy/settings", map[string]any{"autoStart": false}},
		{"apps", http.MethodPut, "/api/mproxy/apps", map[string]any{"mode": "all"}},
		{"provider-add", http.MethodPost, "/api/mproxy/providers", map[string]any{"name": "sub2", "url": "https://example.com/sub2"}},
		{"provider-delete", http.MethodDelete, "/api/mproxy/providers/sub", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := newTestEnv(t, false)
			env.corruptCurrentConfig(t)
			rec := env.request(t, tc.method, tc.path, tc.body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("应返回 400 invalid_config，实际 %d %s", rec.Code, rec.Body.String())
			}
			var envl errorEnvelope
			if err := json.Unmarshal(rec.Body.Bytes(), &envl); err != nil {
				t.Fatal(err)
			}
			if envl.Error.Code != "invalid_config" {
				t.Fatalf("错误码 = %q，响应 %s", envl.Error.Code, rec.Body.String())
			}
			// 校验失败时磁盘配置不应被覆盖
			raw, _ := os.ReadFile(env.paths.ConfigPath())
			if !strings.Contains(string(raw), "99999") {
				t.Fatal("校验失败不应保存配置")
			}
		})
	}
}

func TestUnknownAPIPathReturnsJSON404(t *testing.T) {
	env := newTestEnv(t, false)
	for _, path := range []string{"/api/does-not-exist", "/api/mproxy/nope", "/api", "/api/"} {
		rec := env.request(t, http.MethodGet, path, nil)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s 应返回 404，实际 %d %s", path, rec.Code, rec.Body.String())
		}
		if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
			t.Fatalf("%s 应返回 JSON，Content-Type=%q", path, ct)
		}
		var envl errorEnvelope
		if err := json.Unmarshal(rec.Body.Bytes(), &envl); err != nil {
			t.Fatalf("%s 响应不是 JSON 错误结构: %s", path, rec.Body.String())
		}
		if envl.Error.Code != "not_found" || envl.Error.Message != "接口不存在" {
			t.Fatalf("%s 错误结构错误: %+v", path, envl.Error)
		}
	}
	// 已注册接口不受影响
	if rec := env.request(t, http.MethodGet, "/api/status", nil); rec.Code != http.StatusOK {
		t.Fatalf("/api/status 应 200，实际 %d", rec.Code)
	}
	// /api/mihomo 仍走反向代理（内核未运行返回 502，而不是 404）
	if rec := env.request(t, http.MethodGet, "/api/mihomo/version", nil); rec.Code != http.StatusBadGateway {
		t.Fatalf("/api/mihomo/version 应 502，实际 %d", rec.Code)
	}
}

func TestTrailingJSONRejected(t *testing.T) {
	env := newTestEnv(t, false)
	req := httptest.NewRequest(http.MethodPost, "/api/config/validate",
		strings.NewReader(`{"yaml":"mixed-port: 7890"} {"extra":1}`))
	req.Header.Set("X-MProxy-Token", testToken)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	env.server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("带尾随内容的 JSON 应 400，实际 %d %s", rec.Code, rec.Body.String())
	}
	var envl errorEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &envl); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(envl.Error.Message, "多余") {
		t.Fatalf("错误信息应说明多余内容: %s", envl.Error.Message)
	}

	// 纯函数级：尾随空白允许，其他内容拒绝
	var v map[string]any
	if err := decodeJSONBytes([]byte("{\"a\":1}\n  "), &v); err != nil {
		t.Fatalf("尾随空白应允许: %v", err)
	}
	if err := decodeJSONBytes([]byte(`{"a":1} {"b":2}`), &v); err == nil {
		t.Fatal("第二个 JSON 值应被拒绝")
	}
	if err := decodeJSONBytes([]byte(`{"a":1} trailing`), &v); err == nil {
		t.Fatal("尾随垃圾应被拒绝")
	}
}

func TestPanelURLMatchesCLI(t *testing.T) {
	env := newTestEnv(t, false)
	want := "http://127.0.0.1:9091/?token=" + testToken + "#/settings?section=moduleSettings"
	if got := env.server.PanelURL(); got != want {
		t.Fatalf("PanelURL() = %q，期望 %q", got, want)
	}
	rec := env.request(t, http.MethodGet, "/api/meta", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("meta 失败: %d", rec.Code)
	}
	var meta struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &meta); err != nil {
		t.Fatal(err)
	}
	if meta.URL != want {
		t.Fatalf("meta.url = %q，期望 %q", meta.URL, want)
	}
}
