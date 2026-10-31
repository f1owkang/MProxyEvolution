package server

import (
	"bufio"
	"net"
	"net/http"
	"time"
)

// statusRecorder 记录响应状态码，并透传 Flush/Hijack（SSE/WebSocket 需要）。
type statusRecorder struct {
	http.ResponseWriter
	status int
	wrote  bool
}

func (r *statusRecorder) WriteHeader(status int) {
	if r.wrote {
		return
	}
	r.status = status
	r.wrote = true
	r.ResponseWriter.WriteHeader(status)
}

func (r *statusRecorder) Write(p []byte) (int, error) {
	if !r.wrote {
		r.WriteHeader(http.StatusOK)
	}
	return r.ResponseWriter.Write(p)
}

// Flush 支持 SSE 流式输出。
func (r *statusRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Hijack 支持 WebSocket 升级。
func (r *statusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if h, ok := r.ResponseWriter.(http.Hijacker); ok {
		return h.Hijack()
	}
	return nil, nil, http.ErrNotSupported
}

// Unwrap 供 http.ResponseController 使用。
func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

// withLogging 只记录 /api 请求，避免静态资源刷屏。
func (s *Server) withLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isAPIPath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		elapsed := time.Since(start).Milliseconds()
		if rec.status >= 400 {
			s.opts.Logger.Printf("%s %s -> %d (%dms) %s", r.Method, r.URL.Path, rec.status, elapsed, r.RemoteAddr)
		} else if elapsed > 1500 {
			s.opts.Logger.Printf("%s %s -> %d (%dms)", r.Method, r.URL.Path, rec.status, elapsed)
		}
	})
}

func isAPIPath(p string) bool {
	return len(p) >= 5 && p[:5] == "/api/"
}
