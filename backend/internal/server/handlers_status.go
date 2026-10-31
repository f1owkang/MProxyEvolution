package server

import (
	"net/http"
	"os"
	"time"

	"mproxy/internal/config"
	"mproxy/internal/core"
	"mproxy/internal/sysutil"
	"mproxy/internal/version"
)

type healthResponse struct {
	OK      bool   `json:"ok"`
	Version string `json:"version"`
	PID     int    `json:"pid"`
}

type backendStatus struct {
	Version   string `json:"version"`
	PID       int    `json:"pid"`
	UptimeSec int64  `json:"uptimeSec"`
	StartedAt string `json:"startedAt"`
	DataDir   string `json:"dataDir"`
	ModuleDir string `json:"moduleDir"`
}

type configStatus struct {
	Path       string         `json:"path"`
	Exists     bool           `json:"exists"`
	Size       int64          `json:"size"`
	ModifiedAt string         `json:"modifiedAt,omitempty"`
	Valid      bool           `json:"valid"`
	Summary    config.Summary `json:"summary"`
}

type updateInfo struct {
	Channel string `json:"channel"`
	Ghproxy string `json:"ghproxy"`
}

type statusResponse struct {
	Backend backendStatus      `json:"backend"`
	Core    core.Status        `json:"core"`
	Config  configStatus       `json:"config"`
	System  sysutil.SystemInfo `json:"system"`
	Update  updateInfo         `json:"update"`
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, healthResponse{OK: true, Version: version.Version, PID: os.Getpid()})
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.buildStatus())
}

func (s *Server) buildStatus() statusResponse {
	resp := statusResponse{
		Backend: backendStatus{
			Version:   version.Version,
			PID:       os.Getpid(),
			UptimeSec: int64(time.Since(s.started).Seconds()),
			StartedAt: s.started.Format(time.RFC3339),
			DataDir:   s.opts.Paths.DataDir,
			ModuleDir: s.opts.Paths.ModuleDir,
		},
		Core: s.opts.Core.Status(),
		Config: configStatus{
			Path: s.opts.Paths.ConfigPath(),
		},
		System: sysutil.CollectSystemInfo(),
	}
	settings := config.DefaultSettings()
	if raw, mod, exists, err := s.opts.Config.Raw(); err == nil {
		resp.Config.Exists = exists
		resp.Config.Size = int64(len(raw))
		if !mod.IsZero() {
			resp.Config.ModifiedAt = mod.Format(time.RFC3339)
		}
	}
	if doc, err := s.opts.Config.Doc(); err == nil {
		resp.Config.Valid = true
		resp.Config.Summary = doc.Summary()
		settings = doc.Settings()
	}
	if st, err := os.Stat(s.opts.Paths.ConfigPath()); err == nil {
		resp.Config.Size = st.Size()
		resp.Config.ModifiedAt = st.ModTime().Format(time.RFC3339)
	}
	resp.Update = updateInfo{Channel: settings.UpdateChannel, Ghproxy: settings.Ghproxy}
	return resp
}

// handleMeta 返回带 token 的面板地址（前端直接用 location 打开）。
func (s *Server) handleMeta(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"url":     s.PanelURL(),
		"version": version.Version,
	})
}
