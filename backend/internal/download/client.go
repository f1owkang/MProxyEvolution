package download

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// UserAgent 是访问 GitHub/订阅时使用的 User-Agent。
const UserAgent = "MProxyEvolution/0.1 (+https://github.com/MProxyEvolution)"

// HTTPError 表示非 2xx 的 HTTP 响应。
type HTTPError struct {
	StatusCode int
	URL        string
	Body       string
}

func (e *HTTPError) Error() string {
	body := strings.TrimSpace(e.Body)
	if len(body) > 300 {
		body = body[:300] + "..."
	}
	if body == "" {
		return fmt.Sprintf("下载失败（HTTP %d）: %s", e.StatusCode, e.URL)
	}
	return fmt.Sprintf("下载失败（HTTP %d）: %s", e.StatusCode, body)
}

// Client 是带自定义 DNS 解析的 HTTP 下载客户端。
type Client struct {
	Resolver  *Resolver
	HTTP      *http.Client
	UserAgent string
}

// New 创建默认下载客户端。
func New() *Client { return NewWithResolver(NewResolver()) }

// NewWithResolver 使用指定 resolver 创建客户端。
func NewWithResolver(r *Resolver) *Client {
	if r == nil {
		r = NewResolver()
	}
	transport := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           r.DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          10,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   20 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		ExpectContinueTimeout: time.Second,
		TLSClientConfig:       tlsConfig(),
	}
	return &Client{
		Resolver:  r,
		HTTP:      &http.Client{Transport: transport},
		UserAgent: UserAgent,
	}
}

func (c *Client) newRequest(ctx context.Context, method, rawURL string, headers map[string]string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("构造请求失败: %w", err)
	}
	ua := c.UserAgent
	if ua == "" {
		ua = UserAgent
	}
	req.Header.Set("User-Agent", ua)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	return req, nil
}

// Get 下载 URL 内容，maxBytes<=0 时默认 64 MiB。
func (c *Client) Get(ctx context.Context, rawURL string, headers map[string]string, maxBytes int64) ([]byte, error) {
	if maxBytes <= 0 {
		maxBytes = 64 << 20
	}
	req, err := c.newRequest(ctx, http.MethodGet, rawURL, headers)
	if err != nil {
		return nil, err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求 %s 失败: %w", rawURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, &HTTPError{StatusCode: resp.StatusCode, URL: rawURL, Body: string(body)}
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("读取响应失败: %w", err)
	}
	if int64(len(data)) > maxBytes {
		return nil, fmt.Errorf("响应内容超过 %d 字节上限", maxBytes)
	}
	return data, nil
}

// GetJSON 下载 JSON 内容（带 Accept 头）。
func (c *Client) GetJSON(ctx context.Context, rawURL string) ([]byte, error) {
	return c.Get(ctx, rawURL, map[string]string{
		"Accept":               "application/vnd.github+json, application/json",
		"X-GitHub-Api-Version": "2022-11-28",
	}, 32<<20)
}

// Download 流式下载到目标文件（先写 .part 再 rename），progress 可为 nil。
func (c *Client) Download(ctx context.Context, rawURL, dst string, headers map[string]string, progress func(done, total int64)) (int64, error) {
	req, err := c.newRequest(ctx, http.MethodGet, rawURL, headers)
	if err != nil {
		return 0, err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return 0, fmt.Errorf("请求 %s 失败: %w", rawURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return 0, &HTTPError{StatusCode: resp.StatusCode, URL: rawURL, Body: string(body)}
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return 0, err
	}
	tmp := dst + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return 0, fmt.Errorf("创建下载临时文件失败: %w", err)
	}
	defer func() {
		_ = f.Close()
		_ = os.Remove(tmp)
	}()

	total := resp.ContentLength
	var done int64
	buf := make([]byte, 32*1024)
	lastReport := time.Now()
	for {
		if err := ctx.Err(); err != nil {
			return done, err
		}
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := f.Write(buf[:n]); werr != nil {
				return done, fmt.Errorf("写入下载文件失败: %w", werr)
			}
			done += int64(n)
			if progress != nil && (time.Since(lastReport) > 200*time.Millisecond || total > 0 && done >= total) {
				progress(done, total)
				lastReport = time.Now()
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return done, fmt.Errorf("读取下载数据失败: %w", rerr)
		}
	}
	if progress != nil {
		progress(done, total)
	}
	if err := f.Sync(); err != nil {
		return done, err
	}
	if err := f.Close(); err != nil {
		return done, err
	}
	if err := os.Rename(tmp, dst); err != nil {
		return done, fmt.Errorf("保存下载文件失败: %w", err)
	}
	return done, nil
}

// IsGzip 判断数据是否为 gzip 格式。
func IsGzip(data []byte) bool {
	return len(data) >= 2 && data[0] == 0x1f && data[1] == 0x8b
}

// Gunzip 解压 gzip 数据。
func Gunzip(data []byte, maxBytes int64) ([]byte, error) {
	if maxBytes <= 0 {
		maxBytes = 128 << 20
	}
	zr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("gzip 解压失败: %w", err)
	}
	defer zr.Close()
	out, err := io.ReadAll(io.LimitReader(zr, maxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("gzip 解压失败: %w", err)
	}
	if int64(len(out)) > maxBytes {
		return nil, fmt.Errorf("解压后内容超过 %d 字节上限", maxBytes)
	}
	return out, nil
}

// GunzipFile 把 gzip 文件解压为普通文件。
func GunzipFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	zr, err := gzip.NewReader(in)
	if err != nil {
		return fmt.Errorf("gzip 解压失败: %w", err)
	}
	defer zr.Close()
	tmp := dst + ".tmp"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, zr); err != nil {
		_ = out.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("解压内核失败: %w", err)
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// ErrEmptyResponse 表示响应内容为空。
var ErrEmptyResponse = errors.New("响应内容为空")
