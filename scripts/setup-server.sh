#!/usr/bin/env bash
# 在公网服务器上运行：部署 Caddy + Authelia + 共享 Filebrowser。
# 前置：服务器已装 Docker / docker compose，已加入 Tailscale 且能 ping 通各 Mac 的 100.x IP。
#
# 用法：
#   cd server && cp .env.example .env && 编辑 .env
#   bash ../scripts/setup-server.sh
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")/../server" && pwd)"
cd "$HERE"

[[ -f .env ]] || { echo "缺少 server/.env，请先 cp .env.example .env 并填写。" >&2; exit 1; }
set -a; source .env; set +a
: "${DOMAIN:?请在 .env 设置 DOMAIN}"

# --- 1. 用 .env 的 DOMAIN 渲染 Authelia 配置中的 {{DOMAIN}} ---
if grep -q '{{DOMAIN}}' authelia/configuration.yml; then
  sed -i.bak "s/{{DOMAIN}}/${DOMAIN}/g" authelia/configuration.yml && rm -f authelia/configuration.yml.bak
  echo "已将 Authelia 配置中的域名替换为 ${DOMAIN}"
fi

# --- 2. 生成 Authelia 所需密钥(若尚未生成) ---
mkdir -p authelia/secrets
gen() { [[ -s "authelia/secrets/$1" ]] || openssl rand -base64 48 | tr -d '\n' > "authelia/secrets/$1"; }
gen JWT_SECRET
gen SESSION_SECRET
gen STORAGE_ENCRYPTION_KEY
echo "Authelia 密钥已就绪(authelia/secrets/，已被 .gitignore 排除)。"
echo "  提示：把这三个 secret 以 AUTHELIA_*_FILE 环境变量挂载，或参考官方 secrets 文档接入。"

# --- 3. 用户库 ---
if [[ ! -f authelia/users_database.yml ]]; then
  cp authelia/users_database.yml.example authelia/users_database.yml
  echo "⚠️  已从模板创建 authelia/users_database.yml，请生成密码哈希并替换占位："
  echo "    docker run --rm authelia/authelia:latest authelia crypto hash generate argon2 --password '你的强密码'"
fi

# --- 4. Filebrowser 数据库占位 ---
touch filebrowser/filebrowser.db

# --- 5. 起服务 ---
echo "启动 docker compose ..."
docker compose up -d

cat <<EOF

✅ 部署完成。请确认：
  • DNS：*.${DOMAIN} 已解析到本服务器公网 IP
  • 防火墙：仅放行 443(以及 80 用于 ACME)，其余端口不对公网开放
  • 访问 https://auth.${DOMAIN} 完成首次 TOTP 绑定
  • 然后 https://term1.${DOMAIN} 登录+2FA → 终端运行 claude

排错：docker compose logs -f caddy authelia
EOF
