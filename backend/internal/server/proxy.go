package server

import (
	"context"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"

	"mproxy/internal/core"
)

// mihomoProxy 把 /api/mihomo/* 反向代理到本机 mihomo external-controller。
type mihomoProxy struct {
	server    *Server
	transport http.RoundTripper
}

func (p *mihomoProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !p.server.opts.Core.Running() {
		writeErr(w, http.StatusBadGateway, "core_not_running", "mihomo 内核未运行，无法访问面板 API", nil)
		return
	}
	doc, err := p.server.opts.Config.Doc()
	if err != nil {
		writeErr(w, http.StatusBadGateway, "config_invalid", "读取配置失败，无法定位控制器", err.Error())
		return
	}
	addr, secret := doc.Controller()
	if addr == "" {
		writeErr(w, http.StatusBadGateway, "controller_missing", "配置中缺少 external-controller", nil)
		return
	}
	target := &url.URL{Scheme: "http", Host: addr}
	rp := &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			originalPath := req.URL.Path
			req.URL.Scheme = target.Scheme
			req.URL.Host = target.Host
			forwarded := strings.TrimPrefix(originalPath, "/api/mihomo")
			if forwarded == "" || forwarded == "/" {
				req.URL.Path = "/"
			} else if !strings.HasPrefix(forwarded, "/") {
				req.URL.Path = "/" + forwarded
			} else {
				req.URL.Path = forwarded
			}
			req.URL.RawPath = ""
			// 移除 MProxyEvolution token，补上 mihomo secret
			if q := req.URL.Query(); q.Has("token") {
				q.Del("token")
				req.URL.RawQuery = q.Encode()
			}
			req.Header.Del("X-MProxy-Token")
			req.Header.Del("Authorization")
			if secret != "" {
				req.Header.Set("Authorization", "Bearer "+secret)
			}
			req.Host = target.Host
		},
		Transport:     p.transport,
		FlushInterval: -1, // SSE 立即刷新
		ErrorHandler: func(rw http.ResponseWriter, req *http.Request, err error) {
			writeErr(rw, http.StatusBadGateway, "core_unreachable", "转发到 mihomo 控制器失败", err.Error())
		},
	}
	rp.ServeHTTP(w, r)
}

// proxyController 返回当前控制器客户端。
func (p *mihomoProxy) proxyController() (*core.Controller, bool) {
	doc, err := p.server.opts.Config.Doc()
	if err != nil {
		return nil, false
	}
	addr, secret := doc.Controller()
	return core.NewController(addr, secret), true
}

// forwardPatch 把 PATCH /configs 直接转发给内核。
func (s *Server) forwardPatch(ctx context.Context, patch map[string]any) error {
	if !s.opts.Core.Running() {
		return nil
	}
	ctl, err := s.opts.Core.Controller()
	if err != nil {
		return err
	}
	return ctl.PatchConfigs(ctx, patch)
}
