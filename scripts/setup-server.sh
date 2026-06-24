#!/usr/bin/env bash
# 在 Ubuntu 网关上以 root 运行：装 Headscale + Authelia(原生 systemd)，部署 PWA，写独立 nginx 站点（mfh 子域）。
#
#   cd mac-fleet-hub && cp server/.env.example server/.env && 编辑 server/.env
#   sudo bash scripts/setup-server.sh
#
# 幂等：可重复运行。改 nginx 前自动备份。需要 root（装包/写 /etc /var/www/systemctl）。
set -euo pipefail

[[ $EUID -eq 0 ]] || { echo "请用 sudo 运行。" >&2; exit 1; }
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SRV="$ROOT/server"
[[ -f "$SRV/.env" ]] || { echo "缺少 server/.env，请先 cp server/.env.example server/.env 并填写。" >&2; exit 1; }
set -a; source "$SRV/.env"; set +a
: "${DOMAIN:?在 .env 设置 DOMAIN}"
: "${FLEET_HOST:?在 .env 设置 FLEET_HOST（服务子域，如 mfh.example.com）}"
SSL_CERT="${SSL_CERT:-/root/.acme.sh/example.com_ecc/fullchain.cer}"
SSL_KEY="${SSL_KEY:-/root/.acme.sh/example.com_ecc/example.com.key}"
NGINX_SITE="${NGINX_SITE:-/etc/nginx/sites-enabled/mac-fleet-hub.conf}"

ARCH=amd64
gh_latest() { curl -fsSL "https://api.github.com/repos/$1/releases/latest" | grep -oE '"tag_name": *"[^"]+"' | head -1 | sed -E 's/.*"v?([^"]+)".*/\1/'; }

echo "==> [1/7] 安装 Headscale"
if ! command -v headscale >/dev/null 2>&1; then
  # 国内网络常连不上 github.com release 下载：可用 HEADSCALE_DEB 指向本地预下载的 .deb
  HS_DEB="${HEADSCALE_DEB:-}"
  if [[ -z "$HS_DEB" || ! -f "$HS_DEB" ]]; then
    HS_VER="${HEADSCALE_VERSION:-$(gh_latest juanfont/headscale)}"; HS_DEB=/tmp/headscale.deb
    curl -fsSL -o "$HS_DEB" "https://github.com/juanfont/headscale/releases/download/v${HS_VER}/headscale_${HS_VER}_linux_${ARCH}.deb"
  fi
  dpkg -i "$HS_DEB" || apt-get -fy install
fi
id headscale >/dev/null 2>&1 || useradd --system --home /var/lib/headscale --shell /usr/sbin/nologin headscale || true
install -d -o headscale -g headscale /var/lib/headscale /etc/headscale
sed -e "s/{{DOMAIN}}/${DOMAIN}/g" -e "s/{{FLEET_HOST}}/${FLEET_HOST}/g" \
    -e "s|{{SSL_CERT}}|${SSL_CERT}|g" -e "s|{{SSL_KEY}}|${SSL_KEY}|g" \
    "$SRV/headscale/config.yaml.example" > /etc/headscale/config.yaml
cp "$SRV/headscale/acl.hujson" /etc/headscale/acl.hujson
# 让 headscale 能读 LE 证书
usermod -aG "$(stat -c %G /etc/letsencrypt/live/${DOMAIN}/fullchain.pem 2>/dev/null || echo root)" headscale 2>/dev/null || true
chmod 0644 /etc/letsencrypt/archive/${DOMAIN}/*.pem 2>/dev/null || true
cp "$SRV/systemd/headscale.service" /etc/systemd/system/headscale.service

echo "==> [2/7] 安装 Authelia"
if [[ ! -x /usr/local/bin/authelia ]]; then
  # 同上：可用 AUTHELIA_TGZ 指向本地预下载的 tar.gz
  AU_TGZ="${AUTHELIA_TGZ:-}"
  if [[ -z "$AU_TGZ" || ! -f "$AU_TGZ" ]]; then
    AU_VER="${AUTHELIA_VERSION:-$(gh_latest authelia/authelia)}"; AU_TGZ=/tmp/authelia.tgz
    curl -fsSL -o "$AU_TGZ" "https://github.com/authelia/authelia/releases/download/v${AU_VER}/authelia-v${AU_VER}-linux-${ARCH}.tar.gz"
  fi
  tar -xzf "$AU_TGZ" -C /tmp
  install -m 0755 "/tmp/authelia-linux-${ARCH}" /usr/local/bin/authelia 2>/dev/null || \
    install -m 0755 /tmp/authelia /usr/local/bin/authelia
fi
id authelia >/dev/null 2>&1 || useradd --system --home /var/lib/authelia --shell /usr/sbin/nologin authelia || true
install -d -o authelia -g authelia /var/lib/authelia /etc/authelia /etc/authelia/secrets
sed -e "s/{{DOMAIN}}/${DOMAIN}/g" -e "s/{{FLEET_HOST}}/${FLEET_HOST}/g" "$SRV/authelia/configuration.yml" > /etc/authelia/configuration.yml
# 用户库（含 argon2 哈希），不入库，从模板拷一次
if [[ ! -f /etc/authelia/users_database.yml ]]; then
  cp "$SRV/authelia/users_database.yml.example" /etc/authelia/users_database.yml
  echo "  ⚠️ 已建 /etc/authelia/users_database.yml，请替换为真实 argon2 哈希："
  echo "     authelia crypto hash generate argon2 --password '强密码'"
fi
# 密钥
gen() { [[ -s "/etc/authelia/secrets/$1" ]] || { openssl rand -base64 48 | tr -d '\n' > "/etc/authelia/secrets/$1"; }; }
gen JWT_SECRET; gen SESSION_SECRET; gen STORAGE_ENCRYPTION_KEY
chown -R authelia:authelia /etc/authelia/secrets /var/lib/authelia
chmod 600 /etc/authelia/secrets/*
cp "$SRV/systemd/authelia.service" /etc/systemd/system/authelia.service

echo "==> [3/7] 部署 PWA 控制台到 /var/www/fleet"
install -d /var/www/fleet/api
cp -r "$SRV/dashboard/." /var/www/fleet/
chown -R www-data:www-data /var/www/fleet 2>/dev/null || true

echo "==> [4/7] 渲染 nginx 独立站点（mfh 子域 server 块）+ ws map/limit"
install -d "$(dirname "$NGINX_SITE")"
sed -e "s|__FLEET_HOST__|${FLEET_HOST}|g" \
    -e "s|__SSL_CERT__|${SSL_CERT}|g" -e "s|__SSL_KEY__|${SSL_KEY}|g" \
    -e "s|__MAC1_IP__|${MAC1_IP}|g" -e "s|__MAC2_IP__|${MAC2_IP}|g" -e "s|__MAC3_IP__|${MAC3_IP}|g" \
    -e "s|__TTYD_PORT__|${TTYD_PORT}|g" -e "s|__FB_PORT__|${FB_PORT}|g" -e "s|__AGENT_PORT__|${AGENT_PORT}|g" \
    "$SRV/nginx/fleet.conf" > "$NGINX_SITE"
{
  printf 'map $http_upgrade $fleet_conn_upgrade { default upgrade; "" close; }\n'
  printf 'limit_req_zone $binary_remote_addr zone=fleet_enroll:1m rate=20r/m;\n'
} > /etc/nginx/conf.d/fleet-map.conf

echo "==> [5/7] nginx 独立站点已写入 ${NGINX_SITE}（mfh 子域，不动现有 rtm 站点）"
grep -Rqs 'sites-enabled/\*' /etc/nginx/nginx.conf \
  || echo "  ⚠️ nginx.conf 似乎未 include sites-enabled/*，请确认 ${NGINX_SITE} 会被加载。"
# 迁移清理：v1 路径模式曾把 'include snippets/fleet.conf;' 注入 rtm 的 443 块；子域模式下应移除。
if grep -rlqs 'snippets/fleet.conf' /etc/nginx/sites-enabled/ /etc/nginx/sites-available/ 2>/dev/null; then
  echo "  ⚠️ 检测到 v1 遗留的 'include snippets/fleet.conf;'。请从 rtm 站点删除该行，"
  echo "     并删 /etc/nginx/snippets/fleet.conf，否则 example.com 下残留旧 /fleet 路由或 nginx -t 失败。"
fi

echo "==> [6/7] 启动 Headscale / Authelia / 状态定时器 + 网关入网"
cp "$SRV/systemd/fleet-nodes.service" /etc/systemd/system/
cp "$SRV/systemd/fleet-nodes.timer"   /etc/systemd/system/
cp "$SRV/systemd/fleet-derp-redirect.service" /etc/systemd/system/
systemctl daemon-reload
# enable 开机自启；headscale/authelia 用 restart 而非 enable --now：重跑脚本时配置常已变
# （换域名/换证书），而 enable --now 对已运行服务是 no-op，不会重载配置——旧域名残留会直接
# 导致 Authelia 登录点击无反应。restart 同时覆盖「首次启动」与「重跑生效」两种情况。
systemctl enable headscale authelia fleet-nodes.timer >/dev/null 2>&1 || true
systemctl restart headscale authelia
systemctl start fleet-nodes.timer
sleep 2
# headscale 用户（id 取数字）+ 可复用 preauthkey（给各 Mac 用，打 tag:fleet-mac）
headscale users create fleet 2>/dev/null || true
FLEET_UID="$(headscale users list -o json 2>/dev/null | grep -oE '"id":[0-9]+' | head -1 | grep -oE '[0-9]+')"
FLEET_UID="${FLEET_UID:-1}"
PAK="$(headscale preauthkeys create -u "$FLEET_UID" --reusable --expiration 24h --tags tag:fleet-mac 2>/dev/null | tail -1 || true)"

# 网关自身入 mesh（nginx 要经 mesh 到各 Mac）。家宽 hairpin 不通 → 用本地解析 + 28443→8443 重定向。
if ! tailscale status >/dev/null 2>&1; then
  command -v tailscale >/dev/null 2>&1 || curl -fsSL https://tailscale.com/install.sh | sh || true
fi
grep -q '127.0.0.1 '"${FLEET_HOST}" /etc/hosts || echo "127.0.0.1 ${FLEET_HOST}" >> /etc/hosts
systemctl enable --now fleet-derp-redirect.service 2>/dev/null || true
if ! tailscale ip -4 >/dev/null 2>&1; then
  GWKEY="$(headscale preauthkeys create -u "$FLEET_UID" --reusable --expiration 1h --tags tag:fleet-gw 2>/dev/null | tail -1)"
  tailscale up --login-server="https://${FLEET_HOST}:${HEADSCALE_LISTEN_PORT:-8443}" --authkey="$GWKEY" --hostname=gateway --accept-dns=false || true
fi

# ---- 自助入网服务 fleet-enroll（TOTP → 一次性 preauthkey）+ 免 clone 安装包 ----
echo "==> [6.5/7] 部署 fleet-enroll 自助入网服务"
install -m 0755 "$SRV/enroll/dist/fleet-enroll-linux-amd64" /usr/local/bin/fleet-enroll
install -d /etc/fleet-enroll
install -d /var/lib/fleet-enroll   # 可写状态：Mac 显示名 names.json（/etc 在 ProtectSystem=full 下只读）
if [[ ! -s /etc/fleet-enroll/totp.secret ]]; then
  head -c 20 /dev/urandom | base32 | tr -d '=' | tr 'a-z' 'A-Z' > /etc/fleet-enroll/totp.secret
  echo "  已生成入网专用 TOTP 密钥（与登录 2FA 分开）"
fi
chmod 600 /etc/fleet-enroll/totp.secret
cat > /etc/fleet-enroll/env <<EOF
ENROLL_LISTEN=127.0.0.1:7090
ENROLL_SECRET_FILE=/etc/fleet-enroll/totp.secret
ENROLL_LOGIN_SERVER=https://${FLEET_HOST}:${HEADSCALE_PUBLIC_PORT:-28443}
ENROLL_HS_USER=${FLEET_UID}
ENROLL_KEY_TTL=10m
ENROLL_NAMES_FILE=/var/lib/fleet-enroll/names.json
EOF
cp "$SRV/systemd/fleet-enroll.service" /etc/systemd/system/fleet-enroll.service
systemctl daemon-reload
# 同理 restart：重跑时 /etc/fleet-enroll/env（ENROLL_LOGIN_SERVER 等）可能随域名变化。
systemctl enable fleet-enroll >/dev/null 2>&1 || true
systemctl restart fleet-enroll
# 发布免 clone 安装包：bootstrap.sh / uninstall.sh / mac-bundle.tar.gz
install -d /var/www/fleet-enroll
install -m 0644 "$SRV/enroll/bootstrap.sh" /var/www/fleet-enroll/bootstrap.sh
install -m 0644 "$ROOT/mac/uninstall.sh"   /var/www/fleet-enroll/uninstall.sh
tar czf /var/www/fleet-enroll/mac-bundle.tar.gz -C "$ROOT" mac
chown -R www-data:www-data /var/www/fleet-enroll 2>/dev/null || true
echo "  入网二维码（请用 Authenticator 添加，与登录 2FA 分开）："
/usr/local/bin/fleet-enroll -show-uri || true

echo "==> [7/7] 校验并 reload nginx"
if nginx -t; then systemctl reload nginx; echo "  nginx reloaded"; else echo "  ⚠️ nginx -t 失败，未 reload，请检查"; fi

cat <<EOF

✅ 网关部署完成。

【推荐】在每台新 Mac 上免 clone 一行安装（会问域名/第几台/入网验证码）：
  curl -fsSL https://${FLEET_HOST}:${GATEWAY_PORT:-20443}/enroll/bootstrap.sh | bash
  （验证码来自上面打印的入网二维码；卸载：curl -fsSL .../enroll/uninstall.sh | bash）

【或】手动方式（注意 MAC_INDEX 各不同）：
  MAC_INDEX=1 LOGIN_SERVER=https://${FLEET_HOST}:${HEADSCALE_PUBLIC_PORT:-28443} AUTHKEY=${PAK:-<preauthkey>} bash mac/setup-mac.sh

入网后：把三台 mesh IP 回填 server/.env 的 MAC{1,2,3}_IP，重跑本脚本（仅刷新 nginx 站点即可）。
然后给网关节点打 tag：headscale nodes list ; headscale nodes tag -i <网关node-id> -t tag:fleet-gw
确认路由端口映射：公网 20443→本机443、28443→本机8443。
访问：https://${FLEET_HOST}:${GATEWAY_PORT:-20443}/  → Authelia 登录(2FA) → 选 Mac → 会话。

排错：journalctl -u headscale -u authelia -f ; tail -f /var/www/fleet/api/nodes.json
EOF
