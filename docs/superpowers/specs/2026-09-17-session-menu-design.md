# DeepSeek 会话菜单与网页入口收敛

## 已确定的界面调整

- 网页仅显示 ChatGPT（内部 assistant 仍为 codex）和 DeepSeek；保留 agent 的 Claude 扫描。
- 会话设置移除终端页和自绘开关，始终使用聊天界面；旧 Claude 偏好回退 ChatGPT，旧终端快照不再恢复。
- 移除聊天错误时的终端回退入口，统一可见的 ChatGPT 名称。

## DeepSeek 菜单方案（2026-09-18 用户确认）

复用 ChatGPT 的同一个菜单组件：置顶/取消置顶、重命名、原生归档、删除。

- 重命名、归档和删除均调用正在运行的 DSH host：`session/rename`、`workspace/archiveSession`、`session/delete`；参数均以 `request` 包装，包含 sessionId，重命名另含 title。
- 用户明确选择原生归档：不增加 Fleet 归档状态，不改 DSH 内部文件；由于当前原生协议没有取消归档，已归档菜单暂不提供“移回当前”，后端也明确拒绝 unarchive。
- 列表通过 `workspace/follow` 的 baseline 获取权威 archivedSessionIds，配合 session/list 实现当前/已归档筛选和搜索。读不到权威归档状态就报错，不能把已归档会话错误显示为当前。
- 置顶沿用 ChatGPT 的 Fleet 偏好语义，复用原子写入逻辑；DeepSeek 使用独立的 `~/.macfleet/dsh-thread-pins.json`，不写入 DSH 数据目录。
- 删除仍有二次确认；仅成功后清理该会话缓存和置顶记录，失败保留当前状态并显示错误。
- 新旧 agent 滚动发布期间，由 /api/info 的 dsh.sessionActions 标明菜单能力；旧 agent 不显示不能执行的操作。
- DSH host 不可用时原生写操作明确失败；不启动第二个 host，不写 DSH 会话库或内部配置。

## 实施及验证边界

网页收敛为独立、可回滚的静态发布。DeepSeek 后端扩展需在方案确认后按 TDD 实现：覆盖 assistant 分派、原生 RPC 参数及失败传播、原生归档筛选、置顶持久化、菜单真实点击对应正确助手。

后端经 `scripts/verify.sh` 后，必须通过签名机 `scripts/release-fleet-agent.sh` 完整签名、公证和逐机部署；不得从开发机替换正式二进制。真实验收只操作专门创建的测试会话，不修改或删除用户已有会话。
