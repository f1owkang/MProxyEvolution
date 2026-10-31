#!/usr/bin/env bash
# MProxyEvolution installer / packaging regression tests.
#
# Run:  bash tests/scripts_test.sh
# Designed to pass on Ubuntu (CI) and Git Bash on Windows (no external tools
# beyond bash/coreutils/GNU grep/sed that both environments provide).
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

passed=0
failed=0

pass() {
  passed=$((passed + 1))
  printf 'ok   %s\n' "$*"
}

fail() {
  failed=$((failed + 1))
  printf 'FAIL %s\n' "$*" >&2
}

check() {
  # check <description> <command...>
  local desc="$1"
  shift
  if "$@" >/dev/null 2>&1; then
    pass "$desc"
  else
    fail "$desc"
  fi
}

prop_get() {
  sed -n "s/^$1=//p" module/module.prop | head -n 1 | tr -d '\r'
}

# ---------------------------------------------------------------------------
# 1. module.prop: exists, LF / no BOM, required keys, X.Y.Z version
# ---------------------------------------------------------------------------
check 'module/module.prop exists' test -f module/module.prop

if [ -f module/module.prop ]; then
  check 'module.prop has no UTF-8 BOM' \
    test "$(head -c 3 module/module.prop | od -An -tx1 | tr -d ' \n')" != "efbbbf"
  check 'module.prop has no CRLF line endings' \
    bash -c '! LC_ALL=C grep -q $'"'"'\r'"'"' module/module.prop'

  for key in id name version versionCode updateJson; do
    check "module.prop contains key '$key'" \
      grep -Eq "^$key=.+" module/module.prop
  done

  prop_version="$(prop_get version)"
  prop_code="$(prop_get versionCode)"
  check 'module.prop version is X.Y.Z without a leading v' \
    grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+([-+][0-9A-Za-z.-]+)?$' <<<"$prop_version"
  check 'module.prop versionCode is a positive integer' \
    grep -Eq '^[1-9][0-9]*$' <<<"$prop_code"

  check 'module.prop updateJson points at raw.githubusercontent.com' \
    grep -Eq '^updateJson=https://raw\.githubusercontent\.com/' module/module.prop
else
  prop_version=""
  prop_code=""
fi

expected_zip="MProxyEvolution-${prop_code:-0}-${prop_version:-0.0.0}.zip"

# ---------------------------------------------------------------------------
# 2. panel.lock: first line is a 40-char lowercase commit sha
# ---------------------------------------------------------------------------
check 'panel.lock exists' test -f panel.lock
check 'panel.lock first line is a 40-char lowercase commit sha' \
  bash -c 'head -n 1 panel.lock | grep -Eq "^[0-9a-f]{40}$"'

# ---------------------------------------------------------------------------
# 3. Release asset naming consistency across build scripts / metadata / CI
# ---------------------------------------------------------------------------
check 'build.sh names the zip MProxyEvolution-<versionCode>-<version>.zip' \
  grep -qF 'zip_name="MProxyEvolution-${VERSION_CODE}-${VERSION_SAFE}.zip"' scripts/build.sh
check 'build.ps1 names the zip MProxyEvolution-<versionCode>-<version>.zip' \
  grep -qF '$zipName = "MProxyEvolution-$VersionCode-$VersionSafe.zip"' scripts/build.ps1
check "update.json zipUrl matches $expected_zip" \
  grep -qF "/releases/download/v${prop_version}/${expected_zip}" update.json
check 'module.yaml release.assetPattern matches MProxyEvolution-*.zip' \
  grep -qF 'assetPattern: "MProxyEvolution-*.zip"' module.yaml
check 'ci.yml validates update.json against MProxyEvolution-<code>-<version>.zip' \
  grep -qF "expected_zip = f'MProxyEvolution-{update_code}-{update_version}.zip'" .github/workflows/ci.yml

# ---------------------------------------------------------------------------
# 4. Installer unzip list ships the panel icon and the license notice
# ---------------------------------------------------------------------------
check "customize.sh unzip list includes 'webroot/icon.png'" \
  grep -qF "'webroot/icon.png'" module/customize.sh
check "customize.sh unzip list includes 'THIRD_PARTY_NOTICES.txt'" \
  grep -qF "'THIRD_PARTY_NOTICES.txt'" module/customize.sh

# ---------------------------------------------------------------------------
# 5. Device-side / build scripts: no CRLF, valid bash syntax
# ---------------------------------------------------------------------------
for f in module/*.sh scripts/*.sh; do
  check "no CRLF line endings in $f" \
    bash -c '! LC_ALL=C grep -q $'"'"'\r'"'"' '"$f"''
  check "bash -n $f" bash -n "$f"
done

# ---------------------------------------------------------------------------
# 6. per-ABI package split (universal + 4 single-architecture packages)
# ---------------------------------------------------------------------------
check 'build.sh supports --split' \
  grep -qF -- '--split' scripts/build.sh
check 'build.sh maps all 4 ABIs to release asset tags' \
  bash -c 'grep -qF "arm64:arm64-v8a" scripts/build.sh \
    && grep -qF "arm:armeabi-v7a" scripts/build.sh \
    && grep -qF "386:x86" scripts/build.sh \
    && grep -qF "amd64:x86_64" scripts/build.sh'
check 'build.sh no longer supports --slim' \
  bash -c '! grep -qF -- "--slim" scripts/build.sh'
check 'build.sh emits per-ABI zip names' \
  grep -qF 'split_name="MProxyEvolution-${VERSION_CODE}-${VERSION_SAFE}-${abi_tag}.zip"' scripts/build.sh
check 'build.ps1 supports -Split' \
  grep -qF '[switch]$Split' scripts/build.ps1
check 'build.ps1 no longer supports -Slim' \
  bash -c '! grep -qF -- "-Slim" scripts/build.ps1'
check 'build.ps1 maps all 4 ABIs to release asset tags' \
  bash -c 'grep -qF "arm64-v8a" scripts/build.ps1 \
    && grep -qF "armeabi-v7a" scripts/build.ps1 \
    && grep -qF "x86_64" scripts/build.ps1'
check 'ci.yml builds split packages' \
  grep -qF 'build.sh --split' .github/workflows/ci.yml
check 'ci.yml verifies per-ABI package layout' \
  grep -qF 'per-ABI zips' .github/workflows/ci.yml
check 'release.yml builds split packages' \
  grep -qF 'build.sh --split' .github/workflows/release.yml
check 'release.yml exports all 4 per-ABI asset names' \
  bash -c 'grep -qF "ASSET_ARM64=" .github/workflows/release.yml \
    && grep -qF "ASSET_ARMV7=" .github/workflows/release.yml \
    && grep -qF "ASSET_X86=" .github/workflows/release.yml \
    && grep -qF "ASSET_X8664=" .github/workflows/release.yml'
check 'release.yml uploads the per-ABI zips and checksums' \
  bash -c 'grep -qF "dist/\${{ env.ASSET_ARM64 }}.sha256" .github/workflows/release.yml \
    && grep -qF "dist/\${{ env.ASSET_X8664 }}.sha256" .github/workflows/release.yml'
check 'README.md documents per-ABI packages' \
  bash -c 'grep -qF -- "-arm64-v8a.zip" README.md \
    && grep -qF -- "-x86_64.zip" README.md \
    && grep -qF "ro.product.cpu.abi" README.md'
check 'README_EN.md documents per-ABI packages' \
  bash -c 'grep -qF -- "-arm64-v8a.zip" README_EN.md \
    && grep -qF -- "-x86_64.zip" README_EN.md \
    && grep -qF "ro.product.cpu.abi" README_EN.md'
check 'customize.sh hints per-ABI mismatch' \
  grep -qF '该包只包含其它架构' module/customize.sh
check 'action.sh uses mproxy action (blocking startup)' \
  grep -qF '"$MPROXY_BIN" action' module/action.sh
check 'action.sh wraps action in a 180s timeout' \
  grep -qF 'timeout 180 "$MPROXY_BIN" action' module/action.sh
check 'action.sh no longer invokes mproxy toggle directly' \
  bash -c '! grep -qF "\"$MPROXY_BIN\" toggle" module/action.sh'
check 'no slim variant references remain in packaging files' \
  bash -c '! grep -riq slim scripts/build.sh scripts/build.ps1 .github/workflows/ci.yml .github/workflows/release.yml README.md README_EN.md module/customize.sh'

# ---------------------------------------------------------------------------
# Summary
# ---------------------------------------------------------------------------
printf '\n%d checks passed, %d failed\n' "$passed" "$failed"
if [ "$failed" -ne 0 ]; then
  exit 1
fi
