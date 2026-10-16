# Desktop-owned Codex runtime 正式发行记录

2026-10-16。签名、公证、分发和两类客户端的实际迁移已执行；全部节点迁移及生产浏览器页面验收仍有待办，不能据此宣称全面完成。

## 发行产物

- CLI agent：`9f36866-20261016`，产物提交 `f1eaa87`，Developer ID identifier `com.macfleet.fleet-agent`。双架构公证 `29e505f6-f6a3-4fbf-9501-bd8a0645b91f` 为 Accepted；正式发布入口完成网关备份、公网下载 SHA/验签及目标机自更新、PID 和 health 核验。
- CLI shared 配置：提交 `7958718`，正式配置入口完成 bundle 分发与 Desktop-owned runtime UAT。真实 Fleet 恢复测试 thread 返回 shared/read_write，writer 锁只有共享 listener PID；两个 RPC 客户端恢复同一 thread 并相互读取改名结果。
- 原生应用：`0.1.13+14`，源码 `7bb5309f341cebb0b5a371da285af0388db38147`。唯一入口完整执行 Universal 构建、真实组件隔离运行、签名、公证、staple、Sparkle 签名、公网逐字节下载比对和原子晋级。

| 原生产物 | Accepted submission |
| --- | --- |
| Fleet Agent | `0df2fd9d-b403-4e63-962c-5c5c7523e7c6` |
| Settings app | `569f47d5-11f9-4739-a4de-9ae7180a5785` |
| DMG | `d80f1570-376f-447c-a816-f1850a5cc6ad` |

DMG SHA-256：`5da1a045590f503ea68015f814d6f8fcc5508f16b20ce5616e2eeba3f71f7ac9`。
更新 ZIP SHA-256：`505149747b0b3474eee8c656f06e4cfe8a0e42ee43255087622fda42dc6f4cd9`。

## 实际迁移与修复

首台空闲原生目标通过正式公网安装命令升级至 `0.1.13+14`。应用及后台严格验签、Gatekeeper、精确版本/PID、设备绑定字节未变及 runtime health 检查通过。Desktop → bridge → bundled Codex 祖先链已验证。真实网关恢复同一测试 thread 为 shared/read_write，writer 仅共享 listener 持有。

该机遗留 CLI-owned Codex LaunchAgent，原生安装器正确拒绝接管。运维先核对文件 uid、加载路径与旧 Fleet 命令，连续确认 idle 并备份，再退役两个旧 CLI job及其已知 GUI overrides，随后重跑原生安装。没有削弱原生归属检查。

实际 rollout 发现并完成红绿回归与修复：

1. setup 内部和迁移外部均需等待 launchd 健康监听；仅修外层不能覆盖安装器提前失败。远端检查失败不按 SSH 故障重跑服务替换，仅 transport exit 255 重试。
2. `/tmp` 目录别名令 Node ESM 的主入口比较静默跳过 CLI；安装与迁移统一使用 `pwd -P`，真实 Node 进程的目录别名测试覆盖该路径。
3. 公网一行安装器需按 Apple lipo 语法将输入文件放在变长 `-verify_arch` 操作之前。严格工具契约与真实双架构 Mach-O 均加入验证；服务端修复 `5704b861b274894ccafa448776d7954b405e20fc` 已部署，数据库一致备份、旧发行和配置密钥保留，healthz/readyz 200，现有 dashboard 文件不变。原生产物未重新签名或替换字节。

`bash scripts/verify.sh` 在 main 和 SaaS 均 exit 0，真实末行 `==> 全部验证通过 ✓`。SaaS 包括 438 项 dashboard、113 项 Swift 和 22 项公共核心行为测试；既有可选跳过项保持明确计数。

## 尚未完成的验收

- 生产浏览器真实页面打开/点击/截图被 Codex 保存的站点拒绝策略阻断。用户已再次授权该本地测试源，随后工具仍返回 saved permission denies access。自动工具另拒绝操作 Codex 自身界面；已交用户在 Settings → Browser 更改站点权限，未改权限文件绕过门禁。前一阶段隔离实例页面/点击/截图证据仍属于隔离 UAT，不冒充本轮生产验收。
- 另一 CLI 目标的旧 Desktop 版本没有可调用的 cua_repl server；共享 writer 验证通过，浏览器验收未通过。
- 当前工作 Mac 等待本轮完成并关闭 Desktop；已准备一次性延后安装，关闭后仍须连续 idle 检查，才运行正式安装器并重开 Desktop。另一活动或无法确认 idle 的目标未重启。
- 原生目标的 FDA 提示仍交系统权限引导，未代替用户授权；它不作为聊天/browser 验收通过的证据。

本地私有证据目录：`~/.codex/visualizations/2026/10/16/01a142b1-39b0-7720-820d-90bc844f5dc4/fleet-runtime-release/`。真实节点/部署地址不进入此记录。
