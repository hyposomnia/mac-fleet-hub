# 0.1.1+2 测试发行记录

## 范围与源码

用户要求打包并上传测试服务器，以便自行下载重装。范围为 abj 独立验收实例与安装源，不替换本机应用、launchd、Desktop 或现有生产网络。

源码提交为 `00693db61db76e6d037a66c02a78fc066d70bbe8`，已推送 `codex/multi-user-server`。独立干净构建目录为 `/private/tmp/fleet-native-build2-source`；保留主工作区的其他并行 UI 改动，没有一并发布。

唯一发行入口为 `FLEET_RELEASE_CONFIG=/private/tmp/fleet-native-build2-prerequisites/release.env bash scripts/release-fleet-agent.sh --native-candidate`。版本 `0.1.1`，构建号 `2`，沿用已有 Developer ID 与 Sparkle 更新密钥，不生成或轮换私钥。

## 新鲜验证

- 主工作区 `bash scripts/verify.sh`：Dashboard 315 pass，发行/打包 14 pass，Swift 46 tests、0 failures，Go 与 Shell 通过，SDK 篡改缓存回归不再 skip。日志 `/private/tmp/fleet-native-build2-workspace-verify.log`；外层 zsh 包装使用了只读变量 `status`，包装退出失败，验证脚本本身末行是 `全部验证通过 ✓`。正式入口在干净源码上重新执行完整验证并通过。
- 干净发行源码的完整验证：Dashboard 296 pass、发行/打包 14 pass、Swift 46 tests、0 failures，Go 与 Shell 通过，末行 `全部验证通过 ✓`。
- `go test -race -tags fleet_desktop ./...`：`ok fleet-agent 5.935s`，退出码 0；日志 `/private/tmp/fleet-native-build2-race.log`。
- 真实内置 agent 的独立副本、版本与重启保存、filebrowser、ttyd/tmux 四项隔离联调全部 PASS。
- Hub、登录启动器和 agent 均为 x86_64/arm64 Universal，应用版本实际为 `0.1.1+2`。

## abj 测试服务

只更新 `/opt/macfleet-saas-uat` 独立实例。新服务与网页包含 OAuth authorize/consent/token、安装说明和敏感路径缓存保护，来源为上述同一源码提交。

先停止写入并保存完整状态与原密钥，再原子切换服务源码；保留一致性备份 `/opt/macfleet-saas-uat/backups/server-before-build2-20261007T113622Z`。首次切换发现归档根目录继承 0700，导致服务执行被拒；自动恢复旧指针和服务，修正发布目录为 0755 后重试成功。没有修改数据库内容或密钥来绕过失败。

真实远端及 HTTPS 检查：

```text
stage_sha256_matches=118
backup_database=ok; users=1; devices=0
/healthz=200
/readyz=200
/auth=200
/api/devices=401
/oauth/token=400
/oauth/authorize=400
/oauth/consent=303
database=ok; oauth_schema=present; users=1; devices=0
macfleet-saas-uat=active
macfleet-saas-uat-web=active
macfleet-saas-uat-headscale=active
macfleet-saas-uat-mesh=active
revision=00693db61db76e6d037a66c02a78fc066d70bbe8
```

OAuth 的 400 是无参数请求被正确拒绝，不是授权成功。303 是未登录确认页跳转登录；真人账号确认和真实设备接入仍需用户完成。使用服务器原有公开证书校验 HTTPS，没有关闭 TLS 校验。日志 `/private/tmp/fleet-native-build2-server/deploy-retry.log`。

## 安装包状态与验收边界

唯一入口的 agent 与主应用公证均为 Accepted，stapler、严格验签和 Gatekeeper 检查通过：

- agent：`ade2fc9f-545c-4831-b3f1-8a6c72698e87`。
- Hub：`654feb40-4e0e-4ead-ab88-6063d930ffd3`。

已有 Sparkle 密钥完成更新 ZIP 签名与验证；DMG 已生成并签名。DMG 提交时 `notarytool` 返回 `No Keychain password item found for profile: mac-fleet-hub-notary`，完整发行流程退出 69。随后只读复查默认与显式 login keychain 的 profile 均返回相同错误；不导出密码、不创建或轮换更新密钥，等待用户在本机终端重新保存公证凭据。

证据目录为 `/private/tmp/fleet-native-release.E8LBxW`，完整日志 `/private/tmp/fleet-native-build2-release.log`。尚未完成 DMG 公证，也未晋级测试下载指针，不提供未公证产物作为安装包。凭据恢复后应重新执行完整唯一入口，不手工跳过阶段将本次中间产物上架。

本机只读核对仍为 `/Applications/Fleet Hub.app` 的 `0.1.0+1`，旧后台 PID `73774`。新架构源码与隔离测试不能代替实际 launchd/TCC 证据。重装后必须由独立 Fleet Agent.app 执行受保护目标读取，并确认 TCC 权限主体为 agent；不要求 Fleet Hub 获得完全磁盘访问。
