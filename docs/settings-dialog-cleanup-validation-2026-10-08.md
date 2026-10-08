# 设置弹窗与文案精简验收

## 范围

在独立分支 `codex/settings-copy-cleanup` 完成 Web 前端改动。不改变账号、权限、设备归属、服务鉴权、安装包验证或已安装的客户端；不发布旧生产网关。合并保留 main 上已经提交并上线的登录星光，主工作区未提交的彩带实验未夹带进入本批。

## 改动清单

- 删除统一设置弹窗顶部标题行，以及会话/自动化子页重复标题。页面名称仍由左侧或手机横向菜单展示；隐藏名称继续用于无障碍标识。
- 关闭按钮移到右上角，实际热区为 44×44；手机横向菜单为其留出空间，不显示多余滚动条。自动化使用文档与关闭按钮不重叠。
- 下载页删除用户指定的签名、公证、管理员与文件权限段落，以及重复的安装引言和实现细节说明。
- 精简登录宣传副标题、注册与扫码说明、安装/更新/授权重试文案、会话缓存解释、设备外观/代理说明和自动化层级提示。
- 保留账号邮箱、操作标签、恢复码不可再次查看及失效提示、验证码步骤、未限定密钥的访问范围、消息保留期限、安装载荷与后台运行说明，以及只给 Fleet Agent 授予完全磁盘访问的操作步骤。
- 下载发行清单仍严格检查 Accepted、应用身份、更新签名与产物路径；删除界面说明不放宽验证。更新共享资源与外壳缓存版本为 v188。

## 红 → 绿与全量验证

新增/调整的行为回归首次定向执行 36 pass / 3 fail，失败分别证明旧签名说明、登录引言及标题栏仍存在；实现后定向 39 pass / 0 fail。手机菜单滚动条契约先失败，再补齐样式并通过。日志：`/private/tmp/fleet-settings-copy-red.log`、`/private/tmp/fleet-settings-copy-mobile-red.log`、`/private/tmp/fleet-settings-copy-final-targeted.log`。

首轮全量检查发现旧标题次数和资源缓存版本契约不一致，修正可见标题预期并统一资源版本，未放宽测试。最终命令：

```bash
FLEET_SPARKLE_ARCHIVE="$HOME/Library/Caches/fleet-hub/Sparkle-2.10.0.zip" bash scripts/verify.sh
```

真实 exit 0，Go、全部 JavaScript、59 项 Swift 及全部 Shell 层通过，无失败。合并已上线星光后再次执行同一命令，真实 exit 0；最终日志：`/private/tmp/fleet-settings-copy-merged-verify.log`。并行提交的原生验签修复已经是分支祖先，但本次交付只替换 Web 静态资源，不把源码测试冒充新的原生发行包。

## 真实浏览器

独立 Go 服务仅监听 `127.0.0.1:7101`，使用全新私有临时状态，合成账号为 `settings-review@fixture.invalid`。测试没有输入、读取或使用真实账号密码、验证器、恢复码或访问密钥，没有创建真实设备。

- 桌面实际打开自动化设置：标题行不占空间，菜单从弹窗顶部开始，使用文档与关闭按钮不重叠；关闭按钮实测 44×44。
- 390×844 手机明暗主题：四个设置页面均可切换，菜单与关闭区域不重叠；菜单高度 60，稳定状态弹窗完整位于视口内，无页面横向溢出。
- 实际点击关闭区域边缘成功关闭，Escape 关闭并恢复入口焦点。账号页的安全表单、恢复码提醒及会话设置的保存入口保留。
- 添加设备页已无用户指定段落；安装、浏览器授权、Agent 磁盘授权、后台常驻及更新入口仍存在。孤立本地实例没有发行包，正确显示暂不提供下载，未制造假链接。

截图：`/private/tmp/fleet-settings-copy-desktop.png`、`/private/tmp/fleet-settings-copy-mobile.png`、`/private/tmp/fleet-settings-copy-mobile-dark.png`，仅包含合成数据。合并后重新加载桌面并复查添加设备、会话设置，标题行不存在、关闭热区仍为 44×44、指定文案不存在且无横向溢出；最终截图为 `/private/tmp/fleet-settings-copy-merged-desktop.png`。abj 静态资源部署与真实 HTTPS 验证另外记录，不将本地浏览器测试声称为现网登录验收。

## abj 独立验收网页发布

2026-10-08 10:51 UTC 从已推送、全量验证通过的不可变提交 `eebefe6616c22f4fc9378920130516a33123a3d1` 归档 13 项前端文件（包含三项测试），更新用户指定的 `https://10.17.74.92:7443` 独立验收实例。保留已上线星光；原生下载与旧生产网关没有修改。

- 新快照：`/opt/macfleet-saas-uat/releases/web-settings-eebefe6616c22f4fc9378920130516a33123a3d1`，`WEB_REVISION` 记录本轮提交；后台 `REVISION` 继续保留 `0da0ae56d4e7389589b23c00998f372522bffe3a`。
- 原快照与备份保留：`web-starlight-1e4debdf198d69b08f69b047b032847db8f959c4`；备份目录为 `/opt/macfleet-saas-uat/backups/settings-copy-eebefe6616c22f4fc9378920130516a33123a3d1-20261008T105116Z`。
- 同时持有服务器与星光静态发行锁；校验旧快照、复制并严格检查只变更上述前端文件，重建 136 项 SHA 清单后原子切换。切换后的检查失败会恢复原指针；没有重启服务。
- 136 项快照 SHA 校验通过；本机独立 TLS 检查的五项公开资源与提交逐字节一致，全部 13 项部署文件与源码 SHA 一致。使用配置的 CA 校验证书，未使用 `curl -k`。
- `/auth` 为 HTTP/2 200、`cache-control: no-store`；`/healthz` 和 `/readyz` 为 200，匿名 `/api/devices` 为 401，私有 app/settings/native bridge/sw 脚本为 303；四项验收服务均 active。
- 后台 PID 前后均为 `597209`；二进制 SHA-256 均为 `aac8231a317451e3d96433776253c4b1d5391ef5a8e53ad504f2db86bb5ce51a`。原生两项下载指针与发行清单未变；数据库、密钥、Headscale、Mac 安装及 Desktop 配置没有修改。

实际发布输出：`/private/tmp/macfleet-settings-copy-deploy.log`；本机 TLS、HTTP 与源码一致性输出：`/private/tmp/macfleet-settings-copy-public-check.log`。合并后全量验证为 Go 通过，JS 各组 21、333、15、18、4 项通过，Swift 59 项通过，Shell 全部通过，真实 exit 0。浏览器视觉证据仅来自本地相同源码和合成账号，不作为现网登录态验收；完成后已退出测试账号、删除临时 cookie 文件、恢复浏览器尺寸并停止隔离服务。
