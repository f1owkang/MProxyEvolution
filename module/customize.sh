#!/system/bin/sh
# MProxyEvolution 安装脚本（Magisk / KernelSU 通用）
#
# 与 CRecordEvolution 保持一致的安装体验：
#   - 8~14 行中文/英文双语输出，带作者、版本、设备信息；
#   - 音量键选择语言 + 二次确认；无按键工具、超时未按键或显式 MPROXY_HEADLESS=1 时自动安装；
#   - 只按当前设备 ABI 解压，四架构包不会把全部二进制写进模块目录；
#   - 已有 config.yaml 绝不覆盖，升级前先停旧进程。

SKIPUNZIP=1

MPROXY_DATA="/data/adb/mproxy"
AUTHOR="f1owkang"

say()
{
	if type ui_print >/dev/null 2>&1; then
		ui_print "$*"
	else
		echo "$*"
	fi
}

die()
{
	if type abort >/dev/null 2>&1; then
		abort "$*"
	else
		# 没有 abort 的兜底环境必须真正终止，否则带错误继续安装会破坏模块目录。
		echo "! $*" >&2
		exit 1
	fi
}

L()
{
	if [ "$T" = en ]; then printf '%s' "$2"; else printf '%s' "$1"; fi
}

rule()
{
	say "------------------------------------------------"
}

# 读一次音量键：UP / DOWN / 空。只采纳按下事件，忽略抬起，避免下一步误判。
read_key()
{
	_rk_limit=${1:-10}
	_rk_n=0
	while [ "$_rk_n" -lt "$_rk_limit" ]; do
		_rk_ev=`timeout 3 getevent -qlc 1 2>/dev/null`
		_rk_key=`printf '%s\n' "$_rk_ev" | awk '{ print $3 }'`
		_rk_state=`printf '%s\n' "$_rk_ev" | awk '{ print $4 }'`
		case "$_rk_key" in
		KEY_VOLUMEUP|KEY_VOLUMEDOWN)
			case "$_rk_state" in
			UP|up|0|00000000) ;;
			*)
				case "$_rk_key" in
				KEY_VOLUMEUP) echo UP ;;
				*) echo DOWN ;;
				esac
				return 0
				;;
			esac
			;;
		esac
		_rk_n=$((_rk_n + 1))
	done
	echo ""
}

[ -n "${MODPATH:-}" ] || die "安装失败：MODPATH 未设置 / MODPATH not set"
[ -n "${ZIPFILE:-}" ] && [ -f "${ZIPFILE:-}" ] || die "安装失败：找不到模块压缩包 / ZIPFILE missing"

# BOOTMODE：Magisk / KernelSU / APatch 的安装器会设为 true。
# 显式 false/0 说明不是在管理器安装环境里执行，继续写模块目录可能破坏系统；未设置时兼容旧安装器，
# 只警告后继续（也便于本地调试）。
case "${BOOTMODE:-}" in
true|TRUE|True) ;;
false|FALSE|False|0)
	die "请从 Magisk / KernelSU / APatch 管理器安装 / Please install from a Magisk / KernelSU / APatch manager" ;;
'')
	say "! 警告：BOOTMODE 未设置，继续尝试安装 / WARNING: BOOTMODE is not set, continuing anyway" ;;
*)
	say "! 警告：未识别的 BOOTMODE=${BOOTMODE}，继续尝试安装 / WARNING: unrecognized BOOTMODE, continuing anyway" ;;
esac

command -v unzip >/dev/null 2>&1 || die "缺少 unzip / unzip not found"

# 解压前先从 zip 读取 module.prop，保证标题和版本可用。
MODID=`unzip -p "$ZIPFILE" module.prop 2>/dev/null | sed -n 's/^id=//p' | head -n1`
MODNAME=`unzip -p "$ZIPFILE" module.prop 2>/dev/null | sed -n 's/^name=//p' | head -n1`
MODVER=`unzip -p "$ZIPFILE" module.prop 2>/dev/null | sed -n 's/^version=//p' | head -n1`
MODCODE=`unzip -p "$ZIPFILE" module.prop 2>/dev/null | sed -n 's/^versionCode=//p' | head -n1`
[ -n "$MODID" ] || MODID="MProxyEvolution"
[ -n "$MODNAME" ] || MODNAME="MihomoProxy Evolution"
[ -n "$MODVER" ] || MODVER="unknown"
[ -n "$MODCODE" ] || MODCODE="0"

# 识别设备 ABI
ABI="$(getprop ro.product.cpu.abi 2>/dev/null)"
[ -n "$ABI" ] || ABI="$(getprop ro.product.cpu.abilist 2>/dev/null | cut -d, -f1)"
[ -n "$ABI" ] || die "无法读取设备 ABI / Cannot read device ABI"
case "$ABI" in
arm64-v8a) ABI_NAME="arm64" ;;
armeabi-v7a|armeabi) ABI_NAME="arm" ;;
x86) ABI_NAME="386" ;;
x86_64) ABI_NAME="amd64" ;;
*) die "不支持的 ABI：$ABI / unsupported ABI: $ABI" ;;
esac

# 语言：升级沿用旧文件，否则跟随系统，默认中文。
T=zh
oldlang=`cat "/data/adb/modules/$MODID/data/lang" 2>/dev/null | tr -d '\r\n'`
case "$oldlang" in
zh*) T=zh ;;
en*) T=en ;;
*)
	for p in persist.sys.locale ro.product.locale persist.sys.language; do
		v=`getprop "$p" 2>/dev/null`
		if [ -n "$v" ]; then
			case "$v" in zh*) T=zh ;; *) T=en ;; esac
			break
		fi
	done
	;;
esac

device=`getprop ro.product.device 2>/dev/null`
android=`getprop ro.build.version.release 2>/dev/null`
[ -n "$android" ] && android="Android $android"

rule
say "$MODNAME  v$MODVER($MODCODE) · $ABI_NAME"
say "$(L "作者：$AUTHOR" "by $AUTHOR")"
rule
say "$(L "设备：" "Device: ")${device:-unknown}${android:+ · $android}"

# APatch 使用 /data/adb/apd 作为守护进程标识；检测到就提示，属于尽力支持。
if [ -e /data/adb/apd ]; then
	say "$(L "检测到 APatch 管理器（尽力支持）" "Detected APatch manager (best-effort support)")"
fi

# 显式非交互安装（ksud/脚本/CI 可设置 MPROXY_HEADLESS=1）直接跳过按键流程。
# 注意：不能仅凭 stdin 不是终端就判定无交互——Magisk/KernelSU 管理器安装时 stdin 通常也不是
# tty，但用户仍可用音量键；因此交互流程只由“能否读取按键”和显式环境变量决定。
# 未按键超时也会自动继续安装（见下方确认步骤），避免无人值守安装被卡住后 abort。
headless=0
if [ "${MPROXY_HEADLESS:-0}" = "1" ]; then
	headless=1
fi

# 两步按键：选语言（12s 超时跟随系统）→ 确认安装（30s 超时自动安装，音量- 取消）。
lang_chosen=0
if [ "$headless" = 1 ]; then
	say "非交互模式：跳过按键确认，自动安装 / non-interactive: installing automatically"
elif command -v getevent >/dev/null 2>&1 && command -v timeout >/dev/null 2>&1; then
	say "[1/2] 选择语言 / Choose language"
	say "    音量+  中文 / Volume+ Chinese"
	say "    音量-  English / Volume- English"
	say "    12 秒后跟随系统 / follows system after 12s"
	case `read_key 4` in
	UP) T=zh; lang_chosen=1 ;;
	DOWN) T=en; lang_chosen=1 ;;
	esac
	say "$(L "[2/2] 确认安装（30 秒未按键则自动安装）" "[2/2] Confirm install (auto-install after 30s)")"
	say "$(L "    音量+ 立即安装 / 音量- 取消" "    Volume+ install / Volume- cancel")"
	case `read_key 10` in
	DOWN) die "$(L "已取消安装" "Installation cancelled")" ;;
	esac
else
	say "    未检测到按键工具，自动安装 / no key tools, installing automatically"
fi

say "$(L "正在写入模块…" "Installing module...")"

# ---------------------------------------------------------------------------
# 按 ABI 解压
# ---------------------------------------------------------------------------
unzip -o "$ZIPFILE" \
	'module.prop' 'customize.sh' 'service.sh' 'post-fs-data.sh' 'action.sh' 'uninstall.sh' \
	'webroot/*' 'webroot/icon.png' 'THIRD_PARTY_NOTICES.txt' \
	"bin/$ABI_NAME/*" "bin/core/$ABI_NAME/*" \
	-d "$MODPATH" >&2 || true

MPROXY_SRC="$MODPATH/bin/$ABI_NAME/mproxy"
MIHOMO_SRC="$MODPATH/bin/core/$ABI_NAME/mihomo"
if [ ! -f "$MPROXY_SRC" ] || [ ! -f "$MIHOMO_SRC" ]; then
	unzip -o "$ZIPFILE" 'bin/*' -d "$MODPATH" >&2 || true
fi
[ -f "$MODPATH/module.prop" ] || die "安装包损坏：缺少 module.prop / corrupt package"
[ -f "$MPROXY_SRC" ] || die "安装包缺少 $ABI_NAME mproxy / missing mproxy"
[ -f "$MIHOMO_SRC" ] || die "安装包缺少 $ABI_NAME mihomo / missing mihomo"

# 删除其他架构
if [ -d "$MODPATH/bin" ]; then
	for dir in "$MODPATH/bin"/*; do
		[ -d "$dir" ] || continue
		name="$(basename "$dir")"
		[ "$name" = "core" ] && continue
		[ "$name" = "$ABI_NAME" ] && continue
		rm -rf "$dir"
	done
fi
if [ -d "$MODPATH/bin/core" ]; then
	for dir in "$MODPATH/bin/core"/*; do
		[ -d "$dir" ] || continue
		[ "$(basename "$dir")" = "$ABI_NAME" ] && continue
		rm -rf "$dir"
	done
fi
rm -rf "$MODPATH/META-INF" 2>/dev/null || true

# 保存语言偏好，供下次升级沿用
if [ "$lang_chosen" = 1 ]; then
	mkdir -p "$MODPATH/data"
	printf '%s\n' "$T" > "$MODPATH/data/lang"
fi

# ---------------------------------------------------------------------------
# 数据目录与旧配置
# ---------------------------------------------------------------------------
mkdir -p "$MPROXY_DATA/bin" "$MPROXY_DATA/state" "$MPROXY_DATA/logs" \
	"$MPROXY_DATA/cache" "$MPROXY_DATA/providers" "$MPROXY_DATA/ruleset" \
	|| die "创建数据目录失败 / cannot create data dir"

if [ -x "$MPROXY_DATA/bin/mproxy" ]; then
	"$MPROXY_DATA/bin/mproxy" stop >/dev/null 2>&1 || true
	sleep 1
fi

cp -f "$MPROXY_SRC" "$MPROXY_DATA/bin/mproxy.new" || die "复制 mproxy 失败 / copy mproxy failed"
chmod 0755 "$MPROXY_DATA/bin/mproxy.new" || die "设置 mproxy 权限失败 / chmod mproxy failed"
mv -f "$MPROXY_DATA/bin/mproxy.new" "$MPROXY_DATA/bin/mproxy" || die "安装 mproxy 失败 / install mproxy failed"

cp -f "$MIHOMO_SRC" "$MPROXY_DATA/bin/mihomo.new" || die "复制 mihomo 失败 / copy mihomo failed"
chmod 0755 "$MPROXY_DATA/bin/mihomo.new" || die "设置 mihomo 权限失败 / chmod mihomo failed"
mv -f "$MPROXY_DATA/bin/mihomo.new" "$MPROXY_DATA/bin/mihomo" || die "安装 mihomo 失败 / install mihomo failed"

if [ -f "$MPROXY_DATA/config.yaml" ]; then
	say "$(L "保留已有 config.yaml" "Keeping existing config.yaml")"
else
	say "$(L "首次安装，配置将在启动时生成" "First install, config will be generated on boot")"
fi

chown -R 0:0 "$MPROXY_DATA" 2>/dev/null || true
chmod 0755 "$MPROXY_DATA" "$MPROXY_DATA/bin" "$MPROXY_DATA/logs" \
	"$MPROXY_DATA/cache" "$MPROXY_DATA/providers" "$MPROXY_DATA/ruleset" 2>/dev/null || true
chmod 0700 "$MPROXY_DATA/state" 2>/dev/null || true
[ -f "$MPROXY_DATA/state/token" ] && chmod 0600 "$MPROXY_DATA/state/token" 2>/dev/null || true

find "$MODPATH" -type f -name '*.sh' -exec chmod 0755 {} \; 2>/dev/null || true
chmod 0755 "$MPROXY_SRC" "$MIHOMO_SRC" 2>/dev/null || true

# SELinux：尽力把模块文件标记为 system_file，部分 ROM 的严格策略会拒绝读取其它上下文的文件。
# chcon 不可用或失败都不阻塞安装（模块仍可通过 mksh 执行）。
if command -v chcon >/dev/null 2>&1; then
	chcon -R u:object_r:system_file:s0 "$MODPATH" 2>/dev/null || true
fi

# ---------------------------------------------------------------------------
# 结果
# ---------------------------------------------------------------------------
rule
say "$(L "安装完成 · 重启后生效" "Done · reboot to activate")"
say "$(L "提示：重启后打开模块 WebUI 即可管理" "Tip: open the module WebUI after reboot")"

# customize.sh 会被安装器 source，不要写 exit。
