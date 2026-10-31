package server

import (
	"net/http"
	"os"
	"time"

	"mproxy/internal/core"
	"mproxy/internal/sysutil"
	"mproxy/internal/version"
)

type diagnosticCheck struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
}

type diagnosticFile struct {
	Name       string `json:"name"`
	Path       string `json:"path"`
	Exists     bool   `json:"exists"`
	Size       int64  `json:"size"`
	ModifiedAt string `json:"modifiedAt,omitempty"`
}

type diagnosticsResponse struct {
	GeneratedAt string             `json:"generatedAt"`
	Version     string             `json:"version"`
	DataDir     string             `json:"dataDir"`
	ModuleDir   string             `json:"moduleDir"`
	Core        core.Status        `json:"core"`
	Summary     any                `json:"summary"`
	System      sysutil.SystemInfo `json:"system"`
	Checks      []diagnosticCheck  `json:"checks"`
	Files       []diagnosticFile   `json:"files"`
	LogTail     []string           `json:"logTail"`
}

func (s *Server) handleDiagnostics(w http.ResponseWriter, r *http.Request) {
	st := s.opts.Core.Status()
	resp := diagnosticsResponse{
		GeneratedAt: time.Now().Format(time.RFC3339),
		Version:     version.Version,
		DataDir:     s.opts.Paths.DataDir,
		ModuleDir:   s.opts.Paths.ModuleDir,
		Core:        st,
		System:      sysutil.CollectSystemInfo(),
		LogTail:     s.opts.Core.LogTail(50),
	}
	if doc, err := s.opts.Config.Doc(); err == nil {
		resp.Summary = doc.Summary()
		resp.Checks = append(resp.Checks,
			diagnosticCheck{Name: "配置有效", OK: true, Detail: s.opts.Paths.ConfigPath()},
			diagnosticCheck{Name: "控制器密钥", OK: st.Controller.SecretSet, Detail: secretDetail(st)},
			diagnosticCheck{Name: "TUN 模式", OK: doc.GetBool(false, "tun", "enable") || doc.Settings().ProxyMode == "mixed", Detail: doc.Settings().ProxyMode},
		)
	} else {
		resp.Checks = append(resp.Checks, diagnosticCheck{Name: "配置有效", OK: false, Detail: err.Error()})
	}
	resp.Checks = append(resp.Checks,
		diagnosticCheck{Name: "内核已安装", OK: st.Installed, Detail: s.opts.Core.CoreBinary()},
		diagnosticCheck{Name: "内核运行中", OK: st.Running, Detail: versionOrUnknown(st.Version)},
		diagnosticCheck{Name: "控制器可访问", OK: st.Controller.Reachable, Detail: st.Controller.Host + ":" + itoa(st.Controller.Port)},
	)
	resp.Files = []diagnosticFile{
		diagnosticInfo("配置文件", s.opts.Paths.ConfigPath()),
		diagnosticInfo("配置备份", s.opts.Paths.BackupPath()),
		diagnosticInfo("内核二进制", s.opts.Core.CoreBinary()),
		diagnosticInfo("内核日志", s.opts.Core.LogPath()),
		diagnosticInfo("后端日志", s.opts.Paths.BackendLogPath()),
	}
	writeJSON(w, http.StatusOK, resp)
}

func diagnosticInfo(name, path string) diagnosticFile {
	f := diagnosticFile{Name: name, Path: path}
	if path == "" {
		return f
	}
	if st, err := os.Stat(path); err == nil {
		f.Exists = true
		f.Size = st.Size()
		f.ModifiedAt = st.ModTime().Format(time.RFC3339)
	}
	return f
}

func secretDetail(st core.Status) string {
	if st.Controller.SecretSet {
		return "已设置"
	}
	return "未设置"
}

func versionOrUnknown(v string) string {
	if v == "" {
		return "未知"
	}
	return v
}

func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	var buf [8]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	return string(buf[i:])
}
