#Requires -Version 5.1
<#
.SYNOPSIS
  Materialize the MProxyEvolution zashboard fork for building.
.DESCRIPTION
  The panel is no longer assembled from upstream + overlay + patch. Our fork keeps
  the MProxyEvolution commits on the `panel` branch (created from zashboard upstream history),
  and `panel.lock` pins the exact commit. This script checks that commit out into
  `build/panel` as a detached worktree; optional -Install/-Build run pnpm there.
#>
[CmdletBinding()]
param(
  [switch]$Prepare,
  [switch]$Install,
  [switch]$Build,
  [switch]$All,
  [string]$Registry = 'https://registry.npmmirror.com'
)
$ErrorActionPreference = 'Stop'
$Root = Split-Path -Parent $PSScriptRoot
$Panel = Join-Path $Root 'build\panel'
$LockFile = Join-Path $Root 'panel.lock'

function Step([string]$Message) { Write-Host "[dev-panel] $Message" -ForegroundColor Cyan }
function Fail([string]$Message) { Write-Host "[dev-panel] ERROR: $Message" -ForegroundColor Red; exit 1 }

if ($All) { $Install = $true; $Build = $true }
if ($Build) { $Install = $true }

if (-not (Test-Path -LiteralPath $LockFile)) { Fail "missing $LockFile" }
$commit = ((Get-Content -LiteralPath $LockFile -TotalCount 1) -split '\s+')[0].Trim()
if (-not $commit) { Fail "panel.lock 第一行必须是 commit" }

Push-Location $Root
try {
  & git cat-file -e "$commit^{commit}" 2>$null
  if ($LASTEXITCODE -ne 0) {
    Step "fetching missing panel commit $commit"
    & git fetch origin '+refs/heads/panel:refs/remotes/origin/panel'
    if ($LASTEXITCODE -ne 0) { Fail "cannot fetch panel branch from origin" }
  }
  if (Test-Path -LiteralPath $Panel) {
    & git worktree remove --force $Panel 2>$null
    if (Test-Path -LiteralPath $Panel) { Remove-Item -LiteralPath $Panel -Recurse -Force }
  }
  New-Item -ItemType Directory -Force -Path (Join-Path $Root 'build') | Out-Null
  Step "checking out panel fork $commit"
  & git worktree add --detach $Panel $commit
  if ($LASTEXITCODE -ne 0) { Fail "git worktree add failed" }
} finally {
  Pop-Location
}

if ($Install) {
  Push-Location $Panel
  try {
    $pnpm = Get-Command pnpm -ErrorAction SilentlyContinue
    if (-not $pnpm) { Fail "pnpm not found; install pnpm 11.x" }
    Step "pnpm install"
    & pnpm install --frozen-lockfile "--registry=$Registry"
    if ($LASTEXITCODE -ne 0) {
      Step "frozen install failed; retry --no-frozen-lockfile --ignore-scripts"
      & pnpm install --no-frozen-lockfile --ignore-scripts "--registry=$Registry"
      if ($LASTEXITCODE -ne 0) { Fail "pnpm install failed" }
    }
  } finally { Pop-Location }
}

if ($Build) {
  Push-Location $Panel
  try {
    Step "pnpm build"
    & pnpm run build
    if ($LASTEXITCODE -ne 0) { Fail "pnpm build failed" }
  } finally { Pop-Location }
  $index = Join-Path $Panel 'web\dist\index.html'
  if (-not (Test-Path -LiteralPath $index)) { Fail "build did not produce web/dist/index.html" }
  Step "panel build ready: $index"
}

Step 'done'
exit 0
