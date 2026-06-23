#!/usr/bin/env bash
# 在 Ubuntu 网关上以 root 运行：装 Headscale + Authelia(原生 systemd)，部署 PWA，注入现有 nginx。
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
sed "s/{{DOMAIN}}/${DOMAIN}/g" "$SRV/headscale/config.yaml.example" > /etc/headscale/config.yaml
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
sed "s/{{DOMAIN}}/${DOMAIN}/g" "$SRV/authelia/configuration.yml" > /etc/authelia/configuration.yml
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

echo "==> [4/7] 渲染 nginx fleet 片段 + ws map"
install -d /etc/nginx/snippets
sed -e "s/__MAC1_IP__/${MAC1_IP}/g" -e "s/__MAC2_IP__/${MAC2_IP}/g" -e "s/__MAC3_IP__/${MAC3_IP}/g" \
    -e "s/__TTYD_PORT__/${TTYD_PORT}/g" -e "s/__FB_PORT__/${FB_PORT}/g" -e "s/__AGENT_PORT__/${AGENT_PORT}/g" \
    "$SRV/nginx/fleet.conf" > /etc/nginx/snippets/fleet.conf
printf 'map $http_upgrade $fleet_conn_upgrade { default upgrade; "" close; }\n' > /etc/nginx/conf.d/fleet-map.conf

echo "==> [5/7] 把 include 注入现有 443 server 块（自动备份）"
SITE="${NGINX_SITE:-/etc/nginx/sites-enabled/rtm.conf}"
if [[ -f "$SITE" ]] && ! grep -q 'snippets/fleet.conf' "$SITE"; then
  cp "$SITE" "${SITE}.bak.$(date +%s)"
  # 在含 'listen 443' 的 server 块的最后一个 } 前插入 include
  awk '
    /server[[:space:]]*\{/ {depth++; buf[depth]=$0; is443[depth]=0; next}
    depth>0 {
      buf[depth]=buf[depth]"\n"$0
      if ($0 ~ /listen[^;]*443/) is443[depth]=1
      n=gsub(/\{/,"{"); o=gsub(/\}/,"}")
      if ($0 ~ /\}/ && depth>0) {
        if (is443[depth]==1) sub(/\}[^}]*$/, "    include snippets/fleet.conf;\n}", buf[depth])
        print buf[depth]; depth--
        next
      }
      next
    }
    {print}
  ' "$SITE" > "${SITE}.new" && mv "${SITE}.new" "$SITE"
  echo "  已注入（备份：${SITE}.bak.*）。若 awk 未命中你的结构，请手动在 example.com:443 server{} 内加：include snippets/fleet.conf;"
fi

echo "==> [6/7] 启动 Headscale / Authelia / 状态定时器"
cp "$SRV/systemd/fleet-nodes.service" /etc/systemd/system/
cp "$SRV/systemd/fleet-nodes.timer"   /etc/systemd/system/
systemctl daemon-reload
systemctl enable --now headscale authelia fleet-nodes.timer
# headscale 用户 + 可复用 preauthkey（打 tag）
headscale users create fleet 2>/dev/null || true
PAK="$(headscale preauthkeys create -u fleet --reusable --expiration 24h --tags tag:fleet-mac 2>/dev/null | tail -1 || true)"

echo "==> [7/7] 校验并 reload nginx"
if nginx -t; then systemctl reload nginx; echo "  nginx reloaded"; else echo "  ⚠️ nginx -t 失败，未 reload，请检查"; fi

cat <<EOF

✅ 网关部署完成。

下一步在每台 Mac 跑（注意 MAC_INDEX 各不同）：
  MAC_INDEX=1 LOGIN_SERVER=https://${DOMAIN}:${HEADSCALE_PUBLIC_PORT:-28443} AUTHKEY=${PAK:-<preauthkey>} bash mac/setup-mac.sh
  MAC_INDEX=2 LOGIN_SERVER=https://${DOMAIN}:${HEADSCALE_PUBLIC_PORT:-28443} AUTHKEY=${PAK:-<preauthkey>} bash mac/setup-mac.sh
  MAC_INDEX=3 LOGIN_SERVER=https://${DOMAIN}:${HEADSCALE_PUBLIC_PORT:-28443} AUTHKEY=${PAK:-<preauthkey>} bash mac/setup-mac.sh

入网后：把三台 mesh IP 回填 server/.env 的 MAC{1,2,3}_IP，重跑本脚本（仅刷新 nginx 片段即可）。
然后给网关节点打 tag：headscale nodes list ; headscale nodes tag -i <网关node-id> -t tag:fleet-gw
确认路由端口映射：公网 20443→本机443、28443→本机8443。
访问：https://${DOMAIN}:${GATEWAY_PORT:-20443}/fleet/  → Authelia 登录(2FA) → 选 Mac → 会话。

排错：journalctl -u headscale -u authelia -f ; tail -f /var/www/fleet/api/nodes.json
EOF
