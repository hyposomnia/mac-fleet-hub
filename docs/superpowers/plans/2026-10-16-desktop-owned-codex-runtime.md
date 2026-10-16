# 实施与证据记录

1. 固定握手、生命周期和迁移契约 → specs/2026-10-16-desktop-owned-codex-runtime.md。
2. 分类、传输、并发 owner 的失败测试 → 最小实现 → 真实子进程验证。
3. GUI 环境所有权失败测试 → hook 安装、清理与 Go agent 适配。
4. 安装/迁移的失败及回滚验证 → 文档和 CHANGELOG。
5. 全仓 verify、正式 bridge 的签名隔离 Desktop 浏览器及同 writer UAT。
6. 公共核心同步 SaaS 原生安装器；按项目正式发布边界交付。

2026-10-16：规格已建立；原型可行性通过，生产实现尚未验收。不修改正在承载本聊天的 Desktop/47682 服务。

2026-10-16：main 核心、GUI 环境所有权、事务 hook 安装和 shell 迁移已完成。19 个新行为测试与全仓 verify 通过；正式 bridge 的隔离签名 Desktop 页面打开/点击/截图和双客户端唯一 writer UAT 通过。SaaS 原生适配尚在下一步；生产安装未替换。

2026-10-16：最终公共核心共 22 项 Node 行为测试与 main 全仓 verify 通过。SaaS 原生适配已在独立工作树完成，保留用户/设备授权协议，包含双域环境、后台包与脚本资源一起回滚及只读 doctor；签名应用的完整 UI 升级验收须随正式发行完成。
