#!/usr/bin/env bash
# mac-fleet-hub 卸载。可本地运行，也可免 clone：
#   curl -fsSL https://<网关域名:端口>/enroll/uninstall.sh | bash
#
# 移除：三个常驻服务 + 二进制 + 配置 + 残留 fleet- tmux 会话。可选退出 Headscale 网络。
set -uo pipefail

bold() { printf "\033[1m%s\033[0m\n" "$1"; }
bold "== mac-fleet-hub 卸载 =="

LA="$HOME/Library/LaunchAgents"
for svc in com.macfleet.ttyd com.macfleet.filebrowser com.macfleet.fleet-agent; do
  launchctl unload "$LA/$svc.plist" 2>/dev/null && echo "  停服务 $svc" || true
  rm -f "$LA/$svc.plist"
done

# 残留 fleet- tmux 会话
if command -v tmux >/dev/null 2>&1; then
  tmux ls 2>/dev/null | sed -n 's/^\(fleet-[a-z0-9]*\):.*/\1/p' | while read -r s; do
    tmux kill-session -t "$s" 2>/dev/null && echo "  收回 tmux 会话 $s"
  done
fi

rm -f "$HOME/.local/bin/fleet-agent" "$HOME/.local/bin/fleet-attach" "$HOME/.local/bin/filebrowser"
rm -f "$HOME/.macfleet-filebrowser.db" "$HOME/.macfleet-proxy.json"
echo "  已移除二进制与配置"

read -r -p "是否退出 Headscale 网络并卸载 tailscaled?（需要 sudo）[y/N] > " yn < /dev/tty || yn=N
if [[ "$yn" =~ ^[Yy]$ ]]; then
  TS="$(command -v tailscale || echo /opt/homebrew/bin/tailscale)"
  sudo "$TS" down 2>/dev/null || true
  sudo "$TS" logout 2>/dev/null || true
  sudo "${TS}d" uninstall-system-daemon 2>/dev/null || sudo tailscaled uninstall-system-daemon 2>/dev/null || true
  echo "  已退出网络并卸载 tailscaled"
else
  echo "  保留 tailscale（彻底移除：sudo tailscale down && sudo tailscaled uninstall-system-daemon）"
fi

bold "✓ 卸载完成"
echo "Homebrew 装的 ttyd/tmux 等未删除，如不需要可自行：brew uninstall ttyd tmux filebrowser"
