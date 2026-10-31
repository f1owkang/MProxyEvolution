package server

import (
	"context"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"mproxy/internal/config"
	"mproxy/internal/core"
	"mproxy/internal/sysutil"
)

// coreActionResponse 是内核操作响应。
type coreActionResponse struct {
	OK     bool        `json:"ok"`
	Status core.Status `json:"status"`
}

func (s *Server) handleCoreStart(w http.ResponseWriter, r *http.Request) {
	// 生命周期操作不随客户端断开取消，否则 WebView 重载可能让内核停在半启动状态。
	ctx, cancel := lifecycleContext(r.Context(), 60*time.Second)
	defer cancel()
	if err := s.opts.Core.Start(ctx); err != nil {
		writeErr(w, http.StatusInternalServerError, "core_start_failed", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, coreActionResponse{OK: true, Status: s.opts.Core.Status()})
}

func (s *Server) handleCoreStop(w http.ResponseWriter, r *http.Request) {
	if err := s.opts.Core.Stop(); err != nil {
		writeErr(w, http.StatusInternalServerError, "core_stop_failed", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, coreActionResponse{OK: true, Status: s.opts.Core.Status()})
}

func (s *Server) handleCoreRestart(w http.ResponseWriter, r *http.Request) {
	// 重启包含停止与启动，必须脱离请求 context，避免客户端断开导致内核停住。
	ctx, cancel := lifecycleContext(r.Context(), 60*time.Second)
	defer cancel()
	if err := s.opts.Core.Restart(ctx); err != nil {
		writeErr(w, http.StatusInternalServerError, "core_restart_failed", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, coreActionResponse{OK: true, Status: s.opts.Core.Status()})
}

func (s *Server) handleCoreReload(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	if err := s.opts.Core.Reload(ctx); err != nil {
		writeErr(w, http.StatusInternalServerError, "core_reload_failed", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, coreActionResponse{OK: true, Status: s.opts.Core.Status()})
}

func (s *Server) channelAndGhproxy() (string, string) {
	settings := config.DefaultSettings()
	if doc, err := s.opts.Config.Doc(); err == nil {
		settings = doc.Settings()
	}
	return settings.UpdateChannel, settings.Ghproxy
}

func (s *Server) handleCoreUpdateCheck(w http.ResponseWriter, r *http.Request) {
	channel := strings.TrimSpace(r.URL.Query().Get("channel"))
	if channel == "" {
		channel, _ = s.channelAndGhproxy()
	}
	if err := config.ValidateChannel(channel); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error(), nil)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	_, ghproxy := s.channelAndGhproxy()
	res, err := s.opts.Core.CheckUpdate(ctx, s.opts.Download, channel, ghproxy)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "update_check_failed", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) handleCoreUpdate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Channel string `json:"channel"`
	}
	if r.ContentLength > 0 {
		if err := decodeJSONBody(r, &req, 1<<20); err != nil {
			writeErr(w, http.StatusBadRequest, "bad_request", err.Error(), nil)
			return
		}
	}
	channel := strings.TrimSpace(req.Channel)
	settingsChannel, ghproxy := s.channelAndGhproxy()
	if channel == "" {
		channel = settingsChannel
	}
	if err := config.ValidateChannel(channel); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error(), nil)
		return
	}
	j := s.opts.Jobs.Start("core-update")
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
		defer cancel()
		result, err := s.opts.Core.Update(ctx, s.opts.Download, channel, ghproxy, j)
		if err != nil {
			s.opts.Logger.Printf("内核更新失败: %v", err)
			j.Fail(err)
			return
		}
		j.Done(result)
	}()
	writeJSON(w, http.StatusAccepted, map[string]any{"jobId": j.Snapshot().ID})
}

func (s *Server) handleJobGet(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	j, ok := s.opts.Jobs.Get(id)
	if !ok {
		writeErr(w, http.StatusNotFound, "job_not_found", "任务不存在", nil)
		return
	}
	writeJSON(w, http.StatusOK, j)
}

func (s *Server) handleJobList(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"jobs": s.opts.Jobs.List()})
}

type logResponse struct {
	Path    string   `json:"path"`
	Lines   []string `json:"lines"`
	Content string   `json:"content"`
}

func (s *Server) handleCoreLogs(w http.ResponseWriter, r *http.Request) {
	writeLogResponse(w, r, s.opts.Core.LogPath())
}

func (s *Server) handleBackendLogs(w http.ResponseWriter, r *http.Request) {
	writeLogResponse(w, r, s.opts.Paths.BackendLogPath())
}

func writeLogResponse(w http.ResponseWriter, r *http.Request, path string) {
	lines := 300
	if v := r.URL.Query().Get("lines"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			lines = n
		}
	}
	if lines <= 0 {
		lines = 300
	}
	if lines > 5000 {
		lines = 5000
	}
	tail, err := sysutil.TailFile(path, lines)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "logs_failed", "读取日志失败", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, logResponse{Path: path, Lines: tail, Content: strings.Join(tail, "\n")})
}

func (s *Server) handleCoreLogsClear(w http.ResponseWriter, r *http.Request) {
	if err := s.opts.Core.ClearLog(); err != nil {
		writeErr(w, http.StatusInternalServerError, "logs_clear_failed", "清空日志失败", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleBackendRestart(w http.ResponseWriter, r *http.Request) {
	if err := s.restartBackend(); err != nil {
		writeErr(w, http.StatusInternalServerError, "backend_restart_failed", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	exitSoon(400 * time.Millisecond)
}

func (s *Server) handleBackendShutdown(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	go func() {
		time.Sleep(200 * time.Millisecond)
		sysutil.RemovePID(s.opts.Paths.BackendPIDPath())
		os.Exit(0)
	}()
}

// PackageInfo 描述一个 Android 应用包。
type PackageInfo struct {
	PackageName string `json:"packageName"`
	UID         int    `json:"uid"`
	System      bool   `json:"system"`
}

func (s *Server) handlePackages(w http.ResponseWriter, r *http.Request) {
	typ := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("type")))
	if typ == "" {
		typ = "user"
	}
	if typ != "user" && typ != "system" && typ != "all" {
		writeErr(w, http.StatusBadRequest, "bad_request", "type 只能是 user / system / all", nil)
		return
	}
	packages, err := listPackages(r.Context(), typ)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "packages_failed", err.Error(), nil)
		return
	}
	sort.Slice(packages, func(i, j int) bool { return packages[i].PackageName < packages[j].PackageName })
	writeJSON(w, http.StatusOK, map[string]any{"packages": packages})
}
