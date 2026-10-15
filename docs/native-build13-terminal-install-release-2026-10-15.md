# Fleet Agent Settings 0.1.12+13

已通过唯一签名机的完整 `release-fleet-agent.sh --native-candidate` 流程发布至 `https://fleet.818.work`。原生源码为 `9cb45a4c68038965a6e0aef409ac7c0b41609d22`，与本轮安装功能的原生输入一致；线上安装脚本的签名参数修复为 `43092ba7f36d48fd7472289c526ec08029fa58ac`。当前 Mac 安装和 Fleet 服务未替换。

## 功能与验证

- Universal 设置应用和独立 Agent 均包含 arm64 / x86_64；应用声明 `FleetInstallProtocol=1`，公开清单声明 `installation_protocol: 1`。
- 一行命令自动安装或更新应用、保存服务 origin、沿用登录启动选择、打开既有 OAuth；设备实际联网后检查后台磁盘证据，必要时打开系统设置和真实独立 Agent 拖拽页，定时复检。
- `bash scripts/verify.sh` 退出 0，真实输出 `==> 全部验证通过 ✓`。签名规则修复的测试先失败，再通过实际 macOS `csreq` 规则解析检查。日志 `/private/tmp/fleet-build13-source-verify.log`。
- 正式包通过真实生产安装脚本的 codesign 规则、严格深层验签和 Gatekeeper。无窗口可执行入口缺少 origin 时返回明确用法错误，未打开正常设置或请求安装。两架构与既有 Sparkle 更新公钥连续性验证通过。
- 正式入口完成组件隔离 UAT、签名、公证、staple、不可变目录上传、公开下载逐字节比较及原子晋级。Agent、设置应用、DMG 均为 Accepted。
- 实际公网 DMG 只读挂载后，确认设置应用 `0.1.12+13`、安装协议 1、Applications 替身存在；下载包内应用的严格验签及 Gatekeeper 通过，随后卸载磁盘映像。
- 完整安装/替换、浏览器授权、系统登录与实际 TCC 开关/拖拽仍待用户验收；只读实包检查不能替代完整安装链路。

## 公证和分发

| 产物 | Apple submission |
| --- | --- |
| Agent | `bbcf1e0f-013e-4fae-b5ab-5ee62750e893` |
| 设置应用 | `0f97fcff-74df-44bb-b985-4d6343de2779` |
| DMG | `378f8095-a470-457d-9dfc-a6848df20a74` |

- 下载源当前指针：`/opt/macfleet-saas-uat/client-native-releases/13`；旧版保留。
- DMG SHA-256：`b08f47e6f0770e882039f4c94d7b43a6ef39d245384cc47fcf110f011dbe0ac1`。
- 更新 ZIP SHA-256：`c90088f804693659717dbe656555bdb7702d778d423b0ba149fce44635c99b68`。
- 公网清单、包大小与实际下载 SHA 均匹配；设置页根据协议字段提供可复制的一行命令及 DMG 下载。
- 线上服务端为 `43092ba`，PID `220629`，二进制 SHA-256 `1bb4016a1306ab863672aa8e23b4a27113cb2eac21866235ef71000619465a0c`。数据库一致备份及原配置、密钥保留，四个 SaaS 相关 systemd 单元均 active。
- 正式构建及公开下载证据目录 `/private/tmp/fleet-native-release.A0cNrs`，正式流程日志 `/private/tmp/macfleet-shared-controls-native-release-accelerated.log`，额外只读包验证 `/private/tmp/fleet-build13-readonly-validation.json`。

下载：`https://fleet.818.work/enroll/clients/13/Fleet-Hub.dmg`。

```bash
curl -fsSL 'https://fleet.818.work/install.sh' | bash
```
