#!/usr/bin/env bash

native_candidate_preflight() {
  : "${FLEET_CMAKE:?}" "${FLEET_SETTINGS_VERSION:?}" "${FLEET_SETTINGS_BUILD:?}"
  : "${FLEET_CODESIGN_IDENTITY:?指定唯一 Developer ID Application 的 SHA-1 identity}"
  : "${FLEET_SPARKLE_ACCOUNT:?指定已配置的私有钥匙串更新密钥账号}"
  : "${FLEET_CANDIDATE_NGINX_CONFIG:?指定该候选实例实际使用的 nginx 配置文件}"
  [[ "$FLEET_CANDIDATE_NGINX_CONFIG" =~ ^/[a-zA-Z0-9_./-]+$ ]] || die 'nginx 配置路径无效。'
  [[ "$FLEET_SETTINGS_VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ && "$FLEET_SETTINGS_BUILD" =~ ^[1-9][0-9]*$ ]] || die '原生应用版本或构建号无效。'
  [[ "$FLEET_CODESIGN_IDENTITY" =~ ^[A-Fa-f0-9]{40}$ ]] || die '签名身份必须使用明确的 SHA-1。'
  security find-identity -v -p codesigning | grep -i "$FLEET_CODESIGN_IDENTITY" | grep 'Developer ID Application:' >/dev/null || die '指定证书不是 Developer ID Application。'
  [[ -x "$FLEET_CMAKE" ]] || die '缺少 CMake 构建工具。'
  bash "$ROOT/scripts/prepare-settings-sdk.sh"
  [[ -x "$ROOT/mac/settings-app/Vendor/bin/generate_keys" ]] || die '先准备并校验 Sparkle SDK。'
  native_public_key="$("$ROOT/mac/settings-app/Vendor/bin/generate_keys" --account "$FLEET_SPARKLE_ACCOUNT" -p)"
  node -e 'if(Buffer.from(process.argv[1],"base64").length!==32)process.exit(1)' "$native_public_key" || die '无法读取已配置的升级公钥；不自动创建或轮换密钥。'
  ssh_retry "$port" "$target" '检查原生应用分发路由及不可变构建号' "test ! -e '$candidate_root/client-native-releases/$FLEET_SETTINGS_BUILD' && sudo -n nginx -T -c '$FLEET_CANDIDATE_NGINX_CONFIG' 2>/dev/null | grep -F '$candidate_root/client-native-releases/' >/dev/null"
}

run_native_candidate_release() {
  local work app agent framework component revision signature remote_input
  work="$(mktemp -d /private/tmp/fleet-native-release.XXXXXX)"
  chmod 0700 "$work"
  revision="$(git -C "$ROOT" rev-parse HEAD)"
  step '构建并隔离验证完整双架构原生应用'
  FLEET_CMAKE="$FLEET_CMAKE" bash "$ROOT/scripts/build-settings-runtime.sh" "$work/runtime"
  bash "$ROOT/scripts/build-settings-filebrowser.sh" "$work/runtime"
  FLEET_SETTINGS_RUNTIME="$work/runtime" FLEET_SETTINGS_VERSION="$FLEET_SETTINGS_VERSION" FLEET_SETTINGS_BUILD="$FLEET_SETTINGS_BUILD" bash "$ROOT/scripts/build-settings-app.sh" --development "$work" | tee "$work/build.log"
  app="$(tail -1 "$work/build.log")"
  [[ "$app" == "$work"/*'/Fleet Hub.app' && -d "$app" ]] || die '构建输出目录无效。'
  node "$ROOT/scripts/settings-runtime-uat.mjs" "$app"
  agent="$app/Contents/Library/LoginItems/Fleet Agent.app"
  framework="$app/Contents/Frameworks/Sparkle.framework"
  plutil -insert SUPublicEDKey -string "$native_public_key" "$app/Contents/Info.plist"
  plutil -insert SUEnableAutomaticChecks -bool false "$app/Contents/Info.plist"
  plutil -insert SUAllowsAutomaticUpdates -bool false "$app/Contents/Info.plist"
  printf '%s\n' '{"channel":"candidate","release_ready":true}' > "$app/Contents/Resources/release-status.json"
  for component in ttyd tmux filebrowser; do
    codesign --force --options runtime --timestamp --sign "$FLEET_CODESIGN_IDENTITY" --identifier "com.macfleet.runtime.$component" "$agent/Contents/Resources/bin/$component"
  done
  codesign --force --options runtime --timestamp --sign "$FLEET_CODESIGN_IDENTITY" --identifier com.macfleet.fleet-agent "$agent"
  for component in "$framework/Versions/B/XPCServices/Downloader.xpc" "$framework/Versions/B/XPCServices/Installer.xpc" "$framework/Versions/B/Autoupdate" "$framework/Versions/B/Updater.app" "$framework"; do
    codesign --force --preserve-metadata=entitlements --options runtime --timestamp --sign "$FLEET_CODESIGN_IDENTITY" "$component"
  done
  codesign --force --options runtime --timestamp --sign "$FLEET_CODESIGN_IDENTITY" --identifier com.macfleet.fleet-hub "$app"
  codesign --verify --deep --strict "$app"
  ditto -c -k --keepParent "$app" "$work/notarize-app.zip"
  xcrun notarytool submit "$work/notarize-app.zip" --keychain-profile "$NOTARY_PROFILE" --wait --timeout 60m --output-format json | tee "$work/app-notary.json"
  node -e 'if(JSON.parse(require("fs").readFileSync(process.argv[1])).status!=="Accepted")process.exit(1)' "$work/app-notary.json"
  xcrun stapler staple "$app"
  xcrun stapler validate "$app"
  spctl --assess --type execute "$app"
  mkdir -p "$work/distribution" "$work/dmg"
  ditto -c -k --keepParent "$app" "$work/distribution/Fleet-Hub-update.zip"
  signature="$("$ROOT/mac/settings-app/Vendor/bin/sign_update" --account "$FLEET_SPARKLE_ACCOUNT" -p "$work/distribution/Fleet-Hub-update.zip")"
  "$ROOT/mac/settings-app/Vendor/bin/sign_update" --account "$FLEET_SPARKLE_ACCOUNT" --verify "$work/distribution/Fleet-Hub-update.zip" "$signature"
  ditto "$app" "$work/dmg/Fleet Hub.app"
  ln -s /Applications "$work/dmg/Applications"
  hdiutil create -volname 'Fleet Hub' -srcfolder "$work/dmg" -format UDZO "$work/distribution/Fleet-Hub.dmg"
  codesign --force --timestamp --sign "$FLEET_CODESIGN_IDENTITY" "$work/distribution/Fleet-Hub.dmg"
  xcrun notarytool submit "$work/distribution/Fleet-Hub.dmg" --keychain-profile "$NOTARY_PROFILE" --wait --timeout 60m --output-format json | tee "$work/dmg-notary.json"
  node -e 'if(JSON.parse(require("fs").readFileSync(process.argv[1])).status!=="Accepted")process.exit(1)' "$work/dmg-notary.json"
  xcrun stapler staple "$work/distribution/Fleet-Hub.dmg"
  xcrun stapler validate "$work/distribution/Fleet-Hub.dmg"
  node - "$work" "$FLEET_CANDIDATE_WEB_BASE" "$FLEET_SETTINGS_VERSION" "$FLEET_SETTINGS_BUILD" "$revision" "$signature" <<'NODE'
const fs = require('fs');
const [directory, origin, version, build, revision, signature] = process.argv.slice(2);
fs.writeFileSync(directory + '/metadata.json', JSON.stringify({ origin, version, build: Number(build), revision, signature,
  appNotary: JSON.parse(fs.readFileSync(directory + '/app-notary.json')), dmgNotary: JSON.parse(fs.readFileSync(directory + '/dmg-notary.json')) }));
NODE
  node "$ROOT/scripts/native-client-release.mjs" "$work/distribution" "$work/metadata.json"
  (cd "$work/distribution" && shasum -a 256 Fleet-Hub.dmg Fleet-Hub-update.zip client-release.json appcast.xml) > "$work/SHA256SUMS"
  remote_input="/tmp/fleet-native-$revision-$FLEET_SETTINGS_BUILD"
  ssh_retry "$port" "$target" '创建原生安装包暂存目录' "umask 077; mkdir -p '$remote_input'"
  scp -o BatchMode=yes -o ConnectTimeout=8 -P "$port" -r "$work/distribution" "$work/SHA256SUMS" "$target:$remote_input/"
  ssh_retry "$port" "$target" '暂存不可变原生安装包，保留当前下载源' "sudo -n flock -n '$candidate_root/.native-publish.lock' bash -c 'set -e; test -f \"$candidate_root/.candidate-instance\"; cd \"$remote_input/distribution\"; sha256sum -c ../SHA256SUMS; install -d -m 0755 \"$candidate_root/client-native-releases\"; test ! -e \"$candidate_root/client-native-releases/$FLEET_SETTINGS_BUILD\"; readlink \"$candidate_root/client-native-current\" > \"$remote_input/previous-native-pointer\" || true; cp -R . \"$candidate_root/client-native-releases/$FLEET_SETTINGS_BUILD\"; find \"$candidate_root/client-native-releases/$FLEET_SETTINGS_BUILD\" -type d -exec chmod 0755 {} +; find \"$candidate_root/client-native-releases/$FLEET_SETTINGS_BUILD\" -type f -exec chmod 0644 {} +'"
  mkdir -p "$work/downloaded"
  for component in Fleet-Hub.dmg Fleet-Hub-update.zip; do
    curl "${curl_args[@]}" "$FLEET_CANDIDATE_WEB_BASE/enroll/clients/$FLEET_SETTINGS_BUILD/$component" -o "$work/downloaded/$component"
    cmp "$work/distribution/$component" "$work/downloaded/$component"
  done
  for component in client-release.json appcast.xml; do
    test -s "$work/distribution/$component"
  done
  ssh_retry "$port" "$target" '原子切换已完成真实下载验证的安装源' "sudo -n flock -n '$candidate_root/.native-publish.lock' bash -c 'set -e; previous=\$(cat \"$remote_input/previous-native-pointer\"); current=\$(readlink \"$candidate_root/client-native-current\" || true); test \"\$previous\" = \"\$current\"; ln -s \"$candidate_root/client-native-releases/$FLEET_SETTINGS_BUILD\" \"$candidate_root/native-next-$FLEET_SETTINGS_BUILD\"; mv -Tf \"$candidate_root/native-next-$FLEET_SETTINGS_BUILD\" \"$candidate_root/client-native-current\"'"
  for component in client-release.json appcast.xml; do
    if ! curl "${curl_args[@]}" "$FLEET_CANDIDATE_WEB_BASE/enroll/$component" -o "$work/downloaded/$component" || ! cmp "$work/distribution/$component" "$work/downloaded/$component"; then
      ssh_retry "$port" "$target" '下载清单验证失败，恢复之前安装源' "sudo -n flock -n '$candidate_root/.native-publish.lock' bash -c 'set -e; current=\$(readlink \"$candidate_root/client-native-current\" || true); test \"\$current\" = \"$candidate_root/client-native-releases/$FLEET_SETTINGS_BUILD\"; previous=\$(cat \"$remote_input/previous-native-pointer\"); if test -n \"\$previous\"; then ln -s \"\$previous\" \"$candidate_root/native-rollback-$FLEET_SETTINGS_BUILD\"; mv -Tf \"$candidate_root/native-rollback-$FLEET_SETTINGS_BUILD\" \"$candidate_root/client-native-current\"; else rm \"$candidate_root/client-native-current\"; fi'"
      die '应用发行清单真实下载核验失败，已恢复之前下载源。'
    fi
  done
  printf '原生候选安装包已发布并实际下载核对；构建号 %s，证据目录 %s。未替换现有 Mac。\n' "$FLEET_SETTINGS_BUILD" "$work"
}
