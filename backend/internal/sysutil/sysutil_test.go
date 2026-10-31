package sysutil

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestLoadOrCreateToken(t *testing.T) {
	dir := t.TempDir()
	paths := Paths{DataDir: dir, ModuleDir: dir}
	tok1, err := LoadOrCreateToken(paths)
	if err != nil {
		t.Fatalf("生成 token 失败: %v", err)
	}
	if len(tok1) < 32 {
		t.Fatalf("token 太短: %q", tok1)
	}
	tok2, err := LoadOrCreateToken(paths)
	if err != nil || tok1 != tok2 {
		t.Fatalf("token 应持久化: %q %q err=%v", tok1, tok2, err)
	}
	st, err := os.Stat(paths.TokenPath())
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && st.Mode().Perm()&0o077 != 0 {
		t.Fatalf("token 权限应为 0600，实际 %v", st.Mode().Perm())
	}
	// 清空 token 后应重新生成
	if err := os.WriteFile(paths.TokenPath(), []byte("\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tok3, err := LoadOrCreateToken(paths)
	if err != nil || tok3 == "" || tok3 == tok1 {
		t.Fatalf("空 token 应重新生成: %q", tok3)
	}
}

func TestTailFileAndTruncate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.log")
	content := strings.Join([]string{"a", "b", "c", "d", "e"}, "\n") + "\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	lines, err := TailFile(path, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 3 || lines[0] != "c" || lines[2] != "e" {
		t.Fatalf("尾行读取错误: %v", lines)
	}
	if err := TruncateFile(path); err != nil {
		t.Fatal(err)
	}
	lines, err = TailFile(path, 10)
	if err != nil || len(lines) != 0 {
		t.Fatalf("清空后应为空: %v %v", lines, err)
	}
	// 文件不存在时返回空
	lines, err = TailFile(filepath.Join(dir, "none.log"), 10)
	if err != nil || len(lines) != 0 {
		t.Fatalf("不存在文件应返回空: %v %v", lines, err)
	}
}

func TestRotatingWriter(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "core.log")
	w, err := NewRotatingWriter(path, 32)
	if err != nil {
		t.Fatal(err)
	}
	line := "0123456789abcdef\n" // 17 字节
	for i := 0; i < 5; i++ {
		if _, err := w.Write([]byte(line)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	// 至少发生一次轮转
	if _, err := os.Stat(path + ".1"); err != nil {
		t.Fatalf("期望产生轮转文件: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("当前日志文件应存在: %v", err)
	}
}

func TestPathsDefaults(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MPROXY_DATA_DIR", dir)
	t.Setenv("MPROXY_MODULE_DIR", dir)
	paths := NewPaths()
	if paths.DataDir != dir || paths.ModuleDir != dir {
		t.Fatalf("环境变量未生效: %+v", paths)
	}
	if paths.ConfigPath() != filepath.Join(dir, "config.yaml") {
		t.Fatalf("config 路径错误: %s", paths.ConfigPath())
	}
	if err := paths.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{paths.StateDir(), paths.BinDir(), paths.LogsDir()} {
		if st, err := os.Stat(p); err != nil || !st.IsDir() {
			t.Fatalf("目录未创建: %s %v", p, err)
		}
	}
	if ABI() == "" || len(ABIAlternatives()) == 0 {
		t.Fatal("ABI 映射不应为空")
	}
}

func TestRemovePIDIfMatch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "core.pid")
	if err := WritePIDFile(path, 4242); err != nil {
		t.Fatal(err)
	}
	// pid 不一致时不能删除别人写下的 pid 文件
	if RemovePIDIfMatch(path, 4243) {
		t.Fatal("pid 不一致时不应删除 pid 文件")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("pid 文件不应被删除: %v", err)
	}
	// 内容非法时也不删除
	if err := os.WriteFile(path, []byte("not-a-pid"), 0o644); err != nil {
		t.Fatal(err)
	}
	if RemovePIDIfMatch(path, 4242) {
		t.Fatal("pid 文件内容非法时不应删除")
	}
	// pid 一致时删除
	if err := WritePIDFile(path, 4242); err != nil {
		t.Fatal(err)
	}
	if !RemovePIDIfMatch(path, 4242) {
		t.Fatal("pid 一致时应删除 pid 文件")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("pid 文件应已删除: %v", err)
	}
	// 文件不存在时返回 false，不 panic
	if RemovePIDIfMatch(path, 4242) {
		t.Fatal("pid 文件不存在时不应返回 true")
	}
}

func TestProcessMatches(t *testing.T) {
	self := os.Getpid()
	if !ProcessMatches(self, "") {
		t.Fatal("空 needle 应退化为存活检查")
	}
	if ProcessMatches(0, "mihomo") {
		t.Fatal("非法 pid 不应匹配")
	}
	if ProcessMatches(1<<30, "mihomo") {
		t.Fatal("不存在的 pid 不应匹配")
	}
	if runtime.GOOS == "windows" {
		// Windows 无法读取命令行，ProcessMatches 退化为存活检查
		if !ProcessMatches(self, "mihomo") {
			t.Fatal("Windows 上应退化为存活检查")
		}
		return
	}
	cmdline, err := ProcessCmdline(self)
	if err != nil {
		t.Fatalf("读取自身 cmdline 失败: %v", err)
	}
	if !ProcessMatches(self, strings.ToUpper(cmdline[:min(4, len(cmdline))])) {
		t.Fatal("命令行匹配应忽略大小写")
	}
	if ProcessMatches(self, "definitely-not-in-this-cmdline-xyz") {
		t.Fatal("命令行不含 needle 时不应匹配")
	}
}

func TestInstallCandidatePath(t *testing.T) {
	target := filepath.Join(t.TempDir(), "mihomo")
	got := InstallCandidatePath(target)
	if runtime.GOOS == "windows" {
		if !strings.HasSuffix(got, ".new.exe") {
			t.Fatalf("Windows 候选内核路径应带 .exe: %s", got)
		}
		return
	}
	if got != target+".new" {
		t.Fatalf("候选内核路径错误: %s", got)
	}
}
