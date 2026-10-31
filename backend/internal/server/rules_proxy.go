package server

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ruleProviderMirrors 是后端代下载规则集时依次尝试的镜像前缀。
// mihomo 静态二进制在 Android 上常找不到系统 CA，且直连 GitHub/jsDelivr 可能被重置；
// 由后端用内嵌 CA 的下载客户端取回再以本机 HTTP 提供给内核，可同时绕开证书与 EOF 问题。
var ruleProviderMirrors = []string{
	"https://gh-proxy.com/",
	"https://ghfast.top/",
	"https://cdn.jsdelivr.net/gh/MetaCubeX/meta-rules-dat@meta/",
}

// handleRuleProvider 是 mihomo rule-providers 的本地拉取入口：
//
//	http://127.0.0.1:9091/rules/<name>.yaml?src=<raw.githubusercontent.com URL>
//
// 只允许 MetaCubeX/meta-rules-dat 的已知规则集，避免变成任意 URL 代理（SSRF）。
func (s *Server) handleRuleProvider(w http.ResponseWriter, r *http.Request) {
	src := r.URL.Query().Get("src")
	if !validRuleSource(src) {
		http.Error(w, "invalid rule source", http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 40*time.Second)
	defer cancel()

	var lastErr error
	for _, u := range ruleProviderCandidates(src) {
		data, err := s.opts.Download.Get(ctx, u, nil, 16<<20)
		if err != nil {
			lastErr = err
			continue
		}
		if len(data) == 0 {
			lastErr = fmt.Errorf("空内容: %s", u)
			continue
		}
		w.Header().Set("Content-Type", "text/yaml; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(data)
		return
	}
	http.Error(w, fmt.Sprintf("规则集下载失败: %v", lastErr), http.StatusBadGateway)
}

// validRuleSource 只接受 MetaCubeX/meta-rules-dat 的 https raw 地址。
func validRuleSource(src string) bool {
	u, err := url.Parse(src)
	if err != nil || u.Scheme != "https" || u.Host != "raw.githubusercontent.com" {
		return false
	}
	return strings.HasPrefix(u.Path, "/MetaCubeX/meta-rules-dat/")
}

// ruleProviderCandidates 返回镜像候选；jsDelivr 需要把 raw 路径映射成 @meta 路径。
func ruleProviderCandidates(src string) []string {
	candidates := make([]string, 0, len(ruleProviderMirrors))
	for _, prefix := range ruleProviderMirrors {
		if strings.Contains(prefix, "cdn.jsdelivr.net") {
			mapped := strings.Replace(src,
				"https://raw.githubusercontent.com/MetaCubeX/meta-rules-dat/meta/",
				"https://cdn.jsdelivr.net/gh/MetaCubeX/meta-rules-dat@meta/", 1)
			if mapped != src {
				candidates = append(candidates, mapped)
				continue
			}
		}
		candidates = append(candidates, prefix+src)
	}
	candidates = append(candidates, src)
	return candidates
}
