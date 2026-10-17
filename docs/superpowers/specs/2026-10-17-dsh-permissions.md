# DeepSeek 会话权限

只修改当前会话，选项来自 DSH `permissionPresets/catalog`；当前值来自 `session/projections` 的 `values.permissions.currentValue`。以 `commands/execute` 的 `/permission <preset>` 写入，检查命令结果并回读确认，不修改全局新会话默认值。

- REQ-1：显示 host 广告的全部选项及当前值，包括自定义预设；未知值不伪装为 Codex 默认值。
- REQ-2：写入前重新校验目录；无效选项、RPC 错误、命令拒绝或回读不一致均失败，浏览器恢复最后确认值。
- REQ-3：旧 agent 或 host 无权限能力时禁用入口；权限读取失败不阻断聊天，但移除可操作选项。
- REQ-4：不在消息发送/队列投递时重放 DSH 权限选择；下次控制快照读取 Desktop 的最新设置。Codex 行为保持不变。
- REQ-5：前端随 Web 发布，Mac 只开发测试，等待用户授权后签名公证发布。

协议依据：当前安装 DSH Desktop 的 dsh-permission-presets、dsh-commands 与 dsh-api-session-controller 生成契约和实现。当前会话使用 command，settings/mutate 仅设置新会话默认值。

验收映射：REQ-1/2/3 → dsh_permissions_test.go、chat_model.test.mjs；REQ-4 → 既有队列能力与投递测试。正式客户端与真实权限切换验收待签名发布。

验证记录：2026-10-17 已在本机运行中的 DSH 只读调用目录与现有会话投影，目录返回 read-only/workspace-write/danger-full-access，当前值返回 workspace-write。没有修改真实会话权限。后端切换与错误路径由隔离 RPC 测试覆盖；前端覆盖旧 agent 禁用、动态选项、读回以及失败回退。Mac 正式签名发布与真实写入验收尚未执行。
