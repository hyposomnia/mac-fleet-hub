# Fleet Hub 0.1.4+5 原生发行与本机升级记录

本批包含后台内联签名规则修复、网页授权前同步后台、独立 Agent 磁盘授权入口及直接展示运行详情。用户授权完成统一打包、发布到 abj 独立验收实例并直接替换本机。

## 发行来源与公证

- 分支：`codex/multi-user-server`。
- 客户端签名源码：`eefff123d2eae49c40a7bb6ac40e354129b35d92`。
- Hub 与独立 Agent：`0.1.4+5`，arm64 / x86_64，最低 macOS 13.0。
- 服务端与网页继续使用基线 `566ad81fe28eadc3c80bf890a7f5646804678358`；本次原生下载发布没有重建服务端。

完整执行唯一入口，exit 0：

```bash
FLEET_RELEASE_CONFIG=<0600 私有发布配置文件> \
  bash scripts/release-fleet-agent.sh --native-candidate
```

首次标准 S3 上传在 Hub 的分段上传阶段报 `HTTPClientError.deadlineExceeded`，未切换下载源。Apple 工具的 `--timeout` 控制提交后的处理等待，不是上传超时。随后加入显式上传选项，保留标准通道默认值，并使用 `FLEET_NOTARY_S3_ACCELERATION=1` 重新完整执行入口。

三项公证均取得 `Accepted`，并通过严格 Developer ID 验签、Gatekeeper 和 stapler 验证：

| 产物 | Accepted 提交 ID |
| --- | --- |
| Fleet Agent.app | `9cd98464-7cd8-4167-85e0-98c6f59afd36` |
| Fleet Hub.app | `50a03da3-25b1-47c6-9e1b-0270064b56de` |
| Fleet-Hub.dmg | `0e2c4ebd-7f1a-462b-bf62-03ccaedd2482` |

## 实际下载与线上状态

安装包位于 `${FLEET_CANDIDATE_WEB_BASE}/enroll/clients/5/Fleet-Hub.dmg`，更新 ZIP 位于同一构建目录。正式入口先暂存不可变产物，通过配置的公有 CA 校验 HTTPS，实际下载并逐字节比较 DMG / ZIP 后才原子切换下载指针；当前清单与 appcast 再次下载核对一致。

| 文件 | 字节数 | SHA-256 |
| --- | ---: | --- |
| Fleet-Hub.dmg | 59388143 | `42ac0a76c537268e6cf4c192c35d5d53c7a862e8c7e791ccccada4efa918bd3d` |
| Fleet-Hub-update.zip | 52919610 | `1fc03bdaa1863bf05dad6b031bb9d7b33ec5036147a7e988e99d9cc3b41ad620` |
| client-release.json | — | `5cfa2e8f6dba1212a4757e4f5c8974d2507a941eabd4d0ea6bdf60adb90db099` |
| appcast.xml | — | `c3712da412f51c0045b9a79fd329575a6b9eaa07f73122d6ef51781d425c4640` |

下载后的 DMG 另行运行 `spctl --assess --type open --context context:primary-signature -vv`，结果为 `accepted / Notarized Developer ID`，`stapler validate` 通过。

2026-10-09 最终只读 HTTPS 与服务检查实际输出：

```text
/healthz HTTP 200
/readyz HTTP 200
/auth HTTP 200
/api/devices HTTP 401
Fleet-Hub.dmg: HTTP/2 200, content-length: 59388143
macfleet-saas-uat active
macfleet-saas-uat-web active
macfleet-saas-uat-headscale active
macfleet-saas-uat-mesh active
```

原生当前指针为 `client-native-releases/5`；服务器当前指针仍为 `releases/baseline-566ad81fe28eadc3c80bf890a7f5646804678358`。

## 本机实际升级

通过已安装 Hub 的“检查更新”下载新包，再点击内置 Sparkle 更新器的“Install and Relaunch”。真实更新流程自行执行空闲检查、应用备份、后台同步与健康验证。

- `/Applications/Fleet Hub.app`：由 `0.1.3+4` 升级到 `0.1.4+5`。
- `~/.macfleet/desktop/runtime/Fleet Agent.app`：实际独立运行副本为 `0.1.4+5`；旧后台为 `0.1.0+1`。
- 后台 PID 从 `73774` 变为 `95480`；升级完成时运行状态为 `unbound`。
- 升级完成后的首次基线比对证明设置和既有配对状态保留，原先不存在的绑定文件仍未创建。Hub 与独立 Agent 的实际可执行文件 SHA 与本次签名载荷一致。
- Hub / Agent 的严格验签、Gatekeeper `Notarized Developer ID` 与 stapler 校验通过。状态与运行目录为 `0700`，当前用户持有的控制 socket 为 `0600`。
- `pending-update.json` 正常清除，旧应用备份保留在 `~/.macfleet/desktop/updates/`。

真实原生界面直接显示“后台运行中”、版本 `0.1.4+5` 与 PID `95480`，没有原来的内联规则错误。磁盘页显示“访问正常”及“授权磁盘访问”，拖拽目标为独立 Fleet Agent。未操作系统磁盘权限开关；这一界面观察不代表所有 TCC/受保护文件场景已验收。

## 随后账号验收发现的接入阻塞

用户随后自行完成网页 OAuth 授权，账号信息及设备编号已经返回。本机新增未完成绑定，配对阶段为 `joining`，`joined=false`，网络配置尚未落盘。后台日志报告组网 TLS 证书校验失败。

只读复现确认：普通 Go HTTPS 请求到同一 `/key?v=1` 返回 200；Tailscale `tlsdial.Config` 的 IP 连接得到空 `ConnectionState.ServerName`，随后报 `x509: certificate signed by unknown authority`。测试证书的 macOS 信任限定于入口 IP；组网校验丢失目标身份后无法匹配该信任。该问题进入后续修复发行，不能把本批公证/安装通过记为账号设备完整入网通过。

## 验证证据

上传选项三个行为测试先红后绿。提交前及完整发行入口内部均运行 `bash scripts/verify.sh`，exit 0：Go 层通过，JS 分组 21、342、15、22、4 项通过，Swift 13 + 51 = 64 项通过，全部 Shell 层通过，0 fail、0 skip。

签名机构建与下载证据目录：`/private/tmp/fleet-native-release.yyANE1`。完整发行日志：`/private/tmp/fleet-hub-build5-accelerated-release.log`；标准上传失败日志：`/private/tmp/fleet-hub-build5-resume-release.log`。私有配置、账号 token、证书私钥及升级私钥没有进入提交。
