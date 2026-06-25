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
if [[ ! -f "$SRV/.env" ]]; then
  if [[ -t 0 ]]; then
    echo "未找到 server/.env，进入交互配置（回车用方括号里的默认/推荐值）："
    read -r -p "  根域名（通配符证书 *.域名 的注册域，如 example.com） > " WZ_DOMAIN < /dev/tty
    while [[ -z "${WZ_DOMAIN}" ]]; do read -r -p "  不能为空，请输入根域名 > " WZ_DOMAIN < /dev/tty; done
    read -r -p "  服务子域 [fleet.${WZ_DOMAIN}] > " WZ_FLEET < /dev/tty; WZ_FLEET="${WZ_FLEET:-fleet.${WZ_DOMAIN}}"
    # 证书自动探测（acme.sh / certbot 常见路径）
    WZ_CERT=""; for c in "/root/.acme.sh/${WZ_DOMAIN}_ecc/fullchain.cer" "/etc/letsencrypt/live/${WZ_DOMAIN}/fullchain.pem"; do [[ -f "$c" ]] && { WZ_CERT="$c"; break; }; done
    WZ_KEY="";  for k in "/root/.acme.sh/${WZ_DOMAIN}_ecc/${WZ_DOMAIN}.key" "/etc/letsencrypt/live/${WZ_DOMAIN}/privkey.pem"; do [[ -f "$k" ]] && { WZ_KEY="$k"; break; }; done
    read -r -p "  证书完整链路径 [${WZ_CERT:-需手填}] > " IN < /dev/tty; WZ_CERT="${IN:-$WZ_CERT}"
    read -r -p "  证书私钥路径 [${WZ_KEY:-需手填}] > " IN < /dev/tty; WZ_KEY="${IN:-$WZ_KEY}"
    read -r -p "  你的网络封了 80/443 吗（需高位端口+路由NAT）？[y/N] > " WZ_NAT < /dev/tty
    if [[ "${WZ_NAT}" =~ ^[Yy] ]]; then
      read -r -p "    web 对外端口 [20443] > " IN < /dev/tty; WZ_GP="${IN:-20443}"
      read -r -p "    Headscale 对外端口 [28443] > " IN < /dev/tty; WZ_HPP="${IN:-28443}"
      echo "    ⚠️ 记得在路由器映射：公网 ${WZ_GP}→本机443、公网 ${WZ_HPP}→本机8443。"
    else WZ_GP=443; WZ_HPP=8443; fi
    read -r -p "  各 Mac 的 mesh IP（空格分隔；首次装可留空，Mac 入网后再回填） > " WZ_MACIPS < /dev/tty
    cat > "$SRV/.env" <<ENVEOF
DOMAIN=${WZ_DOMAIN}
FLEET_HOST=${WZ_FLEET}
SSL_CERT=${WZ_CERT}
SSL_KEY=${WZ_KEY}
GATEWAY_PORT=${WZ_GP}
HEADSCALE_PUBLIC_PORT=${WZ_HPP}
HEADSCALE_LISTEN_PORT=8443
MAC_IPS=${WZ_MACIPS}
TTYD_PORT=7681
FB_PORT=8080
AGENT_PORT=7682
NGINX_SITE=/etc/nginx/sites-enabled/mac-fleet-hub.conf
ENVEOF
    echo "  ✓ 已写入 server/.env（细调可直接编辑后重跑；参数含义见 AGENTS.md）"
  else
    echo "缺少 server/.env：请 cp server/.env.example server/.env 填写，或在交互终端运行本脚本进入向导。" >&2
    exit 1
  fi
fi
set -a; source "$SRV/.env"; set +a
: "${DOMAIN:?在 .env 设置 DOMAIN}"
: "${FLEET_HOST:?在 .env 设置 FLEET_HOST（服务子域，如 fleet.example.com）}"
: "${SSL_CERT:?在 .env 设置 SSL_CERT（证书完整链路径）}"
: "${SSL_KEY:?在 .env 设置 SSL_KEY（证书私钥路径）}"
# MAC_IPS 首轮可空：只装网关，Mac 入网后把其 mesh IP 填进 .env 重跑即可。
MAC_IPS="${MAC_IPS:-}"
[[ -n "$MAC_IPS" ]] || echo "  ⚠️ MAC_IPS 为空：本次仅部署网关（暂不生成 Mac 反代块）；Mac 入网后回填重跑。"
NGINX_SITE="${NGINX_SITE:-/etc/nginx/sites-enabled/mac-fleet-hub.conf}"
# 对外端口：默认标准端口、无 NAT（见 .env 注释）。nginx 内部恒听 443。
GATEWAY_PORT="${GATEWAY_PORT:-443}"
HEADSCALE_PUBLIC_PORT="${HEADSCALE_PUBLIC_PORT:-8443}"
HEADSCALE_LISTEN_PORT="${HEADSCALE_LISTEN_PORT:-8443}"
# 生成对外 URL 基址：对外端口为标准 443 时省略端口后缀。
url_base() { [[ "$2" == "443" ]] && echo "https://$1" || echo "https://$1:$2"; }
WEB_BASE="$(url_base "$FLEET_HOST" "$GATEWAY_PORT")"
HS_BASE="$(url_base "$FLEET_HOST" "$HEADSCALE_PUBLIC_PORT")"

# ---- 前置检查（fail-fast；本脚本不装 nginx、不签证书，二者需提前就绪）----
command -v nginx >/dev/null 2>&1 || { echo "✗ 未找到 nginx。本脚本不安装 nginx，请先 'apt install nginx' 再重跑。" >&2; exit 1; }
[[ -f "$SSL_CERT" ]] || { echo "✗ 证书文件不存在: $SSL_CERT" >&2; echo "  本脚本不签证书：请先用 acme.sh/certbot 签好 *.${DOMAIN} 通配符证书，把 fullchain/key 路径填进 server/.env。" >&2; exit 1; }
[[ -f "$SSL_KEY"  ]] || { echo "✗ 证书私钥不存在: $SSL_KEY" >&2; exit 1; }
command -v apt-get >/dev/null 2>&1 || echo "  ⚠️ 未检测到 apt-get：本脚本按 Debian/Ubuntu 编写（dpkg/useradd），其他发行版需自行调整。"
if command -v getent >/dev/null 2>&1 && ! getent hosts "$FLEET_HOST" >/dev/null 2>&1; then
  echo "  ⚠️ ${FLEET_HOST} 当前解析不到——请确认 DNS 已指向本网关公网 IP，否则手机/各 Mac 连不上。"
fi

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
    -e "s|{{HS_BASE}}|${HS_BASE}|g" -e "s/{{HEADSCALE_LISTEN_PORT}}/${HEADSCALE_LISTEN_PORT}/g" \
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
sed -e "s/{{DOMAIN}}/${DOMAIN}/g" -e "s/{{FLEET_HOST}}/${FLEET_HOST}/g" -e "s|{{WEB_BASE}}|${WEB_BASE}|g" "$SRV/authelia/configuration.yml" > /etc/authelia/configuration.yml
# 用户库（含 argon2 哈希），不入库，从模板拷一次
if [[ ! -f /etc/authelia/users_database.yml ]]; then
  if [[ -t 0 ]]; then
    # 首次：交互式建登录用户（用户名 + 密码 → argon2id 哈希）。明文不落盘。
    read -r -p "  设置登录用户名 [admin] > " AU_USER < /dev/tty; AU_USER="${AU_USER:-admin}"
    read -r -s -p "  设置登录密码（不回显）> " AU_PW < /dev/tty; echo
    AU_HASH="$(/usr/local/bin/authelia crypto hash generate argon2 --password "$AU_PW" 2>/dev/null | grep -oE '\$argon2id\$[^[:space:]]+' | head -1)"
    unset AU_PW
    if [[ -n "$AU_HASH" ]]; then
      cat > /etc/authelia/users_database.yml <<EOF
users:
  ${AU_USER}:
    displayname: '${AU_USER}'
    password: '${AU_HASH}'
    email: '${AU_USER}@${DOMAIN}'
    groups:
      - admins
EOF
      echo "  ✓ 已创建登录用户 ${AU_USER}（密码哈希已写入，明文未留存）"
    else
      cp "$SRV/authelia/users_database.yml.example" /etc/authelia/users_database.yml
      echo "  ⚠️ 哈希生成失败，已拷占位库，请手动替换：authelia crypto hash generate argon2 --password '强密码'"
    fi
  else
    cp "$SRV/authelia/users_database.yml.example" /etc/authelia/users_database.yml
    echo "  ⚠️ 非交互运行：已建占位用户库，请替换为真实 argon2 哈希："
    echo "     authelia crypto hash generate argon2 --password '强密码'"
  fi
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

echo "==> [4/7] 渲染 nginx 独立站点（子域 server 块）+ ws map/limit"
install -d "$(dirname "$NGINX_SITE")"
# 按 MAC_IPS 循环渲染每台 Mac 的反代块（单 Mac 模板 fleet-mac.conf；台数任意）。
# 第 i 个 IP → mac i（路径 /m{i}/，与 dashboard 按节点名 mac<N> 枚举对齐）。
MAC_BLOCKS="$(mktemp)"; i=0
for ip in ${MAC_IPS}; do
  i=$((i + 1))
  sed -e "s|__N__|${i}|g" -e "s|__MAC_IP__|${ip}|g" "$SRV/nginx/fleet-mac.conf" >> "$MAC_BLOCKS"
done
echo "     渲染 ${i} 台 Mac 的反代块（MAC_IPS=${MAC_IPS}）"
# 先用 awk 把 __MAC_LOCATIONS__ 标记行替换为生成的所有 Mac 块（从文件读，兼容 BSD/GNU awk），
# 再 sed 替换标量占位。
awk -v f="$MAC_BLOCKS" '/__MAC_LOCATIONS__/{while((getline line < f)>0) print line; close(f); next} {print}' "$SRV/nginx/fleet.conf" \
  | sed -e "s|__FLEET_HOST__|${FLEET_HOST}|g" \
        -e "s|__SSL_CERT__|${SSL_CERT}|g" -e "s|__SSL_KEY__|${SSL_KEY}|g" \
        -e "s|__TTYD_PORT__|${TTYD_PORT}|g" -e "s|__FB_PORT__|${FB_PORT}|g" -e "s|__AGENT_PORT__|${AGENT_PORT}|g" \
  > "$NGINX_SITE"
rm -f "$MAC_BLOCKS"
{
  printf 'map $http_upgrade $fleet_conn_upgrade { default upgrade; "" close; }\n'
  printf 'limit_req_zone $binary_remote_addr zone=fleet_enroll:1m rate=20r/m;\n'
} > /etc/nginx/conf.d/fleet-map.conf

echo "==> [5/7] nginx 独立站点已写入 ${NGINX_SITE}（子域，不动同机其它站点）"
grep -Rqs 'sites-enabled/\*' /etc/nginx/nginx.conf \
  || echo "  ⚠️ nginx.conf 似乎未 include sites-enabled/*，请确认 ${NGINX_SITE} 会被加载。"
# 迁移清理：v1 路径模式曾把 'include snippets/fleet.conf;' 注入既有站点的 443 块；子域模式下应移除。
if grep -rlqs 'snippets/fleet.conf' /etc/nginx/sites-enabled/ /etc/nginx/sites-available/ 2>/dev/null; then
  echo "  ⚠️ 检测到 v1 遗留的 'include snippets/fleet.conf;'。请从既有站点删除该行，"
  echo "     并删 /etc/nginx/snippets/fleet.conf，否则根域下残留旧 /fleet 路由或 nginx -t 失败。"
fi

echo "==> [6/7] 启动 Headscale / Authelia / 状态定时器 + 网关入网"
cp "$SRV/systemd/fleet-nodes.service" /etc/systemd/system/
cp "$SRV/systemd/fleet-nodes.timer"   /etc/systemd/system/
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

# 网关自身入 mesh（nginx 要经 mesh 到各 Mac）。用本地解析让网关直连自己的 headscale。
if ! tailscale status >/dev/null 2>&1; then
  command -v tailscale >/dev/null 2>&1 || curl -fsSL https://tailscale.com/install.sh | sh || true
fi
grep -q '127.0.0.1 '"${FLEET_HOST}" /etc/hosts || echo "127.0.0.1 ${FLEET_HOST}" >> /etc/hosts
# DERP 回环重定向：仅当对外端口 ≠ 监听端口（即你做了 NAT 映射）才需要，解决网关 hairpin。
if [[ "$HEADSCALE_PUBLIC_PORT" != "$HEADSCALE_LISTEN_PORT" ]]; then
  sed -e "s/__HS_PUBLIC_PORT__/${HEADSCALE_PUBLIC_PORT}/g" -e "s/__HS_LISTEN_PORT__/${HEADSCALE_LISTEN_PORT}/g" \
      "$SRV/systemd/fleet-derp-redirect.service" > /etc/systemd/system/fleet-derp-redirect.service
  systemctl daemon-reload
  systemctl enable --now fleet-derp-redirect.service 2>/dev/null || true
  echo "  已启用 DERP 回环重定向（${HEADSCALE_PUBLIC_PORT}→${HEADSCALE_LISTEN_PORT}，NAT hairpin）"
else
  systemctl disable --now fleet-derp-redirect.service 2>/dev/null || true
  rm -f /etc/systemd/system/fleet-derp-redirect.service; systemctl daemon-reload
fi
if ! tailscale ip -4 >/dev/null 2>&1; then
  GWKEY="$(headscale preauthkeys create -u "$FLEET_UID" --reusable --expiration 1h --tags tag:fleet-gw 2>/dev/null | tail -1)"
  tailscale up --login-server="https://${FLEET_HOST}:${HEADSCALE_LISTEN_PORT:-8443}" --authkey="$GWKEY" --hostname=gateway --accept-dns=false || true
fi
# 确保网关节点带 tag:fleet-gw（用 fleet-gw key 入网会自动打；这里兜「已在 mesh / 重跑」未打的情况）。
GW_ID="$(headscale nodes list 2>/dev/null | awk '/gateway/{print $1; exit}')"
[[ -n "${GW_ID:-}" ]] && headscale nodes tag -i "$GW_ID" -t tag:fleet-gw >/dev/null 2>&1 || true

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
ENROLL_LOGIN_SERVER=${HS_BASE}
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
# fleet-agent 自更新源：单独服务各架构二进制，agent `update` 从 ${WEB_BASE}/enroll/dist 拉。
install -d /var/www/fleet-enroll/dist
install -m 0644 "$ROOT"/mac/fleet-agent/dist/fleet-agent-darwin-* /var/www/fleet-enroll/dist/ 2>/dev/null || true
chown -R www-data:www-data /var/www/fleet-enroll 2>/dev/null || true
echo "  入网二维码（请用 Authenticator 添加，与登录 2FA 分开）："
/usr/local/bin/fleet-enroll -show-uri || true

echo "==> [7/7] 校验并 reload nginx"
if nginx -t; then systemctl reload nginx; echo "  nginx reloaded"; else echo "  ⚠️ nginx -t 失败，未 reload，请检查"; fi

cat <<EOF

✅ 网关部署完成。

【推荐】在每台新 Mac 上免 clone 一行安装（会问域名/第几台/入网验证码）：
  curl -fsSL ${WEB_BASE}/enroll/bootstrap.sh | bash
  （验证码来自上面打印的入网二维码；卸载：curl -fsSL .../enroll/uninstall.sh | bash）

【或】手动方式（注意 MAC_INDEX 各不同）：
  MAC_INDEX=1 LOGIN_SERVER=${HS_BASE} AUTHKEY=${PAK:-<preauthkey>} bash mac/setup-mac.sh

入网后：把各 Mac 的 mesh IP 按顺序填进 server/.env 的 MAC_IPS（空格分隔，第 N 个对应 mac N），
重跑本脚本（仅刷新 nginx 站点即可）。
然后给网关节点打 tag：headscale nodes list ; headscale nodes tag -i <网关node-id> -t tag:fleet-gw
（若用了高位端口：确认路由器已做 NAT 映射 公网${GATEWAY_PORT}→本机443、公网${HEADSCALE_PUBLIC_PORT}→本机${HEADSCALE_LISTEN_PORT}。默认标准端口则无需 NAT。）
访问：${WEB_BASE}/  → Authelia 登录(2FA) → 选 Mac → 会话。

排错：journalctl -u headscale -u authelia -f ; tail -f /var/www/fleet/api/nodes.json
EOF
