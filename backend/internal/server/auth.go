package server

import (
	"crypto/subtle"
	"net/http"
	"strings"
)

// tokenFromRequest 从 X-MProxy-Token / Bearer / ?token= 中取 token。
func tokenFromRequest(r *http.Request) string {
	if v := strings.TrimSpace(r.Header.Get("X-MProxy-Token")); v != "" {
		return v
	}
	auth := strings.TrimSpace(r.Header.Get("Authorization"))
	if len(auth) > 7 && strings.EqualFold(auth[:7], "Bearer ") {
		return strings.TrimSpace(auth[7:])
	}
	if v := strings.TrimSpace(r.URL.Query().Get("token")); v != "" {
		return v
	}
	return ""
}

// withAuth 保护 /api/*；静态资源、/healthz 与 MPROXY_DEV=1 时公开。
func (s *Server) withAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.opts.DevMode || r.URL.Path == "/healthz" || !strings.HasPrefix(r.URL.Path, "/api/") {
			next.ServeHTTP(w, r)
			return
		}
		tok := tokenFromRequest(r)
		expected := s.opts.Token
		if expected == "" || subtle.ConstantTimeCompare([]byte(tok), []byte(expected)) != 1 {
			writeErr(w, http.StatusUnauthorized, "unauthorized", "缺少或无效的访问令牌", nil)
			return
		}
		next.ServeHTTP(w, r)
	})
}
