// Package webui 内嵌 zashboard 构建产物（placeholder 保证无 panel 构建时也能编译）。
package webui

import (
	"embed"
	"io/fs"
)

//go:embed dist
var embedded embed.FS

// Files 返回 dist 目录的文件系统。
func Files() fs.FS {
	sub, err := fs.Sub(embedded, "dist")
	if err != nil {
		return embedded
	}
	return sub
}
