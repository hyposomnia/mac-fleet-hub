# Codex 原生标题生成调用验证

2026-10-17，在签名 Mac 当前运行的 Codex Desktop shared app-server 上验证。
源码基线：`ad55476097c3060c84730adab5eca4ba2e7adc02`。

## 结果

通过。复用当前 Desktop 登录态和共享 app-server，无额外 API key。
本次只验证协议可用性，未接入 Fleet 自动命名，也未发布客户端。

原生 Desktop 的 `ThreadMetadataGenerationService.generateTitle` 本身是内部 IPC 服务；
它最终通过 app-server 创建 `ephemeral: true` 的线程，执行结构化生成，再调用
`thread/name/set` 更新正式会话。Fleet 可复用下层调用链，无须调用该内部 IPC。

原生实现会选择专用临时生成模型，并在不可用时选择模型列表中的合适默认模型。
本探针从 `model/list` 选择实际公布的默认模型 `gpt-6.1-sol`，不宣称与原生专用模型完全一致。

## 真实调用

1. 通过 `~/.macfleet/codex-app-server.sock` 连接现有 shared app-server。
2. `thread/start` 设置 `ephemeral: true`、`threadSource: "thread_title"`、
   `permissions: ":read-only"`、`approvalPolicy: "never"`，禁用工具、应用、技能和记忆。
3. `turn/start` 提供标题与简介的 `outputSchema`，收集 `agentMessage`，等待 `turn/completed`。
4. 创建独立的非临时测试会话，`thread/name/set` 写回标题，`thread/read` 核对一致，最后归档。
5. 对临时生成线程执行 `thread/unsubscribe`；超时只中断探针自己的 turn。

首次实验发现 ephemeral 线程不能更新元数据，返回：

```text
ephemeral thread does not support metadata updates
```

所以生成线程保持 ephemeral，标题写回正式目标线程。没有改名或发送消息到既有会话。

成功实验使用合成需求“网页统一管理多台 Mac 的终端、文件和 AI 会话，手机查看任务状态”。

```text
advertised default model: gpt-6.1-sol
AI title: 多台 Mac 网页管理与移动端任务监控
AI description: 构建统一网页，管理多台 Mac 的终端、文件和 AI 会话，并支持手机查看任务状态。
thread/name/set + thread/read: title matches
--- PASS: TestCodexNativeTitleSpike (8.13s)
PASS
ok  fleet-agent 9.328s
```

## 复现

测试默认跳过，不在普通验证中连接真实 Desktop 或消耗模型用量。
显式运行会使用当前账号的一次模型调用，并创建、归档一个独立测试线程：

```bash
cd mac/fleet-agent
FLEET_TITLE_SPIKE=1 go test -run '^TestCodexNativeTitleSpike$' -count=1 -v .
```

需 Desktop shared runtime 已启动且当前进程获准连接私有 socket。

## 后续集成约束

- 首条消息触发异步生成，不阻塞消息发送。
- 生成线程临时、只读、工具关闭，限制输入、超时和重复请求。
- 正式会话持久化标题，尊重用户手动改名；生成期间需检查目标名称是否变化。
- 失败允许重试，不能把截取原文作为“AI 已总结”的成功结果。
- 同步列表、缓存聊天标题和正在打开的窗口。
