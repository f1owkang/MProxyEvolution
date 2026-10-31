package sysutil

import (
	"os"
	"runtime"
	"sync"
)

var (
	androidOnce sync.Once
	androidFlag bool
)

// IsAndroid 判断当前是否运行在 Android 用户空间。
//
// 后端为了保持 CGO_ENABLED=0 可构建，交叉编译目标可能是 GOOS=linux；
// 此时 runtime.GOOS 为 "linux"，不能再用它判断 Android。这里通过 Android
// 特有的路径探测（getprop / /data/adb）来判断，确保数据目录、DNS、应用列表、
// 内核资产选择等逻辑在 Android 上仍然正确。
func IsAndroid() bool {
	androidOnce.Do(func() {
		if runtime.GOOS == "android" {
			androidFlag = true
			return
		}
		for _, p := range []string{"/system/bin/getprop", "/system/bin/app_process", "/data/adb/ksu", "/data/adb/magisk"} {
			if _, err := os.Stat(p); err == nil {
				androidFlag = true
				return
			}
		}
	})
	return androidFlag
}

// Platform 返回面向 mihomo 发布资产的平台名（Android 固定为 android）。
func Platform() string {
	if IsAndroid() {
		return "android"
	}
	return runtime.GOOS
}

// GetPropPath 返回可用的 getprop 可执行文件路径。
func GetPropPath() string {
	for _, p := range []string{"/system/bin/getprop", "getprop"} {
		if p == "getprop" {
			return p
		}
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return "getprop"
}
