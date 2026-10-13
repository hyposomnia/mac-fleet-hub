# Fleet Hub 0.1.9+10 发布与本机替换

正式入口 `scripts/release-fleet-agent.sh --native-candidate` 在私有配置指定的唯一签名机完整执行，退出 0。源码为 `codex/multi-user-server` 的 `576b29fa4d4b184bbe47f55860792b1fb5431c90`。本版包含 Darwin 用户态 tsnet 的系统路由修复，以及此前预览中的白底列表、运行按钮、登录启动和统一导航图标。

## 发行证据

| 产物 | Apple 状态 | 提交 ID |
| --- | --- | --- |
| 独立 Agent | Accepted | 919c7bbf-e285-4e93-88dd-29693989fd92 |
| Hub | Accepted | 1f175217-3d4d-48d1-b3d2-e54ddc4afbb4 |
| DMG | Accepted | 6acdbe2c-2ec8-43a6-941e-105162faf365 |

staple、严格验签、Gatekeeper 和 Sparkle 更新签名校验通过。abj 核对四项 SHA，真实下载 DMG 和 ZIP 并比较字节后才切换第 10 版，随后下载清单和 appcast 再次匹配。第 9 版保留用于回滚。

```text
f62224eb62e8a3a29187e2f29e64a808593f593b97727176816a1ce8798a7601  Fleet-Hub.dmg
c1b021a80f634e2b424f35ca2db440a8ecb26c1a50d7338f621ed1b9e737b0cb  Fleet-Hub-update.zip
```

沿用已有 Developer ID、公证 profile 和 Sparkle 密钥，没有创建或轮换凭据。发行工具和已有公用 CA 的失效临时路径已恢复到签名机持久缓存/私有配置目录；证书保持原有信任范围。正式成功记录来自完整加速上传入口，没有从中断的公证步骤手工续接。

## 本机安装与重连

已通过 `/Applications/Fleet Hub.app` 的内置更新器完成 Install and Relaunch。Hub 和独立 Agent 均为 `0.1.9+10`；安装后 Agent PID 从 `9637` 变为 `64925`，runtime 为 running，后台磁盘权限为 verified。Hub 的 118 个文件及独立 Agent 的 16 个文件分别与已签名产物匹配，两者均为 `Notarized Developer ID`。pending-update 恢复记录已清除。

```text
1ff1adf7325b72cd876d46ffc89d5f196e9189b33c81a5d5929cd7314cc9ad1d  Hub executable
1a92172a1c54d9ac09f992c8bb09c8084d5610b9a59773be702248aa8cee794f  Agent executable
```

binding.json、settings.json 的 SHA 与安装前完全一致，关联仍完整且未锁定。通过当前源码的同一 NativeManagement API 再次重启，PID 变为 `66108` 并恢复 running；随后重新执行网关请求验证，排除仅靠缓存或进程启动判断成功。

| 重启后请求 | 真实结果 |
| --- | --- |
| `/api/health` | 200，ok |
| `/api/sessions?assistant=codex&scope=all` | 200，28 个会话 |
| `/api/info` | 200，shared，connected=true |
| `/api/chat/history`，既有会话 | 200，40 个事件 |
| 当前 Desktop thread 的 Fleet history | 200，无其他应用 writer 冲突 |
| 服务器设备授权 status | 200，active |

请求使用服务器现有 userspace HTTP mesh proxy，设备凭据私下读取且没有输出。四项网关服务均 active。CUA 显示正式安装的 Fleet Hub、后台运行中、`0.1.9+10` 与新 PID，已不是界面预览。

## 共享与验收范围

原 shared keeper PID `6468`、listener PID `6474` 未重启，监听仅为 `127.0.0.1:47682`；Unix proxy 为当前 UID、0600。Aqua 环境为原 shared WebSocket，旧 local-daemon 变量不存在。Desktop PID `29779` 与 Fleet proxy 均连接该 listener，当前 Desktop thread 的 writer lock 仅由 `6474` 持有；读取同一 thread 已成功。

现有 keeper 仍使用此前预览的资源路径，目录保留以保护正在进行的 Desktop 活动；正常登录启动器的冷启动会从已安装 Agent 资源渲染启动定义。本次没有终止 Desktop 或迁移活动 writer。

目标 thread 的实际 MCP 清单中，`codex_apps` 为 connected、465 个工具，`cua_repl` 为 connected、3 个工具，`node_repl` 为 connected、4 个工具。旧 `codex_app` 条目为 failed、0 工具；Agent 的旧插件布局检查继续报告 AppToolsSupported/Ready=false。未把它报告为已修复，也未在活动 Desktop turn 中重载 MCP。浏览器登录、发送新 turn 和连接器写操作没有在本次验收中执行。

## 验证入口

正式入口执行 `bash scripts/verify.sh`，真实输出结尾为：

```text
ok  fleet-agent 3.824s
ok  fleet-agent 0.598s [native route / TLS]
ok  fleet-enroll (cached)
ok  fleet-enroll/multiuser 10.861s
JavaScript: 447 passed, 0 failed, 0 skipped
Swift: 17 + 80 tests, 0 failures
==> 全部验证通过 ✓
```

隔离真实运行组件验收全部通过；Hub 和登录启动器两架构的 Mach-O 最低系统版本实测为 13.0。证据目录 `/private/tmp/fleet-native-release.Mu3M2Q`，完整发行输出 `/private/tmp/fleet-native-build10-release-resumed-20261013.log`，重启后网关输出 `/private/tmp/fleet-native-build10-postrestart-gateway-20261013.log`，同 thread 输出 `/private/tmp/fleet-native-build10-same-thread-20261013.log`。
