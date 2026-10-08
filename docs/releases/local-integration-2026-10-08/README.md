# 本地代码整合与发布 · 2026-10-08

全部本地分支均已成为 main 的祖先；主工作区与 iOS 工作树均无未提交改动。主分支包含多用户服务、原生 Mac 客户端、iOS 共享工作区、账号弹窗及最新 Web 修复。数字图标和输入器分支已被主分支较新的实现覆盖，合并时保留新版本及大小写规则。

## 已完成发布

| 目标 | 不可变提交 / 版本 | 实际结果 |
| --- | --- | --- |
| 全部功能源码 | `0da0ae56d4e7389589b23c00998f372522bffe3a`；后续 `4d549db` 仅记录兼容发布合并 | 已推送 main，所有本地功能分支已合并 |
| 生产网关静态 Web | `5cd303f066201e601b8233b097f631e0beff17d7` / v186 | 109 个静态文件 SHA 一致，运行时节点目录保留，五项服务 active |
| abj 完整账号服务及 Web | `0da0ae56d4e7389589b23c00998f372522bffe3a` / v186 | 135 个发布文件 SHA 一致，四项服务 active，数据库 quick_check ok，原加密密钥保留 |
| 原生 Mac 安装源 | 已发行 `0.1.3+4`，源码 `3ba3ea2` | Mac 源码与整合版相同，已有三项 Accepted 产物继续使用；在线清单和两项包 SHA 核对通过 |
| iOS | 整合版模拟器 Debug 构建 | BUILD SUCCEEDED；手机 9 项单元测试、10 项有效 UI 测试通过；iPad 宽屏用例通过 |

生产网关仍采用既有单用户鉴权及设备协议。兼容静态发布基于已发布的 Web，加入会话恢复、半透明磨砂浮层、输入区锚定与阴影、三字符设备图标及大小写规则。它没有把新账号系统的私有 API 前端接到旧服务器上。

abj 则运行完整整合版，包括 iOS 的共享接口与新版账号系统。其应用服务停止写入后备份，再原子切换版本指针，验证失败会回退。部署期间第一次健康请求遇到启动瞬间 502，轮询后恢复；最终所有检查通过。

## 真实输出

完整项目验证见 [verify.txt](verify.txt)，末尾为 `==> 全部验证通过 ✓`。共享模块 21 项、主 Dashboard 332 项均零失败；另有账号弹窗、公证预检、发行、Swift 与 Shell 层。发行测试包含一项按运行平台跳过，不计为通过。

生产静态发布见 [production-deploy.txt](production-deploy.txt)：

```text
static_sha256_matches=109
runtime_nodes_present
nginx=active
fleet-enroll=active
headscale=active
authelia=active
fleet-nodes.timer=active
```

公网登录页面 200，匿名根页面 302，资源引用 `style.css?v=186`、`app.js?v=186`，磁盘全部文件与发布提交一致。浏览器扩展连接失败，原生 Chrome 操作又被用户正在进行的操作中断，未取得部署后的登录态浏览器验收；不把本地/模拟器结果当作现网浏览器证据。

abj 发布见 [acceptance-deploy.txt](acceptance-deploy.txt)：健康、就绪及认证页面 200；匿名私有设备 API 401，私有 app.js/settings_dialog.js/sw.js 303；OAuth 空请求 400，未登录 consent 303。公开 JS/CSS 的 HTTPS 字节与发布包一致。数据库用户 1、设备 0，未自动创建归属。

手机测试 `TEST SUCCEEDED`：11 项 UI 用例中只有宽屏专属用例跳过，10 项实际执行通过。该宽屏用例随后在 iPad 独立执行通过；没有将跳过计为手机覆盖。完整 xcresult 位于本机 `/private/tmp/fleet-all-merge-ios-build/Logs/Test/`。

## 备份与回滚

- 生产静态备份：`/var/backups/mac-fleet-hub/dashboard-before-v186-20261008T073022Z.tgz`；保留运行时 api 目录，回滚时恢复该备份并核对原始 SHA 清单。
- abj 状态、原密钥与配置备份：`/opt/macfleet-saas-uat/backups/server-before-all-merge-20261008T072052Z`；原版本指针记录在备份中。回滚须停止应用写入后恢复原指针，涉及状态时保持数据库与原密钥一致。
- 新原生安装源保持 build 4 的不可变包。未重新签名或轮换升级密钥，未替换已安装的 Mac 客户端。

## 尚需的信息及未完成部分

完整生产账号切换尚未执行。旧设备、名称和自动化记录需要指定已注册并绑定 TOTP 的登录邮箱作为 owner；用户在浏览器输入密码及验证码。不能按首个用户、设备名称或 SSH 用户推断归属。

正式 Mac 滚动发布的只读预检通过签名身份及公证凭据检查，但在本机失败：本机运行原生 `com.macfleet.desktop-agent`，旧私有发布清单仍检查 `com.macfleet.fleet-agent` 和 mesh 7682，实际均不存在。流程在预检退出，没有执行构建、替换、重启或其他节点滚动更新。后续须先对齐真实客户端类型与账号迁移目标。

iOS 未发布至真机、TestFlight 或 App Store；本次证据为模拟器构建与运行。FDA、真实设备入网及两个正式原生版本间升级仍须分别取得实际证据。
