package server

import (
	"net/http"
	"os"
	"strings"
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
	system := sysutil.CollectSystemInfo()
	resp := diagnosticsResponse{
		GeneratedAt: time.Now().Format(time.RFC3339),
		Version:     version.Version,
		DataDir:     s.opts.Paths.DataDir,
		ModuleDir:   s.opts.Paths.ModuleDir,
		Core:        st,
		System:      system,
		LogTail:     s.opts.Core.LogTail(50),
	}
	tunEnabled := false
	tunDevice := sysutil.DefaultTUNDeviceName
	if doc, err := s.opts.Config.Doc(); err == nil {
		resp.Summary = doc.Summary()
		tunEnabled = doc.GetBool(false, "tun", "enable")
		if v := strings.TrimSpace(doc.GetString("", "tun", "device")); v != "" {
			tunDevice = v
		}
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
		// 以下为运行可靠性诊断（新增，保持 JSON 结构兼容）。
		managerCheck(system.Manager),
		privateDNSCheck(tunEnabled),
		tunDeviceCheck(tunEnabled, tunDevice, st.Running),
		lastExitCheck(s.opts.Paths.LastExitPath()),
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

// managerCheck 报告当前 root 管理器（KernelSU / APatch / Magisk）。
// 非 Android 开发环境未识别属正常，不报红；Android 上未识别才视为异常。
func managerCheck(manager string) diagnosticCheck {
	detail := ""
	switch manager {
	case "kernelsu":
		detail = "KernelSU（/data/adb/ksu）"
	case "apatch":
		detail = "APatch（/data/adb/apd）"
	case "magisk":
		detail = "Magisk（/data/adb/magisk）"
	default:
		if !sysutil.IsAndroid() {
			return diagnosticCheck{Name: "管理器框架", OK: true, Detail: "非 Android，跳过"}
		}
		detail = "未识别（未知管理器）"
	}
	return diagnosticCheck{Name: "管理器框架", OK: manager != "unknown", Detail: detail}
}

// privateDNSCheck 报告系统 Private DNS 状态：仅在 TUN 启用时，
// 开启的 Private DNS 才会造成 DoT 绕行；未启用 TUN 时只展示状态，不报红。
func privateDNSCheck(tunEnabled bool) diagnosticCheck {
	if !sysutil.IsAndroid() {
		return diagnosticCheck{Name: "Private DNS", OK: true, Detail: "非 Android，跳过"}
	}
	mode := sysutil.PrivateDNSMode()
	if mode == "" {
		return diagnosticCheck{Name: "Private DNS", OK: true, Detail: "未启用或无法读取"}
	}
	if strings.EqualFold(mode, "off") {
		return diagnosticCheck{Name: "Private DNS", OK: true, Detail: "已关闭"}
	}
	if !tunEnabled {
		return diagnosticCheck{Name: "Private DNS", OK: true, Detail: "当前为 " + mode + "（未启用 TUN，不处理）"}
	}
	return diagnosticCheck{Name: "Private DNS", OK: false, Detail: "当前为 " + mode + "（TUN 运行时会被临时关闭）"}
}

// tunDeviceCheck 检查配置的 TUN 设备是否残留：内核未运行但设备仍在，通常是强杀残留。
// 只有 mihomo 默认设备名会被后端自动清理；自定义设备名需用户手动处理。
func tunDeviceCheck(enabled bool, device string, coreRunning bool) diagnosticCheck {
	if !enabled {
		return diagnosticCheck{Name: "TUN 设备", OK: true, Detail: "未启用 TUN"}
	}
	device = strings.TrimSpace(device)
	if device == "" {
		device = sysutil.DefaultTUNDeviceName
	}
	if !sysutil.IsAndroid() {
		return diagnosticCheck{Name: "TUN 设备", OK: true, Detail: device + "（非 Android 不检测）"}
	}
	exists := sysutil.TUNDeviceExists(device)
	if !exists {
		return diagnosticCheck{Name: "TUN 设备", OK: true, Detail: device + " 不存在"}
	}
	if coreRunning {
		return diagnosticCheck{Name: "TUN 设备", OK: true, Detail: device + " 存在（内核运行中）"}
	}
	if device == sysutil.DefaultTUNDeviceName {
		return diagnosticCheck{Name: "TUN 设备", OK: false, Detail: device + " 存在但内核未运行，疑似上次异常退出残留（下次启动将自动清理）"}
	}
	return diagnosticCheck{Name: "TUN 设备", OK: false, Detail: device + " 存在但内核未运行，疑似上次异常退出残留；自定义设备名需手动清理"}
}

// lastExitCheck 读取 state/last_exit，报告上次内核退出是否为异常退出。
func lastExitCheck(path string) diagnosticCheck {
	info, err := sysutil.ReadLastExit(path)
	if err != nil {
		if os.IsNotExist(err) {
			return diagnosticCheck{Name: "上次内核退出", OK: true, Detail: "无记录"}
		}
		return diagnosticCheck{Name: "上次内核退出", OK: false, Detail: "读取失败: " + err.Error()}
	}
	detail := info.Summary()
	if info.Intentional || info.Err == "" {
		return diagnosticCheck{Name: "上次内核退出", OK: true, Detail: detail}
	}
	return diagnosticCheck{Name: "上次内核退出", OK: false, Detail: detail}
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
