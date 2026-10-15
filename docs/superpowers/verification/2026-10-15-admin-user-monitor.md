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

Chrome 控制不可用，未取得截图或浏览器视觉验收证据。HTTP、文件及进程核验与浏览器视觉验收分别报告。

## pm 服务器发布

- 服务端源码 `11323384e44650669a06dff754fe2f80314c68d2`，由干净提交编译 Linux amd64，不涉及 Mac 产物。
- 发布前确认 pm 正在运行的旧服务端仍来自 `85fa8d7`，不含用户在线查询；网页已包含新版后台。此次同步服务端与网页，避免接口与页面不一致。
- 发布到 `/opt/macfleet-saas-uat/releases/full-11323384e44650669a06dff754fe2f80314c68d2`。143 个发布文件的清单与 SHA-256 全部匹配，拒绝额外文件。
- 原目录 `full-32caa0cee1b3a7cef2160be50961c9f67c006b19` 保留；数据库使用 SQLite 在线备份并通过 integrity_check，原加密密钥与配置备份在 pm 私有 `backups/admin-11323384e44650669a06dff754fe2f80314c68d2-20261015T050658Z`，未修改数据库 schema。
- `current` 原子切换，重启 `macfleet-saas-uat.service`，新 PID `202553`；运行进程与磁盘二进制 SHA-256 均为 `d231aa84de22ff4165960d0c20cdf7ffd9ef35c16315ddf17da9bfe39cf18de2`。
- 两个 SaaS systemd 服务均 active；公网 `healthz=200 ok`、`readyz=200 ok`，未登录 `/admin` 为 303 到 `/auth?next=%2Fadmin`，未登录管理员用户 API 为 401。
- Main 文档提交 `d77c9b3` 与 SaaS 文档提交 `1132338` 已推送，移除旧合并及上线限制；正式 Mac 签名公证要求保留。两条分支各自执行 `bash scripts/verify.sh`，均退出 0。

## 最终网页核验

- 网页源码 `e0ce6d776ece83159fa3bb6d130bde34adfc6cf7`，包含并行提交 `8a47d21`；移除后台不再使用的 `device_appearance.js` 引用。后台依赖的六个资源均从公网实际下载，HTTP 200 且逐字节、SHA-256 与源码一致。
- 最终目录 `/opt/macfleet-saas-uat/releases/full-e0ce6d776ece83159fa3bb6d130bde34adfc6cf7`；143 个发布文件清单与 SHA-256 一致。沿用已验证的 `1132338` 服务端，PID 仍为 `202553`，此轮网页切换未重启服务。
- 再次验证公网：`/healthz HTTP 200 ok`、`/readyz HTTP 200 ok`、`/admin HTTP 303`、`/api/admin/users HTTP 401`。
- 最终源码再次运行完整 `bash scripts/verify.sh`：Go、Dashboard 21+423 项、Swift 112 项及 Shell 验证通过，退出 0；既有 SDK 缓存破坏测试仍按原条件跳过。

## 上次访问时间（ADM-6）

- 列表在在线状态与设备数之间增加“上次访问时间”，沿用本地时间格式；零值及缺失值显示 `—`，不显示 Unix 原始数字或 1970 年。
- 新增测试先真实失败：管理员响应缺少 `last_access_at`，旧库缺少该列，列表缺少时间列。实现后新时间记录、设备心跳隔离、退出及重启后保留、较早请求不覆盖较新时间均通过。
- 迁移覆盖已过期 full 会话与待验证会话，保留可用的历史访问记录；故障注入令回填失败时，启动失败且新增列回滚，解除故障后可迁移并重复启动。
- 合并并行界面提交 `73cfeba` 后执行 `bash scripts/verify.sh`，真实输出为 `==> 全部验证通过 ✓`、`VERIFY_EXIT_CODE=0`；Go multiuser 25.607s、Dashboard 21+423 项、Swift 112 项及 Shell 检查通过，既有 SDK 缓存测试按原条件跳过。
- 发布源码 `55d83bbe0931d4cfd7095ecd3878dbb778041651`，pm 当前目录 `releases/full-55d83bbe0931d4cfd7095ecd3878dbb778041651`；143 个发布文件的清单及 SHA-256 匹配。原版本与私有一致备份保留。
- 重启后 PID `205332`，运行进程及磁盘二进制 SHA-256 为 `f05461d7d79258c230dcbcee4699ccf16b617b755645c188b09d927b7277056c`；实际数据库中 5 个原有用户的访问时间均不小于备份中的可恢复记录，integrity_check 通过，加密密钥保持一致。
- 六个后台资源从公网下载均为 HTTP 200，逐字节与源码一致；下载的 account.js 含新增时间列及对应格式化逻辑。公网 `/healthz HTTP 200 ok`、`/readyz HTTP 200 ok`、未登录 `/admin HTTP 303`、管理员用户 API HTTP 401。浏览器视觉验收仍未取得证据。
