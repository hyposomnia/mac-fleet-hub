# Desktop 拥有的 Codex 共享运行时

状态：main 共享核心已实现并隔离验收；SaaS 适配进行中。适用 main 的共享核心；SaaS 原生安装器须使用同一启动契约后才能发布。

## 意图与证据

Fleet 与 Desktop 同时使用同一会话、同一物理 writer，并保留内置浏览器。26.1007.71326 的原生 browser-use 授权要求当前 Desktop 是工具进程的祖先；launchd 启动的 keeper 即使使用 OpenAI 签名 Node 也不满足。2026-10-16 的隔离 PoC 已真实打开页面、点击并截图，两客户端同时恢复同一线程成功。

进程链为 Desktop → bundled signed Node bridge → bundled Codex listener → 官方工具进程。Fleet 只连接该 listener；Unix 0600 TCP 透明代理保留旧已公证 agent 的连接协议。不得改 App、签名验证、站点授权或会话 writer 锁。

## 契约

| ID | 可执行验收 |
| --- | --- |
| REQ-01 | 只有 Desktop 直接启动、默认 stdio app-server 的正式 initialize 才创建/附加共享运行时。网络检查、显式 --listen、预览及其他 CLI 命令原参数透传。正式握手依据 Codex Desktop 客户端、requestAttestation 布尔值和 optOutNotificationMethods 数组；未知 Desktop 握手报兼容错误，不猜 plugins 参数。 |
| REQ-02 | 同用户同配置并发启动仅一个 listener。首次 spawn 与 guard 登记之间极窄的 owner 崩溃窗口若留下进程，端口占用会阻止替代启动并要求人工恢复；不冒险接入未知进程。系统 flock（lockf -k）持锁，不使用文件存在、TTL 或 PID 作为锁；附加者核对 Desktop 进程实例、owner 实例、Codex 实例、端点和配置。其他 Desktop 实例、占用端口、无效或不安全状态拒绝附加。 |
| REQ-03 | listener 只绑定 ws://127.0.0.1:port；Unix 代理、状态文件 0600，状态目录 0700。拒绝符号链接/异属主/可组写状态，不删除普通文件冒充 socket。此模式不承诺同机同 UID 的隔离。 |
| REQ-04 | stdio ↔ WebSocket 逐条原样转发 JSON-RPC，包括未知字段、server request、审批和大于 16MiB 的行；遵守背压，排队有界（64MiB，超限明确断开），不重放写请求。 |
| REQ-05 | 一个 stdio 连接结束不停止仍由 Desktop 拥有的 runtime；附加者退出不杀 owner。Desktop 退出或 runtime 崩溃后关闭代理、清理自身状态，Fleet 显式 unavailable；不得启动独立 writer。owner 强制崩溃由持锁 guard 回收已登记的 Codex，锁释放前完成回收。 |
| REQ-06 | 安装 hook 到持久目录，通过 CODEX_CLI_PATH 启动；清除旧 WS/local-daemon 设置并设置 FORCE_CLI。保存 GUI 原值；重复启用不覆盖备份；恢复只改仍属于 Fleet 的值，用户后改的值保留。外部 CLI override 冲突在修改前拒绝。agent 不得再持续注入旧 WS 地址。 |
| REQ-07 | 安装不打断活动 turn；首次迁移要求 Desktop 关闭且旧服务空闲，失败恢复文件和环境。配置安装成功与 runtime ready 分别报告；只有 Desktop 启动后才检验 listener。agent 更新/重启不改变 Desktop runtime。 |
| REQ-08 | 完全重开的隔离签名 Desktop 中，使用正式 bridge，两个客户端恢复相同 thread，验证唯一 writer PID；内置浏览器打开本地页、点击、截图，native 日志无 ancestry 拒绝。正常权限审批保留。 |

## 设计与生命周期

launcher 每次从 App manifest/已知布局解析官方 CLI 与 Node，exec Node，不复制 App 二进制。私有 JSON 安装配置给出 appPath、listenURL、proxySocket、codexHome 和 stateDir；安装路径不引用临时目录。

bridge 先读取第一条 initialize，分类后决定透传或共享。网络检查使用 Desktop 的 network-initialize 协议；LaTeX 用独立 clientInfo。正式 profile 属于观察到的集成契约，升级改变时应失败可见并重新 UAT，不宣称 OpenAI 公共兼容保证。

启动者竞争同一永久 flock 文件。锁由 guard 子进程持有，guard 收到父进程管道 EOF 时停止已登记 Codex 并等待退出才释放锁；guard 不在 browser 祖先链上。持锁 owner 创建 listener，确认 /readyz 后创建 Unix proxy，原子发布完整状态。附加者只附加已验证且 ready 的同一代实例；启动超时 15 秒。端口有未知服务时拒绝接入。

Desktop 关闭会丢失内置浏览器宿主，因此本架构明确要求 Desktop 常驻。Fleet 不自动打开 Desktop，也不切换独立后端；队列和原有请求恢复规则不变。进程强制退出属于运行时故障，进行中的请求可能失败；不自动重放 turn/start。普通 stdio EOF 只断开该连接，owner 保持父子链直到 Desktop 退出。

## 交付 / 回滚

先 main 规格与红绿测试、核心和安装适配、全仓验证、隔离真实 UAT；再将公共核心合入 SaaS 并调整原生生命周期。正式发布必须走签名机构建公证流程。迁移前关闭 Desktop，检查 idle；保留旧 plist/helper/env 的备份。失败使用同一事务恢复，不能以切换独立 writer 作为恢复。

## 覆盖矩阵

| 需求 | 自动化证据 | 实现 |
| --- | --- | --- |
| 01 | tests/codex-desktop-bridge.test.mjs 分类/辅助进程 | mac/codex-desktop-bridge.mjs |
| 02/03/05 | 同文件真实多进程/冲突/退出/崩溃测试 | bridge 与 runtime guard |
| 04 | 同文件真实 WS 双向、大行和背压测试 | bridge transport |
| 06 | tests/codex-desktop-env.test.mjs | env manager；agent 启动不再改 GUI |
| 07 | 安装迁移行为测试及 shell 检查 | setup / migrate / uninstall |
| 08 | 隔离 UAT：docs/superpowers/verification/2026-10-16-desktop-owned-codex-runtime.md | 正式 launcher/bridge + 签名 Desktop |
