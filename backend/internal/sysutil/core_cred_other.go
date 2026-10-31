//go:build !linux

package sysutil

import "os/exec"

// ApplyCoreNetAdmin 在非 Linux 平台为空操作（桌面开发不涉及 TUN/策略路由）。
func ApplyCoreNetAdmin(_ *exec.Cmd) {}
