# DeepSeek 原生会话菜单实施计划

按 2026-09-18 用户已确认的原生归档方案执行；缺少原生取消归档时暂不支持恢复。

## 1. 原生操作与路由

Files: `mac/fleet-agent/dsh_catalog.go`、`dsh_catalog_test.go`、`main.go`。

- [x] 先通过真实 HTTP handler 测试 `assistant=dsh` 的 rename/archive/delete。断言假 host 收到精确端点及 `request` 参数；unarchive 必须非 200 且没有 host 写调用，host 错误不能变成功。
- [x] 运行 `cd mac/fleet-agent && go test -run TestDSHSessionAction -v`，确认现有代码因返回 400 而失败。
- [x] 实现 `(*dshChatBackend).mutateSession(ctx context.Context, sessionID, action, value string) error`，在现有 handleSessionAction 内按助手分派，不改变 Codex 接口。写调用不自动重放。

请求例：`{"assistant":"dsh","sessionId":"session-menu-test","action":"rename","value":"新标题"}`。
对应 host：`session/rename`，args 为 `{"request":{"sessionId":"session-menu-test","title":"新标题"}}`。

## 2. 原生归档列表与置顶

Files: `dsh_catalog.go`、`dsh_catalog_test.go`、`codex_catalog.go`、`main.go`。

- [x] 先测试 `workspace/follow` baseline 中的 archivedSessionIds 对当前/归档列表产生互斥结果；中文标题搜索、取消流、非法首帧与超时取消均验证。
- [x] 运行 `go test -run TestDSHSessionList -v` 看失败，再实现 10 秒有界目录读取；归档读取失败直接返回错误。
- [x] 提取已有置顶读写为接受路径的共享函数，保留 Codex 原路径和包装函数；DeepSeek 路径由 ChatQueueFile 所在 Fleet 目录派生。测试置顶跨后端对象重建仍存在、取消置顶与成功删除清理、两助手互不干扰。
- [x] 运行 `go test -run 'TestDSHSession|TestCodex.*Pin' -v`。

## 3. 同一个网页菜单组件

Files: `server/dashboard/app.js`、`chat_model.test.mjs`、`index.html`、`sw.js`、`main.go`。

- [x] 先在现有 VM DOM 测试中渲染 DSH 会话行并点击菜单项，验证请求使用该行 assistant/macId；当前菜单含归档，已归档菜单不含恢复；失败删除保留缓存、成功删除清理缓存。
- [x] 把现有 renderCodexSessionMenu/mutateCodexSession 等改成助手无关的函数，按 dsh.sessionActions 显示原生支持的选项，保留 ChatGPT 的移回当前。
- [x] /api/info 报告 DSH sessionActions，升级 PWA 外壳版本；运行 `node --test server/dashboard/chat_model.test.mjs server/dashboard/assistant_gate.test.mjs`。

## 4. 验证和发布

- [x] 更新 CHANGELOG；运行 `bash scripts/verify.sh`：Go 通过，Dashboard 189/189，全部 Shell 测试通过。另跑定向 race 测试通过。
- [x] 桌面 1280px / 手机 390px 浏览器验证菜单点击、原生归档请求、归档菜单没有恢复项，无 JS 错误。
- [x] 用专门测试会话验收重命名→置顶→归档→归档列表可见→删除，不发送模型请求、不操作用户既有会话。`FLEET_DSH_MENU_UAT=1 go test -run TestDSHSessionMenuLive -v` 通过，测试会话已清理。
- [x] 本机以私有配置确认签名机身份，执行 `bash scripts/release-fleet-agent.sh --check`。Developer ID 签名证书可用，但钥匙串缺少 `mac-fleet-hub-notary` 公证 profile，预检失败。
- [ ] 公证凭据恢复后执行正式发布入口 `bash scripts/release-fleet-agent.sh`，不跳过签名、公证、空闲守卫或逐节点验证。
- [ ] 后端发布验证完成后备份并部署 dashboard；校验线上静态文件哈希、核心服务及登录入口。

源码按 main 正常提交、推送。发布因缺少公证凭据暂缓，未替换生产 agent 或部署 v126 网页；现网保留已经上线的 v125。此处凭据配置是实际发布前提，不是新增审批步骤。
