# DeepSeek 会话菜单与网页入口收敛

## 已确定的界面调整

- 网页仅显示 ChatGPT（内部 assistant 仍为 codex）和 DeepSeek；保留 agent 的 Claude 扫描。
- 会话设置移除终端页和自绘开关，始终使用聊天界面；旧 Claude 偏好回退 ChatGPT，旧终端快照不再恢复。
- 移除聊天错误时的终端回退入口，统一可见的 ChatGPT 名称。

## DeepSeek 菜单方案（待用户审阅）

推荐完整复用 ChatGPT 的菜单：置顶/取消置顶、重命名、归档/移回当前、删除。

- 重命名与删除调用正在运行的 DSH host：`session/rename`、`session/delete`，参数均为 `request` 包装；前者含 sessionId/title，后者含 sessionId。
- 置顶及可恢复归档保存在 Fleet 自己的每台 Mac 偏好文件，以 assistant/sessionId 隔离；跨浏览器共享。DSH Desktop 的原生归档状态不被修改，网页明确标为“在 Fleet 中归档”。
- 列表读取后统一按 Fleet 归档状态筛选、搜索，置顶优先；刷新保留结果。恢复归档移回当前列表。
- 删除仍有二次确认；成功后清理该会话缓存和 Fleet 偏好，失败保留当前状态并显示错误。
- DSH host 不可用时原生写操作明确失败；不启动第二个 host，不写 DSH 会话库或内部配置。

选择此方案的原因：本机 DSH Desktop 的生成协议具备 `workspace/archiveSession`，但没有取消归档接口；直接调用它无法实现与 ChatGPT 一致的可恢复归档。另一可选方案是使用原生归档、暂不提供移回当前，但会改变用户预期的菜单行为。

## 实施及验证边界

网页收敛为独立、可回滚的静态发布。DeepSeek 后端扩展需在方案确认后按 TDD 实现：覆盖 assistant 分派、原生 RPC 参数及失败传播、归档筛选与跨重启持久化、菜单真实点击对应正确助手。

后端经 `scripts/verify.sh` 后，必须通过签名机 `scripts/release-fleet-agent.sh` 完整签名、公证和逐机部署；不得从开发机替换正式二进制。真实验收只操作专门创建的测试会话，不修改或删除用户已有会话。
