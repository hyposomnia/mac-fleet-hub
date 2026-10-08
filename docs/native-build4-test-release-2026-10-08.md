# Fleet Hub build 4 独立验收发布

## 范围与版本

本轮只更新 abj 的独立验收实例与其客户端下载源，未更新其他服务器、现有 Mac、launchd、Desktop 或原有 mesh。

| 项目 | 实际发行值 |
| --- | --- |
| 客户端 | `0.1.3+4`，Universal `x86_64 arm64`，最低 macOS 13.0 |
| 客户端签名源码 | `3ba3ea28bf9f7749ff578e020977906c184c9a90` |
| 服务器源码 | `2d320e2a367ebc99c3499d89c0f7f21c93df5c24`，Web 外壳 v185 |
| 安装包路径 | `${FLEET_CANDIDATE_WEB_BASE}/enroll/clients/4/Fleet-Hub.dmg` |
| 更新包路径 | `${FLEET_CANDIDATE_WEB_BASE}/enroll/clients/4/Fleet-Hub-update.zip` |

客户端签名后补齐了服务器登录动效资源路由，两个源码 revision 不同。后续文档提交及并行设备文字图标修改不属于已发行 build 4 的签名源码，不覆盖不可变安装包。

## 本批功能

- 原生服务器地址自动保存、裸地址补 HTTPS、并发草稿保护；授权前保存设置，每次重新发起 OAuth，非安装位置提供安全安装或打开路径。登录启动移至运行状态。
- Hub 内置独立 Fleet Agent；未安装时直接安装并启动。图标放大、交互整区可点，完全磁盘访问拖拽引导定位真实后台应用，不要求 Hub 获得磁盘权限。
- Web 账号、添加设备、自动化、会话设置使用统一弹窗，账号与下载页分离，保留安全流程及内联未保存确认。
- 登录页轻量主题、独立输入框底色、登录与注册链接互返、可降级网格与鼠标探针。

## 正式入口与公证

从干净不可变源码执行唯一入口，完整命令 exit 0：

```bash
FLEET_RELEASE_CONFIG=/private/tmp/fleet-native-build4-release.env \
  bash /private/tmp/fleet-native-build2-source/scripts/release-fleet-agent.sh --native-candidate
```

私有发行配置为 `0600`，不提交其内容。日志：`/private/tmp/fleet-native-build4-standard-release.log`；证据目录：`/private/tmp/fleet-native-release.jSgATX`。

| 公证对象 | Submission ID | 实际结果 |
| --- | --- | --- |
| 独立 Fleet Agent | `22ae2fbd-9d67-4635-afb1-1e739b65e7f3` | Accepted |
| Fleet Hub | `d06b8c4a-6ccf-4954-bb75-3236c4c96086` | Accepted |
| DMG | `f617d5f6-5b00-4c97-8ec3-e548aa05c087` | Accepted |

加速上传首次超时后，改为 Apple 官方 `--no-s3-acceleration` 并从新不可变提交完整重跑；失败产物未发布。此前钥匙串 lookup 错误不证明凭据被删除，用户终端和自动执行均已读取原有历史，本轮未重建或轮换凭据。

## 真实下载与运行验证

| 产物 | 下载字节数 | SHA-256 |
| --- | --- | --- |
| Fleet-Hub.dmg | 59386032 | `66314a61ab5031e1171544cab4085a5a9973928a87e909f2d40b9af81477a59c` |
| Fleet-Hub-update.zip | 52917876 | `e0255af985b79a44333ba8f988a318299cb144e8f1122379ceb64e8df7142196` |

发行入口真实 HTTPS 下载并校验 SHA；收尾再次核对在线清单与已有下载文件一致，DMG HEAD 为 200、字节数一致。只读挂载验证 DMG 包含 Fleet Hub.app、Applications 快捷方式及真正的内置 Fleet Agent.app，完成后已卸载挂载。

主应用、后台及 DMG 的 Gatekeeper 均为 `accepted / Notarized Developer ID`，严格验签与 staple 验证通过。主程序、登录启动器、agent 的实际 Mach-O 最低系统为 13.0；更新签名通过，Sparkle 公钥与原有安装一致，未轮换。

完整发行入口的隔离运行验收使用真正的内置 agent、filebrowser、ttyd、tmux，版本、私有管理、维护态和重启检查均 PASS，不依赖 Homebrew。日志：`/private/tmp/fleet-native-build4-download-verify.log`、`/private/tmp/fleet-native-build4-closeout-download.log`。

## abj 服务切换与回滚

第一次服务切换的校验错误地把私有 `/app.js` 的正确 303 视为失败，触发自动回滚，恢复旧指针和服务。随后修正校验，并用先红后绿的 HTTP 测试只将纯登录视觉资源 `auth_effects.js` 列为公开；私有脚本鉴权未放宽。

从服务器修复提交重新构建并切换成功，126 个发布文件 SHA 匹配。停止写入后备份状态、配置及原加密密钥，备份路径 `/opt/macfleet-saas-uat/backups/server-before-build4-20261008T061804Z`；数据库 quick_check 为 ok，用户 1、设备 0，原 key 未变，未修改账号归属。

当前服务器指针为 `/opt/macfleet-saas-uat/releases/server-2d320e2a367ebc99c3499d89c0f7f21c93df5c24`，客户端指针为 `/opt/macfleet-saas-uat/client-native-releases/4`。四个服务 `macfleet-saas-uat`、`macfleet-saas-uat-web`、`macfleet-saas-uat-headscale`、`macfleet-saas-uat-mesh` 均实际 active。

部署及收尾 HTTPS 检查：`/healthz`、`/readyz`、`/auth`、`/auth_effects.js` 均 200，未认证 `/api/devices` 为 401；`/app.js`、`/settings_dialog.js`、`/sw.js` 匿名均 303。部署时对公开 JS/CSS 逐个核对真实 HTTPS 字节一致。最终日志：`/private/tmp/fleet-native-build4-server/deploy-final.log`。

本轮 Chrome 的 abj 浏览器检查遇到 `ERR_CERT_AUTHORITY_INVALID`，未绕过警告；HTTPS 命令检查使用已配置的验收 CA，不关闭证书校验。浏览器布局与弹窗交互证据仍为此前独立本地测试，不能冒充部署后的浏览器验收。

## 全量验证与用户验收边界

服务器发布前完整 `bash scripts/verify.sh` exit 0，Go agent/server、340 项 JS、18 项发行测试、4 项公证预检测试、58 项 Swift 及全部 Shell 层通过，0 fail、0 skip。日志：`/private/tmp/fleet-native-build4-serverfix-final-verify.log`。

收尾在最新本地源码 `0861a7296a8dcfd7bf6a0f02e0fc5531f3db4542` 再运行同一入口，结果仍全部通过，日志 `/private/tmp/fleet-native-build4-closeout-verify.log`；这次源码检查不改变上述已部署 revision。

本机仍是 `0.1.0+1`，旧后台 PID `73774` 未被替换或重启。用户后续手动退出旧应用并停后台，将新 Hub 拖入 Applications 替换，通过运行状态安装/启动独立 Agent，再填写服务地址完成浏览器登录确认。完全磁盘访问应授予引导定位的 Fleet Agent，而不是设置程序 Hub。

真实安装后的 TCC 归属、受保护文件访问、设备 OAuth 入网及升级后权限保留仍待用户验收；公证、单元测试及隔离运行不替代这些证据。
