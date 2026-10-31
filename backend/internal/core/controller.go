package core

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Controller 是 mihomo external-controller 的轻量客户端。
type Controller struct {
	Addr   string
	Secret string
	HTTP   *http.Client
}

// NewController 创建控制器客户端（地址形如 127.0.0.1:9090）。
func NewController(addr, secret string) *Controller {
	if addr == "" {
		addr = "127.0.0.1:9090"
	}
	return &Controller{
		Addr:   addr,
		Secret: secret,
		HTTP: &http.Client{
			Timeout: 3 * time.Second,
			Transport: &http.Transport{
				MaxIdleConns:       4,
				IdleConnTimeout:    30 * time.Second,
				DisableCompression: false,
				ForceAttemptHTTP2:  false,
			},
		},
	}
}

func (c *Controller) do(ctx context.Context, method, path string, body any, timeout time.Duration) (*http.Response, error) {
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("序列化请求失败: %w", err)
		}
		reader = bytes.NewReader(b)
	}
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://"+c.Addr+path, reader)
	if err != nil {
		return nil, fmt.Errorf("构造控制器请求失败: %w", err)
	}
	if c.Secret != "" {
		req.Header.Set("Authorization", "Bearer "+c.Secret)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("连接 mihomo 控制器 %s 失败: %w", c.Addr, err)
	}
	return resp, nil
}

func readErrorBody(resp *http.Response) string {
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
	text := strings.TrimSpace(string(b))
	if text == "" {
		return fmt.Sprintf("HTTP %d", resp.StatusCode)
	}
	return fmt.Sprintf("HTTP %d: %s", resp.StatusCode, text)
}

// Version 读取 /version，返回 version 字段。
func (c *Controller) Version(ctx context.Context) (string, error) {
	resp, err := c.do(ctx, http.MethodGet, "/version", nil, 2*time.Second)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("读取内核版本失败（%s）", readErrorBody(resp))
	}
	var out struct {
		Version string `json:"version"`
		Meta    bool   `json:"meta"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("解析内核版本失败: %w", err)
	}
	return out.Version, nil
}

// Configs 读取 /configs 实时配置。
func (c *Controller) Configs(ctx context.Context) (map[string]any, error) {
	resp, err := c.do(ctx, http.MethodGet, "/configs", nil, 2*time.Second)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("读取内核配置失败（%s）", readErrorBody(resp))
	}
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("解析内核配置失败: %w", err)
	}
	return out, nil
}

// Reload 热重载指定配置文件：PUT /configs?force=true {"path": ...}。
func (c *Controller) Reload(ctx context.Context, configPath string) error {
	resp, err := c.do(ctx, http.MethodPut, "/configs?force=true", map[string]any{"path": configPath}, 15*time.Second)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		return fmt.Errorf("热重载配置失败（%s）", readErrorBody(resp))
	}
	resp.Body.Close()
	return nil
}

// PatchConfigs 把 PATCH /configs 转发给运行中的内核。
func (c *Controller) PatchConfigs(ctx context.Context, patch map[string]any) error {
	resp, err := c.do(ctx, http.MethodPatch, "/configs", patch, 15*time.Second)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		return fmt.Errorf("应用内核配置补丁失败（%s）", readErrorBody(resp))
	}
	resp.Body.Close()
	return nil
}

// PutConfigsPayload 用内联 YAML 覆盖运行中内核的配置。
func (c *Controller) PutConfigsPayload(ctx context.Context, payload string) error {
	resp, err := c.do(ctx, http.MethodPut, "/configs?force=true", map[string]any{"payload": payload}, 20*time.Second)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		return fmt.Errorf("应用完整配置失败（%s）", readErrorBody(resp))
	}
	resp.Body.Close()
	return nil
}
