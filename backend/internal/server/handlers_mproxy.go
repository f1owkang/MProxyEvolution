package server

import (
	"errors"
	"net/http"
	"strings"

	"mproxy/internal/config"
	"mproxy/internal/sysutil"
)

type settingsResponse struct {
	X           config.Settings `json:"x"`
	CoreVersion string          `json:"coreVersion"`
	ABI         string          `json:"abi"`
	Installed   bool            `json:"installed"`
	Running     bool            `json:"running"`
	CanUpdate   bool            `json:"canUpdate"`
}

func (s *Server) handleSettingsGet(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.settingsResponse())
}

func (s *Server) settingsResponse() settingsResponse {
	st := s.opts.Core.Status()
	settings := config.DefaultSettings()
	if doc, err := s.opts.Config.Doc(); err == nil {
		settings = doc.Settings()
	}
	return settingsResponse{
		X:           settings,
		CoreVersion: st.Version,
		ABI:         sysutil.ABI(),
		Installed:   st.Installed,
		Running:     st.Running,
		CanUpdate:   st.Installed,
	}
}

func (s *Server) handleSettingsPatch(w http.ResponseWriter, r *http.Request) {
	var patch config.SettingsPatch
	if err := decodeJSONBody(r, &patch, 1<<20); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error(), nil)
		return
	}
	oldDoc, err := s.opts.Config.Doc()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "config_invalid", "当前配置无效，无法保存设置", err.Error())
		return
	}
	newDoc := oldDoc.Clone()
	changed, err := newDoc.ApplySettings(patch)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error(), nil)
		return
	}
	if !changed {
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":       true,
			"changed":  false,
			"settings": newDoc.Settings(),
		})
		return
	}
	if output, err := s.saveValidatedDoc(newDoc); err != nil {
		writeSaveError(w, output, err, "保存设置失败")
		return
	}
	outcome := s.applyConfig(r.Context(), oldDoc, newDoc, "auto")
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":        true,
		"changed":   true,
		"applied":   outcome.Applied,
		"restarted": outcome.Restarted,
		"warnings":  outcome.Warnings,
		"settings":  newDoc.Settings(),
	})
}

// writeSaveError 把配置校验/保存失败转成统一错误响应：校验失败返回 400 invalid_config，
// 序列化/保存失败返回 500 save_failed。
func writeSaveError(w http.ResponseWriter, output string, err error, saveMessage string) {
	var ve *validationError
	if errors.As(err, &ve) {
		writeErr(w, http.StatusBadRequest, "invalid_config", "配置校验失败", output)
		return
	}
	writeErr(w, http.StatusInternalServerError, "save_failed", saveMessage, err.Error())
}

type appsResponse struct {
	Mode       config.AppsMode `json:"mode"`
	Apps       []string        `json:"apps"`
	TunEnabled bool            `json:"tunEnabled"`
}

func (s *Server) handleAppsGet(w http.ResponseWriter, r *http.Request) {
	doc, err := s.opts.Config.Doc()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "config_invalid", err.Error(), nil)
		return
	}
	mode, apps := doc.Apps()
	writeJSON(w, http.StatusOK, appsResponse{
		Mode:       mode,
		Apps:       apps,
		TunEnabled: doc.GetBool(false, "tun", "enable"),
	})
}

func (s *Server) handleAppsPut(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Mode string   `json:"mode"`
		Apps []string `json:"apps"`
	}
	if err := decodeJSONBody(r, &req, 8<<20); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error(), nil)
		return
	}
	oldDoc, err := s.opts.Config.Doc()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "config_invalid", "当前配置无效，无法保存应用分流", err.Error())
		return
	}
	newDoc := oldDoc.Clone()
	mode := config.AppsMode(strings.ToLower(strings.TrimSpace(req.Mode)))
	if mode == "" {
		mode = config.AppsAll
	}
	if err := newDoc.SetApps(mode, req.Apps); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error(), nil)
		return
	}
	if output, err := s.saveValidatedDoc(newDoc); err != nil {
		writeSaveError(w, output, err, "保存应用分流失败")
		return
	}
	outcome := s.applyConfig(r.Context(), oldDoc, newDoc, "restart")
	m, apps := newDoc.Apps()
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":        true,
		"applied":   outcome.Applied,
		"restarted": outcome.Restarted,
		"warnings":  outcome.Warnings,
		"mode":      m,
		"apps":      apps,
	})
}

func (s *Server) handleProviderAdd(w http.ResponseWriter, r *http.Request) {
	var spec config.ProviderSpec
	if err := decodeJSONBody(r, &spec, 1<<20); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error(), nil)
		return
	}
	oldDoc, err := s.opts.Config.Doc()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "config_invalid", "当前配置无效，无法添加订阅", err.Error())
		return
	}
	newDoc := oldDoc.Clone()
	if err := newDoc.AddProvider(spec); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error(), nil)
		return
	}
	if output, err := s.saveValidatedDoc(newDoc); err != nil {
		writeSaveError(w, output, err, "保存订阅失败")
		return
	}
	outcome := s.applyConfig(r.Context(), oldDoc, newDoc, "reload")
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":        true,
		"applied":   outcome.Applied,
		"restarted": outcome.Restarted,
		"warnings":  outcome.Warnings,
		"providers": newDoc.Providers(),
	})
}

func (s *Server) handleProviderDelete(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.PathValue("name"))
	if name == "" {
		writeErr(w, http.StatusBadRequest, "bad_request", "provider 名称不能为空", nil)
		return
	}
	oldDoc, err := s.opts.Config.Doc()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "config_invalid", "当前配置无效，无法删除订阅", err.Error())
		return
	}
	newDoc := oldDoc.Clone()
	if !newDoc.DeleteProvider(name) {
		writeErr(w, http.StatusNotFound, "provider_not_found", "订阅不存在: "+name, nil)
		return
	}
	if output, err := s.saveValidatedDoc(newDoc); err != nil {
		writeSaveError(w, output, err, "保存订阅失败")
		return
	}
	outcome := s.applyConfig(r.Context(), oldDoc, newDoc, "reload")
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":        true,
		"applied":   outcome.Applied,
		"restarted": outcome.Restarted,
		"warnings":  outcome.Warnings,
		"providers": newDoc.Providers(),
	})
}
