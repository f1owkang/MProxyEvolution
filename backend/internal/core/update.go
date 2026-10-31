package core

import (
	"archive/zip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"mproxy/internal/download"
	"mproxy/internal/job"
	"mproxy/internal/sysutil"
)

// 上游内核信息。
const (
	ReleaseRepo = "MetaCubeX/mihomo"
	releasesAPI = "https://api.github.com/repos/" + ReleaseRepo + "/releases?per_page=40"
)

// Asset 是 GitHub release 中的一个文件。
type Asset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Size               int64  `json:"size"`
}

// Release 是 GitHub release 摘要。
type Release struct {
	TagName    string  `json:"tag_name"`
	Prerelease bool    `json:"prerelease"`
	Draft      bool    `json:"draft"`
	Assets     []Asset `json:"assets"`
}

// UpdateCheck 是更新检查结果。
type UpdateCheck struct {
	Current   string `json:"current"`
	Latest    string `json:"latest"`
	Available bool   `json:"available"`
	Channel   string `json:"channel"`
	ABI       string `json:"abi"`
}

// InstallResult 是内核安装结果。
type InstallResult struct {
	Version string `json:"version"`
	Path    string `json:"path"`
	Asset   string `json:"asset"`
}

// FetchReleases 拉取 mihomo release 列表。
func FetchReleases(ctx context.Context, client *download.Client) ([]Release, error) {
	body, err := client.GetJSON(ctx, releasesAPI)
	if err != nil {
		return nil, fmt.Errorf("检查内核更新失败（GitHub API 请求失败，可能触发速率限制）: %w", err)
	}
	var releases []Release
	if err := json.Unmarshal(body, &releases); err != nil {
		return nil, fmt.Errorf("解析内核发布信息失败: %w", err)
	}
	if len(releases) == 0 {
		return nil, fmt.Errorf("上游没有可用的内核发布")
	}
	return releases, nil
}

// SelectRelease 按更新通道选择 release：stable 只用正式版，alpha 用最新（含预发布）。
func SelectRelease(releases []Release, channel string) (Release, error) {
	for _, r := range releases {
		if r.Draft {
			continue
		}
		if channel == configChannelAlpha {
			return r, nil
		}
		if !r.Prerelease {
			return r, nil
		}
	}
	return Release{}, fmt.Errorf("上游没有符合 %s 通道的内核版本", channel)
}

const configChannelAlpha = "alpha"

// AssetMatchers 返回当前平台在 release 资产名中的匹配片段（按优先级）。
func AssetMatchers(goos, goarch, goarm string) []string {
	switch goos {
	case "android":
		switch goarch {
		case "arm64":
			return []string{"-android-arm64-v8-", "-android-arm64-"}
		case "arm":
			return []string{"-android-armv7-", "-android-arm-"}
		case "386":
			return []string{"-android-386-"}
		case "amd64":
			return []string{"-android-amd64-"}
		}
	case "windows":
		switch goarch {
		case "amd64":
			return []string{"-windows-amd64-v", "-windows-amd64-"}
		case "arm64":
			return []string{"-windows-arm64-"}
		case "386":
			return []string{"-windows-386-"}
		}
	case "linux":
		switch goarch {
		case "amd64":
			return []string{"-linux-amd64-"}
		case "arm64":
			return []string{"-linux-arm64-"}
		case "arm":
			return []string{"-linux-armv7-", "-linux-arm-"}
		case "386":
			return []string{"-linux-386-"}
		}
	case "darwin":
		switch goarch {
		case "amd64":
			return []string{"-darwin-amd64-"}
		case "arm64":
			return []string{"-darwin-arm64-"}
		}
	}
	return nil
}

// PickAsset 从 release 资产中选出适合当前平台的内核文件。
func PickAsset(assets []Asset, goos, goarch, goarm string) (Asset, error) {
	matchers := AssetMatchers(goos, goarch, goarm)
	if len(matchers) == 0 {
		return Asset{}, fmt.Errorf("暂不支持当前平台 %s/%s 的自动安装", goos, goarch)
	}
	best := Asset{}
	bestScore := -1 << 30
	for _, a := range assets {
		name := a.Name
		matched := -1
		for i, m := range matchers {
			if strings.Contains(name, m) {
				matched = i
				break
			}
		}
		if matched < 0 {
			continue
		}
		score := 1000 - matched*50
		if strings.HasSuffix(name, ".gz") || strings.HasSuffix(name, ".zip") {
			score += 20
		}
		if strings.Contains(name, "compatible") {
			score -= 100
		}
		if strings.Contains(name, "-go1") {
			// 针对特定 Go 版本构建的变体，优先使用通用发行版
			score -= 200
		}
		if score > bestScore || (score == bestScore && len(name) < len(best.Name)) {
			bestScore = score
			best = a
		}
	}
	if best.Name == "" {
		return Asset{}, fmt.Errorf("上游发布中没有找到适用于 %s/%s 的内核文件", goos, goarch)
	}
	return best, nil
}

// CheckUpdate 检查是否有新版本。
func (m *Manager) CheckUpdate(ctx context.Context, client *download.Client, channel, ghproxy string) (UpdateCheck, error) {
	if channel == "" {
		channel = "stable"
	}
	res := UpdateCheck{Channel: channel, ABI: sysutil.ABI(), Current: m.Version()}
	releases, err := FetchReleases(ctx, client)
	if err != nil {
		return res, err
	}
	release, err := SelectRelease(releases, channel)
	if err != nil {
		return res, err
	}
	res.Latest = release.TagName
	res.Available = res.Current == "" || res.Current != res.Latest
	return res, nil
}

// WithGhproxy 给下载地址加 ghproxy 前缀。
func WithGhproxy(ghproxy, rawURL string) string {
	ghproxy = strings.TrimSpace(ghproxy)
	if ghproxy == "" {
		return rawURL
	}
	if !strings.HasSuffix(ghproxy, "/") {
		ghproxy += "/"
	}
	return ghproxy + rawURL
}

// Update 执行“检查 → 下载 → 安装 → 重启”的完整更新流程，写入 job 进度。
func (m *Manager) Update(ctx context.Context, client *download.Client, channel, ghproxy string, j *job.Job) (InstallResult, error) {
	result := InstallResult{Path: m.paths.InstallCorePath()}
	if channel == "" {
		channel = "stable"
	}
	if j != nil {
		j.Update(2, "正在检查最新内核版本…")
	}
	releases, err := FetchReleases(ctx, client)
	if err != nil {
		return result, err
	}
	release, err := SelectRelease(releases, channel)
	if err != nil {
		return result, err
	}
	goarm := os.Getenv("GOARM")
	if runtime.GOARCH == "arm" && goarm == "" {
		goarm = "7"
	}
	asset, err := PickAsset(release.Assets, sysutil.Platform(), runtime.GOARCH, goarm)
	if err != nil {
		return result, err
	}
	result.Version = release.TagName
	result.Asset = asset.Name
	if j != nil {
		j.Update(5, "正在下载内核 "+asset.Name+" …")
	}
	wasRunning := m.Running()
	if wasRunning {
		if err := m.Stop(); err != nil {
			return result, fmt.Errorf("更新前停止内核失败: %w", err)
		}
	}
	restarted := false
	defer func() {
		if wasRunning && !restarted {
			// 更新失败时尽量恢复旧内核
			if startErr := m.Start(context.Background()); startErr != nil {
				m.logger.Printf("更新失败后恢复内核启动失败: %v", startErr)
			}
		}
	}()

	if err := m.paths.EnsureDirs(); err != nil {
		return result, err
	}
	downloadURL := WithGhproxy(ghproxy, asset.BrowserDownloadURL)
	tmpDownload := filepath.Join(m.paths.BinDir(), "mihomo.download")
	defer os.Remove(tmpDownload)
	_, err = client.Download(ctx, downloadURL, tmpDownload, nil, func(done, total int64) {
		if j == nil {
			return
		}
		progress := 5
		if total > 0 {
			progress = 5 + int(done*65/total)
		}
		j.Update(progress, "正在下载内核…")
	})
	if err != nil {
		return result, fmt.Errorf("下载内核失败: %w", err)
	}
	if j != nil {
		j.Update(72, "正在安装内核…")
	}
	target := m.paths.InstallCorePath()
	newPath := sysutil.InstallCandidatePath(target)
	defer os.Remove(newPath)
	if err := m.installNewBinary(tmpDownload, newPath); err != nil {
		return result, fmt.Errorf("安装新内核失败: %w", err)
	}
	// 先校验候选内核：能执行并解析出版本号才允许替换正式二进制，
	// 避免把不完整/架构不匹配的文件安装到 bin/mihomo。
	if j != nil {
		j.Update(78, "正在校验新内核…")
	}
	newVer, verr := probeVersionAt(newPath)
	if verr != nil {
		return result, fmt.Errorf("新内核校验失败（下载的文件可能不完整或架构不匹配）: %w", verr)
	}
	if rel := strings.TrimPrefix(strings.TrimSpace(release.TagName), "v"); rel != "" &&
		strings.TrimPrefix(newVer, "v") != rel {
		m.logger.Printf("警告: 新内核版本 %s 与发布标签 %s 不一致", newVer, release.TagName)
	}
	// 原子替换：旧内核先改名为 .old，再用 .new 覆盖正式路径；失败时自动恢复旧内核。
	if j != nil {
		j.Update(85, "正在替换内核…")
	}
	oldPath, ierr := installBinary(target, newPath)
	if ierr != nil {
		return result, ierr
	}
	// 替换后再探测正式路径（实际被执行的正是这个文件），失败则回滚。
	installedVer, perr := probeVersionAt(target)
	if perr != nil {
		if rerr := rollbackBinary(target, oldPath); rerr != nil {
			return result, fmt.Errorf("内核校验失败: %w；回滚旧内核也失败: %v", perr, rerr)
		}
		return result, fmt.Errorf("内核校验失败，已恢复旧内核: %w", perr)
	}
	_ = commitBinary(oldPath)
	_ = os.Chmod(target, 0o755)
	m.mu.Lock()
	m.versionCache = installedVer
	m.mu.Unlock()
	if installedVer != "" {
		result.Version = installedVer
	}
	if wasRunning {
		if j != nil {
			j.Update(95, "正在启动内核…")
		}
		if err := m.Start(ctx); err != nil {
			return result, fmt.Errorf("内核已更新，但自动启动失败: %w", err)
		}
		restarted = true
	}
	if j != nil {
		j.Update(100, "内核已更新到 "+result.Version)
	}
	m.logger.Printf("内核已更新: %s（%s）", result.Version, result.Asset)
	return result, nil
}

// installNewBinary 把下载文件解压/解压为候选内核路径 dst（<target>.new），
// 不触碰正在使用的正式二进制；普通裸二进制直接写入。
func (m *Manager) installNewBinary(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return fmt.Errorf("读取下载文件失败: %w", err)
	}
	_ = os.Remove(dst)
	switch {
	case download.IsGzip(data):
		if err := download.GunzipFile(src, dst); err != nil {
			return fmt.Errorf("解压内核失败: %w", err)
		}
	case isZip(data):
		if err := extractZipBinary(src, dst); err != nil {
			return err
		}
	default:
		if err := os.WriteFile(dst, data, 0o755); err != nil {
			return fmt.Errorf("写入内核候选文件失败: %w", err)
		}
	}
	if err := os.Chmod(dst, 0o755); err != nil {
		_ = os.Remove(dst)
		return fmt.Errorf("设置内核可执行权限失败: %w", err)
	}
	st, err := os.Stat(dst)
	if err != nil {
		return fmt.Errorf("候选内核文件不可用: %w", err)
	}
	if st.Size() == 0 {
		_ = os.Remove(dst)
		return fmt.Errorf("候选内核文件为空")
	}
	return nil
}

func isZip(data []byte) bool {
	return len(data) >= 4 && data[0] == 'P' && data[1] == 'K' && (data[2] == 3 || data[2] == 5) && (data[3] == 4 || data[3] == 6)
}

func extractZipBinary(src, dst string) error {
	zr, err := zip.OpenReader(src)
	if err != nil {
		return fmt.Errorf("解压内核压缩包失败: %w", err)
	}
	defer zr.Close()
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		name := strings.ToLower(f.Name)
		if !strings.HasSuffix(name, ".exe") && !strings.HasSuffix(name, "mihomo") {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return fmt.Errorf("读取压缩包内容失败: %w", err)
		}
		tmp := dst + ".tmp"
		out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
		if err != nil {
			_ = rc.Close()
			return err
		}
		if _, err := io.Copy(out, rc); err != nil {
			_ = out.Close()
			_ = rc.Close()
			_ = os.Remove(tmp)
			return fmt.Errorf("解压内核失败: %w", err)
		}
		_ = out.Close()
		_ = rc.Close()
		if err := os.Rename(tmp, dst); err != nil {
			_ = os.Remove(tmp)
			return err
		}
		return nil
	}
	return fmt.Errorf("压缩包内没有找到 mihomo 可执行文件")
}

// probeVersionAt 运行指定二进制的 `-v` 并解析版本号（必须是 vX.Y.Z 形式的 semver）。
func probeVersionAt(binary string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, binary, "-v").CombinedOutput()
	if err != nil && len(out) == 0 {
		return "", fmt.Errorf("执行 %s -v 失败: %w", binary, err)
	}
	v := parseVersion(string(out))
	if v == "" {
		return "", fmt.Errorf("无法识别内核版本输出: %s", strings.TrimSpace(string(out)))
	}
	return v, nil
}

// installBinary 把 newPath 安装到 target：旧内核先改名为 <target>.old，
// 再用 rename 原子替换正式路径。替换失败时自动恢复旧内核。
// 返回旧内核备份路径（没有旧内核时为空）；该函数只做文件操作，便于测试。
func installBinary(target, newPath string) (string, error) {
	oldPath := target + ".old"
	if err := os.Remove(oldPath); err != nil && !os.IsNotExist(err) {
		return "", fmt.Errorf("清理旧内核备份失败: %w", err)
	}
	hadOld := false
	if _, err := os.Stat(target); err == nil {
		if err := os.Rename(target, oldPath); err != nil {
			return "", fmt.Errorf("备份旧内核失败: %w", err)
		}
		hadOld = true
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("检查旧内核失败: %w", err)
	}
	if err := os.Rename(newPath, target); err != nil {
		if hadOld {
			if rerr := os.Rename(oldPath, target); rerr != nil {
				return "", fmt.Errorf("替换内核失败: %w；恢复旧内核失败: %v", err, rerr)
			}
		}
		return "", fmt.Errorf("替换内核失败: %w", err)
	}
	if hadOld {
		return oldPath, nil
	}
	return "", nil
}

// rollbackBinary 删除校验失败的新内核并恢复旧内核备份。
func rollbackBinary(target, oldPath string) error {
	if err := os.Remove(target); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("删除损坏的内核失败: %w", err)
	}
	if oldPath == "" {
		return nil
	}
	if err := os.Rename(oldPath, target); err != nil {
		return fmt.Errorf("恢复旧内核失败: %w", err)
	}
	return nil
}

// commitBinary 删除旧内核备份（成功安装后的清理）。
func commitBinary(oldPath string) error {
	if oldPath == "" {
		return nil
	}
	if err := os.Remove(oldPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("删除旧内核备份失败: %w", err)
	}
	return nil
}
