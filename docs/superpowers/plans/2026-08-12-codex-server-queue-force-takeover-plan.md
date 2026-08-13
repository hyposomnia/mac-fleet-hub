# Codex 服务端队列与强制接管实现计划

1. 在 `mac/fleet-agent/chat_queue.go` 定义队列项、状态机、原子 JSON 存储和 session FIFO 查询；在 `chat_queue_test.go` 先覆盖持久化、重启恢复、clientMessageId幂等和非法转换。
2. 在 `mac/fleet-agent/chat.go` 增加 queue list/enqueue/decision HTTP handlers并注册路由；在 `chat_handlers_test.go` 覆盖请求校验、409语义和响应字段。
3. 在 `mac/fleet-agent/codex_chat.go` 增加服务端 worker适配器：复用现有 `Resume`/`Input`，writer冲突进入 waiting，成功记录 turnId；测试浏览器 context取消不停止任务、重启不重复投递、session FIFO。
4. 在 `mac/fleet-agent/codex_takeover.go` 实现默认 daemon writer审计、active rollout判定、审计版本和受控重启；所有进程操作注入函数，测试第一次 force无副作用、confirm-force审计变化拒绝、确认后重启并重新投递。
5. 在 `server/dashboard/chat_model.js` 增加队列消息投影与按 clientMessageId对账；测试队列状态不与 rollout user_done重复。
6. 在 `server/dashboard/app.js` 移除 localStorage权威队列，增加旧数据迁移、queue API和SSE处理；入队立即渲染右侧消息，所有操作走服务端。
7. 在 `server/dashboard/style.css` 实现右侧消息下状态卡，复用 chat request/approval视觉层级；在 `chat_model.test.mjs` 增加DOM、动作和持久队列契约测试。
8. bump PWA版本，更新 `CHANGELOG.md`；运行 `gofmt`、Go定向测试、Dashboard测试和 `bash scripts/verify.sh`。
9. 用 `-trimpath -ldflags="-s -w"` 重建amd64/arm64 dist，合并远端最新代码后提交推送。
10. 部署网关静态文件和三台可达 Mac agent；在 `.3` 真实验证：入队→关页面→队列仍在→writer释放/强制接管→消息只发送一次。
