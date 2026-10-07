# 0.1.2+3 测试发行记录

## 范围与不可变源码

用户恢复 Apple 公证凭据及 abj 内网连接后，继续已授权的测试发行。范围仅为 `/opt/macfleet-saas-uat` 独立验收服务与下载源；不替换本机 Applications、launchd、Desktop 或其他生产部署。账号密码、钥匙串密码及公证凭据不进入共享日志。

源码为 `86f89c8ff85e6abbd740e966f1f8d233c27060b6`，分支 `codex/multi-user-server`，从干净目录 `/private/tmp/fleet-native-build2-source` 构建。主工作区其他并行 UI 改动保留，未纳入本次发布。

唯一发行入口：

```bash
FLEET_RELEASE_CONFIG=/private/tmp/fleet-native-build3-release.env \
  bash scripts/release-fleet-agent.sh --native-candidate
```

私有配置使用 `0600`，版本 `0.1.2`，构建号 `3`，沿用既有 Developer ID 与 Sparkle 更新密钥；没有创建、导出或轮换私钥。首次内网检查连续三次 SSH 失败后安全停止；用户恢复连接后重新预检，通过后才开始本次完整流程。

## 构建验证

正式入口重新执行 `bash scripts/verify.sh`，Dashboard 296 pass，发行/打包 16 pass、0 skip，Swift 52 tests、0 failures，Go 和 Shell 全部通过：

```text
==> 全部验证通过 ✓
```

源码、捆绑运行组件、主应用、登录启动器和 agent 均从同一提交构建。实际运行组件隔离联调全部 PASS，覆盖独立后台副本、私有控制 socket、版本与重启保存、维护期间拒绝设置、filebrowser、ttyd 和 tmux。隔离运行没有安装真实 launchd、接入用户 mesh 或伪报 FDA。

日志 `/private/tmp/fleet-native-build3-release.log`，证据目录 `/private/tmp/fleet-native-release.cFQjOa`。

## abj 测试服务

从同一提交生成 Linux 服务与 Dashboard，核对 118 项 SHA 后切换独立实例。先停止写入，备份完整状态及原加密密钥，再原子切换服务指针；失败有恢复旧指针与服务的守卫。没有修改已有用户、设备数据或密钥。

一致性备份：`/opt/macfleet-saas-uat/backups/server-before-build3-20261007T150207Z`。当前服务指针为 `/opt/macfleet-saas-uat/releases/server-86f89c8ff85e6abbd740e966f1f8d233c27060b6`。

真实远端与本机 HTTPS 验证：

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
account_js_matches=ok
```

400 是无参数 OAuth 请求被拒绝，303 是未登录确认页跳转登录；这些不是真人授权成功的证据。重启就绪等待中短暂 502 后恢复，最终所有检查通过。HTTPS 使用测试实例现有公开证书校验，没有关闭 TLS 验证。新版 Web 说明包含内置后台、直接安装及浮动窗拖拽授权；源文件与真实 HTTPS 下载字节相同。部署日志 `/private/tmp/fleet-native-build3-server/deploy.log`。

## 公证阶段

独立 Fleet Agent.app 公证 Accepted：`b61de8b2-676f-4322-9be1-7f9c2e6b59ad`；主 Fleet Hub.app 公证 Accepted：`da8abac3-4eb6-4a21-a21a-0bf9ac3e3c9d`。两者 stapler、严格验签与 Gatekeeper 检查通过；整包更新 ZIP 已使用既有 Sparkle 密钥签名并验证。

DMG 公证 Accepted：`1e38779a-9af7-440e-9115-13c2a4e19ccf`。完整唯一入口退出码 0，DMG staple/validate 通过；先暂存不可变构建号，实际下载 DMG 与更新 ZIP 并核字节，再原子晋级测试下载指针至 `/opt/macfleet-saas-uat/client-native-releases/3`，最后核对在线清单与 appcast。

## 实际下载与 Gatekeeper

从测试服务真实下载，不是只检查本地构建文件。清单版本 `0.1.2`、构建号 `3`，源码 revision 与上述提交一致：

| 文件 | 字节 | SHA-256 |
|---|---:|---|
| `Fleet-Hub.dmg` | 59362906 | `79ee26a8e3a8622d48e220321b18fd6234365e3e22144a8d1854b248d90962dd` |
| `Fleet-Hub-update.zip` | 52879357 | `3c6237fa90461aa3591c19c651ffbce320dc6df1b05bb33282826c4d4832e342` |

DMG 下载端点 HTTP/2 200，内容长度与清单一致，支持 Range，独立构建路径为 immutable。清单实时再次下载字节一致；没有使用关闭 TLS 验证的参数。

对下载 DMG 做 `hdiutil verify` 并只读挂载：实际含 `Fleet Hub.app` 和指向 `/Applications` 的快捷方式，主包内含真实 Fleet Agent.app。未执行应用或安装。

主应用和后台分别通过 `codesign --verify --deep --strict`；下载 DMG、主应用和后台的 stapler validate 均通过，Gatekeeper 三项均为 `accepted / source=Notarized Developer ID`。主/后台版本均为 `0.1.2+3`，identifier 分别为 `com.macfleet.fleet-hub` 与 `com.macfleet.fleet-agent`，同一 Developer ID 团队；主应用、登录启动器及后台均为 x86_64/arm64 Universal。

`sparkle_public_key_unchanged=ok`，已与现有安装的升级公钥比对，不轮换更新身份。验证日志 `/private/tmp/fleet-native-build3-download-verify.log`；只读挂载已卸载。

## 安装与权限验收边界

本机只读核对仍是 `0.1.0+1`，现有后台 PID `73774`，没有被本次发布替换或重启。下载、验签、公证和隔离运行不等于正式安装后的 TCC/FDA、浏览器账号确认或真实设备入网已验收。

用户重装时先在旧 Hub 停止后台并退出，将新 DMG 中的 Hub 拖入应用程序并确认替换。首次没有安装时可直接打开后点击“安装并启动”。在“磁盘权限”打开系统设置，将浮动窗中的独立 Fleet Agent.app 图标拖入并开启开关，返回 Hub 重启并检查；不要求 Hub 获得磁盘权限。之后在“关联账号”使用当前测试服务网页地址完成新的浏览器 OAuth 确认。
