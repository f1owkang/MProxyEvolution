// Package sysutil 提供运行环境相关的基础能力：路径、token、日志轮转、进程与系统信息。
package sysutil

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// DefaultPort 是后端固定监听的端口。
const DefaultPort = 9091

// DefaultController 是默认的 mihomo 控制器地址。
const DefaultController = "127.0.0.1:9090"

// Paths 描述 MProxyEvolution 使用的数据目录与模块目录。
type Paths struct {
	DataDir   string `json:"dataDir"`
	ModuleDir string `json:"moduleDir"`
}

// NewPaths 解析 MPROXY_DATA_DIR / MPROXY_MODULE_DIR，未设置时按平台取默认值。
func NewPaths() Paths {
	return Paths{
		DataDir:   resolveDataDir(),
		ModuleDir: resolveModuleDir(),
	}
}

func resolveDataDir() string {
	if v := strings.TrimSpace(os.Getenv("MPROXY_DATA_DIR")); v != "" {
		return absPath(v)
	}
	if IsAndroid() {
		return "/data/adb/mproxy"
	}
	if cwd, err := os.Getwd(); err == nil {
		return filepath.Join(cwd, ".dev-data")
	}
	return ".dev-data"
}

func resolveModuleDir() string {
	if v := strings.TrimSpace(os.Getenv("MPROXY_MODULE_DIR")); v != "" {
		return absPath(v)
	}
	if IsAndroid() {
		return "/data/adb/modules/MProxyEvolution"
	}
	if cwd, err := os.Getwd(); err == nil {
		return cwd
	}
	return "."
}

func absPath(p string) string {
	if a, err := filepath.Abs(p); err == nil {
		return a
	}
	return p
}

// ConfigPath 返回唯一配置文件路径。
func (p Paths) ConfigPath() string { return filepath.Join(p.DataDir, "config.yaml") }

// BackupPath 返回单份备份路径。
func (p Paths) BackupPath() string { return filepath.Join(p.DataDir, "config.yaml.bak") }

// StateDir 返回状态目录。
func (p Paths) StateDir() string { return filepath.Join(p.DataDir, "state") }

// TokenPath 返回后端 API token 路径。
func (p Paths) TokenPath() string { return filepath.Join(p.StateDir(), "token") }

// BinDir 返回数据目录下的二进制目录。
func (p Paths) BinDir() string { return filepath.Join(p.DataDir, "bin") }

// LogsDir 返回日志目录。
func (p Paths) LogsDir() string { return filepath.Join(p.DataDir, "logs") }

// CoreLogPath 返回内核日志路径。
func (p Paths) CoreLogPath() string { return filepath.Join(p.LogsDir(), "core.log") }

// BackendLogPath 返回后端日志路径。
func (p Paths) BackendLogPath() string { return filepath.Join(p.LogsDir(), "backend.log") }

// CorePIDPath 返回内核 pid 文件路径。
func (p Paths) CorePIDPath() string { return filepath.Join(p.StateDir(), "core.pid") }

// BackendPIDPath 返回后端 pid 文件路径。
func (p Paths) BackendPIDPath() string { return filepath.Join(p.StateDir(), "backend.pid") }

// LastExitPath 返回上次内核退出状态文件路径（诊断/看门狗使用）。
func (p Paths) LastExitPath() string { return filepath.Join(p.StateDir(), "last_exit") }

// PrivateDNSStatePath 返回临时关闭系统 Private DNS 时保存原模式的状态文件路径。
func (p Paths) PrivateDNSStatePath() string { return filepath.Join(p.StateDir(), "private_dns") }

// ProvidersDir 返回订阅 provider 缓存目录。
func (p Paths) ProvidersDir() string { return filepath.Join(p.DataDir, "providers") }

// RulesDir 返回规则集缓存目录。
func (p Paths) RulesDir() string { return filepath.Join(p.DataDir, "rules") }

// CoreBinaryName 返回当前平台的内核文件名。
func CoreBinaryName() string {
	if runtime.GOOS == "windows" {
		return "mihomo.exe"
	}
	return "mihomo"
}

// InstallCorePath 返回从 GitHub 安装内核时的目标路径（data/bin/mihomo[.exe]）。
func (p Paths) InstallCorePath() string {
	return filepath.Join(p.BinDir(), CoreBinaryName())
}

// CoreBinaryCandidates 返回内核二进制的候选路径（按优先级）。
func (p Paths) CoreBinaryCandidates() []string {
	names := []string{CoreBinaryName()}
	if runtime.GOOS == "windows" {
		names = append(names, "mihomo") // 兼容无扩展名的开发二进制
	}
	var out []string
	seen := map[string]bool{}
	add := func(v string) {
		if v == "" || seen[v] {
			return
		}
		seen[v] = true
		out = append(out, v)
	}
	for _, n := range names {
		add(filepath.Join(p.BinDir(), n))
	}
	for _, n := range names {
		// 模块打包结构：module/bin/core/<abi>/mihomo
		for _, abi := range ABIAlternatives() {
			add(filepath.Join(p.ModuleDir, "bin", "core", abi, n))
		}
	}
	for _, n := range names {
		add(filepath.Join(p.ModuleDir, "bin", n))
	}
	return out
}

// FindCoreBinary 返回第一个存在且可执行的内核路径，找不到返回空字符串。
func (p Paths) FindCoreBinary() string {
	for _, cand := range p.CoreBinaryCandidates() {
		st, err := os.Stat(cand)
		if err == nil && !st.IsDir() && st.Size() > 0 {
			return cand
		}
	}
	return ""
}

// EnsureDirs 创建数据目录所需的一切子目录。
func (p Paths) EnsureDirs() error {
	dirs := []struct {
		path string
		mode os.FileMode
	}{
		{p.DataDir, 0o755},
		{p.StateDir(), 0o700},
		{p.BinDir(), 0o755},
		{p.LogsDir(), 0o755},
		{p.ProvidersDir(), 0o755},
		{p.RulesDir(), 0o755},
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d.path, d.mode); err != nil {
			return fmt.Errorf("创建目录 %s 失败: %w", d.path, err)
		}
	}
	return nil
}

// ABI 返回当前平台的 ABI 名称（Android 使用 armeabi 风格）。
func ABI() string {
	switch runtime.GOARCH {
	case "arm64":
		return "arm64-v8a"
	case "arm":
		return "armeabi-v7a"
	case "386":
		return "x86"
	case "amd64":
		return "x86_64"
	}
	return runtime.GOARCH
}

// ABIAlternatives 返回可能出现的 ABI 目录名（用于查找模块自带内核）。
func ABIAlternatives() []string {
	primary := ABI()
	alts := []string{primary, runtime.GOARCH}
	switch runtime.GOARCH {
	case "arm64":
		alts = append(alts, "arm64")
	case "arm":
		alts = append(alts, "armv7", "arm")
	case "386":
		alts = append(alts, "x86", "386")
	case "amd64":
		alts = append(alts, "x86_64", "amd64")
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(alts))
	for _, a := range alts {
		if a == "" || seen[a] {
			continue
		}
		seen[a] = true
		out = append(out, a)
	}
	return out
}

// LoadOrCreateToken 读取 state/token，不存在时用密码学随机数生成 0600 文件。
func LoadOrCreateToken(paths Paths) (string, error) {
	if b, err := os.ReadFile(paths.TokenPath()); err == nil {
		if tok := strings.TrimSpace(string(b)); tok != "" {
			return tok, nil
		}
	}
	tok, err := randomHex(24)
	if err != nil {
		return "", fmt.Errorf("生成 API token 失败: %w", err)
	}
	if err := os.MkdirAll(paths.StateDir(), 0o700); err != nil {
		return "", fmt.Errorf("创建状态目录失败: %w", err)
	}
	tmp := paths.TokenPath() + ".tmp"
	if err := os.WriteFile(tmp, []byte(tok+"\n"), 0o600); err != nil {
		return "", fmt.Errorf("写入 API token 失败: %w", err)
	}
	if err := os.Rename(tmp, paths.TokenPath()); err != nil {
		_ = os.Remove(tmp)
		return "", fmt.Errorf("保存 API token 失败: %w", err)
	}
	_ = os.Chmod(paths.TokenPath(), 0o600)
	return tok, nil
}

// randomHex 生成 n 字节的十六进制随机串。
func randomHex(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// RandomHex 生成随机十六进制串（用于 mihomo secret 等）。
func RandomHex(n int) (string, error) { return randomHex(n) }
