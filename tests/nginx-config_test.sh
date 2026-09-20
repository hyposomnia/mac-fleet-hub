#!/usr/bin/env bash
# nginx 站点配置回归测试：大文件上传的 body 限制 + setup-server.sh 的 Mac 块渲染。
#
# 背景：auth_request 的子请求也按 /authz location 自己的 client_max_body_size 校验原始
# 请求体（没有显式配置时继承 http 级默认值 8m），超限时子请求被 413，auth_request 再把
# 非预期状态转成 500，浏览器只能看到 `file/upload?...: 500`。
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SITE="$ROOT/server/nginx/fleet.conf"
MAC_TMPL="$ROOT/server/nginx/fleet-mac.conf"
SETUP="$ROOT/scripts/setup-server.sh"
tmpdir="$(mktemp -d "${TMPDIR:-/tmp}/macfleet-nginx.XXXXXX")"
trap 'rm -rf "$tmpdir"' EXIT

fail() { echo "nginx-config: $*" >&2; exit 1; }

# 打印某个 location 块的正文（匹配行起，到同缩进的 "}" 止）。
block_of() {
  awk -v want="$1" '
    index($0, want) { inside = 1 }
    inside { print }
    inside && $0 ~ /^[[:space:]]*}[[:space:]]*$/ { exit }
  ' "$2"
}

# 1) /authz 子请求必须放宽 body 限制，否则超过 http 级 8m 的上传在鉴权阶段就被挡成 500。
block_of 'location = /authz' "$SITE" | grep -q 'client_max_body_size 513m' \
  || fail 'server/nginx/fleet.conf 的 location = /authz 缺少 client_max_body_size 513m'

# 2) 每台 Mac 的 api / files 反代块同样要放宽，与 fleet-agent 的 512 MiB 上限对齐。
for loc in 'location ^~ /m__N__/api/' 'location ^~ /m__N__/files/'; do
  block_of "$loc" "$MAC_TMPL" | grep -q 'client_max_body_size 513m' \
    || fail "server/nginx/fleet-mac.conf 的 ${loc} 缺少 client_max_body_size 513m"
done

# 3) 模板注释里也写了 __MAC_LOCATIONS__ 占位名，渲染必须跳过注释行；否则整段 Mac 块会
#    被插到文件头注释位置，location 重复、nginx -t 直接失败。
awk_prog="$(sed -n "s/.*awk -v f=\"\$MAC_BLOCKS\" '\([^']*\)'.*/\1/p" "$SETUP" | head -1)"
[[ -n "$awk_prog" ]] \
  || fail '未能从 setup-server.sh 提取 __MAC_LOCATIONS__ 渲染程序（脚本已改写？请同步本测试）'
[[ "$awk_prog" == *'!/^[[:space:]]*#/'* ]] \
  || fail 'setup-server.sh 的渲染程序会把注释行里的 __MAC_LOCATIONS__ 一起展开'

# 4) 用脚本里的真实渲染程序跑一遍：两块 Mac 块只能各出现一次。
{
  sed -e 's|__N__|1|g' -e 's|__MAC_IP__|100.64.0.2|g' "$MAC_TMPL"
  sed -e 's|__N__|2|g' -e 's|__MAC_IP__|100.64.0.3|g' "$MAC_TMPL"
} > "$tmpdir/mac-blocks"
printf '%s\n' "$awk_prog" > "$tmpdir/prog.awk"
awk -v f="$tmpdir/mac-blocks" -f "$tmpdir/prog.awk" "$SITE" > "$tmpdir/rendered.conf"

[[ "$(grep -c 'location ^~ /m1/api/' "$tmpdir/rendered.conf")" == 1 ]] \
  || fail '渲染后 /m1/api/ 出现多次：Mac 块被展开到了注释区'
[[ "$(grep -c '__MAC_LOCATIONS__' "$tmpdir/rendered.conf")" == 1 ]] \
  || fail '渲染后占位行 __MAC_LOCATIONS__ 数量不为 1（应只剩文件头注释那一处）'
grep -q 'location ^~ /m2/files/' "$tmpdir/rendered.conf" \
  || fail '渲染结果缺少第二台 Mac 的反代块'

# 5) 公网消息 API 必须只由 Bearer key 认证，不能被 Authelia Cookie 拦住；密钥管理和消息记录则反过来
#    必须保留 auth_request，避免公网 key 自己轮换/撤销密钥或读取所有消息正文。
for loc in 'location = /api/v1/messages' 'location ^~ /api/v1/messages/'; do
  block="$(block_of "$loc" "$SITE")"
  [[ "$block" == *'proxy_set_header Authorization $http_authorization;'* ]] \
    || fail "${loc} 没有向 fleet-enroll 传递 Authorization"
  [[ "$block" != *'auth_request /authz;'* ]] \
    || fail "${loc} 不应使用 Authelia auth_request"
done
for loc in 'location = /api/settings/access-key' 'location = /api/settings/access-key/rotate' 'location = /api/message-records' \
  'location = /api/automation/access-keys' 'location ^~ /api/automation/access-keys/' \
  'location = /api/automation/message-records'; do
  block_of "$loc" "$SITE" | grep -q 'auth_request /authz;' \
    || fail "${loc} 缺少 Authelia auth_request"
done

echo "nginx-config tests passed"
