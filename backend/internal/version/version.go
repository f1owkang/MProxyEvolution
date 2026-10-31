// Package version 保存后端版本号，可通过 -ldflags "-X mproxy/internal/version.Version=..." 注入。
package version

// Version 是 MProxyEvolution 后端版本号（默认 0.1.0，构建时可用 ldflags 覆盖）。
var Version = "0.1.0"
