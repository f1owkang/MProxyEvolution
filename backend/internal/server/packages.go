package server

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"mproxy/internal/sysutil"
)

// listPackages 枚举 Android 应用包；非 Android 平台返回空列表。
func listPackages(ctx context.Context, typ string) ([]PackageInfo, error) {
	if !sysutil.IsAndroid() {
		return []PackageInfo{}, nil
	}
	all, err := runPM(ctx, "list", "packages", "-U")
	if err != nil {
		return nil, fmt.Errorf("读取应用列表失败（pm 命令不可用）: %w", err)
	}
	systemSet := map[string]bool{}
	if typ != "system" {
		// 仅在需要区分用户/系统应用时查询系统包
		if out, err := runPM(ctx, "list", "packages", "-s"); err == nil {
			for _, line := range strings.Split(out, "\n") {
				if name, ok := parsePackageLine(line); ok {
					systemSet[name] = true
				}
			}
		}
	} else if out, err := runPM(ctx, "list", "packages", "-s"); err == nil {
		for _, line := range strings.Split(out, "\n") {
			if name, ok := parsePackageLine(line); ok {
				systemSet[name] = true
			}
		}
	}
	var out []PackageInfo
	for _, line := range strings.Split(all, "\n") {
		name, ok := parsePackageLine(line)
		if !ok {
			continue
		}
		uid := parseUIDLine(line)
		isSystem := systemSet[name]
		switch typ {
		case "system":
			if !isSystem {
				continue
			}
		case "user":
			if isSystem {
				continue
			}
		}
		out = append(out, PackageInfo{PackageName: name, UID: uid, System: isSystem})
	}
	return out, nil
}

func runPM(ctx context.Context, args ...string) (string, error) {
	cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	pm := "pm"
	if _, err := os.Stat("/system/bin/pm"); err == nil {
		pm = "/system/bin/pm"
	}
	cmd := exec.CommandContext(cctx, pm, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("%s", msg)
	}
	return stdout.String(), nil
}

func parsePackageLine(line string) (string, bool) {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "package:") {
		return "", false
	}
	rest := strings.TrimPrefix(line, "package:")
	name := rest
	if idx := strings.IndexAny(rest, " \t"); idx >= 0 {
		name = rest[:idx]
	}
	name = strings.TrimSpace(name)
	return name, name != ""
}

func parseUIDLine(line string) int {
	sc := bufio.NewScanner(strings.NewReader(line))
	sc.Split(bufio.ScanWords)
	for sc.Scan() {
		word := sc.Text()
		if strings.HasPrefix(word, "uid:") {
			if v, err := strconv.Atoi(strings.TrimPrefix(word, "uid:")); err == nil {
				return v
			}
		}
	}
	return 0
}
