// Package core 管理 mihomo 子进程（启停、重启、热重载、状态）与内核版本/安装。
package core

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"mproxy/internal/config"
	"mproxy/internal/sysutil"
)

// 常见错误。
var (
	ErrNotInstalled   = errors.New("未安装 mihomo 内核")
	ErrAlreadyRunning = errors.New("mihomo 内核已在运行")
	ErrNotRunning     = errors.New("mihomo 内核未运行")
)

// ControllerStatus 是控制器与端口状态。
type ControllerStatus struct {
	Host       string `json:"host"`
	Port       int    `json:"port"`
	Reachable  bool   `json:"reachable"`
	SecretSet  bool   `json:"secretSet"`
	Mode       string `json:"mode"`
	MixedPort  int    `json:"mixedPort"`
	SocksPort  int    `json:"socksPort"`
	HTTPPort   int    `json:"httpPort"`
	TunEnabled bool   `json:"tunEnabled"`
}

// Status 是内核状态。
type Status struct {
	Installed  bool             `json:"installed"`
	Running    bool             `json:"running"`
	PID        int              `json:"pid"`
	Version    string           `json:"version"`
	StartedAt  string           `json:"startedAt"`
	UptimeSec  int64            `json:"uptimeSec"`
	Restarts   int              `json:"restarts"`
	LastExit   string           `json:"lastExit,omitempty"`
	Controller ControllerStatus `json:"controller"`
}

// Manager 管理 mihomo 子进程。
type Manager struct {
	paths  sysutil.Paths
	cfg    *config.Manager
	logger *log.Logger

	// startMu 串行化整个“检查 → 启动 → 等待控制器”过程，避免并发 Start 拉起多个内核。
	// 注意：持有 startMu 时不要长期持有 mu。
	startMu sync.Mutex

	mu          sync.Mutex
	pid         int
	startedAt   time.Time
	lastExitErr error

	// 自愈（watchdog）状态，均由 mu 保护：
	// intentionalStop 标记“主动停止/重启中”，用于区分崩溃与用户操作；
	// restarts 是当前连续自动重启次数（手动 Start 时清零）；
	// lastExit* 保存最近一次内核退出信息（写 state/last_exit 并在 Status 暴露）。
	intentionalStop bool
	restarts        int
	lastExitAt      time.Time
	lastExitPID     int
	lastExitReason  string
	// starting 表示 start() 已拉起进程但尚未确认控制器就绪；此期间的进程退出
	// 由 start() 自己处理（返回错误或由看门狗重试），afterExit 不应再触发一次自愈。
	starting bool

	// restartMu/restarting 保证同一时间只有一个自动重启 goroutine，避免重启风暴。
	restartMu  sync.Mutex
	restarting bool

	versionCache string
	external     bool
}

// maxAutoRestarts 是内核非主动退出后的最大自动重启次数。
const maxAutoRestarts = 5

// restartBackoffFn 计算第 n 次（从 0 开始）自动重启前的等待时间，测试可替换。
var restartBackoffFn = restartBackoff

// restartBackoff 返回指数退避等待时间：0.5s、1s、2s、4s、8s……上限 30s。
func restartBackoff(attempt int) time.Duration {
	if attempt < 0 {
		attempt = 0
	}
	d := 500 * time.Millisecond << uint(attempt)
	if d > 30*time.Second || d <= 0 {
		d = 30 * time.Second
	}
	return d
}

// NewManager 创建内核管理器，并接管已有 pid 文件。
func NewManager(paths sysutil.Paths, cfg *config.Manager, logger *log.Logger) *Manager {
	if logger == nil {
		logger = log.Default()
	}
	m := &Manager{paths: paths, cfg: cfg, logger: logger}
	m.adoptExisting()
	m.loadLastExit()
	return m
}

// loadLastExit 在后端重启后恢复上次内核退出信息（state/last_exit），
// 供 /api/status 与诊断页展示；内核正在运行（已接管）时视为过期，不加载。
func (m *Manager) loadLastExit() {
	if m.PID() > 0 {
		return
	}
	info, err := sysutil.ReadLastExit(m.paths.LastExitPath())
	if err != nil || info.Time.IsZero() {
		return
	}
	m.mu.Lock()
	m.lastExitAt = info.Time
	m.lastExitPID = info.PID
	m.lastExitReason = info.Err
	m.mu.Unlock()
}

// coreProcessNeedle 是内核命令行必须包含的片段，用于 pid 身份校验。
const coreProcessNeedle = "mihomo"

// adoptExisting 接管 pid 文件中仍在运行的 mihomo 内核。
func (m *Manager) adoptExisting() {
	pid, err := sysutil.ReadPID(m.paths.CorePIDPath())
	if err == nil {
		if sysutil.IsProcessAlive(pid) && sysutil.ProcessMatches(pid, coreProcessNeedle) {
			m.pid = pid
			if st, serr := os.Stat(m.paths.CorePIDPath()); serr == nil {
				m.startedAt = st.ModTime()
			}
			m.logger.Printf("已接管运行中的 mihomo 内核（pid=%d）", pid)
			return
		}
		// pid 文件指向的进程已退出或不是 mihomo（pid 被复用/残留）：只删除自己的 pid 文件，
		// 绝不能仅凭 pid 就去结束一个可能完全无关的进程。
		sysutil.RemovePIDIfMatch(m.paths.CorePIDPath(), pid)
	}
	// 没有 pid 文件时，先清理同一路径下残留的 mihomo 进程，再检查外部控制器。
	// 模块升级/后端重启后旧内核可能仍占着 7890/7891/9090/1053，导致新内核
	// 全部绑定失败，同时旧控制器的 secret 不同会让状态检测返回 401。
	m.killStaleCores()
	if m.controllerReachable() {
		m.external = true
		m.logger.Printf("检测到外部启动的 mihomo 控制器（%s），后端仅接管状态与代理", m.controllerAddr())
	}
}

// verifiedPID 返回经过身份校验的内核 pid（优先内存值，其次 pid 文件）。
// pid 存活但命令行不是 mihomo 时说明 pid 被复用，只删除不匹配的 pid 文件，
// 返回 0（视为未运行），绝不返回该 pid。
func (m *Manager) verifiedPID() int {
	m.mu.Lock()
	pid := m.pid
	m.mu.Unlock()
	if pid > 0 {
		if sysutil.ProcessMatches(pid, coreProcessNeedle) {
			return pid
		}
		m.mu.Lock()
		if m.pid == pid {
			m.pid = 0
			m.startedAt = time.Time{}
		}
		m.mu.Unlock()
		sysutil.RemovePIDIfMatch(m.paths.CorePIDPath(), pid)
	}
	pid, err := sysutil.ReadPID(m.paths.CorePIDPath())
	if err != nil || pid <= 0 || !sysutil.ProcessMatches(pid, coreProcessNeedle) {
		if err == nil && pid > 0 {
			sysutil.RemovePIDIfMatch(m.paths.CorePIDPath(), pid)
		}
		return 0
	}
	return pid
}

// killStaleCores 结束 cmdline 指向本模块内核路径、但未被 pid 文件接管的残留进程。
func (m *Manager) killStaleCores() {
	if !sysutil.IsAndroid() {
		return
	}
	target := m.paths.InstallCorePath()
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return
	}
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid <= 0 || pid == os.Getpid() {
			continue
		}
		raw, err := sysutil.ProcessCmdline(pid)
		if err != nil {
			continue
		}
		if !strings.Contains(raw, target) {
			continue
		}
		m.logger.Printf("清理残留 mihomo 进程（pid=%d）", pid)
		if err := sysutil.StopPID(pid, 3*time.Second); err != nil {
			m.logger.Printf("清理残留 mihomo 失败（pid=%d）: %v", pid, err)
		}
	}
}

// controllerAddr 返回当前配置的控制器地址。
func (m *Manager) controllerAddr() string {
	if doc, err := m.cfg.Doc(); err == nil {
		addr, _ := doc.Controller()
		return addr
	}
	return sysutil.DefaultController
}

// controllerReachable 检查 external-controller /version 是否可访问。
func (m *Manager) controllerReachable() bool {
	doc, err := m.cfg.Doc()
	if err != nil {
		return false
	}
	addr, secret := doc.Controller()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, verr := NewController(addr, secret).Version(ctx)
	return verr == nil
}

// isExternal 判断当前内核是否由外部进程托管。
func (m *Manager) isExternal() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.pid == 0 && m.external
}

// Paths 返回数据路径。
func (m *Manager) Paths() sysutil.Paths { return m.paths }

// Logger 返回后端日志器。
func (m *Manager) Logger() *log.Logger { return m.logger }

// CoreBinary 返回当前使用的内核路径（不存在返回空）。
func (m *Manager) CoreBinary() string { return m.paths.FindCoreBinary() }

// Installed 判断内核是否已安装。
func (m *Manager) Installed() bool { return m.CoreBinary() != "" }

// PID 返回内核进程 pid（0 表示未运行）。
func (m *Manager) PID() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.pid
}

// Running 判断内核是否运行：优先检查托管进程，其次检查外部控制器是否可达。
func (m *Manager) Running() bool {
	m.mu.Lock()
	pid := m.pid
	external := m.external
	m.mu.Unlock()
	if pid > 0 {
		if sysutil.IsProcessAlive(pid) {
			return true
		}
		// 进程已退出，清理状态；只删除内容仍等于该 pid 的 pid 文件。
		m.mu.Lock()
		if m.pid == pid {
			m.pid = 0
			m.startedAt = time.Time{}
		}
		m.mu.Unlock()
		sysutil.RemovePIDIfMatch(m.paths.CorePIDPath(), pid)
	}
	if m.controllerReachable() {
		if !external {
			m.mu.Lock()
			m.external = true
			m.mu.Unlock()
		}
		return true
	}
	if external {
		m.mu.Lock()
		m.external = false
		m.mu.Unlock()
	}
	return false
}

// Start 启动 mihomo 内核并等待控制器可用（最多 15s）。
// 这是用户/接口触发的启动：会清除“主动停止”标记并重置自动重启计数，
// 与看门狗内部使用的 start 区分开。
func (m *Manager) Start(ctx context.Context) error {
	m.mu.Lock()
	m.intentionalStop = false
	m.restarts = 0
	m.mu.Unlock()
	return m.start(ctx, false)
}

// start 是内部启动流程；不重置自动重启计数，供看门狗复用。
// retryPending 为 true 表示调用方（看门狗）还会继续重试：启动失败时保留 Private DNS
// 的关闭状态；false 表示一次性启动，失败即恢复系统 DNS 设置。
func (m *Manager) start(ctx context.Context, retryPending bool) error {
	// startMu 串行化整个“检查 → 启动 → 等待控制器”窗口：面板按钮、自动启动、
	// 更新后重启等并发调用不会重复拉起内核。窗口内不长期持有 m.mu。
	m.startMu.Lock()
	defer m.startMu.Unlock()

	// 锁内重新检查：可能已有别的 Start 拉起内核，或 pid 文件指向仍在运行的 mihomo。
	if pid := m.verifiedPID(); pid > 0 {
		m.mu.Lock()
		if m.pid == 0 {
			m.pid = pid
			if st, err := os.Stat(m.paths.CorePIDPath()); err == nil {
				m.startedAt = st.ModTime()
			}
		}
		m.mu.Unlock()
		return ErrAlreadyRunning
	}
	if m.controllerReachable() {
		m.mu.Lock()
		m.external = true
		m.mu.Unlock()
		return fmt.Errorf("%w：检测到已有 mihomo 控制器在运行（%s）", ErrAlreadyRunning, m.controllerAddr())
	}
	binary := m.CoreBinary()
	if binary == "" {
		return fmt.Errorf("%w：请先安装内核（数据目录 bin 下未找到 mihomo）", ErrNotInstalled)
	}
	doc, err := m.cfg.Doc()
	if err != nil {
		return fmt.Errorf("读取配置失败: %w", err)
	}
	m.prepareTUNInterface(doc)
	if err := doc.ValidateStructure(); err != nil {
		return fmt.Errorf("配置校验失败: %w", err)
	}
	if err := m.paths.EnsureDirs(); err != nil {
		return err
	}
	// TUN 需要接管全部 DNS 流量：先临时关闭系统 Private DNS（DoT），否则 853 端口
	// 的加密 DNS 会绕过 mihomo fake-ip。失败只记录日志，绝不影响启动。
	dnsChanged := false
	if sysutil.IsAndroid() && doc.GetBool(false, "tun", "enable") {
		dnsChanged = m.disablePrivateDNS()
	}
	// 只有“内核正在运行”或“看门狗还会重试”时保留 Private DNS 关闭状态；
	// 其它失败路径（手动启动失败/更新失败）恢复系统设置，避免用户 DNS 被留在 off。
	started := false
	defer func() {
		if started || retryPending || !dnsChanged {
			return
		}
		m.restorePrivateDNS()
	}()
	prepareAndroidNetwork(m.logger)
	logFile, err := m.openCoreLog()
	if err != nil {
		return fmt.Errorf("打开内核日志失败: %w", err)
	}
	cmd := exec.Command(binary, "-d", m.paths.DataDir, "-f", m.paths.ConfigPath())
	cmd.Dir = m.paths.DataDir
	// Stdout/Stderr 必须是真实的 *os.File：如果传 io.Writer（如 RotatingWriter），
	// os/exec 会创建管道；后端退出后内核继续写日志会收到 SIGPIPE 而被系统杀掉。
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	sysutil.SetDetached(cmd)
	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		return fmt.Errorf("启动 mihomo 内核失败: %w", err)
	}
	pid := cmd.Process.Pid
	m.mu.Lock()
	m.pid = pid
	m.startedAt = time.Now()
	m.lastExitErr = nil
	m.starting = true
	m.mu.Unlock()
	// 把内核移出管理器的 cgroup，避免 root 管理器切后台/清理时连带杀掉内核。
	// 平台不支持或 cgroup v1 只记录日志，不阻断启动。
	m.moveToRootCgroup(pid)
	if err := sysutil.WritePIDFile(m.paths.CorePIDPath(), pid); err != nil {
		m.logger.Printf("写入内核 pid 文件失败: %v", err)
	}
	m.logger.Printf("mihomo 内核已启动（pid=%d，二进制=%s）", pid, binary)
	go m.wait(cmd, logFile)

	if err := m.waitController(ctx, 15*time.Second); err != nil {
		tail := strings.Join(m.LogTail(30), "\n")
		// 只做低层清理，不标记“主动停止”，否则看门狗会误以为用户要停止而放弃重启。
		_ = m.stopProcess()
		m.mu.Lock()
		m.starting = false
		m.mu.Unlock()
		if tail == "" {
			tail = "（暂无日志）"
		}
		return fmt.Errorf("内核启动失败：%v\n最近日志：\n%s", err, tail)
	}
	started = true
	m.mu.Lock()
	m.starting = false
	m.mu.Unlock()
	m.clearLastExit()
	m.logger.Printf("mihomo 控制器已就绪（pid=%d）", pid)
	return nil
}

// prepareTUNInterface 修正 TUN 模式下的网卡配置。
// 注意：mihomo 的默认网卡键是顶层 `interface-name`，`tun.interface-name` 并不是有效键。
// 只有在 TUN 开启、用户显式关闭了 `tun.auto-detect-interface` 且顶层 interface-name 为空时，
// 才探测默认网卡写入，绝不覆盖用户“自动探测网卡”的设置；没有变化时不重写配置。
func (m *Manager) prepareTUNInterface(doc *config.Doc) {
	if !doc.GetBool(false, "tun", "enable") {
		return
	}
	if !doc.Has("tun", "auto-detect-interface") || doc.GetBool(true, "tun", "auto-detect-interface") {
		return
	}
	if strings.TrimSpace(doc.GetString("", "interface-name")) != "" {
		return
	}
	iface := sysutil.DefaultInterface()
	if iface == "" {
		m.logger.Printf("TUN 已关闭自动探测，但未探测到默认网卡，继续启动")
		return
	}
	doc.SetTop("interface-name", iface)
	if _, err := m.cfg.Save(doc); err != nil {
		m.logger.Printf("写入 TUN 默认网卡失败（继续启动）: %v", err)
		return
	}
	m.logger.Printf("TUN 已关闭自动探测，写入顶层 interface-name: %s", iface)
}

// coreLogMaxBytes 是内核日志单文件上限，超过后在启动时轮转为 core.log.1。
const coreLogMaxBytes = 5 << 20

// openCoreLog 以追加方式打开内核日志文件，返回真实的 *os.File。
// 文件超过 5MiB 时先轮转为 core.log.1（只保留一份历史日志）。
func (m *Manager) openCoreLog() (*os.File, error) {
	path := m.paths.CoreLogPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	if st, err := os.Stat(path); err == nil && st.Size() > coreLogMaxBytes {
		rotated := path + ".1"
		_ = os.Remove(rotated)
		if err := os.Rename(path, rotated); err != nil {
			// 改名失败时退回截断，保证日志仍可写入
			_ = os.Truncate(path, 0)
		}
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	return f, nil
}

func (m *Manager) wait(cmd *exec.Cmd, logFile *os.File) {
	err := cmd.Wait()
	if logFile != nil {
		_ = logFile.Close()
	}
	m.afterExit(cmd.Process.Pid, err)
}

// afterExit 处理内核进程退出：清理状态、记录上次退出信息，并按配置决定是否自愈。
// 抽成独立方法便于在测试中直接模拟“内核异常退出”，无需真实子进程。
func (m *Manager) afterExit(pid int, err error) {
	now := time.Now()
	m.mu.Lock()
	tracked := m.pid == pid
	intentional := m.intentionalStop
	starting := m.starting
	if tracked {
		m.pid = 0
		m.startedAt = time.Time{}
		m.lastExitErr = err
		m.lastExitAt = now
		m.lastExitPID = pid
		m.lastExitReason = exitReason(err)
	}
	m.mu.Unlock()
	if !tracked {
		// 新内核/新状态已接管（重启或更新期间旧进程稍后退出）：只记录日志，
		// 不写 last_exit、不触发自愈，避免覆盖当前内核的状态。
		m.logger.Printf("旧 mihomo 内核已退出（pid=%d，当前状态已被接管）", pid)
		return
	}
	// 比较后删除：只删除内容仍记录本进程 pid 的 pid 文件，避免误删新内核的 pid 文件。
	sysutil.RemovePIDIfMatch(m.paths.CorePIDPath(), pid)
	m.writeLastExit(now, pid, err, intentional)
	if err != nil {
		m.logger.Printf("mihomo 内核已退出: %v", err)
	} else {
		m.logger.Printf("mihomo 内核已退出")
	}
	if intentional {
		// 主动停止：Stop() 负责恢复 Private DNS，看门狗不介入。
		return
	}
	if starting {
		// 启动过程中的提前退出由 start() 自己处理（返回错误或看门狗继续重试），
		// 这里再触发一次自愈会与调用方形成重启风暴。
		return
	}
	if m.autoStartEnabled() {
		m.maybeAutoRestart()
		return
	}
	// 自动重启已关闭（或配置不可读）：恢复系统 Private DNS，避免被长期留在 off。
	m.restorePrivateDNS()
}

// exitReason 返回写入状态文件的退出原因文本（正常退出为空串）。
func exitReason(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// writeLastExit 持久化上次退出信息；失败只告警，不影响自愈流程。
func (m *Manager) writeLastExit(at time.Time, pid int, err error, intentional bool) {
	info := sysutil.LastExitInfo{
		Time:        at,
		PID:         pid,
		Err:         exitReason(err),
		Intentional: intentional,
	}
	if werr := sysutil.WriteLastExit(m.paths.LastExitPath(), info); werr != nil {
		m.logger.Printf("写入上次内核退出状态失败: %v", werr)
	}
}

// clearLastExit 在启动成功后清除“上次异常退出”记录，避免诊断页持续提示旧故障。
func (m *Manager) clearLastExit() {
	sysutil.RemoveLastExit(m.paths.LastExitPath())
	m.mu.Lock()
	m.lastExitAt = time.Time{}
	m.lastExitPID = 0
	m.lastExitReason = ""
	m.mu.Unlock()
}

// autoStartEnabled 读取 x-mproxy.auto-start；配置不可读时按 false 处理（不自愈）。
func (m *Manager) autoStartEnabled() bool {
	doc, err := m.cfg.Doc()
	if err != nil {
		return false
	}
	return doc.Settings().AutoStart
}

// maybeAutoRestart 在非主动退出后启动看门狗 goroutine；同一时间只允许一个。
func (m *Manager) maybeAutoRestart() {
	m.restartMu.Lock()
	if m.restarting {
		m.restartMu.Unlock()
		m.logger.Printf("内核自动重启已在进行中，跳过本次触发")
		return
	}
	m.restarting = true
	m.restartMu.Unlock()
	go m.restartLoop()
}

// restartLoop 按指数退避自动重启内核，最多 maxAutoRestarts 次；每次尝试都检查
// “主动停止”标记，用户中途 Stop/Update 时立即放弃，避免看门狗与用户操作打架。
func (m *Manager) restartLoop() {
	defer func() {
		m.restartMu.Lock()
		m.restarting = false
		m.restartMu.Unlock()
	}()
	for {
		m.mu.Lock()
		stopped := m.intentionalStop
		attempt := m.restarts
		m.mu.Unlock()
		if stopped {
			m.logger.Printf("内核已主动停止，取消自动重启")
			return
		}
		if attempt >= maxAutoRestarts {
			m.logger.Printf("内核自动重启已达上限（%d 次），停止自愈；请检查配置或手动启动", maxAutoRestarts)
			m.restorePrivateDNS()
			return
		}
		delay := restartBackoffFn(attempt)
		if !m.sleepRestart(delay) {
			m.logger.Printf("内核已主动停止，取消自动重启")
			return
		}
		m.mu.Lock()
		if m.intentionalStop {
			m.mu.Unlock()
			m.logger.Printf("内核已主动停止，取消自动重启")
			return
		}
		m.restarts++
		current := m.restarts
		m.mu.Unlock()
		m.logger.Printf("检测到内核异常退出，%s 后进行第 %d/%d 次自动重启", delay, current, maxAutoRestarts)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		err := m.start(ctx, true)
		cancel()
		if err == nil {
			m.logger.Printf("内核自动重启成功（pid=%d，累计重启 %d 次）", m.PID(), current)
			return
		}
		if errors.Is(err, ErrAlreadyRunning) {
			m.logger.Printf("内核已在运行（可能由其它途径启动），停止自动重启")
			m.restorePrivateDNS()
			return
		}
		if errors.Is(err, ErrNotInstalled) {
			m.logger.Printf("内核未安装，停止自动重启")
			m.restorePrivateDNS()
			return
		}
		m.logger.Printf("内核第 %d/%d 次自动重启失败: %v", current, maxAutoRestarts, err)
	}
}

// sleepRestart 分片等待退避时间，期间一旦检测到主动停止立即返回 false。
func (m *Manager) sleepRestart(d time.Duration) bool {
	deadline := time.Now().Add(d)
	for {
		m.mu.Lock()
		stopped := m.intentionalStop
		m.mu.Unlock()
		if stopped {
			return false
		}
		remain := time.Until(deadline)
		if remain <= 0 {
			return true
		}
		if remain > 100*time.Millisecond {
			remain = 100 * time.Millisecond
		}
		time.Sleep(remain)
	}
}

// disablePrivateDNS 尽力临时关闭系统 Private DNS（仅 Android + TUN 时调用），
// 返回是否成功关闭（用于启动失败时决定是否恢复）。
func (m *Manager) disablePrivateDNS() bool {
	disabled, err := sysutil.DisablePrivateDNS(m.paths.PrivateDNSStatePath())
	if err != nil {
		m.logger.Printf("临时关闭系统 Private DNS 失败（继续启动）: %v", err)
		return false
	}
	if disabled {
		m.logger.Printf("TUN 已开启：已临时关闭系统 Private DNS（原模式保存在 %s）", m.paths.PrivateDNSStatePath())
	}
	return disabled
}

// restorePrivateDNS 恢复系统 Private DNS（非 Android 或未修改时为空操作）。
func (m *Manager) restorePrivateDNS() {
	if err := sysutil.RestorePrivateDNS(m.paths.PrivateDNSStatePath()); err != nil {
		m.logger.Printf("恢复系统 Private DNS 失败: %v", err)
	}
}

// moveToRootCgroup 尽力把内核移入根 cgroup；平台不支持时静默跳过。
func (m *Manager) moveToRootCgroup(pid int) {
	if err := sysutil.MoveProcessToRootCgroup(pid); err != nil {
		if errors.Is(err, sysutil.ErrNotSupported) {
			return
		}
		m.logger.Printf("将内核移入根 cgroup 失败（继续运行）: %v", err)
		return
	}
	m.logger.Printf("已将 mihomo 内核移入根 cgroup（pid=%d）", pid)
}

func (m *Manager) waitController(ctx context.Context, timeout time.Duration) error {
	doc, err := m.cfg.Doc()
	if err != nil {
		return err
	}
	addr, secret := doc.Controller()
	ctl := NewController(addr, secret)
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		if !m.Running() {
			m.mu.Lock()
			exitErr := m.lastExitErr
			m.mu.Unlock()
			if exitErr != nil {
				return fmt.Errorf("内核进程提前退出: %v", exitErr)
			}
			return fmt.Errorf("内核进程提前退出")
		}
		vctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		_, verr := ctl.Version(vctx)
		cancel()
		if verr == nil {
			return nil
		}
		lastErr = verr
		time.Sleep(300 * time.Millisecond)
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("等待控制器超时")
	}
	return fmt.Errorf("等待 mihomo 控制器就绪超时（15s）: %v", lastErr)
}

// Stop 主动结束 mihomo 内核进程：先标记“主动停止”抑制看门狗，停止成功后
// 恢复被临时关闭的系统 Private DNS。
func (m *Manager) Stop() error {
	m.mu.Lock()
	m.intentionalStop = true
	m.mu.Unlock()
	if err := m.stopProcess(); err != nil {
		return err
	}
	m.restorePrivateDNS()
	return nil
}

// stopProcess 是低层停止逻辑：不发“主动停止”标记、不恢复 Private DNS。
// 启动失败清理等内部流程复用它，避免误触发/误抑制看门狗。
func (m *Manager) stopProcess() error {
	m.mu.Lock()
	pid := m.pid
	m.mu.Unlock()
	if pid <= 0 {
		if p, err := sysutil.ReadPID(m.paths.CorePIDPath()); err == nil {
			pid = p
		}
	}
	// pid 身份校验：pid 可能已被系统复用，命令行不是 mihomo 时绝不结束该进程，
	// 只删除不匹配的 pid 文件并视为未运行。
	if pid > 0 && sysutil.IsProcessAlive(pid) && !sysutil.ProcessMatches(pid, coreProcessNeedle) {
		m.logger.Printf("pid 文件指向的进程不是 mihomo（pid=%d），跳过停止并删除 pid 文件", pid)
		sysutil.RemovePIDIfMatch(m.paths.CorePIDPath(), pid)
		pid = 0
	}
	if pid <= 0 || !sysutil.IsProcessAlive(pid) {
		if m.isExternal() {
			m.logger.Printf("mihomo 内核由外部进程托管，MProxyEvolution 无法停止，已跳过")
			return nil
		}
		sysutil.RemovePID(m.paths.CorePIDPath())
		m.mu.Lock()
		m.pid = 0
		m.startedAt = time.Time{}
		m.external = false
		m.mu.Unlock()
		return nil
	}
	if err := sysutil.StopPID(pid, 5*time.Second); err != nil {
		return fmt.Errorf("停止 mihomo 内核失败: %w", err)
	}
	// 比较后删除：只删除内容仍记录该 pid 的 pid 文件。
	sysutil.RemovePIDIfMatch(m.paths.CorePIDPath(), pid)
	m.mu.Lock()
	if m.pid == pid {
		m.pid = 0
		m.startedAt = time.Time{}
	}
	m.external = false
	m.mu.Unlock()
	m.logger.Printf("mihomo 内核已停止（pid=%d）", pid)
	return nil
}

// Restart 重启内核。
func (m *Manager) Restart(ctx context.Context) error {
	if m.isExternal() {
		return fmt.Errorf("mihomo 内核由外部进程托管（未通过 MProxyEvolution 启动），无法重启；请先停止外部进程")
	}
	if err := m.Stop(); err != nil {
		return err
	}
	return m.Start(ctx)
}

// Reload 通过控制器热重载 config.yaml。
func (m *Manager) Reload(ctx context.Context) error {
	if !m.Running() {
		return ErrNotRunning
	}
	doc, err := m.cfg.Doc()
	if err != nil {
		return fmt.Errorf("读取配置失败: %w", err)
	}
	addr, secret := doc.Controller()
	if err := NewController(addr, secret).Reload(ctx, m.paths.ConfigPath()); err != nil {
		return err
	}
	m.logger.Printf("已热重载 mihomo 配置")
	return nil
}

// Controller 返回当前配置对应的控制器客户端。
func (m *Manager) Controller() (*Controller, error) {
	doc, err := m.cfg.Doc()
	if err != nil {
		return nil, err
	}
	addr, secret := doc.Controller()
	return NewController(addr, secret), nil
}

// Version 返回内核版本：运行中优先询问控制器，否则执行 mihomo -v。
func (m *Manager) Version() string {
	if m.Running() {
		if ctl, err := m.Controller(); err == nil {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			v, verr := ctl.Version(ctx)
			cancel()
			if verr == nil && v != "" {
				return v
			}
		}
	}
	m.mu.Lock()
	cached := m.versionCache
	m.mu.Unlock()
	if cached != "" {
		return cached
	}
	binary := m.CoreBinary()
	if binary == "" {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, binary, "-v").CombinedOutput()
	if err != nil && len(out) == 0 {
		return ""
	}
	v := parseVersion(string(out))
	if v != "" {
		m.mu.Lock()
		m.versionCache = v
		m.mu.Unlock()
	}
	return v
}

var versionRe = regexp.MustCompile(`v\d+\.\d+\.\d+(?:[-+][0-9A-Za-z.\-]+)?`)

func parseVersion(out string) string {
	return versionRe.FindString(out)
}

// ClearVersionCache 在内核更新后清除版本缓存。
func (m *Manager) ClearVersionCache() {
	m.mu.Lock()
	m.versionCache = ""
	m.mu.Unlock()
}

// Status 汇总内核状态。
func (m *Manager) Status() Status {
	doc, _ := m.cfg.Doc()
	addr, secret := "127.0.0.1:9090", ""
	st := Status{
		Installed: m.Installed(),
		Running:   m.Running(),
		PID:       m.PID(),
	}
	// 自愈状态：重启次数与上次退出原因（仅诊断/面板展示，字段为新增可忽略）。
	m.mu.Lock()
	st.Restarts = m.restarts
	exitAt, exitPID, lastReason := m.lastExitAt, m.lastExitPID, m.lastExitReason
	m.mu.Unlock()
	if !exitAt.IsZero() {
		st.LastExit = formatLastExit(exitAt, exitPID, lastReason)
	}
	if doc != nil {
		addr, secret = doc.Controller()
		st.Controller.Mode = doc.GetString("rule", "mode")
		st.Controller.MixedPort = doc.GetInt(0, "mixed-port")
		st.Controller.SocksPort = doc.GetInt(0, "socks-port")
		st.Controller.HTTPPort = doc.GetInt(0, "port")
		st.Controller.TunEnabled = doc.GetBool(false, "tun", "enable")
	}
	host, port := splitHostPort(addr)
	st.Controller.Host = host
	st.Controller.Port = port
	st.Controller.SecretSet = secret != ""
	if st.Running {
		st.Version = m.Version()
		m.mu.Lock()
		started := m.startedAt
		m.mu.Unlock()
		if !started.IsZero() {
			st.StartedAt = started.Format(time.RFC3339)
			st.UptimeSec = int64(time.Since(started).Seconds())
		}
		ctl := NewController(addr, secret)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		if cfgMap, err := ctl.Configs(ctx); err == nil {
			st.Controller.Reachable = true
			if v, ok := cfgMap["mode"].(string); ok && v != "" {
				st.Controller.Mode = v
			}
			st.Controller.MixedPort = readInt(cfgMap, "mixed-port")
			st.Controller.SocksPort = readInt(cfgMap, "socks-port")
			st.Controller.HTTPPort = readInt(cfgMap, "port")
			if tun, ok := cfgMap["tun"].(map[string]any); ok {
				if v, ok := tun["enable"].(bool); ok {
					st.Controller.TunEnabled = v
				}
			}
			if ver, ok := cfgMap["version"].(string); ok && ver != "" && st.Version == "" {
				st.Version = ver
			}
		}
		cancel()
	}
	return st
}

// formatLastExit 把上次退出信息格式化为 Status.LastExit 的单行文本。
func formatLastExit(at time.Time, pid int, reason string) string {
	s := at.Format(time.RFC3339) + fmt.Sprintf(" pid=%d", pid)
	if reason != "" {
		s += " err=" + reason
	}
	return s
}

func readInt(m map[string]any, key string) int {
	switch v := m[key].(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	}
	return 0
}

func splitHostPort(addr string) (string, int) {
	if addr == "" {
		return "127.0.0.1", 9090
	}
	idx := strings.LastIndex(addr, ":")
	if idx < 0 {
		return addr, 9090
	}
	host := strings.Trim(addr[:idx], "[]")
	port := 9090
	if p, err := parsePort(addr[idx+1:]); err == nil {
		port = p
	}
	return host, port
}

func parsePort(s string) (int, error) {
	var p int
	_, err := fmt.Sscanf(s, "%d", &p)
	if err != nil || p <= 0 || p > 65535 {
		return 0, fmt.Errorf("非法端口")
	}
	return p, nil
}

// LogTail 返回内核日志末尾 n 行。
func (m *Manager) LogTail(n int) []string {
	lines, err := sysutil.TailFile(m.paths.CoreLogPath(), n)
	if err != nil {
		return []string{}
	}
	return lines
}

// LogPath 返回内核日志路径。
func (m *Manager) LogPath() string { return m.paths.CoreLogPath() }

// ClearLog 清空内核日志。
// 内核日志不再由 RotatingWriter 包装（stdout 直接指向 *os.File），轮转大小在每次
// 启动 openCoreLog 时按文件实际大小判断，因此截断后不存在需要额外重置的写入器状态。
// 内核仍在运行时它持有的 fd 以 O_APPEND 打开，后续日志会从文件末尾继续写入。
func (m *Manager) ClearLog() error {
	return sysutil.TruncateFile(m.paths.CoreLogPath())
}

// prepareAndroidNetwork 做 Android 上启动内核需要的前置准备（尽力而为）。
func prepareAndroidNetwork(logger *log.Logger) {
	if !sysutil.IsAndroid() {
		return
	}
	if err := os.MkdirAll("/dev/net", 0o755); err != nil {
		logger.Printf("创建 /dev/net 失败: %v", err)
	}
	if _, err := os.Stat("/dev/net/tun"); os.IsNotExist(err) {
		if err := os.Symlink("/dev/tun", "/dev/net/tun"); err != nil {
			logger.Printf("创建 /dev/net/tun 软链失败: %v", err)
		}
	}
	if err := os.WriteFile("/proc/sys/net/ipv4/ip_forward", []byte("1\n"), 0o644); err != nil {
		logger.Printf("开启 ip_forward 失败: %v", err)
	}
}
