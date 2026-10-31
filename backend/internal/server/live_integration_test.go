//go:build integration

package server

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"os"
	"testing"

	"mproxy/internal/config"
	"mproxy/internal/core"
	"mproxy/internal/download"
	"mproxy/internal/job"
	"mproxy/internal/sysutil"
)

// 可选集成测试：需要本机已有 mihomo 控制器。
//
//	MPROXY_LIVE_CONTROLLER=127.0.0.1:9090 MPROXY_LIVE_SECRET=devsecret \
//	  go test -tags integration ./internal/server -run TestLiveMihomoProxy -v
//
// 该测试不会修改 .devdata：配置写入临时目录。
func liveConfig(t *testing.T, addr, secret string) *testEnv {
	t.Helper()
	dir := t.TempDir()
	paths := sysutil.Paths{DataDir: dir, ModuleDir: dir}
	if err := paths.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	logger := log.New(io.Discard, "", 0)
	cfg, err := config.Open(paths)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := cfg.Doc()
	if err != nil {
		t.Fatal(err)
	}
	doc.SetTop("external-controller", addr)
	doc.SetTop("secret", secret)
	if _, err := cfg.Save(doc); err != nil {
		t.Fatal(err)
	}
	coreMgr := core.NewManager(paths, cfg, logger)
	srv, err := New(Options{
		Paths: paths, Token: testToken, Config: cfg, Core: coreMgr,
		Jobs: job.NewManager(), Download: download.New(), Logger: logger,
	})
	if err != nil {
		t.Fatal(err)
	}
	return &testEnv{server: srv, paths: paths, config: cfg, core: coreMgr}
}

func TestLiveMihomoProxy(t *testing.T) {
	addr := os.Getenv("MPROXY_LIVE_CONTROLLER")
	if addr == "" {
		t.Skip("未设置 MPROXY_LIVE_CONTROLLER，跳过真实内核代理测试")
	}
	secret := os.Getenv("MPROXY_LIVE_SECRET")
	env := liveConfig(t, addr, secret)
	if !env.core.Running() {
		t.Fatalf("无法连接 live mihomo 控制器 %s", addr)
	}
	rec := env.request(t, http.MethodGet, "/api/mihomo/version", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("代理 /version 失败: %d %s", rec.Code, rec.Body.String())
	}
	var ver struct {
		Version string `json:"version"`
		Meta    bool   `json:"meta"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &ver); err != nil || ver.Version == "" {
		t.Fatalf("代理返回内核版本异常: %s", rec.Body.String())
	}
	// PATCH /configs：写临时 YAML，同时转发到真实内核
	rec = env.request(t, http.MethodPatch, "/api/mihomo/configs", map[string]any{"log-level": "info"})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("PATCH configs 失败: %d %s", rec.Code, rec.Body.String())
	}
	rec = env.request(t, http.MethodGet, "/api/mihomo/configs", nil)
	if rec.Code != http.StatusOK || rec.Body.Len() == 0 {
		t.Fatalf("GET configs 失败: %d", rec.Code)
	}
}
