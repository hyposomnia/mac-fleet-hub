# Titanium Web v156 发布验证

- 发布来源：`codex/titanium-web-release`，基于生产 `origin/main`。
- 仅移植已批准的 Titanium 样式、图标、主题控制器和菜单键盘交互；保留 Authelia 退出、`/api/nodes.json` 和既有设备路由。
- 不包含多用户服务器、账号页、客户端、dist 或系统配置变更。
- `bash scripts/verify.sh` exit 0；Go 两层通过，Dashboard 226/226，Shell 所有检查通过。完整输出见 verify.txt。
- PWA 缓存及 HTML 资源引用统一为 v156，新 theme.js 在 CSS 前同步加载。

## 发布与回滚

从不可变提交的 `server/dashboard` 生成 Git archive，发布前将线上静态目录备份到非 Web 目录。覆盖静态资源并保留运行时 `api/`，恢复目录 755、文件 644，无需重启 nginx。逐文件验证 SHA256，检查 nodes.json 非空及 nginx/fleet-enroll/headscale/fleet-nodes.timer。

回滚：将备份解压到临时目录，覆盖原静态文件（排除运行时 api/）；旧外壳 sw.js 使用 network-first 更新。不要删除当前节点 JSON。
