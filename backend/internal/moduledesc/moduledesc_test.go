package moduledesc

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"mproxy/internal/config"
	"mproxy/internal/core"
)

func TestBuildRunningTUN(t *testing.T) {
	summary := config.Summary{
		ProxyMode:  "tun",
		TunEnabled: true,
		MixedPort:  7890,
		ProxyCount: 3,
		RuleCount:  12,
	}
	st := core.Status{Installed: true, Running: true, Version: "v1.19.33"}
	desc := Build("0.2.0", st, summary)
	want := "MihomoProxy Evolution | 运行中 · TUN · 混合端口 7890 · 节点 3 · 规则 12 | 内核 v1.19.33"
	if desc != want {
		t.Fatalf("运行中描述错误:\n got: %s\nwant: %s", desc, want)
	}
}

func TestBuildMixedAndStopped(t *testing.T) {
	summary := config.Summary{
		ProxyMode:  "mixed",
		MixedPort:  7891,
		ProxyCount: 2,
		RuleCount:  0,
	}
	st := core.Status{Installed: true, Running: false, Version: "1.19.33"}
	desc := Build("", st, summary)
	if !strings.Contains(desc, "已停止") || !strings.Contains(desc, "Mixed") {
		t.Fatalf("已停止/Mixed 描述错误: %s", desc)
	}
	if !strings.Contains(desc, "混合端口 7891") || !strings.Contains(desc, "节点 2") {
		t.Fatalf("端口/节点描述缺失: %s", desc)
	}
	if strings.Contains(desc, "规则 0") {
		t.Fatalf("规则数为 0 时不应展示: %s", desc)
	}
	if !strings.Contains(desc, "内核 v1.19.33") {
		t.Fatalf("内核版本应补 v 前缀: %s", desc)
	}
}

func TestBuildNotInstalled(t *testing.T) {
	summary := config.Summary{
		ProxyMode:  "tun",
		TunEnabled: true,
		MixedPort:  7890,
		ProxyCount: 5,
		RuleCount:  20,
	}
	st := core.Status{Installed: false, Running: false}
	desc := Build("", st, summary)
	if !strings.Contains(desc, "内核未安装") {
		t.Fatalf("未安装描述错误: %s", desc)
	}
	if strings.Contains(desc, "节点") || strings.Contains(desc, "规则") {
		t.Fatalf("未安装内核时不应展示节点/规则数: %s", desc)
	}
	if !strings.Contains(desc, "混合端口 7890") {
		t.Fatalf("混合端口应保留: %s", desc)
	}
}

func TestBuildEmptyVersionFallback(t *testing.T) {
	st := core.Status{Installed: true, Running: true}
	desc := Build("", st, config.Summary{ProxyMode: "tun", TunEnabled: true})
	if strings.Contains(desc, "内核 v") || strings.Contains(desc, "版本 v") {
		t.Fatalf("版本为空时不应展示版本段: %s", desc)
	}
	// 内核版本缺失时用应用版本兜底
	desc = Build("0.3.0", st, config.Summary{})
	if !strings.Contains(desc, "版本 v0.3.0") {
		t.Fatalf("应用版本兜底失败: %s", desc)
	}
}

func TestBuildSingleLineAndLength(t *testing.T) {
	summary := config.Summary{
		ProxyMode:  "tun",
		TunEnabled: true,
		MixedPort:  7890,
		ProxyCount: 12345,
		RuleCount:  67890,
	}
	st := core.Status{Installed: true, Running: true, Version: strings.Repeat("9", 120)}
	desc := Build("0.1.0", st, summary)
	if len(desc) > maxDescriptionBytes {
		t.Fatalf("描述超过长度上限: %d 字节", len(desc))
	}
	if !utf8.ValidString(desc) {
		t.Fatal("描述必须是合法 UTF-8")
	}
	if strings.ContainsAny(desc, "\r\n") {
		t.Fatalf("描述不能包含换行: %q", desc)
	}
}

const sampleModuleProp = "id=MProxyEvolution\n" +
	"name=MihomoProxy Evolution\n" +
	"version=0.1.0\n" +
	"versionCode=1\n" +
	"author=f1owkang\n" +
	"description=旧描述\n" +
	"updateJson=https://example.com/update.json\n"

func writeModuleProp(t *testing.T, content string) (dir, path string) {
	t.Helper()
	dir = t.TempDir()
	path = filepath.Join(dir, "module.prop")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("写入测试 module.prop 失败: %v", err)
	}
	return dir, path
}

func TestWriteDescriptionReplacesOnlyDescription(t *testing.T) {
	dir, path := writeModuleProp(t, sampleModuleProp)
	if err := WriteDescription(dir, "运行中 · TUN"); err != nil {
		t.Fatalf("写入描述失败: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got := string(raw)
	if strings.Contains(got, "旧描述") {
		t.Fatalf("旧描述未替换: %s", got)
	}
	if strings.ContainsAny(got, "\r") {
		t.Fatalf("输出必须是 LF: %q", got)
	}
	if strings.HasPrefix(got, "\xEF\xBB\xBF") {
		t.Fatal("输出不能带 BOM")
	}
	wantLines := []string{
		"id=MProxyEvolution",
		"name=MihomoProxy Evolution",
		"version=0.1.0",
		"versionCode=1",
		"author=f1owkang",
		"description=运行中 · TUN",
		"updateJson=https://example.com/update.json",
	}
	gotLines := strings.Split(strings.TrimSuffix(got, "\n"), "\n")
	if !reflect.DeepEqual(gotLines, wantLines) {
		t.Fatalf("module.prop 行内容/顺序被改变:\n got: %#v\nwant: %#v", gotLines, wantLines)
	}
}

func TestWriteDescriptionIdempotent(t *testing.T) {
	dir, path := writeModuleProp(t, sampleModuleProp)
	if err := WriteDescription(dir, "已停止 · Mixed"); err != nil {
		t.Fatal(err)
	}
	old := time.Unix(1700000000, 0)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	if err := WriteDescription(dir, "已停止 · Mixed"); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !st.ModTime().Equal(old) {
		t.Fatalf("内容相同时不应重写文件（mtime 变化: %v -> %v）", old, st.ModTime())
	}
}

func TestWriteDescriptionMissingFile(t *testing.T) {
	dir := t.TempDir()
	err := WriteDescription(dir, "任意描述")
	if !errors.Is(err, ErrModulePropNotFound) {
		t.Fatalf("module.prop 缺失应返回哨兵错误: %v", err)
	}
	if err := WriteDescription("", "任意描述"); !errors.Is(err, ErrModulePropNotFound) {
		t.Fatalf("空模块目录应返回哨兵错误: %v", err)
	}
}

func TestWriteDescriptionRejectsNewline(t *testing.T) {
	dir, path := writeModuleProp(t, sampleModuleProp)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteDescription(dir, "第一行\n第二行"); err == nil {
		t.Fatal("含换行的描述应被拒绝")
	}
	if err := WriteDescription(dir, "第一行\r第二行"); err == nil {
		t.Fatal("含 CR 的描述应被拒绝")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("被拒绝时不应修改 module.prop")
	}
}

func TestWriteDescriptionNormalizesCRLFAndAppendsMissing(t *testing.T) {
	dir, path := writeModuleProp(t, "id=MProxyEvolution\r\nname=MihomoProxy Evolution\r\nversion=0.1.0\r\n")
	if err := WriteDescription(dir, "新描述"); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := "id=MProxyEvolution\nname=MihomoProxy Evolution\nversion=0.1.0\ndescription=新描述\n"
	if string(raw) != want {
		t.Fatalf("CRLF 归一化/缺失 description 追加失败:\n got: %q\nwant: %q", string(raw), want)
	}
}
