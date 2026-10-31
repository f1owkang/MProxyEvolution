<#
.SYNOPSIS
    构建 MProxyEvolution 模块包：面板 → 后端（4 架构）→ mihomo 内核（4 架构）→ 模块目录 → zip + sha256。

.DESCRIPTION
    默认完整构建，产物写入 build/ 与 dist/：
      build/panel                 面板构建目录（由 scripts/dev-panel.ps1 组装）
      build/backend/<abi>/mproxy  Go 后端
      build/core/<abi>/mihomo     mihomo 内核
      build/module-root           zip 的根目录（module/** + bin/**）
      dist/MProxyEvolution-<versionCode>-<version>.zip   可刷入的模块包（universal，四架构）
      dist/MProxyEvolution-<versionCode>-<version>.zip.sha256
      dist/MProxyEvolution-<versionCode>-<version>-arm64-v8a.zip      -Split 时的 4 个单架构包：
      dist/MProxyEvolution-<versionCode>-<version>-armeabi-v7a.zip    arm64-v8a / armeabi-v7a / x86 / x86_64
      dist/MProxyEvolution-<versionCode>-<version>-x86.zip            （同名 .sha256）
      dist/MProxyEvolution-<versionCode>-<version>-x86_64.zip

    本脚本只负责“构建/组装/打包”；面板由 scripts/dev-panel.ps1 按 panel.lock 检出 fork
    commit 到 build/panel。

.PARAMETER Version
    构建版本号，默认 0.1.0。会写入后端 ldflags、模块 module.prop 的 version；zip 文件名另带 module.prop 的 versionCode。

.PARAMETER SkipPanel
    跳过面板构建（复用 build/panel/web/dist 或 backend 内嵌产物）。

.PARAMETER SkipBackend
    跳过 Go 后端编译（复用 build/backend）。

.PARAMETER SkipCore
    跳过 mihomo 内核编译（复用 build/core）。

.PARAMETER SkipZip
    只组装 build/module-root，不生成 zip / sha256。

.PARAMETER Split
    额外产出 4 个单架构包（arm64-v8a / armeabi-v7a / x86 / x86_64）；universal 包始终产出。

.PARAMETER Clean
    构建前清理 build/ 与 dist/。

.EXAMPLE
    pwsh -File scripts/build.ps1
    pwsh -File scripts/build.ps1 -Version 0.2.0 -Clean
    pwsh -File scripts/build.ps1 -SkipPanel -SkipCore
    pwsh -File scripts/build.ps1 -Split

.NOTES
    需要：git、Go 1.26+、Node.js 20+、pnpm 10+（或 npm）。
    交叉编译内核/后端均为 CGO_ENABLED=0，无需 Android NDK。
#>
[CmdletBinding()]
param(
    [string]$Version = '0.1.0',
    [switch]$SkipPanel,
    [switch]$SkipBackend,
    [switch]$SkipCore,
    [switch]$SkipZip,
    [switch]$Split,
    [switch]$Clean
)

$ErrorActionPreference = 'Stop'

# ---------------------------------------------------------------------------
# 基础路径与通用工具
# ---------------------------------------------------------------------------
$ScriptDir = $PSScriptRoot
if (-not $ScriptDir) { $ScriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path }
$Root = (Resolve-Path (Join-Path $ScriptDir '..')).Path

$BuildDir = Join-Path $Root 'build'
$PanelDir = Join-Path $BuildDir 'panel'
$BackendDir = Join-Path $Root 'backend'
$MihomoDir = Join-Path $Root 'upstream/mihomo'
$ModuleSourceDir = Join-Path $Root 'module'
$BackendDistDir = Join-Path $BackendDir 'internal/webui/dist'
$ModuleRoot = Join-Path $BuildDir 'module-root'
$DistDir = Join-Path $Root 'dist'

$VersionSafe = ($Version -replace '[^0-9A-Za-z._+-]', '-')

# 产物命名统一为 KernelSU Modules 的规范：{id}-{versionCode}-{version}.zip
$VersionCode = $null
foreach ($line in Get-Content -LiteralPath (Join-Path $ModuleSourceDir 'module.prop')) {
    if ($line -match '^versionCode=(.+)$') { $VersionCode = $Matches[1].Trim() }
}
if (-not $VersionCode -or $VersionCode -notmatch '^\d+$') {
    throw "module/module.prop 的 versionCode 缺失或不是整数：'$VersionCode'"
}

function Write-Step {
    param([string]$Message)
    Write-Host ""
    Write-Host "==> $Message" -ForegroundColor Cyan
}

function Write-Info {
    param([string]$Message)
    Write-Host "    $Message" -ForegroundColor DarkGray
}

function Write-Warn {
    param([string]$Message)
    Write-Host "    [警告] $Message" -ForegroundColor Yellow
}

function Assert-Command {
    param([string]$Name, [string]$Hint)
    if (-not (Get-Command $Name -ErrorAction SilentlyContinue)) {
        throw "未找到命令 '$Name'。$Hint"
    }
}

function Assert-File {
    param([string]$Path, [string]$Message)
    if (-not (Test-Path -LiteralPath $Path -PathType Leaf)) {
        throw "$Message（缺少文件：$Path）"
    }
}

function Invoke-External {
    param(
        [Parameter(Mandatory = $true)][string]$FilePath,
        [string[]]$Arguments = @(),
        [string]$WorkingDirectory = $Root
    )
    $display = $FilePath + ' ' + ($Arguments -join ' ')
    Write-Info $display
    Push-Location -LiteralPath $WorkingDirectory
    try {
        if ($Arguments.Count -eq 0) {
            & $FilePath
        } else {
            & $FilePath @Arguments
        }
        $code = $LASTEXITCODE
        if ($null -ne $code -and $code -ne 0) {
            throw "命令失败（退出码 $code）：$display"
        }
    }
    finally {
        Pop-Location
    }
}

function Invoke-Capture {
    param(
        [Parameter(Mandatory = $true)][string]$FilePath,
        [string[]]$Arguments = @(),
        [string]$WorkingDirectory = $Root
    )
    Push-Location -LiteralPath $WorkingDirectory
    try {
        $output = & $FilePath @Arguments 2>$null
        $code = $LASTEXITCODE
        if ($null -ne $code -and $code -ne 0) {
            throw "命令失败（退出码 $code）：$FilePath $($Arguments -join ' ')"
        }
        return (($output | Out-String).Trim())
    }
    finally {
        Pop-Location
    }
}

function Invoke-WithEnvironment {
    param(
        [Parameter(Mandatory = $true)][hashtable]$Variables,
        [Parameter(Mandatory = $true)][scriptblock]$Script
    )
    $saved = @{}
    foreach ($key in $Variables.Keys) {
        $saved[$key] = [Environment]::GetEnvironmentVariable($key, 'Process')
        [Environment]::SetEnvironmentVariable($key, [string]$Variables[$key], 'Process')
    }
    try {
        & $Script
    }
    finally {
        foreach ($key in $saved.Keys) {
            [Environment]::SetEnvironmentVariable($key, $saved[$key], 'Process')
        }
    }
}

function Remove-SafePath {
    param([string]$Path)
    $full = [System.IO.Path]::GetFullPath($Path)
    $rootFull = [System.IO.Path]::GetFullPath($Root).TrimEnd('\', '/')
    if (-not $full.StartsWith($rootFull + [System.IO.Path]::DirectorySeparatorChar, [System.StringComparison]::OrdinalIgnoreCase)) {
        throw "拒绝删除仓库之外的路径：$full"
    }
    if (Test-Path -LiteralPath $full) {
        Remove-Item -LiteralPath $full -Recurse -Force
    }
}

function Ensure-Directory {
    param([string]$Path)
    if (-not (Test-Path -LiteralPath $Path)) {
        New-Item -ItemType Directory -Path $Path -Force | Out-Null
    }
}

# 创建模块 zip。$ExcludePatterns 为相对路径通配符（如 bin/386/*），单架构包用它排除其它 ABI。
function New-ModuleZip {
    param(
        [Parameter(Mandatory = $true)][string]$ZipPath,
        [string[]]$ExcludePatterns = @()
    )
    if (Test-Path -LiteralPath $ZipPath) { Remove-Item -LiteralPath $ZipPath -Force }
    $entryCount = 0
    $zipStream = [System.IO.Compression.ZipFile]::Open($ZipPath, [System.IO.Compression.ZipArchiveMode]::Create)
    try {
        $moduleRootFull = (Resolve-Path -LiteralPath $ModuleRoot).Path
        Get-ChildItem -LiteralPath $moduleRootFull -Recurse -File | ForEach-Object {
            $relative = $_.FullName.Substring($moduleRootFull.Length + 1).Replace('\', '/')
            $skip = $false
            foreach ($pattern in $ExcludePatterns) {
                if ($relative -like $pattern) { $skip = $true; break }
            }
            if (-not $skip) {
                [System.IO.Compression.ZipFileExtensions]::CreateEntryFromFile(
                    $zipStream, $_.FullName, $relative,
                    [System.IO.Compression.CompressionLevel]::Optimal
                ) | Out-Null
                $entryCount++
            }
        }
    }
    finally {
        $zipStream.Dispose()
    }
    Write-Info "zip 包含 $entryCount 个文件"
    return $entryCount
}

function Write-Sha256File {
    param([Parameter(Mandatory = $true)][string]$ZipPath)
    $zipName = Split-Path -Leaf $ZipPath
    $hash = (Get-FileHash -LiteralPath $ZipPath -Algorithm SHA256).Hash.ToLowerInvariant()
    $shaLine = "$hash  $zipName`n"
    [System.IO.File]::WriteAllText("$ZipPath.sha256", $shaLine, (New-Object System.Text.UTF8Encoding($false)))
    return $hash
}

# 单架构包结构校验：必须含目标 ABI 的后端与内核，且不能残留其它 ABI 目录。
function Assert-SplitZipLayout {
    param(
        [Parameter(Mandatory = $true)][string]$ZipPath,
        [Parameter(Mandatory = $true)][string]$AbiDir
    )
    $archive = [System.IO.Compression.ZipFile]::OpenRead($ZipPath)
    try {
        $names = @($archive.Entries | ForEach-Object { $_.FullName })
    }
    finally {
        $archive.Dispose()
    }
    foreach ($required in @("bin/$AbiDir/mproxy", "bin/core/$AbiDir/mihomo")) {
        if ($names -notcontains $required) {
            throw "架构包缺少 $required：$ZipPath"
        }
    }
    foreach ($other in @('arm64', 'arm', '386', 'amd64')) {
        if ($other -eq $AbiDir) { continue }
        foreach ($forbidden in @("bin/$other/", "bin/core/$other/")) {
            $bad = @($names | Where-Object { $_.StartsWith($forbidden) })
            if ($bad.Count -gt 0) {
                throw "架构包不应包含 $forbidden：$ZipPath"
            }
        }
    }
    Write-Info "架构包结构校验通过：$AbiDir"
}

# ---------------------------------------------------------------------------
# 0. 环境检查
# ---------------------------------------------------------------------------
Write-Host "MProxyEvolution 构建脚本 (PowerShell)" -ForegroundColor Green
Write-Host "  仓库根目录：$Root"
Write-Host "  构建版本：  $Version"

foreach ($required in @('module/module.prop', 'scripts/build.sh', 'THIRD_PARTY_NOTICES.txt')) {
    Assert-File (Join-Path $Root $required) "仓库结构不完整：$required"
}
Assert-Command 'git' '请安装 Git，并确保 git 在 PATH 中。'

if ($Clean) {
    Write-Step '清理 build/ 与 dist/'
    Remove-SafePath $BuildDir
    Remove-SafePath $DistDir
}

if (-not $SkipCore) {
    # 只需要 mihomo submodule；面板来自 panel.lock 检出的 fork worktree。
    if (-not (Test-Path -LiteralPath (Join-Path $MihomoDir 'go.mod'))) {
        Write-Step '初始化 git submodule（upstream/mihomo）'
        Invoke-External -FilePath 'git' -Arguments @('submodule', 'update', '--init', '--recursive') -WorkingDirectory $Root
    }
}

# ---------------------------------------------------------------------------
# 1. 面板构建
# ---------------------------------------------------------------------------
if (-not $SkipPanel) {
    Write-Step '组装面板（scripts/dev-panel.ps1 -Prepare）'
    $DevPanelPs1 = Join-Path $ScriptDir 'dev-panel.ps1'
    if (-not (Test-Path -LiteralPath $DevPanelPs1)) {
        throw "缺少 scripts/dev-panel.ps1（由 UI 工程师维护），无法组装面板。"
    }
    Push-Location -LiteralPath $Root
    try {
        & $DevPanelPs1 -Prepare
        $devRc = $LASTEXITCODE
    }
    finally {
        Pop-Location
    }
    if ($null -ne $devRc -and $devRc -ne 0) {
        throw "scripts/dev-panel.ps1 -Prepare 失败（退出码 $devRc）。"
    }
    Assert-File (Join-Path $PanelDir 'package.json') 'dev-panel 未生成 build/panel/package.json，面板组装失败。'

    Write-Step '安装面板依赖（pnpm install --frozen-lockfile）'
    $pkgManager = $null
    if (Get-Command 'pnpm' -ErrorAction SilentlyContinue) { $pkgManager = 'pnpm' }
    elseif (Get-Command 'npm' -ErrorAction SilentlyContinue) { $pkgManager = 'npm' }
    else { throw "未找到 pnpm/npm。请安装 Node.js 20+ 与 pnpm 10+（npm i -g pnpm），或运行 corepack enable。" }

    $registryArgs = @()
    if ($env:MPROXY_NPM_REGISTRY) { $registryArgs = @("--registry=$($env:MPROXY_NPM_REGISTRY)") }

    if ($pkgManager -eq 'pnpm') {
        try {
            Invoke-External -FilePath 'pnpm' -Arguments (@('install', '--frozen-lockfile') + $registryArgs) -WorkingDirectory $PanelDir
        }
        catch {
            Write-Warn "pnpm install --frozen-lockfile 失败，回退 --no-frozen-lockfile --ignore-scripts（build/panel 无 .git，husky prepare 可能失败）。"
            Invoke-External -FilePath 'pnpm' -Arguments (@('install', '--no-frozen-lockfile', '--ignore-scripts') + $registryArgs) -WorkingDirectory $PanelDir
        }
        Write-Step '构建面板（pnpm build）'
        Invoke-External -FilePath 'pnpm' -Arguments @('build') -WorkingDirectory $PanelDir
    }
    else {
        if (Test-Path -LiteralPath (Join-Path $PanelDir 'package-lock.json')) {
            Invoke-External -FilePath 'npm' -Arguments @('ci') -WorkingDirectory $PanelDir
        } else {
            Invoke-External -FilePath 'npm' -Arguments @('install', '--no-audit', '--no-fund') -WorkingDirectory $PanelDir
        }
        Write-Step '构建面板（npm run build）'
        Invoke-External -FilePath 'npm' -Arguments @('run', 'build') -WorkingDirectory $PanelDir
    }

    # dev-panel / vite 约定产物目录：build/panel/web/dist（兼容 build/panel/dist）
    $panelDist = $null
    foreach ($candidate in @('web/dist', 'dist')) {
        $path = Join-Path $PanelDir $candidate
        if (Test-Path -LiteralPath (Join-Path $path 'index.html')) { $panelDist = $path; break }
    }
    if (-not $panelDist) {
        throw "未找到面板构建产物（期望 build/panel/web/dist/index.html 或 build/panel/dist/index.html）。请检查 dev-panel 与 vite 配置。"
    }
    Write-Info "面板产物：$panelDist"
}

# 注意：即使 -SkipPanel，也必须把已有面板产物复制到后端嵌入目录，否则后端会内嵌
# 占位 index.html，导致安装后 WebUI 只显示“面板尚未构建”。（曾经踩过的坑）
if (-not $panelDist) {
    foreach ($candidate in @('web/dist', 'dist')) {
        $path = Join-Path $PanelDir $candidate
        if (Test-Path -LiteralPath (Join-Path $path 'index.html')) { $panelDist = $path; break }
    }
}
if (-not $panelDist) {
    throw "-SkipPanel 但 build/panel 中找不到已构建的面板产物。请先运行 scripts/dev-panel.ps1 -Build，或去掉 -SkipPanel。"
}

Write-Step '复制面板产物到 backend/internal/webui/dist（保留一个 index.html）'
Ensure-Directory $BackendDistDir
Get-ChildItem -LiteralPath $BackendDistDir -Force | Where-Object { $_.Name -ne 'index.html' } | Remove-Item -Recurse -Force
Copy-Item -Path (Join-Path $panelDist '*') -Destination $BackendDistDir -Recurse -Force
Assert-File (Join-Path $BackendDistDir 'index.html') '面板产物缺少 index.html，无法内嵌到后端。'
$assetCount = (Get-ChildItem -LiteralPath $BackendDistDir -Recurse -File | Measure-Object).Count
Write-Info "已复制 $assetCount 个文件到 $BackendDistDir"

# ---------------------------------------------------------------------------
# 2. Go 后端（4 架构交叉编译）
# ---------------------------------------------------------------------------
$BackendTargets = @(
    [pscustomobject]@{ Abi = 'arm64'; GOARCH = 'arm64'; GOARM = $null; Label = 'linux/arm64' },
    [pscustomobject]@{ Abi = 'arm';   GOARCH = 'arm';   GOARM = '7';    Label = 'linux/arm (GOARM=7)' },
    [pscustomobject]@{ Abi = '386';   GOARCH = '386';   GOARM = $null; Label = 'linux/386' },
    [pscustomobject]@{ Abi = 'amd64'; GOARCH = 'amd64'; GOARM = $null; Label = 'linux/amd64' }
)

if (-not $SkipBackend) {
    Write-Step '编译 Go 后端（4 架构）'
    Assert-Command 'go' '请安装 Go 1.26+，并确保 go 在 PATH 中。'
    $goModPath = Join-Path $BackendDir 'go.mod'
    Assert-File $goModPath 'backend/go.mod 不存在，后端尚未就绪。'

    $modulePath = $null
    foreach ($line in (Get-Content -LiteralPath $goModPath -TotalCount 20)) {
        if ($line -match '^\s*module\s+(\S+)\s*$') { $modulePath = $Matches[1]; break }
    }
    if (-not $modulePath) { throw "无法从 backend/go.mod 解析 module 路径。" }
    Write-Info "Go module：$modulePath"

    # 后端实际符号 mproxy/internal/version.Version；其余候选由链接器静默忽略，兼容后续改动。
    $ldSymbols = @(
        "$modulePath/internal/version.Version",
        "$modulePath/internal/buildinfo.Version",
        'main.version'
    )
    $ldflags = '-s -w ' + (($ldSymbols | ForEach-Object { "-X $_=$Version" }) -join ' ')

    foreach ($target in $BackendTargets) {
        $outDir = Join-Path $BuildDir ("backend/{0}" -f $target.Abi)
        Ensure-Directory $outDir
        $outFile = Join-Path $outDir 'mproxy'
        $envVars = @{
            CGO_ENABLED = '0'
            GOOS        = 'linux'
            GOARCH      = $target.GOARCH
        }
        if ($target.GOARM) { $envVars['GOARM'] = $target.GOARM }

        Write-Info "后端 -> $($target.Label) => $outFile"
        $buildAction = {
            Invoke-External -FilePath 'go' -Arguments @(
                'build', '-trimpath', '-ldflags', $ldflags,
                '-o', $outFile, './cmd/mproxy'
            ) -WorkingDirectory $BackendDir
        }
        Invoke-WithEnvironment -Variables $envVars -Script $buildAction
        Assert-File $outFile "后端编译产物缺失（$($target.Label)）"
    }
}

# ---------------------------------------------------------------------------
# 3. mihomo 内核（4 架构，静态 linux / with_gvisor）
#    android/arm 需要 cgo 外部链接；为保证 CGO_ENABLED=0 可构建，统一使用
#    GOOS=linux 静态二进制（在 Android 上可直接运行，mihomo 自带 DNS）。
# ---------------------------------------------------------------------------
$CoreTargets = @(
    [pscustomobject]@{ Abi = 'arm64'; GOARCH = 'arm64'; GOARM = $null; Label = 'linux/arm64' },
    [pscustomobject]@{ Abi = 'arm';   GOARCH = 'arm';   GOARM = '7';    Label = 'linux/arm (GOARM=7)' },
    [pscustomobject]@{ Abi = '386';   GOARCH = '386';   GOARM = $null; Label = 'linux/386' },
    [pscustomobject]@{ Abi = 'amd64'; GOARCH = 'amd64'; GOARM = $null; Label = 'linux/amd64' }
)

if (-not $SkipCore) {
    Write-Step '编译 mihomo 内核（4 架构，with_gvisor）'
    Assert-Command 'go' '请安装 Go 1.26+，并确保 go 在 PATH 中。'
    Assert-File (Join-Path $MihomoDir 'go.mod') 'upstream/mihomo 子模块未就绪。请执行 git submodule update --init --recursive。'

    $mihomoTag = ''
    try {
        $mihomoTag = Invoke-Capture -FilePath 'git' -Arguments @('describe', '--tags', '--always') -WorkingDirectory $MihomoDir
    } catch {
        $mihomoTag = ''
    }
    if (-not $mihomoTag) {
        $mihomoTag = 'v0.0.0'
        Write-Warn "无法从 upstream/mihomo 获取版本 tag，回退为 $mihomoTag"
    }
    Write-Info "mihomo 版本：$mihomoTag"

    $coreLdflags = "-s -w -X github.com/metacubex/mihomo/constant.Version=$mihomoTag"

    foreach ($target in $CoreTargets) {
        $outDir = Join-Path $BuildDir ("core/{0}" -f $target.Abi)
        Ensure-Directory $outDir
        $outFile = Join-Path $outDir 'mihomo'
        $envVars = @{
            CGO_ENABLED = '0'
            GOOS        = 'linux'
            GOARCH      = $target.GOARCH
        }
        if ($target.GOARM) { $envVars['GOARM'] = $target.GOARM }

        Write-Info "内核 -> $($target.Label) => $outFile"
        $buildAction = {
            Invoke-External -FilePath 'go' -Arguments @(
                'build', '-tags', 'with_gvisor', '-trimpath',
                '-ldflags', $coreLdflags, '-o', $outFile, '.'
            ) -WorkingDirectory $MihomoDir
        }
        Invoke-WithEnvironment -Variables $envVars -Script $buildAction
        Assert-File $outFile "内核编译产物缺失（$($target.Label)）"
    }
}

# ---------------------------------------------------------------------------
# 4. 组装 build/module-root
# ---------------------------------------------------------------------------
Write-Step '组装模块目录 build/module-root'
Remove-SafePath $ModuleRoot
Ensure-Directory $ModuleRoot
Copy-Item -Path (Join-Path $ModuleSourceDir '*') -Destination $ModuleRoot -Recurse -Force
Write-Info "已复制 module/** -> $ModuleRoot"

if (-not (Test-Path -LiteralPath (Join-Path $ModuleRoot 'module.prop'))) {
    throw "module/module.prop 缺失，模块目录组装失败。"
}

# 第三方许可证与 Notice 必须随模块包分发（GPL-3.0 合规）
$NoticesFile = Join-Path $Root 'THIRD_PARTY_NOTICES.txt'
Assert-File $NoticesFile '仓库根缺少 THIRD_PARTY_NOTICES.txt。'
Copy-Item -LiteralPath $NoticesFile -Destination (Join-Path $ModuleRoot 'THIRD_PARTY_NOTICES.txt') -Force
Assert-File (Join-Path $ModuleRoot 'THIRD_PARTY_NOTICES.txt') 'THIRD_PARTY_NOTICES.txt 复制进 module-root 失败。'
Write-Info '已复制 THIRD_PARTY_NOTICES.txt -> module-root'

$copiedBins = 0
foreach ($target in $BackendTargets) {
    $src = Join-Path $BuildDir ("backend/{0}/mproxy" -f $target.Abi)
    $dstDir = Join-Path $ModuleRoot ("bin/{0}" -f $target.Abi)
    if (Test-Path -LiteralPath $src) {
        Ensure-Directory $dstDir
        Copy-Item -LiteralPath $src -Destination (Join-Path $dstDir 'mproxy') -Force
        $copiedBins++
    }
    elseif (-not $SkipBackend) {
        throw "缺少后端产物：$src"
    }
    else {
        Write-Warn "缺少后端产物：$src（-SkipBackend 模式，zip 将不完整）"
    }
}
foreach ($target in $CoreTargets) {
    $src = Join-Path $BuildDir ("core/{0}/mihomo" -f $target.Abi)
    $dstDir = Join-Path $ModuleRoot ("bin/core/{0}" -f $target.Abi)
    if (Test-Path -LiteralPath $src) {
        Ensure-Directory $dstDir
        Copy-Item -LiteralPath $src -Destination (Join-Path $dstDir 'mihomo') -Force
        $copiedBins++
    }
    elseif (-not $SkipCore) {
        throw "缺少内核产物：$src"
    }
    else {
        Write-Warn "缺少内核产物：$src（-SkipCore 模式，zip 将不完整）"
    }
}
Write-Info "已放入 $copiedBins 个二进制（期望 8 个）"

# 把构建版本写入模块 module.prop 的 version（源文件保持 0.1.0 不变）
# 注意：必须以 UTF-8 无 BOM + LF 写入，否则 Magisk / KernelSU 解析首行 id 会失败。
# 不能用 [System.IO.File]::WriteAllLines —— 它在 Windows 上写 CRLF。
$propPath = Join-Path $ModuleRoot 'module.prop'
$propLines = Get-Content -LiteralPath $propPath
$propLines = @($propLines | ForEach-Object {
    if ($_ -match '^version=') { "version=$Version" } else { $_ }
})
$propText = ($propLines -join "`n") + "`n"
[System.IO.File]::WriteAllText($propPath, $propText, (New-Object System.Text.UTF8Encoding($false)))
Write-Info "module.prop version -> $Version"

if ($SkipZip) {
    Write-Step '已跳过打包（-SkipZip）'
    Write-Host "模块目录：$ModuleRoot" -ForegroundColor Green
    exit 0
}

# ---------------------------------------------------------------------------
# 5. 打包 zip + sha256（universal 始终产出；-Split 额外产出 4 个单架构包）
# ---------------------------------------------------------------------------
Write-Step '打包 dist/MProxyEvolution-<versionCode>-<version>.zip'
Ensure-Directory $DistDir
$zipName = "MProxyEvolution-$VersionCode-$VersionSafe.zip"
$zipPath = Join-Path $DistDir $zipName

Add-Type -AssemblyName System.IO.Compression
Add-Type -AssemblyName System.IO.Compression.FileSystem

[void](New-ModuleZip -ZipPath $zipPath)
$hash = Write-Sha256File -ZipPath $zipPath
Write-Info "SHA256：$hash"

$splitPaths = @()
if ($Split) {
    Write-Step '打包单架构包（-Split）'
    # ABI 目录名 -> 发布资产后缀（arm64→arm64-v8a、arm→armeabi-v7a、386→x86、amd64→x86_64）
    $splitPackages = @(
        [pscustomobject]@{ Dir = 'arm64'; Tag = 'arm64-v8a' },
        [pscustomobject]@{ Dir = 'arm';   Tag = 'armeabi-v7a' },
        [pscustomobject]@{ Dir = '386';   Tag = 'x86' },
        [pscustomobject]@{ Dir = 'amd64'; Tag = 'x86_64' }
    )
    foreach ($pkg in $splitPackages) {
        $splitName = "MProxyEvolution-$VersionCode-$VersionSafe-$($pkg.Tag).zip"
        $splitPath = Join-Path $DistDir $splitName
        $excludes = @()
        foreach ($other in @('arm64', 'arm', '386', 'amd64')) {
            if ($other -eq $pkg.Dir) { continue }
            $excludes += @("bin/$other/", "bin/$other/*", "bin/core/$other/", "bin/core/$other/*")
        }
        [void](New-ModuleZip -ZipPath $splitPath -ExcludePatterns $excludes)
        Assert-SplitZipLayout -ZipPath $splitPath -AbiDir $pkg.Dir
        $splitHash = Write-Sha256File -ZipPath $splitPath
        Write-Info "SHA256：$splitHash"
        $splitPaths += $splitPath
    }
}

# ---------------------------------------------------------------------------
# 6. 结果摘要
# ---------------------------------------------------------------------------
Write-Host ""
Write-Host "构建完成 ✔" -ForegroundColor Green
Write-Host "  universal 包：$zipPath"
Write-Host "  universal 校验和：$zipPath.sha256"
$sizeMb = [math]::Round((Get-Item -LiteralPath $zipPath).Length / 1MB, 2)
Write-Host "  universal 大小：  $sizeMb MiB"
if ($Split) {
    foreach ($splitPath in $splitPaths) {
        $splitSizeMb = [math]::Round((Get-Item -LiteralPath $splitPath).Length / 1MB, 2)
        Write-Host "  单架构包：      $splitPath"
        Write-Host "  单架构校验和：  $splitPath.sha256"
        Write-Host "  单架构大小：    $splitSizeMb MiB"
    }
}
