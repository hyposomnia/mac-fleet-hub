# 管理后台用户状态验收

日期：2026-10-15。分支：`codex/multi-user-server`。功能提交 `8040321`，最终验证包含并行界面更新 `044cdc2`。

- ADM-1：后台去除前台导航，品牌指向 `/admin`，外观使用现有三种图标；账号页导航与认证流程的既有测试保持通过。
- ADM-2：先运行新增 Go 测试，真实失败为用户 `online=<nil>` 和 `page_size=0`；实现后最近活动、300/301 秒边界、过期、未完整认证、禁用、退出、多个会话及零设备用户全部通过。
- ADM-3：列表显示用户邮箱、在线与设备数；服务端 `page_size` 驱动分页，保留编码搜索及权限隔离测试。
- ADM-4：先运行新增账号页测试，真实失败为仍有前台导航和缺少用户抽屉；实现后抽屉加载、设备空态、设备在线、失败重试、关闭后重新打开同一用户的迟到响应隔离、焦点恢复通过。
- ADM-5：详情只读取选定用户的设备与事件元信息，事件分页及边界通过；移除管理写操作入口，既有后台 API 与权限测试保持通过。

在测试子进程中过滤继承的 `FLEET_*` 后执行 `bash scripts/verify.sh`，保留系统 PATH / HOME，不改变运行服务环境。合并最终代码后的真实输出节选：

```text
==> Go 测试：mac/fleet-agent (go test ./...)
ok  fleet-agent (cached)
==> 原生组网验证：系统路由、IP TLS 目标身份及证书拒绝条件
ok  fleet-agent 0.789s
==> Go 测试：server/enroll (go test ./...)
ok  fleet-enroll (cached)
ok  fleet-enroll/multiuser (cached)
==> Dashboard JS 测试：server/dashboard (node --test)
ℹ tests 21
ℹ pass 21
ℹ fail 0
ℹ tests 423
ℹ pass 423
ℹ fail 0
Executed 112 tests, with 0 failures (0 unexpected)
tailscale-utils tests passed
==> 全部验证通过 ✓
VERIFY_EXIT_CODE=0
```

账号页及主题专项：62/62 通过。完整入口的打包测试中，已有的 `SDK preparation replaces a tampered extracted cache from the verified archive` 按原测试条件跳过，其余执行项通过。

本轮 Chrome 连接未成功，随后原生控制被停止；未取得截图或浏览器视觉验收证据。源代码与测试结果不代表已上线。按本轮 AGENTS.md 的源码、提交推送及验证授权范围，本次不部署服务器、不替换 Mac、launchd 或 Desktop。
