#!/bin/bash
set -euo pipefail
PATH=/usr/bin:/bin:/usr/sbin:/sbin
export PATH
FLEET_INSTALL_ORIGIN=__FLEET_INSTALL_ORIGIN__
fail() { printf '安装未完成：%s\n' "$*" >&2; exit 1; }
[[ "$(/usr/bin/uname -s)" == Darwin ]] || fail '此安装命令仅适用于 macOS。'
[[ "$(/usr/bin/id -u)" != 0 ]] || fail '请以当前登录用户运行，不要使用 sudo。'
ARCH="$(/usr/bin/uname -m)"
[[ "$ARCH" == arm64 || "$ARCH" == x86_64 ]] || fail '不支持此 Mac 架构。'
SYSTEM="$(/usr/bin/sw_vers -productVersion)"
version_at_least() {
  /usr/bin/awk -v actual="$1" -v required="$2" 'BEGIN {
    split(actual,a,"."); split(required,b,".");
    for (i=1;i<=3;i++) { if (a[i]+0>b[i]+0) exit 0; if (a[i]+0<b[i]+0) exit 1 }
    exit 0
  }'
}
version_at_least "$SYSTEM" 13.0 || fail '需要 macOS 13 或更新版本。'
WORK="$(/usr/bin/mktemp -d /private/tmp/fleet-native-install.XXXXXX)"
trap '/bin/rm -rf "$WORK"' EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
umask 077
read_value() { /usr/bin/plutil -extract "$1" raw -o - "$2"; }
download() {
  local status
  status="$(/usr/bin/curl --fail --silent --show-error --proto '=https' --tlsv1.2 --connect-timeout 15 --max-time "$4" --max-filesize "$3" --output "$2" --write-out '%{http_code}' "$1")" || fail '下载失败，请检查网络后重试。'
  [[ "$status" == 200 ]] || fail '下载地址发生跳转或暂不可用，请从本服务的设置页重新获取安装命令。'
}
printf '正在获取 Fleet Agent Settings 发行信息…\n'
MANIFEST="$WORK/client-release.json"
download "$FLEET_INSTALL_ORIGIN/enroll/client-release.json" "$MANIFEST" 65536 60
[[ "$(/usr/bin/wc -c < "$MANIFEST")" -le 65536 ]] || fail '发行清单过大。'
[[ "$(read_value schema "$MANIFEST")" == 1 && "$(read_value bundle_id "$MANIFEST")" == com.macfleet.fleet-hub && "$(read_value notarization "$MANIFEST")" == Accepted ]] || fail '服务器尚未提供有效的正式客户端。'
[[ "$(read_value installation_protocol "$MANIFEST" 2>/dev/null || true)" == 1 ]] || fail '当前客户端暂不支持命令安装，请在网页设置中下载安装包。'
BUILD="$(read_value build "$MANIFEST")"
VERSION="$(read_value version "$MANIFEST")"
MINIMUM="$(read_value minimum_macos "$MANIFEST")"
[[ "$BUILD" =~ ^[1-9][0-9]*$ && "$VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ && "$MINIMUM" =~ ^[0-9]+\.[0-9]+(\.[0-9]+)?$ ]] || fail '发行版本信息无效。'
version_at_least "$SYSTEM" "$MINIMUM" || fail "此版本需要 macOS ${MINIMUM} 或更新版本。"
ARCH0="$(read_value architectures.0 "$MANIFEST")"
ARCH1="$(read_value architectures.1 "$MANIFEST" 2>/dev/null || true)"
[[ "$ARCH0" == arm64 || "$ARCH0" == x86_64 ]] || fail '发行架构无效。'
[[ -z "$ARCH1" || "$ARCH1" == arm64 || "$ARCH1" == x86_64 ]] || fail '发行架构无效。'
[[ "$ARCH0" == "$ARCH" || "$ARCH1" == "$ARCH" ]] || fail '发行包不支持此 Mac 架构。'
ASSET="$(read_value assets.update.path "$MANIFEST")"
SHA="$(read_value assets.update.sha256 "$MANIFEST")"
SIZE="$(read_value assets.update.size "$MANIFEST")"
[[ "$ASSET" == "/enroll/clients/$BUILD/Fleet-Hub-update.zip" && "$SHA" =~ ^[a-f0-9]{64}$ && "$SIZE" =~ ^[1-9][0-9]*$ ]] || fail '安装包信息无效。'
printf '正在下载 Fleet Agent Settings %s…\n' "$VERSION"
ZIP="$WORK/update.zip"
download "$FLEET_INSTALL_ORIGIN$ASSET" "$ZIP" "$SIZE" 1200
ACTUAL_SIZE="$(/usr/bin/wc -c < "$ZIP")"
ACTUAL_SIZE="${ACTUAL_SIZE//[[:space:]]/}"
[[ "$ACTUAL_SIZE" == "$SIZE" ]] || fail '安装包大小校验失败。'
ACTUAL_SHA="$(/usr/bin/shasum -a 256 "$ZIP")"
[[ "${ACTUAL_SHA%% *}" == "$SHA" ]] || fail '安装包 SHA-256 校验失败。'
/usr/bin/ditto -x -k "$ZIP" "$WORK/unpacked" || fail '安装包解压失败。'
APP="$WORK/unpacked/Fleet Agent Settings.app"
PLIST="$APP/Contents/Info.plist"
[[ -d "$APP" && ! -L "$APP" && -f "$PLIST" && ! -L "$PLIST" ]] || fail '安装包缺少完整的设置应用。'
[[ "$(read_value CFBundleIdentifier "$PLIST")" == com.macfleet.fleet-hub && "$(read_value CFBundleExecutable "$PLIST")" == 'Fleet Agent Settings' && "$(read_value FleetInstallProtocol "$PLIST")" == 1 ]] || fail '应用身份或安装协议不匹配。'
[[ "$(read_value CFBundleVersion "$PLIST")" == "$BUILD" && "$(read_value CFBundleShortVersionString "$PLIST")" == "$VERSION" ]] || fail '应用版本与发行清单不匹配。'
EXECUTABLE="$APP/Contents/MacOS/Fleet Agent Settings"
[[ -x "$EXECUTABLE" && ! -L "$EXECUTABLE" ]] || fail '安装程序不可执行。'
/usr/bin/lipo "$EXECUTABLE" -verify_arch "$ARCH" || fail '安装程序架构不匹配。'
/usr/bin/codesign --verify --deep --strict -R '=identifier "com.macfleet.fleet-hub" and anchor apple generic and certificate 1[field.1.2.840.113635.100.6.2.6] exists and certificate leaf[field.1.2.840.113635.100.6.1.13] exists' "$APP" || fail '应用 Developer ID 签名校验失败。'
/usr/sbin/spctl --assess --type execute "$APP" || fail '应用未通过 macOS 安全评估。'
"$EXECUTABLE" --fleet-install-cli "--fleet-origin=$FLEET_INSTALL_ORIGIN"
