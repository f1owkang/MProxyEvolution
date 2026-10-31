package server

import (
	"io/fs"
	"net/http"
	"path"
	"strings"
	"time"

	"mproxy/internal/webui"
)

// staticHandler 服务内嵌面板：/assets/* 长缓存，其余路径回退 index.html。
func (s *Server) staticHandler() http.Handler {
	dist := webui.Files()
	fileServer := http.FileServer(http.FS(dist))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// /api 路径不允许 SPA 回退（正常由 mux 的 /api/ 路由处理，这里兜底 /api）。
		if r.URL.Path == "/api" || strings.HasPrefix(r.URL.Path, "/api/") {
			s.handleAPINotFound(w, r)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		reqPath := path.Clean("/" + strings.TrimPrefix(r.URL.Path, "/"))
		if strings.HasPrefix(reqPath, "/assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		if reqPath == "/" || reqPath == "" {
			s.serveIndex(w, r, dist)
			return
		}
		name := strings.TrimPrefix(reqPath, "/")
		if st, err := fs.Stat(dist, name); err == nil && !st.IsDir() {
			fileServer.ServeHTTP(w, r)
			return
		}
		if strings.HasPrefix(reqPath, "/assets/") {
			http.NotFound(w, r)
			return
		}
		// SPA 路由回退到 index.html
		s.serveIndex(w, r, dist)
	})
}

func (s *Server) serveIndex(w http.ResponseWriter, r *http.Request, dist fs.FS) {
	data, err := fs.ReadFile(dist, "index.html")
	if err != nil {
		http.Error(w, "面板静态资源缺失，请先构建 zashboard", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	http.ServeContent(w, r, "index.html", time.Time{}, strings.NewReader(string(data)))
}
