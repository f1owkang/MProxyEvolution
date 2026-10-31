package sysutil

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// RotatingWriter 是一个按大小轮转的日志写入器：超过 maxBytes 后把当前文件改名为 .1。
type RotatingWriter struct {
	mu       sync.Mutex
	path     string
	maxBytes int64
	file     *os.File
	size     int64
}

// NewRotatingWriter 打开（或创建）日志文件，maxBytes <= 0 时默认 5 MiB。
func NewRotatingWriter(path string, maxBytes int64) (*RotatingWriter, error) {
	if maxBytes <= 0 {
		maxBytes = 5 << 20
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	w := &RotatingWriter{path: path, maxBytes: maxBytes}
	if err := w.open(); err != nil {
		return nil, err
	}
	return w, nil
}

func (w *RotatingWriter) open() error {
	f, err := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	st, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return err
	}
	w.file = f
	w.size = st.Size()
	return nil
}

// Write 实现 io.Writer，写入前检查是否需要轮转。
func (w *RotatingWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		return 0, fmt.Errorf("日志文件未打开: %s", w.path)
	}
	if w.size+int64(len(p)) > w.maxBytes {
		w.rotateLocked()
	}
	n, err := w.file.Write(p)
	w.size += int64(n)
	return n, err
}

func (w *RotatingWriter) rotateLocked() {
	_ = w.file.Close()
	// 只保留一份历史日志：core.log -> core.log.1
	_ = os.Remove(w.path + ".1")
	if err := os.Rename(w.path, w.path+".1"); err != nil && !os.IsNotExist(err) {
		// 改名失败时继续截断写，避免日志功能中断
		_ = os.Remove(w.path)
	}
	_ = w.open()
}

// Close 关闭底层文件。
func (w *RotatingWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		return nil
	}
	err := w.file.Close()
	w.file = nil
	return err
}

// TailFile 读取文件末尾 n 行；文件不存在时返回空切片。
func TailFile(path string, n int) ([]string, error) {
	if n <= 0 {
		n = 300
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return []string{}, nil
		}
		return nil, err
	}
	return tailLines(string(data), n), nil
}

func tailLines(s string, n int) []string {
	if s == "" {
		return []string{}
	}
	// 统一换行并去掉末尾空行
	lines := splitLines(s)
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines
}

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			line := s[start:i]
			if len(line) > 0 && line[len(line)-1] == '\r' {
				line = line[:len(line)-1]
			}
			out = append(out, line)
			start = i + 1
		}
	}
	out = append(out, s[start:])
	return out
}

// TruncateFile 清空文件内容（不存在时创建）。
func TruncateFile(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, nil, 0o644)
}

// ReadFileString 读取文本文件，404 时返回空串。
func ReadFileString(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	return string(b), nil
}
