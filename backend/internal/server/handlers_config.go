package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"mproxy/internal/config"
)

// validationError 表示配置未通过校验。
type validationError struct {
	Output string
}

func (e *validationError) Error() string { return "配置校验失败: " + e.Output }

// applyOutcome 描述配置应用结果。
type applyOutcome struct {
	Applied   bool
	Restarted bool
	Method    string
	Warnings  []string
	Message   string
}

type applyResponse struct {
	OK               bool           `json:"ok"`
	Saved            bool           `json:"saved"`
	Applied          bool           `json:"applied"`
	Restarted        bool           `json:"restarted"`
	Summary          config.Summary `json:"summary"`
	ValidationOutput string         `json:"validationOutput"`
	Warnings         []string       `json:"warnings,omitempty"`
	Message          string         `json:"message,omitempty"`
}

type configGetResponse struct {
	YAML             string         `json:"yaml"`
	Summary          config.Summary `json:"summary"`
	Path             string         `json:"path"`
	ModifiedAt       string         `json:"modifiedAt,omitempty"`
	Valid            bool           `json:"valid"`
	ValidationOutput string         `json:"validationOutput"`
}

func (s *Server) handleConfigGet(w http.ResponseWriter, r *http.Request) {
	raw, mod, _, err := s.opts.Config.Raw()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "config_invalid", err.Error(), nil)
		return
	}
	resp := configGetResponse{
		YAML: string(raw),
		Path: s.opts.Paths.ConfigPath(),
	}
	if !mod.IsZero() {
		resp.ModifiedAt = mod.Format("2006-01-02T15:04:05Z07:00")
	}
	if doc, derr := s.opts.Config.Doc(); derr == nil {
		resp.Summary = doc.Summary()
		resp.Valid = true
	} else {
		resp.Valid = false
		resp.ValidationOutput = derr.Error()
	}
	if loadErr := s.opts.Config.LoadError(); loadErr != nil {
		resp.Valid = false
		resp.ValidationOutput = loadErr.Error()
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleConfigValidate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		YAML string `json:"yaml"`
	}
	if err := decodeJSONBody(r, &req, 16<<20); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error(), nil)
		return
	}
	if strings.TrimSpace(req.YAML) == "" {
		writeErr(w, http.StatusBadRequest, "bad_request", "yaml 不能为空", nil)
		return
	}
	res := s.validateRaw([]byte(req.YAML))
	writeJSON(w, http.StatusOK, map[string]any{"ok": res.OK, "output": res.Output})
}

func (s *Server) validateRaw(raw []byte) config.ValidationResult {
	res := config.ValidateText(raw)
	if !res.OK {
		return res
	}
	if bin := s.opts.Core.CoreBinary(); bin != "" {
		return config.ValidateWithCore(bin, s.opts.Paths.DataDir, raw)
	}
	return res
}

func (s *Server) handleConfigPut(w http.ResponseWriter, r *http.Request) {
	var req struct {
		YAML  string `json:"yaml"`
		Apply string `json:"apply"`
	}
	if err := decodeJSONBody(r, &req, 16<<20); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error(), nil)
		return
	}
	if strings.TrimSpace(req.YAML) == "" {
		writeErr(w, http.StatusBadRequest, "bad_request", "yaml 不能为空", nil)
		return
	}
	newDoc, err := config.Parse([]byte(req.YAML))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_config", "配置解析失败", err.Error())
		return
	}
	oldDoc, _ := s.opts.Config.Doc()
	preserveMProxySettings(oldDoc, newDoc)
	// 完整 YAML 替换时保留控制器/密钥，否则面板会失联。
	preserveControllerFallback(oldDoc, newDoc)
	output, verr := s.validateDocBytes(newDoc, []byte(req.YAML))
	if verr != nil {
		writeErr(w, http.StatusBadRequest, "invalid_config", "配置校验失败", output)
		return
	}
	if _, err := s.opts.Config.Save(newDoc); err != nil {
		writeErr(w, http.StatusInternalServerError, "save_failed", "保存配置失败", err.Error())
		return
	}
	outcome := s.applyConfig(r.Context(), oldDoc, newDoc, req.Apply)
	resp := s.buildApplyResponse(newDoc, output, outcome)
	writeJSON(w, http.StatusOK, resp)
}

// preserveMProxySettings 在用户编辑 YAML 时保留 x-mproxy 模块设置（若被删除）。
func preserveMProxySettings(oldDoc, newDoc *config.Doc) {
	if oldDoc == nil || newDoc == nil {
		return
	}
	if newDoc.Has("x-mproxy") {
		return
	}
	if x, ok := oldDoc.Get("x-mproxy"); ok {
		newDoc.SetTop("x-mproxy", x)
	}
}

// preserveControllerFallback 在完整配置替换时，若缺少控制器/密钥则沿用旧值。
func preserveControllerFallback(oldDoc, newDoc *config.Doc) {
	if oldDoc == nil || newDoc == nil {
		return
	}
	if !newDoc.Has("external-controller") {
		if v, ok := oldDoc.Get("external-controller"); ok {
			newDoc.SetTop("external-controller", v)
		}
	}
	if !newDoc.Has("secret") {
		if v, ok := oldDoc.Get("secret"); ok {
			newDoc.SetTop("secret", v)
		}
	}
}

func (s *Server) validateDocBytes(doc *config.Doc, raw []byte) (string, error) {
	if err := doc.ValidateStructure(); err != nil {
		return err.Error(), &validationError{Output: err.Error()}
	}
	// tun.enable 与 x-mproxy.proxy-mode 必须一致；这里统一拦截所有写入路径。
	if err := doc.ValidateConsistency(); err != nil {
		return err.Error(), &validationError{Output: err.Error()}
	}
	res := s.validateRaw(raw)
	if !res.OK {
		return res.Output, &validationError{Output: res.Output}
	}
	return res.Output, nil
}

// saveValidatedDoc 在保存前执行与 PUT /api/config 相同的校验（结构 + 一致性 + mihomo -t）。
// 返回校验输出；validationError 表示校验失败，其他错误表示序列化/保存失败。
func (s *Server) saveValidatedDoc(doc *config.Doc) (string, error) {
	raw, err := doc.Marshal()
	if err != nil {
		return "", fmt.Errorf("序列化配置失败: %w", err)
	}
	output, verr := s.validateDocBytes(doc, raw)
	if verr != nil {
		return output, verr
	}
	if _, err := s.opts.Config.Save(doc); err != nil {
		return output, err
	}
	return output, nil
}

// lifecycleContext 返回不随请求取消、但仍带独立超时的生命周期 context。
// 用于内核启停等操作：客户端断开（如 WebView 重载）不能把内核中断在半启动/半停止状态。
func lifecycleContext(parent context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(parent), timeout)
}

func (s *Server) buildApplyResponse(doc *config.Doc, validationOutput string, outcome applyOutcome) applyResponse {
	resp := applyResponse{
		OK:               true,
		Saved:            true,
		Applied:          outcome.Applied,
		Restarted:        outcome.Restarted,
		ValidationOutput: validationOutput,
		Warnings:         outcome.Warnings,
		Message:          outcome.Message,
	}
	if doc != nil {
		resp.Summary = doc.Summary()
	}
	return resp
}

// applyConfig 应用新配置：auto 仅在关键字段变化时重启，否则热重载。
func (s *Server) applyConfig(ctx context.Context, oldDoc, newDoc *config.Doc, mode string) applyOutcome {
	out := applyOutcome{}
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		mode = "auto"
	}
	switch mode {
	case "none":
		out.Message = "配置已保存，未应用到内核"
		return out
	case "auto", "reload", "restart":
	default:
		out.Warnings = append(out.Warnings, "未知的 apply 模式，已按 auto 处理")
		mode = "auto"
	}
	if !s.opts.Core.Running() {
		out.Warnings = append(out.Warnings, "内核未运行，配置已保存，启动内核后生效")
		return out
	}
	doRestart := mode == "restart"
	if mode == "auto" {
		if oldDoc == nil {
			doRestart = true
		} else if changes := config.CriticalChanges(oldDoc, newDoc); len(changes) > 0 {
			doRestart = true
			out.Message = "检测到关键配置变化（" + strings.Join(changes, "、") + "），已重启内核"
		}
	}
	var err error
	if doRestart {
		// 重启是“停止 → 启动”的复合操作，不能随客户端断开而取消。
		restartCtx, cancel := lifecycleContext(ctx, 60*time.Second)
		defer cancel()
		err = s.opts.Core.Restart(restartCtx)
		if err != nil {
			out.Warnings = append(out.Warnings, "重启内核失败: "+err.Error())
			return out
		}
		out.Applied = true
		out.Restarted = true
		out.Method = "restart"
		return out
	}
	err = s.opts.Core.Reload(ctx)
	if err != nil {
		out.Warnings = append(out.Warnings, "热重载失败: "+err.Error())
		return out
	}
	out.Applied = true
	out.Method = "reload"
	return out
}

func (s *Server) handleConfigImport(w http.ResponseWriter, r *http.Request) {
	var req struct {
		URL     string `json:"url"`
		Content string `json:"content"`
		Mode    string `json:"mode"`
		Apply   string `json:"apply"`
	}
	if err := decodeJSONBody(r, &req, 16<<20); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error(), nil)
		return
	}
	var data []byte
	switch {
	case strings.TrimSpace(req.Content) != "":
		data = []byte(req.Content)
	case strings.TrimSpace(req.URL) != "":
		url := strings.TrimSpace(req.URL)
		if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
			writeErr(w, http.StatusBadRequest, "bad_request", "订阅地址必须是 http(s) 链接", nil)
			return
		}
		body, err := s.opts.Download.Get(r.Context(), url, map[string]string{"User-Agent": "clash.meta"}, 16<<20)
		if err != nil {
			writeErr(w, http.StatusBadGateway, "download_failed", "下载订阅失败", err.Error())
			return
		}
		data = body
	default:
		writeErr(w, http.StatusBadRequest, "bad_request", "content 与 url 至少提供一个", nil)
		return
	}
	current, err := s.opts.Config.Doc()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "config_invalid", "当前配置无效，无法导入", err.Error())
		return
	}
	newDoc, imported, warnings, err := config.Import(current, data, req.Mode)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "import_failed", "导入配置失败", err.Error())
		return
	}
	raw, err := newDoc.Marshal()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "import_failed", "序列化导入配置失败", err.Error())
		return
	}
	output, verr := s.validateDocBytes(newDoc, raw)
	if verr != nil {
		var ve *validationError
		msg := "导入后的配置校验失败"
		if errors.As(verr, &ve) {
			msg = "导入后的配置校验失败"
		}
		writeErr(w, http.StatusBadRequest, "invalid_config", msg, output)
		return
	}
	if _, err := s.opts.Config.Save(newDoc); err != nil {
		writeErr(w, http.StatusInternalServerError, "save_failed", "保存配置失败", err.Error())
		return
	}
	outcome := s.applyConfig(r.Context(), current, newDoc, req.Apply)
	resp := s.buildApplyResponse(newDoc, output, outcome)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":               resp.OK,
		"saved":            resp.Saved,
		"applied":          resp.Applied,
		"restarted":        resp.Restarted,
		"imported":         imported,
		"warnings":         append(warnings, outcome.Warnings...),
		"summary":          resp.Summary,
		"validationOutput": resp.ValidationOutput,
	})
}

func (s *Server) handleConfigExport(w http.ResponseWriter, r *http.Request) {
	raw, _, _, err := s.opts.Config.Raw()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "config_invalid", err.Error(), nil)
		return
	}
	w.Header().Set("Content-Type", "application/x-yaml; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="config.yaml"`)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(raw)
}

func (s *Server) handleConfigBackup(w http.ResponseWriter, r *http.Request) {
	data, err := s.opts.Config.Backup()
	if err != nil {
		if os.IsNotExist(err) {
			writeErr(w, http.StatusNotFound, "backup_not_found", "还没有可用的配置备份", nil)
			return
		}
		writeErr(w, http.StatusInternalServerError, "backup_failed", "读取备份失败", err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/x-yaml; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="config.yaml.bak"`)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func (s *Server) handleConfigRestore(w http.ResponseWriter, r *http.Request) {
	var req struct {
		YAML  string `json:"yaml"`
		Apply string `json:"apply"`
	}
	body, err := readBody(r, 16<<20)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error(), nil)
		return
	}
	if len(strings.TrimSpace(string(body))) > 0 {
		if err := decodeJSONBytes(body, &req); err != nil {
			writeErr(w, http.StatusBadRequest, "bad_request", "请求体解析失败", err.Error())
			return
		}
	}
	var raw []byte
	if strings.TrimSpace(req.YAML) != "" {
		raw = []byte(req.YAML)
	} else {
		data, err := s.opts.Config.Backup()
		if err != nil {
			writeErr(w, http.StatusBadRequest, "backup_not_found", "没有可恢复的备份", err.Error())
			return
		}
		raw = data
	}
	newDoc, err := config.Parse(raw)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_config", "备份配置解析失败", err.Error())
		return
	}
	oldDoc, _ := s.opts.Config.Doc()
	output, verr := s.validateDocBytes(newDoc, raw)
	if verr != nil {
		writeErr(w, http.StatusBadRequest, "invalid_config", "备份配置校验失败", output)
		return
	}
	if _, err := s.opts.Config.Save(newDoc); err != nil {
		writeErr(w, http.StatusInternalServerError, "save_failed", "保存配置失败", err.Error())
		return
	}
	outcome := s.applyConfig(r.Context(), oldDoc, newDoc, req.Apply)
	writeJSON(w, http.StatusOK, s.buildApplyResponse(newDoc, output, outcome))
}

func (s *Server) handleConfigReset(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Confirm bool   `json:"confirm"`
		Apply   string `json:"apply"`
	}
	if err := decodeJSONBody(r, &req, 1<<20); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error(), nil)
		return
	}
	if !req.Confirm {
		writeErr(w, http.StatusBadRequest, "confirm_required", "重置配置需要 confirm=true", nil)
		return
	}
	oldDoc, _ := s.opts.Config.Doc()
	newDoc, err := config.NewDefaultWithRandomSecret()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "reset_failed", "生成默认配置失败", err.Error())
		return
	}
	raw, err := newDoc.Marshal()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "reset_failed", "序列化默认配置失败", err.Error())
		return
	}
	output, verr := s.validateDocBytes(newDoc, raw)
	if verr != nil {
		writeErr(w, http.StatusInternalServerError, "invalid_config", "默认配置校验失败", output)
		return
	}
	if _, err := s.opts.Config.Save(newDoc); err != nil {
		writeErr(w, http.StatusInternalServerError, "save_failed", "保存配置失败", err.Error())
		return
	}
	outcome := s.applyConfig(r.Context(), oldDoc, newDoc, req.Apply)
	writeJSON(w, http.StatusOK, s.buildApplyResponse(newDoc, output, outcome))
}
