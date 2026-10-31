// MProxyEvolution 后端 CLI：serve / boot / stop / status / toggle / url。
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"mproxy/internal/config"
	"mproxy/internal/core"
	"mproxy/internal/download"
	"mproxy/internal/job"
	"mproxy/internal/server"
	"mproxy/internal/sysutil"
	"mproxy/internal/version"
)

const usage = `MProxyEvolution 后端

用法:
  mproxy serve                 常驻运行后端（固定监听 127.0.0.1:9091）
  mproxy boot [--boot]         检测后端，未启动则以脱离方式拉起，最后一行输出带 token 的面板地址
  mproxy stop                  停止内核与后端
  mproxy status                输出中文状态摘要（未运行返回非 0）
  mproxy toggle                运行中则停止，否则拉起
  mproxy url                   输出带 token 的面板地址
  mproxy version               输出版本号

环境变量:
  MPROXY_DATA_DIR              数据目录（默认 Android: /data/adb/mproxy，开发: ./.dev-data）
  MPROXY_MODULE_DIR            模块目录（用于查找内置内核）
  MPROXY_DEV=1                 关闭 /api 鉴权（仅本地开发）
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	cmd := os.Args[1]
	args := os.Args[2:]
	switch cmd {
	case "serve":
		runServe(args)
	case "boot":
		runBoot(args)
	case "stop":
		runStop(args)
	case "status":
		runStatus(args)
	case "toggle":
		runToggle(args)
	case "url":
		runURL(args)
	case "version", "-v", "--version":
		fmt.Println(version.Version)
	case "help", "-h", "--help":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "未知子命令: %s\n\n%s", cmd, usage)
		os.Exit(2)
	}
}

// runServe 常驻运行后端。
func runServe(_ []string) {
	paths := sysutil.NewPaths()
	if err := paths.EnsureDirs(); err != nil {
		fatalf("%v", err)
	}
	token, err := sysutil.LoadOrCreateToken(paths)
	if err != nil {
		fatalf("%v", err)
	}
	logWriter, err := sysutil.NewRotatingWriter(paths.BackendLogPath(), 5<<20)
	if err != nil {
		fatalf("打开后端日志失败: %v", err)
	}
	defer logWriter.Close()
	logger := log.New(io.MultiWriter(os.Stderr, logWriter), "", log.LstdFlags)

	cfg, err := config.Open(paths)
	if err != nil {
		fatalf("加载配置失败: %v", err)
	}
	if loadErr := cfg.LoadError(); loadErr != nil {
		logger.Printf("警告: 配置文件无效，请通过面板修复: %v", loadErr)
	}
	mgr := core.NewManager(paths, cfg, logger)
	jobs := job.NewManager()
	dl := download.New()

	if err := sysutil.WritePIDFile(paths.BackendPIDPath(), os.Getpid()); err != nil {
		logger.Printf("写入后端 pid 失败: %v", err)
	}
	// 只删除自己写下的 pid 文件：若 pid 已被别的进程复用，不要误删。
	defer sysutil.RemovePIDIfMatch(paths.BackendPIDPath(), os.Getpid())

	// 后端自身也脱离 root 管理器的 cgroup：否则管理器切后台/清理时可能连带杀掉后端，
	// 代理随之失去自愈与面板。平台不支持时静默跳过，失败只告警。
	if err := sysutil.MoveProcessToRootCgroup(os.Getpid()); err != nil {
		if !errors.Is(err, sysutil.ErrNotSupported) {
			logger.Printf("警告: 后端进程移入根 cgroup 失败（继续运行）: %v", err)
		}
	} else {
		logger.Printf("后端进程已移入根 cgroup（pid=%d）", os.Getpid())
	}

	srv, err := server.New(server.Options{
		Paths:     paths,
		Token:     token,
		DevMode:   isDevMode(),
		Config:    cfg,
		Core:      mgr,
		Jobs:      jobs,
		Download:  dl,
		Logger:    logger,
		Args:      []string{"serve"},
		StartedAt: time.Now(),
	})
	if err != nil {
		fatalf("初始化后端失败: %v", err)
	}
	logger.Printf("MProxyEvolution 后端 %s 启动，数据目录 %s", version.Version, paths.DataDir)

	// 按 auto-start 异步启动内核：开机时网络可能晚于服务就绪（慢开机/飞行模式），
	// 先等默认路由最长 120s，再带退避重试启动，避免永久停留在“已安装未运行”。
	// 整个流程在 goroutine 中执行，绝不阻塞后端 HTTP 服务。
	if doc, derr := cfg.Doc(); derr == nil && doc.Settings().AutoStart && mgr.Installed() && !mgr.Running() {
		go autoStartCore(mgr, logger)
	} else if derr == nil && !mgr.Installed() {
		logger.Printf("未安装 mihomo 内核，可稍后在面板中安装")
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		logger.Printf("收到退出信号，后端退出（内核保持运行）")
		os.Exit(0)
	}()

	if err := srv.ListenAndServe(); err != nil {
		fatalf("后端运行失败: %v", err)
	}
}

// bootNetworkTimeout 是开机后等待默认路由的最长时间（Android）。慢开机/飞行模式
// 下网络可能远晚于 sys.boot_completed 就绪，短等待会让内核永久停在“已安装未运行”。
const bootNetworkTimeout = 120 * time.Second

// devNetworkTimeout 是非 Android 开发环境的网络探测时长（桌面没有 Android 策略路由，
// 长时间等待只会拖慢本地联调）。
const devNetworkTimeout = 8 * time.Second

// autoStartBudget 是自动启动内核的总预算（等待网络 + 重试）。
const autoStartBudget = 5 * time.Minute

// autoStartCore 等待网络后带退避重试启动内核，运行在独立 goroutine，不阻塞 serve。
func autoStartCore(mgr *core.Manager, logger *log.Logger) {
	ctx, cancel := context.WithTimeout(context.Background(), autoStartBudget)
	defer cancel()

	networkTimeout := devNetworkTimeout
	if sysutil.IsAndroid() {
		networkTimeout = bootNetworkTimeout
	}
	if !sysutil.WaitForNetworkBackoff(networkTimeout) {
		logger.Printf("等待默认网络路由超时（%s），仍尝试启动内核；联网后可手动重启内核", networkTimeout)
	}

	// 最多 3 次尝试，失败后按 2s、5s 退避；ErrAlreadyRunning/ErrNotInstalled 不重试。
	const maxAttempts = 3
	backoffs := []time.Duration{2 * time.Second, 5 * time.Second}
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		err := mgr.Start(ctx)
		if err == nil {
			if attempt > 1 {
				logger.Printf("自动启动内核成功（第 %d 次尝试）", attempt)
			}
			return
		}
		if errors.Is(err, core.ErrAlreadyRunning) {
			logger.Printf("内核已在运行，跳过自动启动: %v", err)
			return
		}
		if errors.Is(err, core.ErrNotInstalled) {
			logger.Printf("自动启动内核失败（未安装内核）: %v", err)
			return
		}
		if attempt >= maxAttempts {
			logger.Printf("自动启动内核失败（已尝试 %d 次），可在面板中手动启动: %v", maxAttempts, err)
			return
		}
		delay := backoffs[attempt-1]
		logger.Printf("自动启动内核失败（第 %d/%d 次），%s 后重试: %v", attempt, maxAttempts, delay, err)
		select {
		case <-ctx.Done():
			logger.Printf("自动启动内核超时，放弃重试: %v", ctx.Err())
			return
		case <-time.After(delay):
		}
	}
}

// runBoot 检测后端，未启动则脱离拉起，最后一行输出面板 URL。
func runBoot(args []string) {
	fs := flag.NewFlagSet("boot", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	bootFlag := fs.Bool("boot", false, "由模块 service.sh 在开机时调用")
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}
	_ = bootFlag

	if ok, _ := checkHealth(1500 * time.Millisecond); ok {
		token, err := readToken()
		if err != nil {
			fatalf("%v", err)
		}
		printPanelURL(token)
		return
	}

	paths := sysutil.NewPaths()
	if err := paths.EnsureDirs(); err != nil {
		fatalf("%v", err)
	}
	exe, err := os.Executable()
	if err != nil {
		fatalf("获取可执行文件路径失败: %v", err)
	}
	cmd := exec.Command(exe, "serve")
	// 不要把 backend.log 作为 detached 子进程的输出：新后端 runServe 里会自己打开
	// RotatingWriter 写同一个文件，两处同时写会导致日志重复、轮转统计失效。
	// 传空串让子进程 stdout/stderr 指向 /dev/null，由新后端的 RotatingWriter 唯一负责。
	if err := sysutil.StartDetached(cmd, ""); err != nil {
		fatalf("拉起后端失败: %v", err)
	}
	deadline := time.Now().Add(20 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		if ok, herr := checkHealth(800 * time.Millisecond); ok {
			token, terr := readToken()
			if terr != nil {
				fatalf("%v", terr)
			}
			printPanelURL(token)
			return
		} else {
			lastErr = herr
		}
		time.Sleep(300 * time.Millisecond)
	}
	tail, _ := sysutil.TailFile(paths.BackendLogPath(), 30)
	fatalf("后端启动超时: %v\n后端日志：\n%s", lastErr, strings.Join(tail, "\n"))
}

// runStop 停止内核与后端。
func runStop(_ []string) {
	paths := sysutil.NewPaths()
	stopped := false
	// 先停内核；结束前校验 pid 对应的命令行确实是 mihomo，避免 pid 被复用时误杀。
	if pid, err := sysutil.ReadPID(paths.CorePIDPath()); err == nil {
		if sysutil.IsProcessAlive(pid) && sysutil.ProcessMatches(pid, "mihomo") {
			if err := sysutil.StopPID(pid, 5*time.Second); err != nil {
				fmt.Fprintf(os.Stderr, "停止 mihomo 内核失败: %v\n", err)
				os.Exit(1)
			}
			stopped = true
		} else {
			// pid 文件与 mihomo 不匹配（进程已退出或 pid 被复用）：只删除 pid 文件并视为未运行。
			sysutil.RemovePIDIfMatch(paths.CorePIDPath(), pid)
		}
	}
	// 再停后端；同样先校验命令行确实是 mproxy。
	if pid, err := sysutil.ReadPID(paths.BackendPIDPath()); err == nil {
		if sysutil.IsProcessAlive(pid) && sysutil.ProcessMatches(pid, "mproxy") {
			if err := sysutil.StopPID(pid, 5*time.Second); err != nil {
				fmt.Fprintf(os.Stderr, "停止 MProxyEvolution 后端失败: %v\n", err)
				os.Exit(1)
			}
			stopped = true
		}
		sysutil.RemovePIDIfMatch(paths.BackendPIDPath(), pid)
	} else if ok, _ := checkHealth(800 * time.Millisecond); ok {
		// 没有 pid 文件但后端仍在运行：尝试通过 API 关闭
		if token, terr := readToken(); terr == nil {
			if err := apiPost(token, "/api/backend/shutdown", nil); err == nil {
				stopped = true
			}
		}
	}
	if stopped {
		fmt.Println("MProxyEvolution 已停止")
	} else {
		fmt.Println("MProxyEvolution 未在运行")
	}
}

// runStatus 输出中文状态摘要。
func runStatus(_ []string) {
	ok, err := checkHealth(2 * time.Second)
	if !ok {
		fmt.Println("MProxyEvolution 后端未运行")
		if err != nil {
			fmt.Fprintf(os.Stderr, "健康检查失败: %v\n", err)
		}
		os.Exit(1)
	}
	token, terr := readToken()
	if terr != nil {
		fatalf("%v", terr)
	}
	var st statusResponse
	if err := apiGet(token, "/api/status", &st); err != nil {
		fatalf("读取状态失败: %v", err)
	}
	fmt.Printf("MProxyEvolution 后端 v%s（pid %d，已运行 %d 秒）\n", st.Backend.Version, st.Backend.PID, st.Backend.UptimeSec)
	if !st.Core.Installed {
		fmt.Println("内核: 未安装")
	} else if st.Core.Running {
		fmt.Printf("内核: %s 运行中（pid %d）\n", unknown(st.Core.Version), st.Core.PID)
	} else {
		fmt.Println("内核: 已安装，未运行")
	}
	fmt.Printf("控制器: %s:%d %s\n", st.Core.Controller.Host, st.Core.Controller.Port, reachText(st.Core.Controller.Reachable))
	fmt.Printf("配置: %s（%s，%d 条规则 / %d 个节点 / %d 个分组）\n",
		st.Config.Path, validText(st.Config.Valid), st.Config.Summary.RuleCount, st.Config.Summary.ProxyCount, st.Config.Summary.GroupCount)
	fmt.Printf("模式: %s，端口: mixed %d / socks %d / http %d，TUN: %s\n",
		st.Config.Summary.ProxyMode, st.Config.Summary.MixedPort, st.Config.Summary.SocksPort, st.Config.Summary.Port, tunText(st.Config.Summary.TunEnabled))
	printPanelURL(token)
}

// runToggle 运行中则停止，否则拉起。
func runToggle(args []string) {
	if ok, _ := checkHealth(1500 * time.Millisecond); ok {
		runStop(nil)
		return
	}
	runBoot(args)
}

// runURL 输出带 token 的面板地址。
func runURL(_ []string) {
	token, err := readToken()
	if err != nil {
		fatalf("%v", err)
	}
	printPanelURL(token)
}

func printPanelURL(token string) {
	fmt.Printf("http://127.0.0.1:9091/?token=%s#/settings?section=moduleSettings\n", token)
}

func readToken() (string, error) {
	paths := sysutil.NewPaths()
	if err := paths.EnsureDirs(); err != nil {
		return "", err
	}
	return sysutil.LoadOrCreateToken(paths)
}

func isDevMode() bool {
	v := strings.TrimSpace(os.Getenv("MPROXY_DEV"))
	return v == "1" || strings.EqualFold(v, "true")
}

func checkHealth(timeout time.Duration) (bool, error) {
	client := &http.Client{Timeout: timeout}
	resp, err := client.Get("http://127.0.0.1:9091/healthz")
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK, nil
}

func apiGet(token, path string, dst any) error {
	req, err := http.NewRequest(http.MethodGet, "http://127.0.0.1:9091"+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("X-MProxy-Token", token)
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return json.Unmarshal(body, dst)
}

func apiPost(token, path string, body []byte) error {
	reader := io.Reader(nil)
	if body != nil {
		reader = strings.NewReader(string(body))
	}
	req, err := http.NewRequest(http.MethodPost, "http://127.0.0.1:9091"+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("X-MProxy-Token", token)
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	return nil
}

// statusResponse 是 CLI 读取 /api/status 的最小结构。
type statusResponse struct {
	Backend struct {
		Version   string `json:"version"`
		PID       int    `json:"pid"`
		UptimeSec int64  `json:"uptimeSec"`
	} `json:"backend"`
	Core struct {
		Installed  bool   `json:"installed"`
		Running    bool   `json:"running"`
		PID        int    `json:"pid"`
		Version    string `json:"version"`
		Controller struct {
			Host      string `json:"host"`
			Port      int    `json:"port"`
			Reachable bool   `json:"reachable"`
		} `json:"controller"`
	} `json:"core"`
	Config struct {
		Path    string `json:"path"`
		Valid   bool   `json:"valid"`
		Summary struct {
			ProxyMode  string `json:"proxyMode"`
			MixedPort  int    `json:"mixedPort"`
			SocksPort  int    `json:"socksPort"`
			Port       int    `json:"port"`
			TunEnabled bool   `json:"tunEnabled"`
			ProxyCount int    `json:"proxyCount"`
			GroupCount int    `json:"groupCount"`
			RuleCount  int    `json:"ruleCount"`
		} `json:"summary"`
	} `json:"config"`
}

func unknown(v string) string {
	if v == "" {
		return "未知版本"
	}
	return v
}

func reachText(ok bool) string {
	if ok {
		return "可达"
	}
	return "不可达"
}

func validText(ok bool) string {
	if ok {
		return "有效"
	}
	return "无效"
}

func tunText(ok bool) string {
	if ok {
		return "开启"
	}
	return "关闭"
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
