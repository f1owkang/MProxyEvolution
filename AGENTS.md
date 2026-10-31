# AGENTS.md

本文件面向在本仓库工作的编码代理：只写「不知道就会写错」的约束，命令与用户文档不重复。
面向用户的介绍、安装与排障在 [README.md](README.md) / [README_EN.md](README_EN.md)。

> **未经用户明确要求，不执行 `git commit`、`git tag`、`git push` 或任何发布动作。**
> 只做被要求的修改；用户未提交的工作区改动不要覆盖、不要回退。

## 命名规范

整个项目有且仅有两种叫法，其他写法一律视为错误：

- **`MihomoProxy Evolution`**：完整展示名 → `module.prop`/`module.yaml` 的 `name`、README 标题、
  面板 UI 文案（i18n 值、欢迎语、错误提示、设置说明、导航标签、boot 页 label）、`webroot` 引导页
  title/h1/footer、Release 标题。
- **`MProxyEvolution`**：短名/技术名 → `module.prop` 的 `id`、日志与 CLI 提示、脚本输出、代码与配置
  注释、`THIRD_PARTY_NOTICES.txt` 标题、CI artifact 名、Issue 模板、文档正文简称。

不要出现裸 `MProxy` 或裸 `MihomoProxy`。机器标识不属于「叫法」，保持不变：二进制 `mproxy`、
`/data/adb/mproxy/**`、`MPROXY_*` 环境变量、`X-MProxy-Token` 头、`/api/mproxy/*`、Go module `mproxy`、
TS 标识符（`MProxyStatus` 等）、`mproxy-boot.js` / `mproxy.patch` 文件名。

## 项目概览

MProxyEvolution 是 KernelSU / Magisk 网络代理模块：mihomo 负责代理、zashboard 负责面板，Go 后端把
两者串成只监听本机的同源服务。模块元数据在 `module/module.prop`（`version=0.1.0`、`versionCode=1`）。
唯一配置 `/data/adb/mproxy/config.yaml`（标准 mihomo YAML + 后端使用的 `x-mproxy:` 段），面板设置
由后端写回同一文件。四架构 `arm64-v8a` / `armeabi-v7a` / `x86` / `x86_64`，Go 后端与 mihomo 内核
都以 `CGO_ENABLED=0 GOOS=linux` 交叉编译，`customize.sh` 安装时只保留当前设备 ABI。

| 路径 | 职责 |
| :-- | :-- |
| `upstream/mihomo/` | git submodule：mihomo 源码，只允许固定 release tag（当前 `v1.19.33`） |
| `panel` 分支 / `panel.lock` | zashboard fork：完整上游历史 + MProxyEvolution commit；`panel.lock` 固定构建 commit |
| `overlay/zashboard/` | 迁移前历史参考，构建不读取；确认 fork 推送后可删除 |
| `backend/` | Go 后端：`server`（HTTP/鉴权/反代/静态）→ `config`（YAML/校验/导入/设置）→ `core`（进程/更新）→ `sysutil`（路径/进程） |
| `module/` | 安装/服务脚本、`module.prop`、bootstrap 页 `webroot/index.html` |
| `scripts/` | `dev-panel.*` 检出 panel commit；`build.*` 构建打包 |
| `.github/workflows/` | `ci.yml`、`release.yml`、`sync-upstream.yml` |

运行链路：KernelSU WebView → `module/webroot/index.html` → `mproxy boot` →
`http://127.0.0.1:9091/?token=...#/settings?section=moduleSettings` → Go 后端（内嵌面板，`/api/mihomo/*`
反代 controller、`/api/config` 读写唯一配置、`/api/core/*` 管内核、`/api/mproxy/*` 页面接口）→
`mproxy serve` 子进程：`mihomo -d /data/adb/mproxy -f config.yaml`。

## 构建与打包

- Go 1.26+（以 `backend/go.mod` 为准）、Node.js 22、pnpm 11.20.0、Git（submodule）；首次拉源码
  `git submodule update --init --recursive`。
- `bash scripts/build.sh [--version 0.2.0] [--split] [--skip-panel] [--skip-backend] [--skip-core] [--skip-zip] [--clean]`；
  Windows 用 `pwsh -File scripts/build.ps1 -Version 0.2.0 -Split ...`。两条路径参数、行为、产物必须一致，
  改一个必须同步另一个。
- 产物：`dist/MProxyEvolution-<versionCode>-<version>.zip` + 同名 `.sha256`（universal，四架构，始终产出，
  约 130 MiB，是 `update.json` 的自动更新目标）；`--split`/`-Split` 额外产出 4 个单架构包 + `.sha256`：
  `-arm64-v8a`（bin/arm64）、`-armeabi-v7a`（bin/arm）、`-x86`（bin/386）、`-x86_64`（bin/amd64），
  每个只含对应 ABI 的后端与内核（约 32-36 MiB，均低于 KernelSU Modules 100MB 上限）。`versionCode` 取自
  `module/module.prop`；`module.yaml` 的 `release.assetPattern` 保持 `MProxyEvolution-*.zip`，
  `update.json` 的 `zipUrl` 仍指向 universal。
- 关键行为（改动构建脚本必须保持）：
  - 构建把版本写入 zip 内 `module-root/module.prop` 的 `version`（源文件保持 `0.1.0`）；写 `module.prop`
    必须 UTF-8 无 BOM（BOM 会让管理器解析首行失败）。
  - 仓库根 `THIRD_PARTY_NOTICES.txt` 必须复制进 zip 根目录，缺失直接失败（GPL-3.0 合规）。
  - `--skip-panel` / `-SkipPanel` 也必须把已有 `build/panel` 产物复制到后端嵌入目录，否则面板只剩占位
    `index.html`；`--skip-backend` / `--skip-core` 缺产物时只告警，CI 永远完整构建。
  - 单架构包必须只保留目标 ABI 的 `bin/<dir>/mproxy` 与 `bin/core/<dir>/mihomo`，排除其余三个 ABI 目录；
    构建脚本与 CI/Release 都要做结构校验（`zip -x` 与 python/.NET 兜底行为一致）；universal 必须仍含全部 8 个 ABI 二进制。
  - 版本号做文件名安全化（非 `[A-Za-z0-9._+-]` 替换为 `-`）；ldflags 注入
    `mproxy/internal/version.Version`（并为 `internal/buildinfo.Version`、`main.version` 兜底）；
    mihomo 版本取 `git -C upstream/mihomo describe --tags --always`，`arm` 固定 `GOARM=7`。
  - 国内网络可设 `MPROXY_NPM_REGISTRY=https://registry.npmmirror.com`；`pnpm install --frozen-lockfile`
    失败时脚本回退 `--no-frozen-lockfile --ignore-scripts`。
  - 完整构建含 4 个 mihomo 变体，耗时明显；只改后端/脚本时用 `--skip-panel --skip-core`。
- 面板 fork：`bash scripts/dev-panel.sh --prepare|--build|--all`（Windows 用 `dev-panel.ps1`）。脚本用
  `git worktree` 按 `panel.lock` 检出到 `build/panel`，不覆盖 overlay、不 apply patch；`build/panel` 是
  生成目录，不要在里面长期改代码。
- 面板改动流程：在 `panel` 分支改 → `cd build/panel && pnpm run type-check && pnpm run build` → 提交 panel
  分支 → 更新 `panel.lock`（新 commit SHA）→ **push `origin panel`**（CI 只 fetch origin 上的 panel commit，
  不 push 会导致 CI 取不到）。上游升级是在 `panel` 分支 `git merge <zashboard-tag>`，冲突由人处理；
  不要 `rebase` 改写历史，也不要重新引入 overlay/patch。

## 开发与测试

```bash
cd backend && gofmt -l . && go vet ./... && go test ./... -count=1   # 必须全绿
bash -n module/*.sh scripts/*.sh                                     # shell 语法
bash scripts/dev-panel.sh --build && cd build/panel && pnpm run type-check && pnpm run build
```

- 改 Go 逻辑必须补/改测试；改 shell 至少 `bash -n`；不要用 bash 语义“修正” POSIX sh。
- 改配置字段/默认值时必须同步 `backend/internal/config/settings.go`、`backend/internal/config/default.yaml`
  注释与面板 `src/api/mproxy.ts`。
- 本地跑后端：`cd backend && MPROXY_DEV=1 MPROXY_DATA_DIR="$PWD/../.devdata" go run ./cmd/mproxy serve`
  （`MPROXY_DEV=1` 关闭鉴权，只用于桌面开发）。后端服务的是编译时内嵌面板：改面板后先复制产物到
  `backend/internal/webui/dist`（`--skip-panel --skip-backend --skip-core --skip-zip` 可只做复制）再重启。
- 本机没有 mihomo 内核时内核接口返回“未安装”；Windows 版 `mihomo.exe` 只用于 UI/API 联调，不代表 TUN 行为。

## 代码风格

### Go

- 一律 `gofmt`；包级注释用中文，导出符号有注释；保持分层：`server` → `config` → `core` → `sysutil`。
- 错误向上返回并带上下文（`fmt.Errorf("...: %w", err)`）；对外错误用中文 message + 稳定英文 `code`
  （`writeErr`），不要在 handler 里手写 JSON。
- handler 统一 `decodeJSONBody`（大小上限、`UseNumber`）→ 校验 → `writeJSON`；纯逻辑放
  `config.Doc` / `core` 方法里便于测试。不要新增第三方依赖（当前只有 `golang.org/x/net`、`gopkg.in/yaml.v3`）。
- 不改变对外 JSON 字段名/结构（面板/API 契约）。进程与文件操作保持“原子 + 明确错误”：临时文件 +
  rename、pid 文件、日志轮转、超时 context。平台差异放 `sysutil`（`IsAndroid()` 用路径探测），
  不要在业务层散落 `runtime.GOOS`。

### 面板 fork（Vue 3 / TypeScript）

- MProxyEvolution 面板改动提交在 `panel` 分支；新页面/组件放 `src/components/mproxy/**` 或
  `src/components/settings/**`，保持 zashboard 风格（2 空格、单引号、无分号、printWidth 100），不要因为
  prettier 配置里 `semi: true` 就批量加分号。
- HTTP 只走 `src/api/mproxy.ts`（token 来自 `window.__MPROXY_TOKEN__` / sessionStorage，`X-MProxy-Token`
  头，30s 超时），不要在组件里直接 `fetch` `/api`，也不要引入第二套配置存储或跨域请求。
- 用户可见文案走 vue-i18n，新增 key 至少覆盖 `zh` / `en`，并同步 `zh-tw` / `ru`。
- `sessionStorage['mproxy/token']`、`/api/mproxy/*` 等契约与标识符不要改名。
- PWA/service worker 已关闭、`dashboardUpgrade` 已禁用，不要重新引入；edge-to-edge 由
  `public/mproxy-boot.js` 的 `enableEdgeToEdge`/`enableInsets` + `index.html` 静态 `/internal/insets.css`
  提供，CSS 用 `--safe-area-inset-*` 不要写死 inset。
- 提交 panel 分支时若 husky/lint-staged 因**既有** lint 错误失败，优先修复或单独说明，不要习惯性 `--no-verify`。

### POSIX sh（module/**）

- shebang 固定 `#!/system/bin/sh`；不用 bash 数组、`[[`、`local`、`function`；变量加引号，路径用
  `$MODPATH` / `$MPROXY_DATA`。
- `ui_print`/`abort` 使用前做可用性兜底；`customize.sh` 被安装器 source，**不要写 `exit`**；
  `SKIPUNZIP=1` 后自己按 ABI 解压。安装器只处理 `/data/adb/mproxy/bin` 的二进制权限，不要给
  `webroot` 加 chmod/set_perm。
- 已有 `config.yaml` 绝不覆盖；升级时先 `mproxy stop`，二进制写 `.new` 再 `mv`（避免 `Text file busy`）。
- `service.sh` 等 `sys.boot_completed=1`（最多约 120s）再 `mproxy boot --boot`，有 `timeout` 时包裹 180s；
  日志追加 `logs/service.log`；控制台输出中文短句，平台差异用能力探测而非管理器类型硬编码。

### YAML / 配置

- `config.yaml` 的键名、`x-mproxy` 字段与默认值是对外契约：`version`、`auto-start`、`proxy-mode`、
  `update-channel`、`ghproxy`、`onboarded`，不要改名或换类型；缺失字段用 `DefaultSettings()` 兼容。
- `tun.enable` 与 `x-mproxy.proxy-mode` 必须一致（tun ↔ true，mixed ↔ false）；`tun.include-package`
  存在即白名单，`exclude-package` 存在即黑名单，互斥。
- `update.json` 与 `.release-baseline` 发版后由 CI 回写，不要手改。

### 文档与行尾

- `README.md` 与 `README_EN.md` 是唯一的用户文档，改一侧必须同步另一侧和标题锚点；项目不维护 `docs/`
  目录，不要重新创建。文档用 LF、UTF-8，不要全仓库格式化 Markdown。
- `.gitattributes` 强制：`.sh`/`.go`/`.vue`/`.ts`/`.js`/`.json`/`.yaml`/`.yml`/`.prop`/`.md`/`.css`/`.html`、
  `module/**`、`backend/**`、`.github/**` 为 LF；`.ps1` 为 CRLF；图片/压缩包 binary。
- 设备端脚本出现 CRLF 会让 mksh 报错；panel 分支提交前同样确认没有手写 CRLF。JSON/YAML 用 2 空格缩进 + LF。

## 领域不变量与已知坑

### 配置与持久化

- 写入顺序：校验 → `config.yaml.tmp` → 备份为 `config.yaml.bak.tmp` → rename 为 `.bak` → rename 临时文件为
  `config.yaml`；任一步失败都不能破坏旧配置。内核已安装时必须先 `mihomo -t -d <dataDir> -f <临时文件>`
  （45s 超时），失败不覆盖正式配置；备份只有一份（`config.yaml.bak`），重置需要 `confirm: true`。
- 完整 YAML 替换（`PUT /api/config`、`PUT /api/mihomo/configs`）必须保留现有 `x-mproxy`；缺失
  `external-controller`/`secret` 时沿用旧值（否则面板失联）。`POST /api/config/restore` 直接应用备份内容。
- `apply=auto` 时只有关键字段变化才重启内核（`external-controller`、`secret`、`mixed-port`、`socks-port`、
  `port`、`allow-lan`、`tun.enable`、`tun.stack`、`x-mproxy.proxy-mode`、`mode`），其余热重载；
  `reload`/`restart`/`none` 显式指定。
- 导入：`merge` 按 `name` 合并 `proxies`/`proxy-groups`/`listeners`，provider 按 key 合并，`rules` 整体替换；
  **白名单外的顶层键（allow-lan、external-controller、secret、端口、tun、dns 等）一律忽略**，`x-mproxy`
  永远以当前配置为准；`replace` 保留 `x-mproxy` 与缺失的控制器/secret。只支持 Clash/mihomo YAML，
  检测到 sing-box 配置报错。
- 分流只写 `tun.include-package`/`exclude-package`：`all` 删除两者，白/黑名单为空报错，
  `PUT /api/mproxy/apps` 显式重启内核。provider 名称不能含 `/`、`\`，URL 必须 http(s)，
  `interval` 默认 86400，`path` 默认 `./providers/<name>.yaml`。
- `ghproxy` 必须以 `/` 结尾（后端会补）且只接受 http(s)，只做下载前缀拼接，不改写 API 请求。

### 同源代理与 token

- 后端只允许监听 `127.0.0.1:9091`（`server.ListenAddr`），**不要**改成 `0.0.0.0` 或暴露 `allow-lan`；
  mihomo controller 默认只监听 `127.0.0.1:9090`。
- token 来自 `state/token`（随机 24 字节 hex，0600，缺失时生成）；`/healthz` 与静态资源公开，其余
  `/api/*` 必须校验 `X-MProxy-Token`、`Authorization: Bearer` 或 `?token=`。
- 反代必须去掉 `X-MProxy-Token`/`Authorization` 与 URL 中的 `token`，补 mihomo 的
  `Authorization: Bearer <secret>`；保留 SSE 立即 flush 与 WebSocket hijack；内核未运行时 502。
  请求体限制：配置 16MB、补丁 8MB。
- 面板 token 只存 `sessionStorage['mproxy/token']` 与 `window.__MPROXY_TOKEN__`，boot 脚本负责地址栏去 token；
  不要新增 cookie/localStorage 长期明文保存。
- 静态服务：`/assets/*` 一年 immutable，其余 `no-cache`；非文件路径回退 `index.html`（SPA），`/api/*` 不能回退。

### 内核生命周期与更新

- `serve`：建目录、加载/生成配置、写 `state/backend.pid`、按 `auto-start` 异步启动内核（Android 最多等默认
  路由 120s、带退避重试 3 次；桌面开发 8s）；收到 SIGINT/SIGTERM 退出但内核保持运行。
- 启动内核：先清理同路径残留进程（仅 Android，按 `/proc/<pid>/cmdline` 匹配）；检测到外部 controller
  可达则只接管状态/代理，不停止外部内核；等待 controller `/version` 最多 15s，失败带日志尾部并清理进程。
  - 启动成功与后端自身启动时尽力移入根 cgroup（cgroup v2 `/sys/fs/cgroup/cgroup.procs`），避免管理器
    切后台/被回收时连带杀掉进程；不支持时只告警，不得阻断启动。
  - 内核异常退出且 `auto-start=true` 时看门狗按指数退避自动重启（最多 5 次）；`Stop`/`Update` 必须置
    “主动停止”标记，避免与用户操作冲突。重启次数与上次退出通过 `/api/status` 的 `restarts`/`lastExit`
    暴露，退出详情写 `state/last_exit`，启动成功后清除。
  - TUN 残留清理必须“安全优先”：仅 Android、TUN 开启、上次异常退出、内核/外部实例未运行、controller
    不可达、设备存在且设备名恰为 mihomo 默认 `Meta` 时才允许 `ip link del`；自定义设备名只记录并跳过，
    任何清理失败都只告警，不得阻断启动。
- `boot`：先探测 `/healthz`，未启动则 detached 拉起 `serve`，20s 内轮询，最后输出带 token 的面板地址；
  `status` 未运行返回非 0，`stop` 先内核后后端，`toggle` 切换。
- TUN 启动前保持 `tun.auto-detect-interface` 不变（默认 true，由 mihomo 自行探测出口网卡）；仅当用户显式
  关闭 auto-detect 且顶层 `interface-name` 为空时，后端才写入探测到的 `interface-name`，避免 Android 策略
  路由下 `default interface lost`。
- 内核更新：GitHub release API（`MetaCubeX/mihomo`）→ stable 只取非 prerelease、alpha 取最新 → 按
  `GOOS/GOARCH/GOARM` 匹配资产 →（可加 ghproxy）下载 → 解压（gz/zip/裸二进制）→ 原子替换 `bin/mihomo` →
  `mihomo -v` 校验 → 按需重启；失败尽量恢复旧内核。进度走 `/api/jobs`。版本号从 `mihomo -v` 或 controller
  `/version` 解析（有缓存，更新后清除）。pid 文件是状态来源但要能接管已有进程，不要只看 pid 文件。
- 下载客户端内嵌 Mozilla CA 根证书（`internal/download/cacert.pem`）并叠加系统根池，修复 Android 上静态
  二进制读不到系统证书导致的 `x509: certificate signed by unknown authority`。

### TUN、DNS 与分流

- `post-fs-data.sh` 尽早建数据目录、开 `ip_forward`、兼容 `/dev/tun` → `/dev/net/tun` 并 `chmod 0600`
  （后端启动时也会尽力重复准备）。
- 默认 TUN 开启、`stack: mixed`、`auto-route: true`、`dns-hijack: any:53`、MTU 1500；DNS 为 fake-ip
  （`198.18.0.1/16`），监听 `0.0.0.0:1053`，nameserver 为 DoH。TProxy/redirect 在 v0.1 拒绝，不要顺手启用。
- TUN 启动前临时保存并关闭系统 Private DNS（`state/private_dns`，0600，已存在不覆盖），停止或自愈失败时
  恢复原值；不要覆盖用户原始模式，也不要让它长期停留在 off。
- 端口默认：后端 9091、controller 9090、mixed 7890、socks 7891、http 0、DNS 1053；改端口要同步默认模板、
  README 与面板默认值，并注意 `CriticalChanges` 会触发内核重启。

### 面板 fork 与 submodule

- 只允许通过 `panel` 分支提交面板改动；`upstream/zashboard` submodule 已不是构建来源。`panel.lock` 是面板
  构建版本的唯一权威；构建脚本按它检出 worktree。
- `upstream/mihomo` 只允许指向 release tag 对应的 commit；不要 `git submodule update --remote`（当前
  `v1.19.33`，mihomo main 曾出现不可靠状态，这是硬性策略）。不要在 submodule 内提交或改写历史，
  不要提交 submodule 里的 node_modules/dist。

### 安全边界

- 模块以 root 运行：安装器/服务脚本里的命令必须是常量或固定模板，**绝不**把用户输入、订阅 URL、YAML、
  应用名或面板字符串拼进 shell 命令。
- 订阅与节点配置含敏感凭据：不要打印 token/secret，不要把 `config.yaml`、`state/token`、`logs/` 提交或上传；
  诊断接口/诊断包只含路径、摘要与日志尾部。
- 不引入远程脚本、CDN、遥测或上传；后端只出站访问 GitHub release、订阅 URL 与本机 mihomo controller。
- 不要提交 `build/`、`dist/`、`*.zip`、`backend/internal/webui/dist` 构建资产、`.devdata/`、日志、签名或密钥。

## CI 与发版

- `ci.yml`：push 到 `main`（忽略 `**/*.md`、`THIRD_PARTY_NOTICES.txt`）、PR、手动。`test` 跑 `sh -n`、
  `gofmt -l backend`、`go vet`、`go test -race ./... -count=1` 与 `bash tests/scripts_test.sh`（安装器/打包
  回归断言）；`metadata` 校验 JSON/YAML 与 `module.prop`/`module.yaml`/`module.json`/`update.json`/
  `.release-baseline` 一致性；`lint` 跑 shellcheck、yamllint、actionlint；`panel` 按 `panel.lock` 检出后跑
  `pnpm run lint` 与 `pnpm run type-check`；`build`（needs test/metadata/lint/panel）用 `--split` 完整构建
  universal + 4 个单架构包（版本 `0.1.0-ci.<sha7>`）并上传 artifact，不发布。
- `release.yml`：push `v*` tag → tag 与 `module.prop` 版本一致性 → 发布前测试 → `build.sh --split --version
  <tag 去掉 v>` → universal + 4 个单架构包预检（每个 <100MB、只含本 ABI）→ 生成 `changelog.txt` → draft
  Release（上传 5 个 zip 与对应 sha256）→ 发布（检查 release immutability，仅告警）→ 回写 `update.json`
  与 `.release-baseline` 到 `main`（`update.json` 仍指向 universal）。
- `sync-upstream.yml`：每天 03:17 UTC 读取两个上游 release tag；mihomo 更新 submodule；zashboard merge 到
  `panel` 分支并创建只更新 `panel.lock` 与 mihomo submodule 的 PR（workflow token 创建的 PR 不自动触发 CI）。
- 版本策略：`module.prop` 的 `version` 是三位 `X.Y.Z` 且不带 `v`；发布 tag 是 `vX.Y.Z`，去掉 `v` 后必须一致；
  `versionCode` 递增且不复用。发版前手动改这两项，`update.json`/`.release-baseline` 由 CI 回写。
- 资产名固定：`MProxyEvolution-<versionCode>-<version>.zip`（universal）+ 同名 `.sha256`；
  `MProxyEvolution-<versionCode>-<version>-<abi>.zip` + 同名 `.sha256`（abi = arm64-v8a / armeabi-v7a /
  x86 / x86_64）+ `changelog.txt`。`module.yaml` 的 `release.assetPattern` 为 `MProxyEvolution-*.zip`
  （会匹配多个包）。`update.json` 的 `zipUrl` 仍指向 universal，保证所有设备自动更新可用。
- `changelog.txt` 基线取 `.release-baseline` 的 commit；缺失/不是 tag 祖先/区间为空时回退上一个正式 `v*` tag，
  再退全量 `git log`；不要用 `git describe`（tag 可能不在 main 祖先链、测试 tag 会干扰）。
- 发布前确认仓库开启 **Enable release immutability**（KernelSU Modules 只接受 immutable release）。
- 未接入 KernelSU Modules 的 `module-release` action：universal 仍超过 100MB 上限（zip-too-large），
  4 个单架构包（约 32-36 MiB）均满足上限；收录时 listing 使用 **arm64-v8a** 单架构包（覆盖绝大多数手机），
  `update.json` 继续指向 universal。当前用发布流水线内的 5 个 zip 结构预检替代；接入时需让
  `assetPattern`/资产选择只命中 arm64-v8a 包。
- 许可合规：zip 必须包含 `THIRD_PARTY_NOTICES.txt`；mihomo 为 GPL-3.0，源码由 `upstream/mihomo` submodule
  （固定 tag）与构建脚本提供，不要移除该文件或改坏复制逻辑。

## 不能本地验证的内容

没有 Android 真机环境，以下只能推演，必须明确告知用户“未在真机验证”并给最小验证步骤：

- 模块安装/升级/卸载：`customize.sh` 的 ABI 选择与解压、`mihomo stop` 替换二进制、保留 `config.yaml`、
  `service.sh` 开机等待、`action.sh`、`uninstall.sh` 与数据保留。
- KernelSU / Magisk WebView 行为：`ksu.exec` 回调、跳转 `127.0.0.1`、`/internal/insets.css`、
  `enableEdgeToEdge`/`enableInsets`、状态栏图标、CSP、`ksu.pm` / `ksu://icon`。
- TUN/真实网络：`/dev/net/tun`、`ip_forward`、fake-ip DNS、按应用分流、与其它 VPN 冲突、订阅下载与 ghproxy；
  交叉编译的 linux 二进制不能在 Windows 运行，Windows 版 `mihomo.exe` 不代表 TUN 行为。
- GitHub Actions 的 release 回写、release immutability、KernelSU Modules 收录与体积限制。

## 提交前检查清单

- [ ] 已读相关源码，没有覆盖用户未提交的改动；未执行未要求的 `git commit` / `git tag` / `git push`。
- [ ] `gofmt -l backend` 无输出、`go vet ./...`、`go test ./... -count=1`、`bash -n module/*.sh scripts/*.sh` 通过。
- [ ] 面板改动提交到 `panel` 分支、更新 `panel.lock` 并 push，`dev-panel.sh --build` 通过；未直接改 `build/panel`。
- [ ] 配置默认值/字段/API 变化已同步 `internal/config`、`default.yaml`、面板 API 客户端与 README。
- [ ] 模块元数据 JSON/YAML 可解析且 `module.prop`/`module.yaml`/`module.json`/`update.json` 一致。
- [ ] 新增/修改文本文件行尾正确（`.ps1` CRLF，其余 LF）；`module.prop` 写入无 BOM。
- [ ] 没有提交 `build/`、`dist/`、zip、`backend/internal/webui/dist` 资产、`.devdata/`、日志、密钥。
- [ ] 没有新增未声明的第三方依赖/远程资源；`THIRD_PARTY_NOTICES.txt` 仍在 zip 根目录；日志/错误不泄露 token。
