package sysutil

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// LastExitInfo 记录上次内核退出信息，持久化在 state/last_exit，供诊断接口与面板提示。
type LastExitInfo struct {
	Time        time.Time
	PID         int
	Err         string
	Intentional bool
}

// WriteLastExit 原子写入上次退出信息（单行 url query 形式，便于解析与日志展示）。
func WriteLastExit(path string, info LastExitInfo) error {
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("创建状态目录失败: %w", err)
		}
	}
	values := url.Values{}
	values.Set("time", info.Time.Format(time.RFC3339))
	values.Set("pid", strconv.Itoa(info.PID))
	values.Set("err", info.Err)
	values.Set("intentional", strconv.FormatBool(info.Intentional))
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(values.Encode()+"\n"), 0o600); err != nil {
		return fmt.Errorf("写入上次退出状态失败: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("保存上次退出状态失败: %w", err)
	}
	return nil
}

// ReadLastExit 读取上次退出信息；文件不存在时返回的错误满足 os.IsNotExist。
func ReadLastExit(path string) (LastExitInfo, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return LastExitInfo{}, err
	}
	values, err := url.ParseQuery(strings.TrimSpace(string(b)))
	if err != nil {
		return LastExitInfo{}, fmt.Errorf("解析上次退出状态失败: %w", err)
	}
	info := LastExitInfo{Err: values.Get("err")}
	if ts := values.Get("time"); ts != "" {
		t, terr := time.Parse(time.RFC3339, ts)
		if terr != nil {
			return LastExitInfo{}, fmt.Errorf("解析上次退出时间失败: %w", terr)
		}
		info.Time = t
	}
	if pid := values.Get("pid"); pid != "" {
		if v, perr := strconv.Atoi(pid); perr == nil {
			info.PID = v
		}
	}
	info.Intentional = values.Get("intentional") == "true"
	return info, nil
}

// RemoveLastExit 删除上次退出状态文件（成功启动后清理，忽略不存在）。
func RemoveLastExit(path string) {
	_ = os.Remove(path)
}

// Summary 返回适合诊断/状态展示的单行摘要。
func (i LastExitInfo) Summary() string {
	if i.Time.IsZero() {
		return ""
	}
	s := i.Time.Format(time.RFC3339) + fmt.Sprintf(" pid=%d", i.PID)
	if i.Err != "" {
		s += " err=" + i.Err
	} else {
		s += " err=正常退出"
	}
	if i.Intentional {
		s += "（主动停止）"
	}
	return s
}
