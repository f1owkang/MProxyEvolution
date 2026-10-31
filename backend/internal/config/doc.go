// Package config 负责唯一 config.yaml 的解析、摘要、校验、原子保存与合并导入。
package config

import (
	"bytes"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Header 是保存配置时写入的说明注释。
const Header = "# MProxyEvolution 唯一配置文件（由 MProxyEvolution 后端维护，可手工编辑）\n" +
	"# x-mproxy 段是 MProxyEvolution 模块设置，mihomo 会忽略；其余为标准 mihomo 配置。\n\n"

// Doc 是一份已解析的 YAML 配置（顶层 mapping）。
type Doc struct {
	root map[string]any
}

// Parse 解析 YAML 文本为配置文档。
func Parse(data []byte) (*Doc, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, fmt.Errorf("配置内容为空")
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	var m map[string]any
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("YAML 解析失败: %w", err)
	}
	if m == nil {
		return nil, fmt.Errorf("配置顶层必须是键值映射（mapping）")
	}
	return &Doc{root: m}, nil
}

// Root 返回底层映射（调用方不应直接修改后未保存）。
func (d *Doc) Root() map[string]any { return d.root }

// Clone 深拷贝配置文档。
func (d *Doc) Clone() *Doc {
	return &Doc{root: deepCopyMap(d.root)}
}

// Marshal 序列化为 YAML 文本（带说明头注释）。
func (d *Doc) Marshal() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteString(Header)
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(d.root); err != nil {
		return nil, fmt.Errorf("YAML 序列化失败: %w", err)
	}
	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("YAML 序列化失败: %w", err)
	}
	return buf.Bytes(), nil
}

// Get 按路径取任意值。
func (d *Doc) Get(path ...string) (any, bool) {
	return lookup(d.root, path)
}

// Has 判断路径是否存在。
func (d *Doc) Has(path ...string) bool {
	_, ok := lookup(d.root, path)
	return ok
}

// GetString 取字符串（数字/布尔会转成字符串），不存在返回 defaultValue。
func (d *Doc) GetString(defaultValue string, path ...string) string {
	v, ok := lookup(d.root, path)
	if !ok || v == nil {
		return defaultValue
	}
	switch t := v.(type) {
	case string:
		return t
	case int:
		return strconv.Itoa(t)
	case int64:
		return strconv.FormatInt(t, 10)
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(t)
	default:
		return defaultValue
	}
}

// GetInt 取整数，不存在或类型不符返回 defaultValue。
func (d *Doc) GetInt(defaultValue int, path ...string) int {
	v, ok := lookup(d.root, path)
	if !ok || v == nil {
		return defaultValue
	}
	switch t := v.(type) {
	case int:
		return t
	case int64:
		return int(t)
	case float64:
		return int(t)
	case uint64:
		return int(t)
	case string:
		if n, err := strconv.Atoi(strings.TrimSpace(t)); err == nil {
			return n
		}
	}
	return defaultValue
}

// GetBool 取布尔值，不存在或类型不符返回 defaultValue。
func (d *Doc) GetBool(defaultValue bool, path ...string) bool {
	v, ok := lookup(d.root, path)
	if !ok || v == nil {
		return defaultValue
	}
	switch t := v.(type) {
	case bool:
		return t
	case string:
		switch strings.ToLower(strings.TrimSpace(t)) {
		case "true", "1", "yes", "on":
			return true
		case "false", "0", "no", "off":
			return false
		}
	case int:
		return t != 0
	case float64:
		return t != 0
	}
	return defaultValue
}

// GetStringSlice 取字符串列表，不存在返回 nil。
func (d *Doc) GetStringSlice(path ...string) []string {
	v, ok := lookup(d.root, path)
	if !ok || v == nil {
		return nil
	}
	return toStringSlice(v)
}

// Set 按路径设置值，自动创建中间 mapping。
func (d *Doc) Set(path []string, value any) {
	if len(path) == 0 {
		if m, ok := value.(map[string]any); ok {
			d.root = m
		}
		return
	}
	d.root = setPath(d.root, path, value)
}

// SetTop 设置顶层键。
func (d *Doc) SetTop(key string, value any) { d.Set([]string{key}, value) }

// Delete 删除某路径。
func (d *Doc) Delete(path ...string) {
	if len(path) == 0 {
		return
	}
	deletePath(d.root, path)
}

func lookup(root map[string]any, path []string) (any, bool) {
	var cur any = root
	for _, key := range path {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		cur, ok = m[key]
		if !ok {
			return nil, false
		}
	}
	return cur, true
}

func setPath(m map[string]any, path []string, value any) map[string]any {
	if m == nil {
		m = map[string]any{}
	}
	key := path[0]
	if len(path) == 1 {
		m[key] = value
		return m
	}
	child, _ := m[key].(map[string]any)
	m[key] = setPath(child, path[1:], value)
	return m
}

func deletePath(m map[string]any, path []string) {
	if m == nil {
		return
	}
	key := path[0]
	if len(path) == 1 {
		delete(m, key)
		return
	}
	if child, ok := m[key].(map[string]any); ok {
		deletePath(child, path[1:])
	}
}

func toStringSlice(v any) []string {
	switch t := v.(type) {
	case []string:
		return append([]string(nil), t...)
	case []any:
		out := make([]string, 0, len(t))
		for _, item := range t {
			if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
				out = append(out, strings.TrimSpace(s))
			}
		}
		return out
	case string:
		if strings.TrimSpace(t) == "" {
			return nil
		}
		return []string{strings.TrimSpace(t)}
	}
	return nil
}

func toStringAnySlice(items []string) []any {
	out := make([]any, 0, len(items))
	for _, s := range items {
		out = append(out, s)
	}
	return out
}

func deepCopyMap(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = deepCopyValue(v)
	}
	return out
}

func deepCopyValue(v any) any {
	switch t := v.(type) {
	case map[string]any:
		return deepCopyMap(t)
	case []any:
		out := make([]any, len(t))
		for i, item := range t {
			out[i] = deepCopyValue(item)
		}
		return out
	}
	return v
}

// mergeMap 递归合并 src 到 dst（src 覆盖标量，嵌套 mapping 递归合并，列表整体替换）。
func mergeMap(dst, src map[string]any) map[string]any {
	if dst == nil {
		dst = map[string]any{}
	}
	for k, v := range src {
		if sm, ok := v.(map[string]any); ok {
			dm, _ := dst[k].(map[string]any)
			dst[k] = mergeMap(dm, sm)
			continue
		}
		dst[k] = deepCopyValue(v)
	}
	return dst
}

// MergePatch 把 PATCH 风格的 JSON 合并进配置（列表整体替换）。
func (d *Doc) MergePatch(patch map[string]any) {
	d.root = mergeMap(d.root, patch)
}

// sortStrings 去重并排序（供 providers/apps 输出稳定顺序）。
func sortStrings(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// ReadFile 读取并解析配置文件。
func ReadFile(path string) (*Doc, []byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	doc, err := Parse(data)
	if err != nil {
		return nil, data, err
	}
	return doc, data, nil
}
