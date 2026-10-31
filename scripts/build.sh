#!/usr/bin/env bash
# MProxyEvolution 构建脚本（Ubuntu / CI / Git Bash）
#
# 流程：submodule（mihomo）→ 面板（dev-panel 检出 panel.lock + pnpm build）→ Go 后端 4 架构 →
#       mihomo 内核 4 架构 → 组装 build/module-root →
#       dist/MProxyEvolution-<versionCode>-<version>.zip + .sha256（universal，四架构）
#       --split 时额外产出 4 个单架构包 + .sha256（arm64-v8a / armeabi-v7a / x86 / x86_64）
#
# 用法：
#   bash scripts/build.sh
#   bash scripts/build.sh --version 0.2.0 --clean
#   bash scripts/build.sh --skip-panel --skip-core
#   bash scripts/build.sh --split
#
# 需要：git、Go 1.26+、Node.js 20+、pnpm 10+（或 npm）、zip、sha256sum。
# 面板由 scripts/dev-panel.sh 按 panel.lock 检出 fork commit 到 build/panel。

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

BUILD_DIR="$ROOT/build"
PANEL_DIR="$BUILD_DIR/panel"
BACKEND_DIR="$ROOT/backend"
MIHOMO_DIR="$ROOT/upstream/mihomo"
MODULE_SOURCE_DIR="$ROOT/module"
BACKEND_DIST_DIR="$BACKEND_DIR/internal/webui/dist"
MODULE_ROOT="$BUILD_DIR/module-root"
DIST_DIR="$ROOT/dist"

VERSION="0.1.0"
SKIP_PANEL=0
SKIP_BACKEND=0
SKIP_CORE=0
SKIP_ZIP=0
CLEAN=0
SPLIT=0

usage() {
  cat <<'EOF'
用法：bash scripts/build.sh [选项]

选项：
  -v, --version <ver>   构建版本号（默认 0.1.0），写入 ldflags / module.prop；
                        zip 文件名另带 module.prop 的 versionCode
      --skip-panel      跳过面板构建（复用 build/panel/web/dist）
      --skip-backend    跳过 Go 后端编译（复用 build/backend）
      --skip-core       跳过 mihomo 内核编译（复用 build/core）
      --skip-zip        只组装 build/module-root，不打包
      --split           额外产出 4 个单架构包（arm64-v8a / armeabi-v7a / x86 / x86_64）
      --clean           构建前清理 build/ 与 dist/
  -h, --help            显示本帮助
EOF
}

log_step() { printf '\n==> %s\n' "$*"; }
log_info() { printf '    %s\n' "$*"; }
log_warn() { printf '    [警告] %s\n' "$*" >&2; }
die() { printf '错误：%s\n' "$*" >&2; exit 1; }

require_cmd() {
  command -v "$1" >/dev/null 2>&1 || die "未找到命令 '$1'。$2"
}

require_file() {
  [ -f "$1" ] || die "$2（缺少文件：$1）"
}

safe_rm_rf() {
  local target="$1"
  case "$target" in
    "$ROOT"/*) rm -rf -- "$target" ;;
    *) die "拒绝删除仓库之外的路径：$target" ;;
  esac
}

# 统计目录下普通文件数量。用 bash globstar 而不是 find：Windows Git Bash 里
# find 可能解析成 C:\Windows\System32\FIND.EXE（会报 “Parameter format not correct”）。
count_files() {
  local dir="$1" n=0 f
  shopt -s globstar nullglob dotglob
  for f in "$dir"/**/*; do
    [ -f "$f" ] && n=$((n + 1))
  done
  shopt -u globstar nullglob dotglob
  printf '%s' "$n"
}

# 把面板构建产物同步到 Go 内嵌目录。不依赖 GNU find，保证 Git Bash / Ubuntu / CI 行为一致。
sync_panel_dist() {
  local src="$1"
  mkdir -p "$BACKEND_DIST_DIR"
  rm -rf -- "$BACKEND_DIST_DIR"
  mkdir -p -- "$BACKEND_DIST_DIR"
  cp -R "$src"/. "$BACKEND_DIST_DIR"/
  require_file "$BACKEND_DIST_DIR/index.html" '面板产物缺少 index.html，无法内嵌到后端'
  local asset_count
  asset_count="$(count_files "$BACKEND_DIST_DIR")"
  log_info "已复制 $asset_count 个文件到 $BACKEND_DIST_DIR"
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    -v|--version)
      [ "$#" -ge 2 ] || die "--version 需要一个参数"
      VERSION="$2"
      shift 2
      ;;
    --version=*)
      VERSION="${1#*=}"
      shift
      ;;
    --skip-panel)   SKIP_PANEL=1; shift ;;
    --skip-backend) SKIP_BACKEND=1; shift ;;
    --skip-core)    SKIP_CORE=1; shift ;;
    --skip-zip)     SKIP_ZIP=1; shift ;;
    --split)        SPLIT=1; shift ;;
    --clean)        CLEAN=1; shift ;;
    -h|--help)      usage; exit 0 ;;
    *) printf '未知参数：%s\n\n' "$1" >&2; usage >&2; exit 2 ;;
  esac
done

VERSION_SAFE="$(printf '%s' "$VERSION" | tr -c 'A-Za-z0-9._+-' '-')"

printf 'MProxyEvolution 构建脚本 (bash)\n'
printf '  仓库根目录：%s\n' "$ROOT"
printf '  构建版本：  %s\n' "$VERSION"

require_file "$ROOT/module/module.prop" "仓库结构不完整：module/module.prop"

# 产物命名统一为 KernelSU Modules 的规范：{id}-{versionCode}-{version}.zip
VERSION_CODE="$(sed -n 's/^versionCode=//p' "$ROOT/module/module.prop" | head -n 1 | tr -d '\r')"
case "$VERSION_CODE" in
  ''|*[!0-9]*) die "module/module.prop 的 versionCode 缺失或不是整数：'$VERSION_CODE'" ;;
esac

require_cmd git '请安装 Git。'

if [ "$CLEAN" -eq 1 ]; then
  log_step '清理 build/ 与 dist/'
  safe_rm_rf "$BUILD_DIR"
  safe_rm_rf "$DIST_DIR"
fi

# ---------------------------------------------------------------------------
# 0. submodule（只需要 mihomo；面板来自 panel.lock 检出的 fork worktree）
# ---------------------------------------------------------------------------
if [ "$SKIP_CORE" -eq 0 ] && [ ! -f "$MIHOMO_DIR/go.mod" ]; then
  log_step '初始化 git submodule（upstream/mihomo）'
  ( cd "$ROOT" && git submodule update --init --recursive )
fi

# ---------------------------------------------------------------------------
# 1. 面板构建
# ---------------------------------------------------------------------------
if [ "$SKIP_PANEL" -eq 0 ]; then
  log_step '组装面板（scripts/dev-panel.sh --prepare）'
  require_file "$SCRIPT_DIR/dev-panel.sh" '缺少 scripts/dev-panel.sh（由 UI 工程师维护）'
  ( cd "$ROOT" && bash "$SCRIPT_DIR/dev-panel.sh" --prepare )
  require_file "$PANEL_DIR/package.json" 'dev-panel 未生成 build/panel/package.json，面板组装失败'

  log_step '安装面板依赖（pnpm install --frozen-lockfile）'
  if [ -n "${MPROXY_NPM_REGISTRY:-}" ]; then
    export npm_config_registry="$MPROXY_NPM_REGISTRY"
    log_info "npm registry：$MPROXY_NPM_REGISTRY"
  fi

  if command -v pnpm >/dev/null 2>&1; then
    if ! ( cd "$PANEL_DIR" && pnpm install --frozen-lockfile ); then
      log_warn 'pnpm install --frozen-lockfile 失败，回退 --no-frozen-lockfile --ignore-scripts（build/panel 无 .git，husky prepare 可能失败）'
      ( cd "$PANEL_DIR" && pnpm install --no-frozen-lockfile --ignore-scripts )
    fi
    log_step '构建面板（pnpm build）'
    ( cd "$PANEL_DIR" && pnpm build )
  elif command -v npm >/dev/null 2>&1; then
    log_warn '未找到 pnpm，回退 npm'
    if [ -f "$PANEL_DIR/package-lock.json" ]; then
      ( cd "$PANEL_DIR" && npm ci )
    else
      ( cd "$PANEL_DIR" && npm install --no-audit --no-fund )
    fi
    log_step '构建面板（npm run build）'
    ( cd "$PANEL_DIR" && npm run build )
  else
    die '未找到 pnpm/npm。请安装 Node.js 20+ 与 pnpm 10+（npm i -g pnpm），或运行 corepack enable。'
  fi

  PANEL_DIST=""
  for candidate in "web/dist" "dist"; do
    if [ -f "$PANEL_DIR/$candidate/index.html" ]; then
      PANEL_DIST="$PANEL_DIR/$candidate"
      break
    fi
  done
  [ -n "$PANEL_DIST" ] || die '未找到面板构建产物（期望 build/panel/web/dist/index.html 或 build/panel/dist/index.html）'
  log_info "面板产物：$PANEL_DIST"

  log_step '复制面板产物到 backend/internal/webui/dist（保留一个 index.html）'
  sync_panel_dist "$PANEL_DIST"
else
  # 注意：--skip-panel 也必须把已有产物复制到后端嵌入目录，否则会内嵌占位页。
  PANEL_DIST=""
  for candidate in "web/dist" "dist"; do
    if [ -f "$PANEL_DIR/$candidate/index.html" ]; then
      PANEL_DIST="$PANEL_DIR/$candidate"
      break
    fi
  done
  [ -n "$PANEL_DIST" ] || die '--skip-panel 但 build/panel 中找不到已构建的面板产物。请先运行 scripts/dev-panel.sh --build'
  log_info "面板产物：$PANEL_DIST"
  log_step '复制面板产物到 backend/internal/webui/dist（保留一个 index.html）'
  sync_panel_dist "$PANEL_DIST"
fi

# ---------------------------------------------------------------------------
# 2. Go 后端（4 架构）
# ---------------------------------------------------------------------------
if [ "$SKIP_BACKEND" -eq 0 ]; then
  log_step '编译 Go 后端（4 架构）'
  require_cmd go '请安装 Go 1.26+。'
  require_file "$BACKEND_DIR/go.mod" 'backend/go.mod 不存在，后端尚未就绪'

  module_path="$(sed -n 's/^[[:space:]]*module[[:space:]][[:space:]]*\([^[:space:]]*\).*/\1/p' "$BACKEND_DIR/go.mod" | head -n 1)"
  [ -n "$module_path" ] || die '无法从 backend/go.mod 解析 module 路径'
  log_info "Go module：$module_path"

  # 后端实际符号 mproxy/internal/version.Version；其余候选由链接器静默忽略，兼容后续改动。
  LDFLAGS="-s -w -X ${module_path}/internal/version.Version=${VERSION}"
  LDFLAGS="$LDFLAGS -X ${module_path}/internal/buildinfo.Version=${VERSION}"
  LDFLAGS="$LDFLAGS -X main.version=${VERSION}"

  build_backend() {
    local abi="$1" goarch="$2" goarm="${3:-}"
    local out_dir="$BUILD_DIR/backend/$abi"
    local out="$out_dir/mproxy"
    mkdir -p "$out_dir"
    log_info "后端 -> linux/$goarch${goarm:+ (GOARM=$goarm)} => $out"
    local envs=(CGO_ENABLED=0 GOOS=linux GOARCH="$goarch")
    [ -n "$goarm" ] && envs+=(GOARM="$goarm")
    ( cd "$BACKEND_DIR" && env "${envs[@]}" go build -trimpath -ldflags "$LDFLAGS" -o "$out" ./cmd/mproxy )
    [ -f "$out" ] || die "后端编译产物缺失（linux/$goarch）"
  }

  build_backend arm64 arm64
  build_backend arm arm 7
  build_backend 386 386
  build_backend amd64 amd64
fi

# ---------------------------------------------------------------------------
# 3. mihomo 内核（4 架构，静态 linux / with_gvisor）
#    android/arm 需要 cgo；为保持 CGO_ENABLED=0，统一构建 linux 静态二进制。
# ---------------------------------------------------------------------------
if [ "$SKIP_CORE" -eq 0 ]; then
  log_step '编译 mihomo 内核（4 架构，with_gvisor）'
  require_cmd go '请安装 Go 1.26+。'
  require_file "$MIHOMO_DIR/go.mod" 'upstream/mihomo 子模块未就绪，请执行 git submodule update --init --recursive'

  mihomo_tag="$(git -C "$MIHOMO_DIR" describe --tags --always 2>/dev/null || true)"
  if [ -z "$mihomo_tag" ]; then
    mihomo_tag="v0.0.0"
    log_warn "无法从 upstream/mihomo 获取版本 tag，回退为 $mihomo_tag"
  fi
  log_info "mihomo 版本：$mihomo_tag"

  CORE_LDFLAGS="-s -w -X github.com/metacubex/mihomo/constant.Version=${mihomo_tag}"

  build_core() {
    local abi="$1" goarch="$2" goarm="${3:-}"
    local out_dir="$BUILD_DIR/core/$abi"
    local out="$out_dir/mihomo"
    mkdir -p "$out_dir"
    log_info "内核 -> linux/$goarch${goarm:+ (GOARM=$goarm)} => $out"
    local envs=(CGO_ENABLED=0 GOOS=linux GOARCH="$goarch")
    [ -n "$goarm" ] && envs+=(GOARM="$goarm")
    ( cd "$MIHOMO_DIR" && env "${envs[@]}" go build -tags with_gvisor -trimpath -ldflags "$CORE_LDFLAGS" -o "$out" . )
    [ -f "$out" ] || die "内核编译产物缺失（linux/$goarch）"
  }

  build_core arm64 arm64
  build_core arm arm 7
  build_core 386 386
  build_core amd64 amd64
fi

# ---------------------------------------------------------------------------
# 4. 组装 build/module-root
# ---------------------------------------------------------------------------
log_step '组装模块目录 build/module-root'
safe_rm_rf "$MODULE_ROOT"
mkdir -p "$MODULE_ROOT"
cp -R "$MODULE_SOURCE_DIR"/. "$MODULE_ROOT"/
require_file "$MODULE_ROOT/module.prop" 'module/module.prop 缺失，模块目录组装失败'
log_info "已复制 module/** -> $MODULE_ROOT"

# 第三方许可证与 Notice 必须随模块包分发（GPL-3.0 合规）
require_file "$ROOT/THIRD_PARTY_NOTICES.txt" '仓库根缺少 THIRD_PARTY_NOTICES.txt'
cp -f "$ROOT/THIRD_PARTY_NOTICES.txt" "$MODULE_ROOT/THIRD_PARTY_NOTICES.txt"
require_file "$MODULE_ROOT/THIRD_PARTY_NOTICES.txt" 'THIRD_PARTY_NOTICES.txt 复制进 module-root 失败'
log_info "已复制 THIRD_PARTY_NOTICES.txt -> $MODULE_ROOT"

copied_bins=0
for abi in arm64 arm 386 amd64; do
  src="$BUILD_DIR/backend/$abi/mproxy"
  if [ -f "$src" ]; then
    mkdir -p "$MODULE_ROOT/bin/$abi"
    cp -f "$src" "$MODULE_ROOT/bin/$abi/mproxy"
    copied_bins=$((copied_bins + 1))
  elif [ "$SKIP_BACKEND" -eq 0 ]; then
    die "缺少后端产物：$src"
  else
    log_warn "缺少后端产物：$src（--skip-backend 模式，zip 将不完整）"
  fi

  core_src="$BUILD_DIR/core/$abi/mihomo"
  if [ -f "$core_src" ]; then
    mkdir -p "$MODULE_ROOT/bin/core/$abi"
    cp -f "$core_src" "$MODULE_ROOT/bin/core/$abi/mihomo"
    copied_bins=$((copied_bins + 1))
  elif [ "$SKIP_CORE" -eq 0 ]; then
    die "缺少内核产物：$core_src"
  else
    log_warn "缺少内核产物：$core_src（--skip-core 模式，zip 将不完整）"
  fi
done
log_info "已放入 $copied_bins 个二进制（期望 8 个）"

# 把构建版本写入模块 module.prop 的 version（源文件保持 0.1.0 不变）
prop_file="$MODULE_ROOT/module.prop"
tmp_prop="$(mktemp)"
MPROXY_BUILD_VERSION="$VERSION" awk '
  BEGIN { v = ENVIRON["MPROXY_BUILD_VERSION"] }
  /^version=/ { print "version=" v; next }
  { print }
' "$prop_file" > "$tmp_prop"
cat "$tmp_prop" > "$prop_file"
rm -f "$tmp_prop"
log_info "module.prop version -> $VERSION"

if [ "$SKIP_ZIP" -eq 1 ]; then
  log_step '已跳过打包（--skip-zip）'
  printf '模块目录：%s\n' "$MODULE_ROOT"
  exit 0
fi

# ---------------------------------------------------------------------------
# 5. 打包 zip + sha256（universal 始终产出；--split 额外产出 4 个单架构包）
# ---------------------------------------------------------------------------
# 列出 zip 内条目。优先用 unzip；构建环境可能只有 python3（无 zip/unzip），
# 因此用 zipfile 兜底，避免单架构包结构校验引入新的硬依赖。
zip_list() {
  local zip_file="$1"
  if command -v unzip >/dev/null 2>&1; then
    unzip -l "$zip_file" 2>/dev/null
  else
    python3 - "$zip_file" <<'PY'
import sys
import zipfile

with zipfile.ZipFile(sys.argv[1]) as zf:
    for name in zf.namelist():
        print(name)
PY
  fi
}

# 创建 zip。额外参数是排除模式（传给 zip -x；python 兜底用 fnmatch 过滤）。
create_zip() {
  local out="$1"
  shift
  rm -f "$out"
  if command -v zip >/dev/null 2>&1; then
    if [ "$#" -gt 0 ]; then
      ( cd "$MODULE_ROOT" && zip -r -9 -q "$out" . -x "$@" )
    else
      ( cd "$MODULE_ROOT" && zip -r -9 -q "$out" . )
    fi
  else
    log_warn '未找到 zip 命令，使用 python3 zipfile 打包'
    require_cmd python3 '请安装 zip 或 python3。'
    python3 - "$MODULE_ROOT" "$out" "$@" <<'PY'
import fnmatch
import os
import sys
import zipfile

root, out = sys.argv[1], sys.argv[2]
excludes = sys.argv[3:]


def excluded(rel):
    return any(fnmatch.fnmatchcase(rel, pat) for pat in excludes)


with zipfile.ZipFile(out, 'w', zipfile.ZIP_DEFLATED) as zf:
    for base, _dirs, files in os.walk(root):
        for name in sorted(files):
            full = os.path.join(base, name)
            rel = os.path.relpath(full, root).replace(os.sep, '/')
            if excluded(rel):
                continue
            zf.write(full, rel)
PY
  fi
  [ -f "$out" ] || die "打包失败：$out 不存在"
}

sha256_file() {
  local file="$1" dir name
  dir="$(dirname "$file")"
  name="$(basename "$file")"
  if command -v sha256sum >/dev/null 2>&1; then
    ( cd "$dir" && sha256sum "$name" > "$name.sha256" )
  elif command -v shasum >/dev/null 2>&1; then
    ( cd "$dir" && shasum -a 256 "$name" > "$name.sha256" )
  else
    die '未找到 sha256sum / shasum，无法生成校验和。'
  fi
}

# 单架构包结构校验：必须含目标 ABI 的后端与内核，且不能残留其它 ABI 目录。
validate_split_zip() {
  local zip_file="$1" target_dir="$2" listing entry other bad
  listing="$(zip_list "$zip_file")"
  [ -n "$listing" ] || die "无法读取架构包内容：$zip_file"
  for entry in "bin/$target_dir/mproxy" "bin/core/$target_dir/mihomo"; do
    case "$listing" in
      *"$entry"*) ;;
      *) die "架构包缺少 $entry：$zip_file" ;;
    esac
  done
  for other in arm64 arm 386 amd64; do
    [ "$other" = "$target_dir" ] && continue
    for bad in "bin/$other/" "bin/core/$other/"; do
      case "$listing" in
        *"$bad"*) die "架构包不应包含 $bad：$zip_file" ;;
      esac
    done
  done
  log_info "架构包结构校验通过：$target_dir"
}

log_step '打包 dist/MProxyEvolution-<versionCode>-<version>.zip'
mkdir -p "$DIST_DIR"
zip_name="MProxyEvolution-${VERSION_CODE}-${VERSION_SAFE}.zip"
zip_path="$DIST_DIR/$zip_name"
create_zip "$zip_path"
sha256_file "$zip_path"

split_paths=()
if [ "$SPLIT" -eq 1 ]; then
  log_step '打包单架构包（--split）'
  # ABI 目录名:发布资产后缀（arm64→arm64-v8a、arm→armeabi-v7a、386→x86、amd64→x86_64）
  for pair in arm64:arm64-v8a arm:armeabi-v7a 386:x86 amd64:x86_64; do
    abi_dir="${pair%%:*}"
    abi_tag="${pair#*:}"
    split_name="MProxyEvolution-${VERSION_CODE}-${VERSION_SAFE}-${abi_tag}.zip"
    split_path="$DIST_DIR/$split_name"
    excludes=()
    for other in arm64 arm 386 amd64; do
      [ "$other" = "$abi_dir" ] && continue
      excludes+=("bin/$other/" "bin/$other/*" "bin/core/$other/" "bin/core/$other/*")
    done
    create_zip "$split_path" "${excludes[@]}"
    validate_split_zip "$split_path" "$abi_dir"
    sha256_file "$split_path"
    split_paths+=("$split_path")
    log_info "已生成 $split_name（$(du -h "$split_path" | cut -f1)）"
  done
fi

printf '\n构建完成 ✔\n'
printf '  universal 包：%s\n' "$zip_path"
printf '  universal 校验和：%s\n' "$zip_path.sha256"
printf '  universal 大小：  %s\n' "$(du -h "$zip_path" | cut -f1)"
if [ "$SPLIT" -eq 1 ]; then
  for split_path in "${split_paths[@]}"; do
    printf '  单架构包：      %s\n' "$split_path"
    printf '  单架构校验和：  %s\n' "$split_path.sha256"
    printf '  单架构大小：    %s\n' "$(du -h "$split_path" | cut -f1)"
  done
fi
