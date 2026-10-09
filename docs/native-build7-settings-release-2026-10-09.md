# Fleet Hub 0.1.6+7：设置页整改与升级恢复

用户新增的五项反馈已统一实现、发布 abj 并直接替换本机安装：

1. 完全磁盘访问使用设置页原有独立 Agent 拖拽图标，授权按钮仅打开系统设置，删除重复的浮动窗口。
2. 删除“仅授权 Fleet Agent，设置应用无需磁盘权限”。网页安装说明同步删除重复说明。
3. `m1` 是服务器自动分配的设备编号，并非电脑名称；运行与关联页改标“设备编号”。网页 OAuth 中的设备名称仍由客户端 hostname 提供，服务器编号及归属协议未改变。
4. 卸载改为文字链接“卸载 Fleet Hub 和 Fleet Agent”。现有卸载流程覆盖独立后台运行目录和 Hub 应用，保留“同时移除本机设置”的独立选项和确认。
5. 运行页去掉“已启用”和“运行详情”，直接保留登录启动开关、后台版本及进程。系统待批准等异常状态仍提示。

## 升级恢复修复

build 6 的本机升级回到 build 5，旧恢复记录仍存在。上次 OAuth 留下未完成关联；实际后台在 `unbound` 提供 HTTP 200 管理接口，但旧健康条件只接受没有关联或完整入网，导致安装和恢复都误判失败。

后台同步与更新恢复共用 `isReadyForManagement`：仅未完成、未锁定、非活跃配对的 `unbound` 允许管理恢复，继续保留关联。正在配对、已锁定、已登记设备的初始 `unbound`、真实运行失败、错误版本/协议与旧 PID 仍拒绝。状态保持未联网，不把管理可用记成设备接入成功。

较新的正式签名应用直接替换中断的旧升级时，允许沿用旧恢复记录，仍执行同团队 Developer ID / Gatekeeper 校验、后台版本同步、新 PID 和管理状态检查，失败保留回滚能力。恢复提示精简为“升级完成”，本机存在未完成关联时提示重新完成关联。

回归先红后绿：未完成关联的更新健康检查失败；真实部署适配器测试复现安装失败及旧后台恢复失败。修复后保留关联并成功启动新后台，未调用 logout。另覆盖更高构建号恢复中断记录、低版本及无效记录拒绝。

## 正式发布

- 分支：`codex/multi-user-server`。
- 签名源码：`12f0c062107a343f9df4d1c6f597eeac4583251e`。
- Hub / Agent：`0.1.6+7`，arm64 / x86_64。
- 完整执行唯一入口 `bash scripts/release-fleet-agent.sh --native-candidate`，exit 0。

| 产物 | Apple 状态 | 提交 ID |
| --- | --- | --- |
| Fleet Agent.app | Accepted | `5b556fd5-590c-472d-8750-4a1bd773da52` |
| Fleet Hub.app | Accepted | `c01d04e6-7328-4b52-9ec9-1ef97846fba0` |
| Fleet-Hub.dmg | Accepted | `e4f94ffa-22d1-4cad-bdc5-9b00c187616d` |

| 文件 | 字节数 | SHA-256 |
| --- | ---: | --- |
| Fleet-Hub.dmg | 59371776 | `5d13ff667afa75f864f07579abc9cfa8f929c1607466a79cf300222869633956` |
| Fleet-Hub-update.zip | 52911768 | `7c2874feb5a8d2c295f0b998cd5e65b4387eaaea1a1abf2d7f1cdeff66dc7caa` |
| client-release.json | — | `f7e946a525dc118552a07276e4c91aff3a22dd10346c81ac4fc8064475fac769` |
| appcast.xml | — | `d14674b0aff6076d7d080f5f094d08231a25391150e97dbb0b3bf396e5a68484` |

完整入口完成运行组件隔离验收、严格签名、公证票据和升级签名检查，以配置 CA 校验 HTTPS，实际下载 DMG / ZIP 逐字节比较后原子切换。清单及 appcast 再次下载核对一致；实际下载 DMG 的 Gatekeeper 为 `accepted / Notarized Developer ID`，stapler 输出 `The validate action worked!`。

安装包：`${FLEET_CANDIDATE_WEB_BASE}/enroll/clients/7/Fleet-Hub.dmg`。原生指针为 `client-native-releases/7`，旧构建保留。

## 网页与线上验证

网页说明及缓存同步发布，`account.js?v=191`、PWA 外壳 v196。复制当前快照后仅替换八个相关文件，139 项完整快照 SHA 校验成功，再原子切换。HTTPS 从服务器及本机下载的 auth.html / account.js 与本次源码逐字节一致。

网页快照为 `releases/web-native-ui-12f0c062107a343f9df4d1c6f597eeac4583251e`；原快照及备份保留。服务端二进制、REVISION 和状态未变，未重启服务。后台 PID 前后均为 `1781205`，二进制 SHA 为 `1637ffcabfa4c9d79a9997e4847cb4545768b61169ce2696956738e1810c50ae`。

最终真实输出：

```text
/healthz HTTP 200
/readyz HTTP 200
/auth HTTP 200
/api/devices HTTP 401
Fleet-Hub.dmg: HTTP/2 200, content-length: 59371776
macfleet-saas-uat active
macfleet-saas-uat-web active
macfleet-saas-uat-headscale active
macfleet-saas-uat-mesh active
manifest_version=0.1.6+7
manifest_revision=12f0c062107a343f9df4d1c6f597eeac4583251e
```

## 本机安装与界面验收

用户此前已明确授权重新打包后直接替换安装。旧内置更新流程被未完成关联阻止，因此本次先验证正式签名载荷与安装应用为同团队，退出 Hub，执行原后台空闲守卫，再只停止 `com.macfleet.desktop-agent`，原子替换 Hub 并保留旧应用备份。重开的正式 Hub 自行同步独立 Agent、验证后台并清除中断 build 6 的恢复记录。

- Hub 和实际独立 Agent 均由 `0.1.4+5` 更新至 `0.1.6+7`。
- 后台 PID 从恢复后的 `43927` 更新至 `82786`。
- 两个实际可执行文件 SHA 与本次正式载荷一致；两应用的 codesign、Gatekeeper `Notarized Developer ID` 和 stapler 均通过。
- settings.json / binding.json / pairing.json 与即时安装前基线 SHA 一致，未撤销账号或消费新授权；旧应用备份保留。
- 状态及独立运行目录为 `0700`，同 UID 的 control.sock 为 `0600`；pending-update.json 由正式 Hub 正常清除。
- 后台管理状态为 `unbound`，旧关联 `complete=false`；磁盘只读检查为 `verified`。
- 真实 GUI 已直接显示版本 `0.1.6+7`、进程 `82786` 和“设备编号 m1”，运行页没有“已启用”或“运行详情”。磁盘页保留独立 Agent 图标，授权动作完成后页面仍可拖拽，没有重复浮窗及底部说明。关于页实际 AX 类型为 `link 卸载 Fleet Hub 和 Fleet Agent`，视觉检查无按钮底色。

未执行真实卸载或系统磁盘权限开关操作；卸载范围由既有流程、隔离测试与本次代码检查验证。实际账号完整接入仍由用户验收：在关联账号页解除上次未完成关联，再重新网页授权。未修改现有 Tailscale、Codex Desktop 或其他 Mac。

## 验证与证据

提交前和正式入口内部的 `bash scripts/verify.sh` 均 exit 0：JS 分组 21、342、15、22、4 项，Swift 14 + 53 = 67 项，Go、IP TLS 及全部 Shell 层通过，0 fail / 0 skip。

- 完整正式发行：`/private/tmp/fleet-hub-build7-ui-recovery-release.log`。
- 签名、公证及下载证据：`/private/tmp/fleet-native-release.iIJ1Ap`。
- 全量验证：`/private/tmp/fleet-build7-full-verify.log`。
- 发布记录收尾验证：`/private/tmp/fleet-build7-record-final-verify.log`，exit 0。
- 回归红：`/private/tmp/fleet-build7-health-red.log`、`/private/tmp/fleet-build7-runtime-red.log`、`/private/tmp/fleet-build7-journal-red.log`。
- 原生回归绿：`/private/tmp/fleet-build7-native-green.log`。
- 网页发布与本机 HTTPS 字节核对：`/private/tmp/fleet-build7-web-deploy.log`、`/private/tmp/fleet-build7-public-web-check.log`。
- 最终线上收据：`/private/tmp/fleet-build7-online-receipt.log`。
- 本机实际安装校验：`/private/tmp/fleet-build7-local-install-check.log`；前后基线均为本机 `0600` 私有文件，不进入仓库。

私有发布配置、证书私钥、账号与设备 token、升级私钥未进入提交。
