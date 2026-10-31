package core

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"mproxy/internal/config"
	"mproxy/internal/sysutil"
)

func TestParseVersion(t *testing.T) {
	cases := map[string]string{
		"Mihomo Meta v1.19.33 windows amd64 with go1.26.10": "v1.19.33",
		"Mihomo Meta v1.19.33-alpha.1 linux arm64":          "v1.19.33-alpha.1",
		"no version here": "",
	}
	for input, want := range cases {
		if got := parseVersion(input); got != want {
			t.Errorf("parseVersion(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestAssetMatchers(t *testing.T) {
	if got := AssetMatchers("android", "arm64", "7"); len(got) == 0 || got[0] != "-android-arm64-v8-" {
		t.Fatalf("android/arm64 匹配规则错误: %v", got)
	}
	if got := AssetMatchers("android", "arm", "7"); len(got) == 0 || got[0] != "-android-armv7-" {
		t.Fatalf("android/arm 匹配规则错误: %v", got)
	}
	if got := AssetMatchers("windows", "amd64", ""); len(got) == 0 {
		t.Fatal("windows/amd64 应有匹配规则")
	}
	if got := AssetMatchers("plan9", "mips", ""); got != nil {
		t.Fatalf("未知平台不应有匹配规则: %v", got)
	}
}

func TestPickAsset(t *testing.T) {
	assets := []Asset{
		{Name: "mihomo-android-arm64-compatible-v1.19.33.gz"},
		{Name: "mihomo-android-arm64-v8-v1.19.33.gz"},
		{Name: "mihomo-android-armv7-v1.19.33.gz"},
		{Name: "mihomo-linux-amd64-v1.19.33.gz"},
		{Name: "mihomo-windows-amd64-v1.19.33.zip"},
		{Name: "mihomo-windows-amd64-v1-go123-v1.19.33.zip"},
	}
	got, err := PickAsset(assets, "android", "arm64", "7")
	if err != nil || got.Name != "mihomo-android-arm64-v8-v1.19.33.gz" {
		t.Fatalf("android/arm64 选错资产: %+v %v", got, err)
	}
	got, err = PickAsset(assets, "windows", "amd64", "")
	if err != nil || got.Name != "mihomo-windows-amd64-v1.19.33.zip" {
		t.Fatalf("windows/amd64 应优先通用发行版: %+v %v", got, err)
	}
	if _, err := PickAsset(assets, "plan9", "mips", ""); err == nil {
		t.Fatal("未知平台应报错")
	}
}

func TestSelectRelease(t *testing.T) {
	releases := []Release{
		{TagName: "v2.0.0-rc1", Prerelease: true},
		{TagName: "v1.19.33"},
		{TagName: "draft", Draft: true},
	}
	got, err := SelectRelease(releases, "stable")
	if err != nil || got.TagName != "v1.19.33" {
		t.Fatalf("stable 选择错误: %+v %v", got, err)
	}
	got, err = SelectRelease(releases, "alpha")
	if err != nil || got.TagName != "v2.0.0-rc1" {
		t.Fatalf("alpha 选择错误: %+v %v", got, err)
	}
}

func TestWithGhproxy(t *testing.T) {
	url := "https://github.com/x/y/releases/download/v1/a.gz"
	if got := WithGhproxy("", url); got != url {
		t.Fatalf("空 ghproxy 应原样返回: %s", got)
	}
	if got := WithGhproxy("https://ghfast.top", url); got != "https://ghfast.top/"+url {
		t.Fatalf("ghproxy 拼接错误: %s", got)
	}
}

func TestManagerWithoutBinary(t *testing.T) {
	dir := t.TempDir()
	paths := sysutil.Paths{DataDir: dir, ModuleDir: dir}
	cfg, err := config.Open(paths)
	if err != nil {
		t.Fatal(err)
	}
	m := NewManager(paths, cfg, nil)
	if m.Installed() {
		t.Fatal("空目录不应检测到内核")
	}
	if m.Running() {
		t.Fatal("空目录不应有运行中的内核")
	}
	st := m.Status()
	if st.Installed || st.Running || st.Version != "" {
		t.Fatalf("状态错误: %+v", st)
	}
	if err := m.Start(nil); err == nil {
		t.Fatal("没有内核时 Start 应报错")
	}
	if err := m.Reload(nil); err == nil {
		t.Fatal("没有内核时 Reload 应报错")
	}
	if len(m.LogTail(10)) != 0 {
		t.Fatal("无日志时应返回空")
	}
}

// newTestManager 创建使用临时数据目录的管理器（不触发真实内核探测）。
func newTestManager(t *testing.T) (*Manager, sysutil.Paths) {
	t.Helper()
	dir := t.TempDir()
	paths := sysutil.Paths{DataDir: dir, ModuleDir: dir}
	cfg, err := config.Open(paths)
	if err != nil {
		t.Fatalf("打开测试配置失败: %v", err)
	}
	m := &Manager{paths: paths, cfg: cfg, logger: log.New(io.Discard, "", 0)}
	return m, paths
}

func TestInstallBinarySwapAndRollback(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "mihomo")

	// 首次安装：没有旧内核，不应产生备份
	candidate := sysutil.InstallCandidatePath(target)
	if err := os.WriteFile(candidate, []byte("v1"), 0o755); err != nil {
		t.Fatal(err)
	}
	oldPath, err := installBinary(target, candidate)
	if err != nil {
		t.Fatalf("首次安装失败: %v", err)
	}
	if oldPath != "" {
		t.Fatalf("没有旧内核时不应返回备份路径: %s", oldPath)
	}
	if b, _ := os.ReadFile(target); string(b) != "v1" {
		t.Fatalf("目标内容错误: %s", b)
	}

	// 再次安装：旧内核应备份到 .old，目标换成新内核
	if err := os.WriteFile(candidate, []byte("v2"), 0o755); err != nil {
		t.Fatal(err)
	}
	oldPath, err = installBinary(target, candidate)
	if err != nil {
		t.Fatalf("覆盖安装失败: %v", err)
	}
	if oldPath != target+".old" {
		t.Fatalf("备份路径错误: %s", oldPath)
	}
	if b, _ := os.ReadFile(target); string(b) != "v2" {
		t.Fatalf("目标未替换为新内核: %s", b)
	}
	if b, _ := os.ReadFile(oldPath); string(b) != "v1" {
		t.Fatalf("旧内核备份内容错误: %s", b)
	}

	// 校验失败回滚：目标恢复为旧内核，备份消失
	if err := rollbackBinary(target, oldPath); err != nil {
		t.Fatalf("回滚失败: %v", err)
	}
	if b, _ := os.ReadFile(target); string(b) != "v1" {
		t.Fatalf("回滚后目标内容错误: %s", b)
	}
	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Fatalf("回滚后备份文件应消失: %v", err)
	}

	// 成功安装后清理备份
	if err := os.WriteFile(candidate, []byte("v3"), 0o755); err != nil {
		t.Fatal(err)
	}
	oldPath, err = installBinary(target, candidate)
	if err != nil {
		t.Fatalf("安装 v3 失败: %v", err)
	}
	if err := commitBinary(oldPath); err != nil {
		t.Fatalf("清理备份失败: %v", err)
	}
	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Fatalf("清理后备份文件应消失: %v", err)
	}

	// 候选文件不存在时应恢复旧内核，不破坏正式二进制
	if err := os.WriteFile(candidate, []byte("v4"), 0o755); err != nil {
		t.Fatal(err)
	}
	oldPath, err = installBinary(target, candidate)
	if err != nil {
		t.Fatalf("安装 v4 失败: %v", err)
	}
	if _, err := installBinary(target, filepath.Join(dir, "missing.new")); err == nil {
		t.Fatal("候选文件不存在时应报错")
	}
	if b, _ := os.ReadFile(target); string(b) != "v4" {
		t.Fatalf("替换失败后应恢复旧内核: %s", b)
	}
}

func TestOpenCoreLogRotation(t *testing.T) {
	m, paths := newTestManager(t)
	path := paths.CoreLogPath()
	big := bytes.Repeat([]byte("x"), int(coreLogMaxBytes)+1)
	if err := os.WriteFile(path, big, 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := m.openCoreLog()
	if err != nil {
		t.Fatalf("打开内核日志失败: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path + ".1"); err != nil {
		t.Fatalf("超过 5MiB 应轮转为 core.log.1: %v", err)
	}
	if st, err := os.Stat(path); err != nil || st.Size() != 0 {
		t.Fatalf("轮转后新日志应为空: %v", err)
	}

	// ClearLog 截断后再次启动不应重复轮转（说明大小统计基于实际文件）
	if err := os.WriteFile(path, big, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := m.ClearLog(); err != nil {
		t.Fatalf("清空内核日志失败: %v", err)
	}
	if st, err := os.Stat(path); err != nil || st.Size() != 0 {
		t.Fatalf("ClearLog 后日志应为空: %v", err)
	}
	_ = os.Remove(path + ".1")
	f, err = m.openCoreLog()
	if err != nil {
		t.Fatalf("再次打开内核日志失败: %v", err)
	}
	_ = f.Close()
	if _, err := os.Stat(path + ".1"); !os.IsNotExist(err) {
		t.Fatalf("截断后不应再次轮转: %v", err)
	}
}

func TestPrepareTUNInterface(t *testing.T) {
	m, paths := newTestManager(t)

	// 1) 默认模板 auto-detect-interface: true：不修改配置，也不写无效键 tun.interface-name
	before, err := os.ReadFile(paths.ConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	doc, err := m.cfg.Doc()
	if err != nil {
		t.Fatal(err)
	}
	m.prepareTUNInterface(doc)
	if !doc.GetBool(false, "tun", "auto-detect-interface") {
		t.Fatal("不应关闭用户的 auto-detect-interface")
	}
	if _, ok := doc.Get("tun", "interface-name"); ok {
		t.Fatal("不应写入不是 mihomo 键的 tun.interface-name")
	}
	if doc.GetString("", "interface-name") != "" {
		t.Fatal("自动探测开启时不应写入顶层 interface-name")
	}
	after, err := os.ReadFile(paths.ConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("没有变化时不应重写配置文件")
	}

	// 2) 用户显式关闭自动探测且已填写 interface-name：保持原值
	doc2, err := config.Parse([]byte("tun:\n  enable: true\n  auto-detect-interface: false\ninterface-name: wlan0\n"))
	if err != nil {
		t.Fatal(err)
	}
	m.prepareTUNInterface(doc2)
	if doc2.GetBool(true, "tun", "auto-detect-interface") || doc2.GetString("", "interface-name") != "wlan0" {
		t.Fatalf("不应改写已有的 interface-name/auto-detect: %+v", doc2.Root())
	}

	// 3) TUN 关闭时不做任何处理
	doc3, err := config.Parse([]byte("tun:\n  enable: false\n  auto-detect-interface: false\n"))
	if err != nil {
		t.Fatal(err)
	}
	m.prepareTUNInterface(doc3)
	if _, ok := doc3.Get("interface-name"); ok {
		t.Fatal("TUN 关闭时不应写入 interface-name")
	}

	// 4) 显式关闭自动探测且 interface-name 为空：只在探测到网卡时写顶层 interface-name，
	//    绝不写入 tun.interface-name，也不改写用户的 auto-detect-interface=false。
	doc4, err := config.Parse([]byte("tun:\n  enable: true\n  auto-detect-interface: false\n"))
	if err != nil {
		t.Fatal(err)
	}
	m.prepareTUNInterface(doc4)
	if doc4.GetBool(true, "tun", "auto-detect-interface") {
		t.Fatal("不应改写用户的 auto-detect-interface=false")
	}
	if _, ok := doc4.Get("tun", "interface-name"); ok {
		t.Fatal("不应写入 tun.interface-name")
	}
}

func TestVerifiedPIDRejectsMismatch(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows 无法读取命令行，ProcessMatches 退化为存活检查")
	}
	m, paths := newTestManager(t)
	pidPath := paths.CorePIDPath()
	// 写入当前测试进程的 pid：命令行不含 mihomo，应被判定为“不是内核”
	if err := sysutil.WritePIDFile(pidPath, os.Getpid()); err != nil {
		t.Fatal(err)
	}
	if pid := m.verifiedPID(); pid != 0 {
		t.Fatalf("命令行不匹配时不应返回 pid: %d", pid)
	}
	if _, err := os.Stat(pidPath); !os.IsNotExist(err) {
		t.Fatalf("不匹配的 pid 文件应被删除: %v", err)
	}
}

func TestStartSerializedByStartMu(t *testing.T) {
	m, _ := newTestManager(t)
	// 直接占用 single-flight 锁：Start 必须先等待锁，说明整个启动过程被串行化。
	m.startMu.Lock()
	started := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		close(started)
		done <- m.Start(context.Background())
	}()
	<-started
	select {
	case err := <-done:
		t.Fatalf("startMu 被占用时 Start 不应返回: %v", err)
	case <-time.After(150 * time.Millisecond):
	}
	m.startMu.Unlock()
	select {
	case err := <-done:
		if !errors.Is(err, ErrNotInstalled) {
			t.Fatalf("无内核时 Start 应返回 ErrNotInstalled，实际: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("释放 startMu 后 Start 未返回")
	}
}

func TestRestartBackoff(t *testing.T) {
	want := []time.Duration{
		500 * time.Millisecond,
		time.Second,
		2 * time.Second,
		4 * time.Second,
		8 * time.Second,
	}
	for i, w := range want {
		if got := restartBackoff(i); got != w {
			t.Fatalf("restartBackoff(%d) = %s, want %s", i, got, w)
		}
	}
	// 上限 30s，且负数按第一次处理。
	if got := restartBackoff(10); got != 30*time.Second {
		t.Fatalf("退避上限应为 30s: %s", got)
	}
	if got := restartBackoff(-5); got != 500*time.Millisecond {
		t.Fatalf("负数应按第一次退避: %s", got)
	}
}

func TestAfterExitSkipsRestartOnIntentionalStop(t *testing.T) {
	m, paths := newTestManager(t)
	m.mu.Lock()
	m.pid = 4242
	m.startedAt = time.Now()
	m.intentionalStop = true
	m.mu.Unlock()

	m.afterExit(4242, errors.New("boom"))

	info, err := sysutil.ReadLastExit(paths.LastExitPath())
	if err != nil {
		t.Fatalf("应写入上次退出状态: %v", err)
	}
	if info.PID != 4242 || !info.Intentional || !strings.Contains(info.Err, "boom") {
		t.Fatalf("上次退出状态错误: %+v", info)
	}
	m.mu.Lock()
	restarts, pid := m.restarts, m.pid
	m.mu.Unlock()
	if restarts != 0 || pid != 0 {
		t.Fatalf("主动停止后不应自愈: restarts=%d pid=%d", restarts, pid)
	}
	m.restartMu.Lock()
	restarting := m.restarting
	m.restartMu.Unlock()
	if restarting {
		t.Fatal("主动停止后不应启动看门狗 goroutine")
	}
}

func TestAfterExitSkipsRestartWhenAutoStartDisabled(t *testing.T) {
	m, _ := newTestManager(t)
	off := false
	doc, err := m.cfg.Doc()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := doc.ApplySettings(config.SettingsPatch{AutoStart: &off}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.cfg.Save(doc); err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	m.pid = 5150
	m.mu.Unlock()

	m.afterExit(5150, errors.New("boom"))

	m.restartMu.Lock()
	restarting := m.restarting
	m.restartMu.Unlock()
	if restarting {
		t.Fatal("auto-start 关闭时不应启动看门狗")
	}
	m.mu.Lock()
	restarts := m.restarts
	m.mu.Unlock()
	if restarts != 0 {
		t.Fatalf("auto-start 关闭时不应累计重启次数: %d", restarts)
	}
}

func TestAfterExitIgnoresUntrackedProcess(t *testing.T) {
	m, paths := newTestManager(t)
	m.mu.Lock()
	m.pid = 777
	m.mu.Unlock()

	// pid 不匹配（旧内核已退出、状态被新内核接管）：不应写 last_exit，也不自愈。
	m.afterExit(888, errors.New("old exit"))

	if _, err := os.Stat(paths.LastExitPath()); !os.IsNotExist(err) {
		t.Fatalf("未接管的旧进程退出不应写 last_exit: %v", err)
	}
	m.mu.Lock()
	restarts := m.restarts
	m.mu.Unlock()
	if restarts != 0 {
		t.Fatalf("不应累计重启次数: %d", restarts)
	}
}

// installFakeKernel 把一个“启动即失败”的可执行文件伪装成 mihomo 内核：
// 用当前测试二进制充当内核，参数不合法会立即退出，让 Start 以“进程提前退出”
// 失败（而不是 ErrNotInstalled），从而覆盖看门狗的重试/上限逻辑。
func installFakeKernel(t *testing.T, paths sysutil.Paths) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("获取测试二进制路径失败: %v", err)
	}
	data, err := os.ReadFile(exe)
	if err != nil {
		t.Fatalf("读取测试二进制失败: %v", err)
	}
	target := paths.InstallCorePath()
	if err := os.WriteFile(target, data, 0o755); err != nil {
		t.Fatalf("写入假内核失败: %v", err)
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(target, 0o755); err != nil {
			t.Fatalf("设置假内核可执行权限失败: %v", err)
		}
	}
}

func TestAutoRestartExhausted(t *testing.T) {
	m, paths := newTestManager(t)
	installFakeKernel(t, paths)
	old := restartBackoffFn
	restartBackoffFn = func(int) time.Duration { return time.Millisecond }
	defer func() { restartBackoffFn = old }()

	m.mu.Lock()
	m.pid = 6001
	m.mu.Unlock()
	// 假内核启动后立即退出：每次自动重启都会以“进程提前退出”失败，便于验证上限。
	m.afterExit(6001, errors.New("signal: killed"))

	deadline := time.Now().Add(20 * time.Second)
	for {
		m.mu.Lock()
		restarts := m.restarts
		m.mu.Unlock()
		m.restartMu.Lock()
		restarting := m.restarting
		m.restartMu.Unlock()
		if !restarting && restarts >= maxAutoRestarts {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("自动重启未按预期结束: restarts=%d restarting=%v", restarts, restarting)
		}
		time.Sleep(5 * time.Millisecond)
	}
	m.mu.Lock()
	restarts := m.restarts
	m.mu.Unlock()
	if restarts != maxAutoRestarts {
		t.Fatalf("重启次数应到达上限 %d，实际 %d", maxAutoRestarts, restarts)
	}
	st := m.Status()
	if st.Restarts != maxAutoRestarts {
		t.Fatalf("Status.Restarts = %d, want %d", st.Restarts, maxAutoRestarts)
	}
	if st.LastExit == "" {
		t.Fatal("Status.LastExit 应包含上次退出信息")
	}
}

func TestStartResetsRestartState(t *testing.T) {
	m, _ := newTestManager(t)
	m.mu.Lock()
	m.restarts = 3
	m.intentionalStop = true
	m.mu.Unlock()
	if err := m.Start(context.Background()); !errors.Is(err, ErrNotInstalled) {
		t.Fatalf("无内核时 Start 应返回 ErrNotInstalled: %v", err)
	}
	m.mu.Lock()
	restarts, intentional := m.restarts, m.intentionalStop
	m.mu.Unlock()
	if restarts != 0 || intentional {
		t.Fatalf("手动 Start 应重置自愈状态: restarts=%d intentional=%v", restarts, intentional)
	}
}

func TestClearLastExit(t *testing.T) {
	m, paths := newTestManager(t)
	if err := sysutil.WriteLastExit(paths.LastExitPath(), sysutil.LastExitInfo{
		Time: time.Now(),
		PID:  1234,
		Err:  "boom",
	}); err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	m.lastExitAt = time.Now()
	m.lastExitReason = "boom"
	m.mu.Unlock()
	m.clearLastExit()
	if _, err := os.Stat(paths.LastExitPath()); !os.IsNotExist(err) {
		t.Fatalf("启动成功应清除 last_exit: %v", err)
	}
	m.mu.Lock()
	cleared := m.lastExitAt.IsZero()
	m.mu.Unlock()
	if !cleared {
		t.Fatal("应清除内存中的上次退出信息")
	}
}
