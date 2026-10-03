# Web 导航 v161

发布范围：设备栏独立收起、会话列表图钉固定/取消固定与悬浮呼出，新建会话移至搜索框右侧。只发布生产兼容的 dashboard 静态资源；不包含多用户服务器、鉴权、Mac 客户端或网络配置。

提交前验证：`bash scripts/verify.sh`，真实完整输出见 `release-verify.txt`。覆盖 Go agent/enroll、dashboard JS、shell 工具及部署守卫。8 个新增侧栏测试覆盖独立偏好、浮层关闭、固定恢复、文件模式、移动断点、受限存储及栅格定位。

## 生产发布

2026-10-02 03:25:57 UTC，从已推送到 main 的不可变提交 `66964d0`，通过 `git archive 66964d0:server/dashboard` 发布 103 个静态文件。完整远端输出见 `production.txt`，公网未认证请求输出见 `http.txt`。

- SHA256：103/103 一致；PWA 缓存 `fleet-shell-v161`。
- nginx、fleet-enroll、headscale、fleet-nodes.timer 均为 active。
- `api/nodes.json` 非空，原运行时 api 目录保留。未重启服务。
- 公网入口返回 HTTP/2 302 至既有认证入口。
- 回滚备份：`/var/backups/mac-fleet-hub/dashboard-before-v161-20261002T032557Z.tgz`，root 0600。回滚时从该备份恢复静态文件，保留 api 目录，再校验旧提交 `40b8373` 的静态文件 SHA256。

## 线上浏览器验收

在 Chrome 已登录的真实生产页 `https://fleet.hyposomnia.top:20443/?release=v161` 应用 PWA 更新后验证：

- 设备栏收起为单列；会话/文件和设备图标竖排，设备名称隐藏；展开恢复。
- 新建会话位于助手选项卡下方、搜索框右侧。
- 取消固定移除会话列，主窗口保持完整可用；悬浮列表入口出现。
- 点击入口呼出原会话列表浮层，显示取消固定图钉；Escape 关闭并恢复入口焦点。
- 点击浮层图钉重新固定列表，图标切为实心，列表恢复普通侧栏。
- 验收结束恢复设备栏展开、会话列表固定，真实列表正常展示。

外部点击关闭、选择会话关闭、浏览器偏好持久化和移动断点行为在新增自动测试中通过；没有创建、发送或归档真实会话。本次未执行 Mac 客户端安装或二进制发布。
