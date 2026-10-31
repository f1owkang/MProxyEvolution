package server

import (
	"net/http"
	"strings"

	"mproxy/internal/config"
)

// handleProxyPatchConfigs 持久化 PATCH /api/mihomo/configs：先合并进 YAML，再转发给内核。
func (s *Server) handleProxyPatchConfigs(w http.ResponseWriter, r *http.Request) {
	var patch map[string]any
	if err := decodeJSONBody(r, &patch, 8<<20); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error(), nil)
		return
	}
	normalized, _ := normalizeJSON(patch).(map[string]any)
	if normalized == nil {
		writeErr(w, http.StatusBadRequest, "bad_request", "补丁内容必须是 JSON 对象", nil)
		return
	}
	doc, err := s.opts.Config.Doc()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "config_invalid", "当前配置无效，无法应用补丁", err.Error())
		return
	}
	newDoc := doc.Clone()
	newDoc.MergePatch(normalized)
	// 面板「后端 → TUN 模式」走 PATCH /configs；保持 x-mproxy.proxy-mode 同步，
	// 避免出现“TUN 开了但模块认为还是 mixed”的双状态。
	if tun, ok := normalized["tun"].(map[string]any); ok {
		if enable, ok := tun["enable"].(bool); ok {
			mode := config.ProxyModeMixed
			if enable {
				mode = config.ProxyModeTun
			}
			newDoc.Set([]string{"x-mproxy", "proxy-mode"}, mode)
		}
	}
	raw, err := newDoc.Marshal()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "marshal_failed", "序列化配置失败", err.Error())
		return
	}
	output, verr := s.validateDocBytes(newDoc, raw)
	if verr != nil {
		writeErr(w, http.StatusBadRequest, "invalid_config", "补丁应用后的配置校验失败", output)
		return
	}
	if _, err := s.opts.Config.Save(newDoc); err != nil {
		writeErr(w, http.StatusInternalServerError, "save_failed", "保存配置失败", err.Error())
		return
	}
	if err := s.forwardPatch(r.Context(), normalized); err != nil {
		writeErr(w, http.StatusBadGateway, "core_patch_failed", "配置已保存，但转发给内核失败", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleProxyPutConfigs 处理 PUT /api/mihomo/configs：payload 为完整 YAML 替换，否则重载。
func (s *Server) handleProxyPutConfigs(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Payload string `json:"payload"`
		Path    string `json:"path"`
	}
	if err := decodeJSONBody(r, &req, 16<<20); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error(), nil)
		return
	}
	reload := strings.EqualFold(r.URL.Query().Get("reload"), "true")
	if strings.TrimSpace(req.Payload) != "" {
		newDoc, err := config.Parse([]byte(req.Payload))
		if err != nil {
			writeErr(w, http.StatusBadRequest, "invalid_config", "完整配置解析失败", err.Error())
			return
		}
		oldDoc, _ := s.opts.Config.Doc()
		if oldDoc != nil {
			if x, ok := oldDoc.Get("x-mproxy"); ok {
				newDoc.SetTop("x-mproxy", x)
			}
			preserveControllerFallback(oldDoc, newDoc)
		}
		raw, err := newDoc.Marshal()
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "marshal_failed", "序列化配置失败", err.Error())
			return
		}
		output, verr := s.validateDocBytes(newDoc, raw)
		if verr != nil {
			writeErr(w, http.StatusBadRequest, "invalid_config", "完整配置校验失败", output)
			return
		}
		if _, err := s.opts.Config.Save(newDoc); err != nil {
			writeErr(w, http.StatusInternalServerError, "save_failed", "保存配置失败", err.Error())
			return
		}
		s.applyConfig(r.Context(), oldDoc, newDoc, "auto")
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if reload || strings.TrimSpace(req.Path) != "" {
		if s.opts.Core.Running() {
			if err := s.opts.Core.Reload(r.Context()); err != nil {
				writeErr(w, http.StatusBadGateway, "core_reload_failed", "重载 MProxyEvolution 配置失败", err.Error())
				return
			}
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleProxyRestart 重启 MProxyEvolution 管理的 mihomo 进程。
func (s *Server) handleProxyRestart(w http.ResponseWriter, r *http.Request) {
	if err := s.opts.Core.Restart(r.Context()); err != nil {
		writeErr(w, http.StatusInternalServerError, "core_restart_failed", err.Error(), nil)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
