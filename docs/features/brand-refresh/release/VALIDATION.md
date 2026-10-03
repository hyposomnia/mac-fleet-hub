# Titanium Web v156 发布验证

- 发布来源：`codex/titanium-web-release`，基于生产 `origin/main`。
- 仅移植已批准的 Titanium 样式、图标、主题控制器和菜单键盘交互；保留 Authelia 退出、`/api/nodes.json` 和既有设备路由。
- 不包含多用户服务器、账号页、客户端、dist 或系统配置变更。
- `bash scripts/verify.sh` exit 0；Go 两层通过，Dashboard 226/226，Shell 所有检查通过。完整输出见 verify.txt。
- PWA 缓存及 HTML 资源引用统一为 v156，新 theme.js 在 CSS 前同步加载。

## 发布与回滚

从不可变提交的 `server/dashboard` 生成 Git archive，发布前将线上静态目录备份到非 Web 目录。覆盖静态资源并保留运行时 `api/`，恢复目录 755、文件 644，无需重启 nginx。逐文件验证 SHA256，检查 nodes.json 非空及 nginx/fleet-enroll/headscale/fleet-nodes.timer。

回滚：将备份解压到临时目录，覆盖原静态文件（排除运行时 api/）；旧外壳 sw.js 使用 network-first 更新。不要删除当前节点 JSON。

## 已执行的生产验证

2026-10-02 发布静态资源提交 `c9e923552b95b2fc0120a2a776e205d95b6867d0`。99/99 文件 SHA256 与不可变提交一致；节点 JSON 非空，四个服务 active；未登录公网入口 HTTP/2 302 到现有认证页面。完整命令输出见 production.txt。

备份：网关 `/var/backups/mac-fleet-hub/dashboard-before-titanium-20261002T005222Z.tgz`，root:root 0600，排除运行时 api/。

Chrome 中已登录页面强制刷新后，新标识、浅钛银外观和原三栏结构正确显示；节点为 4/4 在线，会话列表正常加载。实测浅色/深色切换、浅色刷新持久化、方向键菜单首项/后续项和 Escape 关闭恢复设置按钮焦点，测试后恢复浅色。未输入密码/TOTP，未发送聊天消息。

本次仅更新 Web 静态目录，未重启网关服务或改变当前 Mac/launchd/Desktop/生产网络。生产机源码 checkout 保持原状态，发布来源由上述不可变 Git 提交及 root 私有发布记录追溯。
