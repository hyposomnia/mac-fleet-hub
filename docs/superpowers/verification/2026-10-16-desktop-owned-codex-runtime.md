# Desktop-owned shared runtime 验证

2026-10-16，本机 macOS、ChatGPT 26.1007.71326、bundled Codex 0.162.0-alpha.17.2。独立 CODEX_HOME、Electron profile、临时端口，未替换承载开发会话的生产服务。

## SDD / TDD

规格：../specs/2026-10-16-desktop-owned-codex-runtime.md。分类、listener 安全、运行时并发/传输、GUI 所有权、事务安装分别先以缺失实现的断言失败，再实现通过。实机发现 manifest entrypoint 是 shell wrapper，修正到 OpenAI 签名 Mach-O；实际主客户端名为 `Codex Desktop`，与网络检查 `codex_desktop` 不同，以针对性红绿测试修正。

行为测试使用真实子进程、kernel flock、HTTP/WebSocket、Unix socket，替身只替代外部 Codex executable。验证审批双向传递、>16MiB 消息、并发唯一 PID、stdin EOF、SIGKILL 后回收、端口冲突、非 socket 文件保护、symlink 拒绝、配置冲突，以及 GUI 幂等/用户后改值/部分失败/事务回滚。普通 shell 层保留 plist/schema 检查。

## 实机 UAT

正式 launcher/bridge 经隔离安装器安装，真实签名检查与 `--version` 成功。Desktop PID 57270 → bridge 57398 → Codex 57450，线程 writer lock 只由 PID 57450 持有。两个额外客户端同时恢复同一真实 thread，一方改名另一方读取确认一致。

通过同一 app-server 的官方 cua_repl 工具创建 iab 页面 `http://127.0.0.1:49359/`，点击 Verify interaction 后可见 `Interaction verified.`，取得并查看真实截图。该源已由用户明确授权；仅隔离测试 thread 预置此授权，生产代码不修改站点权限。原生 Desktop 日志 ancestry 拒绝为 0。

冷启动的一次过早探针返回 Browser is not available: iab；待 Desktop 日志发布 browser backend ready 并在测试中 reload MCP 后打开、点击和截图成功。不能把 `/readyz` 当作全部浏览器后端就绪，也不能据此声称任何 Desktop 版本均兼容。

本地证据：`~/.codex/visualizations/2026/10/16/01a142b1-39b0-7720-820d-90bc844f5dc4/fleet-browser-production-bridge/{verification.json,browser-verified.png}`。截图和摘要无凭据；临时认证、profile、站点偏好在测试清理时删除。

## 自动验证

`bash scripts/verify.sh` 最终一轮 exit 0：Go agent/enroll、355 个 dashboard JS、22 个 runtime/env/install 行为测试和全部 shell 层均通过。真实输出为 `ok fleet-agent 3.143s`（首轮），`ok fleet-enroll (cached)`，`tests 355 / pass 355 / fail 0`，`tests 22 / pass 22 / fail 0`，最后 `==> 全部验证通过 ✓`。

源码验证、隔离实机验证与正式签名发布分别计数；此记录不声称生产部署完成。

补充红绿测试覆盖 legacy GUI 快照迁移、显式 80 端口、真实 Unix proxy 双向流量和元数据端点不一致。最终全仓检查后再次确认生产 `/readyz` HTTP 200；隔离实例、临时认证及站点偏好已清理。SaaS 原生适配及其签名发行仍在对应分支维护。
