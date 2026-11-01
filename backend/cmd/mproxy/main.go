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
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"mproxy/internal/config"
	"mproxy/internal/core"
	"mproxy/internal/download"
	"mproxy/internal/job"
	"mproxy/internal/moduledesc"
	"mproxy/internal/server"
	"mproxy/internal/sysutil"
	"mproxy/internal/version"
)

const usage = `MProxyEvolution 后端

用法:
  mproxy serve                 常驻运行后端（固定监听 127.0.0.1:9091）
  mproxy boot [--boot]         检测后端，未启动则拉起；默认不自动启动内核，--boot 供 service.sh 开机自启
  mproxy stop                  停止内核与后端
  mproxy status                输出中文状态摘要（未运行返回非 0）
  mproxy toggle                运行中则停止，否则拉起
  mproxy action                模块页「执行」按钮：切换服务；启动时阻塞等待内核就绪，失败立即报错
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
	case "action":
		runAction(args)
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

	// mihomo 是 Go 静态二进制，Android 上可能读不到系统 CA 库，导致订阅/规则集
	// TLS 校验报 x509 unknown authority。后端内嵌了 Mozilla 根证书：写到数据目录
	// 并通过 SSL_CERT_FILE 传给 mihomo，动态/静态链接都不需要改。
	caPath := filepath.Join(paths.DataDir, "cacert.pem")
	if err := os.WriteFile(caPath, download.CACertPEM(), 0o644); err != nil {
		logger.Printf("警告: 写入 mihomo CA 证书失败（继续运行）: %v", err)
	} else {
		_ = os.Setenv("SSL_CERT_FILE", caPath)
		logger.Printf("mihomo 证书环境已就绪: SSL_CERT_FILE=%s", caPath)
	}

	cfg, err := config.Open(paths)
	if err != nil {
		fatalf("加载配置失败: %v", err)
	}
	if loadErr := cfg.LoadError(); loadErr != nil {
		logger.Printf("警告: 配置文件无效，请通过面板修复: %v", loadErr)
	}
	mgr := core.NewManager(paths, cfg, logger)
	// 动态更新模块描述：让 KernelSU/Magisk 模块页无需打开 WebUI 就能看到当前状态。
	// 先注册状态回调（启动/停止/崩溃/看门狗重启后刷新），再立即写一次（显示“已停止/已安装”）。
	mgr.SetStateHook(func() { refreshModuleDescription(mgr, cfg, paths, logger) })
	refreshModuleDescription(mgr, cfg, paths, logger)
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

	noAutoStart := os.Getenv("MPROXY_NO_AUTOSTART") == "1"
	// 按 auto-start 异步启动内核：不再先阻塞等待默认路由（Android 策略路由下
	// `/proc/net/route`/`ip route` 常探测不到主表默认路由，最长白等 120s）。
	// 直接启动内核，若 TUN 未生效再停止内核、等待网络后退避重试，既加快首屏也保留慢开机兜底。
	// 整个流程在 goroutine 中执行，绝不阻塞后端 HTTP 服务。
	if noAutoStart {
		logger.Printf("按本次启动策略不自动启动内核（由模块「执行」按钮控制）")
	} else if doc, derr := cfg.Doc(); derr == nil && doc.Settings().AutoStart && mgr.Installed() && !mgr.Running() {
		go autoStartCore(mgr, cfg, logger)
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

// autoStartBudget 是自动启动内核的总预算（启动 + TUN 重试）。
const autoStartBudget = 5 * time.Minute

// autoStartBackoffs 是内核启动失败或 TUN 未生效时的重试间隔（最多 5 次尝试）。
var autoStartBackoffs = []time.Duration{
	2 * time.Second,
	5 * time.Second,
	10 * time.Second,
	20 * time.Second,
}

// tunStartFailed 判断“内核进程已启动且控制器可达，但 TUN 实际未生效”。
// TUN 建链失败时 mihomo 进程不会退出，只靠 Running 会误判成功。
func tunStartFailed(st core.Status, tunExpected bool) bool {
	return tunExpected && st.Installed && st.Running && st.Controller.Reachable && !st.Controller.TunEnabled
}

// autoStartCore 立即尝试启动内核；启动失败或 TUN 未生效时按短退避重试，
// 运行在独立 goroutine，不阻塞 serve。参考 akashaProxy / box4magisk / tunclash：
// 开机后直接启动内核，把网络未就绪交给重试/watchdog，而不是先白等两分钟。
func autoStartCore(mgr *core.Manager, cfg *config.Manager, logger *log.Logger) {
	ctx, cancel := context.WithTimeout(context.Background(), autoStartBudget)
	defer cancel()

	tunExpected := false
	if doc, err := cfg.Doc(); err == nil {
		tunExpected = doc.GetBool(false, "tun", "enable")
	}
	const maxAttempts = 5
	startedAt := time.Now()
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		err := mgr.Start(ctx)
		if err == nil {
			if tunExpected {
				if st := mgr.Status(); tunStartFailed(st, true) {
					logger.Printf("自动启动后 TUN 未生效，切换手工策略路由")
					if ferr := mgr.EnableManualTUNFallback(ctx); ferr != nil {
						logger.Printf("切换手工 TUN 失败（内核保持可用状态，可在面板重试）: %v", ferr)
					} else {
						logger.Printf("已切换手工 TUN 模式（tun.auto-route=false），耗时 %s", time.Since(startedAt).Round(time.Millisecond))
					}
					return
				}
			}
			if attempt > 1 {
				logger.Printf("自动启动内核成功（第 %d 次尝试，耗时 %s）", attempt, time.Since(startedAt).Round(time.Millisecond))
			} else {
				logger.Printf("自动启动内核成功（耗时 %s）", time.Since(startedAt).Round(time.Millisecond))
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
		// mihomo v1.19.33 在 Android 原生 TUN auto-route 失败时可能 panic
		// （exit status 2）。参考 akashaProxy 的兜底：立即切手工 TUN，
		// 不要用同一份原生配置重试 5 次。
		if strings.Contains(err.Error(), "exit status 2") {
			logger.Printf("原生 TUN 启动崩溃（exit status 2），回退手工 TUN")
			if ferr := mgr.EnableManualTUNFallback(ctx); ferr != nil {
				logger.Printf("回退手工 TUN 失败: %v", ferr)
			} else {
				logger.Printf("已回退手工 TUN（tun.auto-route=false），耗时 %s", time.Since(startedAt).Round(time.Millisecond))
				return
			}
		}
		if attempt >= maxAttempts {
			logger.Printf("自动启动内核失败（已尝试 %d 次），可在面板中手动启动: %v", maxAttempts, err)
			return
		}
		delay := autoStartBackoffs[attempt-1]
		logger.Printf("自动启动内核失败（第 %d/%d 次），%s 后重试: %v", attempt, maxAttempts, delay, err)
		if !sleepCtx(ctx, delay) {
			return
		}
	}
}

// sleepCtx 等待 d，上下文取消时返回 false（启动预算耗尽）。
func sleepCtx(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}

// refreshModuleDescription 计算当前内核状态与配置摘要，并写回模块的 module.prop
// description 行（KernelSU/Magisk 模块页会自动重新读取）。
// 桌面开发环境没有 module.prop 时静默跳过；其它错误只记日志，绝不影响后端运行。
func refreshModuleDescription(mgr *core.Manager, cfg *config.Manager, paths sysutil.Paths, logger *log.Logger) {
	summary := config.Summary{}
	if doc, err := cfg.Doc(); err == nil {
		summary = doc.Summary()
	}
	desc := moduledesc.Build(version.Version, mgr.Status(), summary)
	if err := moduledesc.WriteDescription(paths.ModuleDir, desc); err != nil {
		if errors.Is(err, moduledesc.ErrModulePropNotFound) {
			return
		}
		logger.Printf("更新模块描述失败: %v", err)
	}
}

// startBackendDetached 确保后端已启动，返回 token；失败时返回错误而不是直接退出，
// 便于 action 子命令在启动内核前给出明确的失败原因。
func startBackendDetached(allowAutoStart bool) (string, error) {
	if ok, _ := checkHealth(1500 * time.Millisecond); ok {
		return readToken()
	}

	paths := sysutil.NewPaths()
	if err := paths.EnsureDirs(); err != nil {
		return "", err
	}
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("获取可执行文件路径失败: %w", err)
	}
	cmd := exec.Command(exe, "serve")
	if !allowAutoStart {
		cmd.Env = append(os.Environ(), "MPROXY_NO_AUTOSTART=1")
	}
	// 不要把 backend.log 作为 detached 子进程的输出：新后端 runServe 里会自己打开
	// RotatingWriter 写同一个文件，两处同时写会导致日志重复、轮转统计失效。
	// 传空串让子进程 stdout/stderr 指向 /dev/null，由新后端的 RotatingWriter 唯一负责。
	if err := sysutil.StartDetached(cmd, ""); err != nil {
		return "", fmt.Errorf("拉起后端失败: %w", err)
	}
	deadline := time.Now().Add(20 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		if ok, herr := checkHealth(800 * time.Millisecond); ok {
			return readToken()
		} else {
			lastErr = herr
		}
		time.Sleep(300 * time.Millisecond)
	}
	tail, _ := sysutil.TailFile(paths.BackendLogPath(), 30)
	if lastErr == nil {
		lastErr = fmt.Errorf("健康检查超时")
	}
	return "", fmt.Errorf("后端启动超时: %w\n后端日志：\n%s", lastErr, strings.Join(tail, "\n"))
}

// runBoot 检测后端，未启动则脱离拉起，最后一行输出面板 URL。
func runBoot(args []string) {
	fs := flag.NewFlagSet("boot", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	bootFlag := fs.Bool("boot", false, "由模块 service.sh 在开机时调用")
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}

	allowAutoStart := *bootFlag
	token, err := startBackendDetached(allowAutoStart)
	if err != nil {
		fatalf("%v", err)
	}
	// 后端起来时内核可能仍在异步启动：等内核/控制器就绪后再返回面板地址，
	// 避免 WebUI 打开后连接、日志、规则等页面空白十几秒。最多等 12s，失败也照常打开。
	if allowAutoStart {
		if ok, _ := checkHealth(1500 * time.Millisecond); ok {
			deadline := time.Now().Add(12 * time.Second)
			for time.Now().Before(deadline) {
				var st statusResponse
				if err := apiGet(token, "/api/status", &st); err == nil {
					tunReady := !st.Config.Summary.TunEnabled || st.Core.Controller.TunEnabled
					if st.Core.Running && st.Core.Controller.Reachable && tunReady {
						break
					}
					if !st.Core.Installed || !st.Config.Summary.AutoStart {
						break
					}
				}
				time.Sleep(300 * time.Millisecond)
			}
		}
	}
	printPanelURL(token)
}

// runStop 停止内核与后端。
func runStop(_ []string) {
	paths := sysutil.NewPaths()
	stopped := false
	// 优先走后端 API：/api/core/stop 会同步执行 Manager.Stop（恢复 Private DNS、清理
	// 手工 TUN 的 iptables/ip rule/route），比直接 kill 进程更完整；随后关闭后端并
	// 阻塞等待 /healthz 不再响应，保证「停止」返回时规则和进程都已收尾。
	if ok, _ := checkHealth(1500 * time.Millisecond); ok {
		if token, terr := readToken(); terr == nil {
			if err := apiPostWithTimeout(token, "/api/core/stop", nil, 30*time.Second); err != nil {
				fmt.Fprintf(os.Stderr, "通过 API 停止 mihomo 内核失败，将回退到 pid 清理: %v\n", err)
			} else {
				stopped = true
			}
			if err := apiPost(token, "/api/backend/shutdown", nil); err != nil {
				fmt.Fprintf(os.Stderr, "通过 API 关闭后端失败，将回退到 pid 清理: %v\n", err)
			} else {
				stopped = true
			}
			deadline := time.Now().Add(10 * time.Second)
			for time.Now().Before(deadline) {
				if healthy, _ := checkHealth(400 * time.Millisecond); !healthy {
					break
				}
				time.Sleep(200 * time.Millisecond)
			}
		}
	}
	// 回退路径：API 不可用/超时后按 pid 清理残留进程（同样校验 cmdline，避免误杀）。
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
	runAction(args)
}

// runAction 是模块页「执行」按钮的实现：运行中则停止；未运行时拉起后端并阻塞等待内核
// 真正就绪（控制器可达、TUN 状态符合预期），失败时立即输出原因与内核/后端日志尾部。
func runAction(_ []string) {
	if ok, _ := checkHealth(1500 * time.Millisecond); ok {
		runStop(nil)
		return
	}

	token, err := startBackendDetached(false)
	if err != nil {
		fatalf("%v", err)
	}

	var st statusResponse
	if err := apiGet(token, "/api/status", &st); err != nil {
		fatalCore("读取内核状态失败: " + err.Error())
	}
	if !st.Core.Installed {
		fatalCore("内核未安装：请打开 WebUI 安装内核后重试")
	}

	if !st.Core.Running {
		if err := apiPostWithTimeout(token, "/api/core/start", nil, 90*time.Second); err != nil {
			// 自动启动可能刚好在并发进行：若状态显示已在运行则继续等待，否则立即报错。
			var cur statusResponse
			if apiGet(token, "/api/status", &cur) != nil || !cur.Core.Running {
				fatalCore("内核启动失败: " + err.Error())
			}
			st = cur
		} else if err := apiGet(token, "/api/status", &st); err != nil {
			fatalCore("读取内核状态失败: " + err.Error())
		}
	}

	deadline := time.Now().Add(25 * time.Second)
	for {
		var cur statusResponse
		if err := apiGet(token, "/api/status", &cur); err == nil {
			st = cur
		}
		if st.Core.Running && st.Core.Controller.Reachable {
			// TUN 开启时要求控制器也确认 tunEnabled；mihomo 启动失败 TUN 时进程仍会存活，
			// 只看 running 会误报成功。
			if !st.Config.Summary.TunEnabled || st.Core.Controller.TunEnabled {
				break
			}
		}
		if time.Now().After(deadline) {
			switch {
			case st.Core.Running && st.Core.Controller.Reachable &&
				st.Config.Summary.TunEnabled && !st.Core.Controller.TunEnabled:
				fatalCore("内核已启动，但 TUN 未生效（控制器报告 tunEnabled=false）；" +
					"常见原因是默认网卡/网络未就绪，联网后重试或检查 config.yaml 的 tun 配置")
			default:
				fatalCore("等待内核就绪超时（控制器不可达）")
			}
		}
		time.Sleep(500 * time.Millisecond)
	}

	fmt.Printf("内核已启动：%s（pid %d）\n", unknown(st.Core.Version), st.Core.PID)
	fmt.Printf("控制器：%s:%d %s\n", st.Core.Controller.Host, st.Core.Controller.Port,
		reachText(st.Core.Controller.Reachable))
	fmt.Printf("TUN：%s\n", tunText(st.Config.Summary.TunEnabled))
	if st.Core.LastExit != "" {
		fmt.Printf("上次退出：%s\n", st.Core.LastExit)
	}
}

// fatalCore 输出启动失败原因，并附带内核/后端日志尾部，便于模块页「执行」按钮当场定位。
func fatalCore(reason string) {
	paths := sysutil.NewPaths()
	var b strings.Builder
	b.WriteString(reason)
	if tail, err := sysutil.TailFile(paths.CoreLogPath(), 20); err == nil && len(tail) > 0 {
		b.WriteString("\n内核日志（最后 20 行）：\n")
		b.WriteString(strings.Join(tail, "\n"))
	}
	if tail, err := sysutil.TailFile(paths.BackendLogPath(), 15); err == nil && len(tail) > 0 {
		b.WriteString("\n后端日志（最后 15 行）：\n")
		b.WriteString(strings.Join(tail, "\n"))
	}
	fatalf("%s", b.String())
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
	fmt.Printf("http://127.0.0.1:9091/?token=%s#/\n", token)
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
	return apiPostWithTimeout(token, path, body, 10*time.Second)
}

// apiPostWithTimeout 与 apiPost 相同，但允许调用方放宽超时（内核启动接口可能阻塞数十秒）。
func apiPostWithTimeout(token, path string, body []byte, timeout time.Duration) error {
	reader := io.Reader(nil)
	if body != nil {
		reader = strings.NewReader(string(body))
	}
	req, err := http.NewRequest(http.MethodPost, "http://127.0.0.1:9091"+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("X-MProxy-Token", token)
	client := &http.Client{Timeout: timeout}
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
		Restarts   int    `json:"restarts"`
		LastExit   string `json:"lastExit"`
		Controller struct {
			Host       string `json:"host"`
			Port       int    `json:"port"`
			Reachable  bool   `json:"reachable"`
			TunEnabled bool   `json:"tunEnabled"`
		} `json:"controller"`
	} `json:"core"`
	Config struct {
		Path    string `json:"path"`
		Valid   bool   `json:"valid"`
		Summary struct {
			ProxyMode  string `json:"proxyMode"`
			AutoStart  bool   `json:"autoStart"`
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
