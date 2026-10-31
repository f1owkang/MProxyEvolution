#!/usr/bin/env bash
#
# Materialize the MProxyEvolution zashboard fork for building.
# The fork keeps MProxyEvolution commits on the `panel` branch; panel.lock pins the
# exact commit. Upstream sync is a `git merge upstream/<tag>` on that branch,
# so there is no text patch to re-apply.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
PANEL="$ROOT/build/panel"
LOCK="$ROOT/panel.lock"
INSTALL=0
BUILD=0
REGISTRY="${MPROXY_NPM_REGISTRY:-https://registry.npmmirror.com}"

step() { printf '[dev-panel] %s\n' "$*"; }
fail() { printf '[dev-panel] ERROR: %s\n' "$*" >&2; exit 1; }

for arg in "$@"; do
  case "$arg" in
    --prepare) ;;
    --install) INSTALL=1 ;;
    --build) INSTALL=1; BUILD=1 ;;
    --all) INSTALL=1; BUILD=1 ;;
    --registry=*) REGISTRY="${arg#*=}" ;;
    -h|--help)
      echo "用法: $0 [--prepare|--install|--build|--all] [--registry=URL]"
      exit 0
      ;;
    *) fail "未知参数: $arg" ;;
  esac
done

[ -f "$LOCK" ] || fail "缺少 $LOCK"
COMMIT="$(awk 'NF{print $1; exit}' "$LOCK")"
[ -n "$COMMIT" ] || fail "panel.lock 第一行必须是 commit"

cd "$ROOT"
if ! git cat-file -e "${COMMIT}^{commit}" 2>/dev/null; then
  step "fetching missing panel commit $COMMIT"
  git fetch origin '+refs/heads/panel:refs/remotes/origin/panel'
fi
if [ -e "$PANEL" ]; then
  git worktree remove --force "$PANEL" >/dev/null 2>&1 || rm -rf "$PANEL"
fi
mkdir -p "$ROOT/build"
step "checking out panel fork $COMMIT"
git worktree add --detach "$PANEL" "$COMMIT" >/dev/null

if [ "$INSTALL" = "1" ]; then
  cd "$PANEL"
  step "pnpm install"
  if ! pnpm install --frozen-lockfile "--registry=$REGISTRY"; then
    step "frozen install failed; retry --no-frozen-lockfile --ignore-scripts"
    pnpm install --no-frozen-lockfile --ignore-scripts "--registry=$REGISTRY"
  fi
fi

if [ "$BUILD" = "1" ]; then
  cd "$PANEL"
  step "pnpm build"
  pnpm run build
  [ -f "$PANEL/web/dist/index.html" ] || fail "build did not produce web/dist/index.html"
  step "panel build ready: $PANEL/web/dist/index.html"
fi

step "done"
