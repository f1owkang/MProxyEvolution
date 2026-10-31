package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// apiError 是统一错误响应体中的 error 对象。
type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Details any    `json:"details,omitempty"`
}

type errorEnvelope struct {
	Error apiError `json:"error"`
}

// writeJSON 写出 JSON 响应。
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if v == nil {
		return
	}
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

// writeErr 写出统一格式的错误响应。
func writeErr(w http.ResponseWriter, status int, code, message string, details any) {
	writeJSON(w, status, errorEnvelope{Error: apiError{Code: code, Message: message, Details: details}})
}

// readBody 读取请求体并限制大小。
func readBody(r *http.Request, maxBytes int64) ([]byte, error) {
	if maxBytes <= 0 {
		maxBytes = 8 << 20
	}
	defer r.Body.Close()
	data, err := io.ReadAll(io.LimitReader(r.Body, maxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("读取请求体失败: %w", err)
	}
	if int64(len(data)) > maxBytes {
		return nil, fmt.Errorf("请求体超过 %d 字节上限", maxBytes)
	}
	return data, nil
}

// decodeJSONBody 解码 JSON 请求体（数字保留为整数，便于写回 YAML）。
func decodeJSONBody(r *http.Request, dst any, maxBytes int64) error {
	data, err := readBody(r, maxBytes)
	if err != nil {
		return err
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return fmt.Errorf("请求体不能为空")
	}
	return decodeJSONBytes(data, dst)
}

// decodeJSONBytes 解码 JSON 字节（UseNumber），并拒绝第一个 JSON 值之后的任何多余内容。
func decodeJSONBytes(data []byte, dst any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if err := dec.Decode(dst); err != nil {
		return fmt.Errorf("请求体不是合法 JSON: %w", err)
	}
	// 第一个值之后必须恰好是 EOF（允许尾随空白）；否则说明请求体包含多余内容。
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return fmt.Errorf("请求体包含多余内容：JSON 值之后不应再有其他数据")
	}
	return nil
}

// normalizeJSON 把 json.Number 转成 int64/float64，便于 yaml 序列化。
func normalizeJSON(v any) any {
	switch t := v.(type) {
	case json.Number:
		if i, err := t.Int64(); err == nil {
			return i
		}
		if f, err := t.Float64(); err == nil {
			return f
		}
		return t.String()
	case map[string]any:
		for k, val := range t {
			t[k] = normalizeJSON(val)
		}
		return t
	case []any:
		for i, val := range t {
			t[i] = normalizeJSON(val)
		}
		return t
	}
	return v
}
