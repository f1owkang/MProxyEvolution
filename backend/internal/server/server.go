// Package server 提供本地 HTTP 服务：静态面板、鉴权 API 与 mihomo 反向代理。
package server

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"time"

	"mproxy/internal/config"
	"mproxy/internal/core"
	"mproxy/internal/download"
	"mproxy/internal/job"
	"mproxy/internal/sysutil"
	"mproxy/internal/version"
)

// ListenAddr 是后端固定监听地址（只监听本地回环）。
const ListenAddr = "127.0.0.1:9091"

// Options 是构造 Server 的依赖。
type Options struct {
	Paths     sysutil.Paths
	Token     string
	DevMode   bool
	Config    *config.Manager
	Core      *core.Manager
	Jobs      *job.Manager
	Download  *download.Client
	Logger    *log.Logger
	Args      []string // 后端启动参数（用于 /api/backend/restart）
	StartedAt time.Time
}

// Server 是 MProxyEvolution 本地后端。
type Server struct {
	opts    Options
	mux     *http.ServeMux
	proxy   *mihomoProxy
	started time.Time
}

// New 创建后端服务并注册路由。
func New(opts Options) (*Server, error) {
	if opts.Config == nil || opts.Core == nil {
		return nil, fmt.Errorf("server 初始化缺少 config/core 依赖")
	}
	if opts.Jobs == nil {
		opts.Jobs = job.NewManager()
	}
	if opts.Download == nil {
		opts.Download = download.New()
	}
	if opts.Logger == nil {
		opts.Logger = log.Default()
	}
	if opts.StartedAt.IsZero() {
		opts.StartedAt = time.Now()
	}
	if opts.Paths.DataDir == "" {
		opts.Paths = sysutil.NewPaths()
	}
	s := &Server{opts: opts, mux: http.NewServeMux(), started: opts.StartedAt}

	transport := &http.Transport{
		Proxy:                 nil, // 控制器与内核都在本机，禁止走代理
		MaxIdleConns:          16,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 0, // SSE/WS 需要长连接，不设响应头超时
		ExpectContinueTimeout: time.Second,
	}
	s.proxy = &mihomoProxy{server: s, transport: transport}

	s.routes()
	return s, nil
}

func (s *Server) routes() {
	m := s.mux
	// 状态与元信息
	m.HandleFunc("GET /healthz", s.handleHealthz)
	m.HandleFunc("GET /api/status", s.handleStatus)
	m.HandleFunc("GET /api/meta", s.handleMeta)

	// 配置
	m.HandleFunc("GET /api/config", s.handleConfigGet)
	m.HandleFunc("PUT /api/config", s.handleConfigPut)
	m.HandleFunc("POST /api/config/validate", s.handleConfigValidate)
	m.HandleFunc("POST /api/config/import", s.handleConfigImport)
	m.HandleFunc("GET /api/config/export", s.handleConfigExport)
	m.HandleFunc("GET /api/config/backup", s.handleConfigBackup)
	m.HandleFunc("POST /api/config/restore", s.handleConfigRestore)
	m.HandleFunc("POST /api/config/reset", s.handleConfigReset)

	// 内核
	m.HandleFunc("POST /api/core/start", s.handleCoreStart)
	m.HandleFunc("POST /api/core/stop", s.handleCoreStop)
	m.HandleFunc("POST /api/core/restart", s.handleCoreRestart)
	m.HandleFunc("POST /api/core/reload", s.handleCoreReload)
	m.HandleFunc("GET /api/core/update/check", s.handleCoreUpdateCheck)
	m.HandleFunc("POST /api/core/update", s.handleCoreUpdate)
	m.HandleFunc("GET /api/jobs/{id}", s.handleJobGet)
	m.HandleFunc("GET /api/jobs", s.handleJobList)
	m.HandleFunc("GET /api/logs/core", s.handleCoreLogs)
	m.HandleFunc("POST /api/logs/core/clear", s.handleCoreLogsClear)
	m.HandleFunc("GET /api/logs/backend", s.handleBackendLogs)
	m.HandleFunc("POST /api/backend/restart", s.handleBackendRestart)
	m.HandleFunc("POST /api/backend/shutdown", s.handleBackendShutdown)
	m.HandleFunc("GET /api/packages", s.handlePackages)

	// MProxyEvolution 页面
	m.HandleFunc("GET /api/mproxy/settings", s.handleSettingsGet)
	m.HandleFunc("PATCH /api/mproxy/settings", s.handleSettingsPatch)
	m.HandleFunc("GET /api/mproxy/apps", s.handleAppsGet)
	m.HandleFunc("PUT /api/mproxy/apps", s.handleAppsPut)
	m.HandleFunc("POST /api/mproxy/providers", s.handleProviderAdd)
	m.HandleFunc("DELETE /api/mproxy/providers/{name}", s.handleProviderDelete)
	m.HandleFunc("GET /api/mproxy/diagnostics", s.handleDiagnostics)

	// mihomo 反向代理（持久化拦截 + 通用代理）
	m.HandleFunc("PATCH /api/mihomo/configs", s.handleProxyPatchConfigs)
	m.HandleFunc("PUT /api/mihomo/configs", s.handleProxyPutConfigs)
	m.HandleFunc("POST /api/mihomo/restart", s.handleProxyRestart)
	m.Handle("/api/mihomo/", s.proxy)
	m.Handle("/api/mihomo", s.proxy)

	// 未注册的 /api/* 返回 JSON 404（ServeMux 会优先匹配上面更具体的方法路由），
	// 避免落到静态面板的 SPA 回退而返回 index.html。
	m.HandleFunc("/api", s.handleAPINotFound)
	m.HandleFunc("/api/", s.handleAPINotFound)

	// 静态面板（SPA fallback）
	m.Handle("/", s.staticHandler())
}

// handleAPINotFound 处理未注册的 /api 路径，统一返回 JSON 404。
func (s *Server) handleAPINotFound(w http.ResponseWriter, r *http.Request) {
	writeErr(w, http.StatusNotFound, "not_found", "接口不存在", nil)
}

// Handler 返回带鉴权中间件的 Handler。
func (s *Server) Handler() http.Handler {
	return s.withAuth(s.withLogging(s.mux))
}

// ListenAndServe 监听 127.0.0.1:9091；重启后端时短暂重试端口。
func (s *Server) ListenAndServe() error {
	var ln net.Listener
	var err error
	for i := 0; i < 20; i++ {
		ln, err = net.Listen("tcp", ListenAddr)
		if err == nil {
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
	if err != nil {
		return fmt.Errorf("监听 %s 失败: %w", ListenAddr, err)
	}
	srv := &http.Server{
		Handler:           s.Handler(),
		ReadHeaderTimeout: 15 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	s.opts.Logger.Printf("MProxyEvolution 后端已监听 http://%s（数据目录 %s）", ListenAddr, s.opts.Paths.DataDir)
	return srv.Serve(ln)
}

// BaseURL 返回后端本地地址。
func (s *Server) BaseURL() string { return "http://" + ListenAddr }

// PanelURL 返回带 token 的面板地址（与 cmd/mproxy 的 CLI 输出保持一致）。
func (s *Server) PanelURL() string {
	return fmt.Sprintf("%s/?token=%s#/", s.BaseURL(), s.opts.Token)
}

// restartBackend 以脱离方式重新拉起自身进程。
func (s *Server) restartBackend() error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("获取后端可执行文件路径失败: %w", err)
	}
	args := s.opts.Args
	if len(args) == 0 {
		args = []string{"serve"}
	}
	cmd := exec.Command(exe, args...)
	// 新进程会自己打开 backend.log；这里若再传同一个日志路径，旧进程的 Writer 与新进程
	// 会同时写/轮转同一文件，出现重复日志。detached 子进程输出直接丢弃。
	if err := sysutil.StartDetached(cmd, ""); err != nil {
		return fmt.Errorf("重启后端失败: %w", err)
	}
	s.opts.Logger.Printf("已拉起新的后端进程（pid=%d），当前进程即将退出", cmd.Process.Pid)
	return nil
}

// exitSoon 在响应写回后退出当前进程。
func exitSoon(delay time.Duration) {
	go func() {
		time.Sleep(delay)
		os.Exit(0)
	}()
}

// backgroundContext 返回用于异步任务的 context。
func backgroundContext() context.Context { return context.Background() }

// Version 返回后端版本号。
func Version() string { return version.Version }
