# Fleet Hub 0.1.5+6：IP 入口组网 TLS 修复发行

`0.1.4+5` 的用户验收发现：网页 OAuth 已返回账号和设备编号，原生界面却持续“正在接入”。后台卡在内置 Tailscale 的 TLS 证书校验；超时后留下未完成关联。完整诊断起点见 [build 5 记录](native-build5-test-release-2026-10-09.md)。

## 修复与回归

TLS 不在 SNI 中发送 IP 字面量。Tailscale `v1.104.0` 的 `dnscache.TLSDialer` 配置了预期 IP，但既有校验器只读 `ConnectionState.ServerName`，取得空值后无法匹配 macOS 钥匙串中限定该 IP 的 SSL 信任。

修复在每条连接克隆的 TLS 配置上包装既有 `VerifyConnection`：预期身份为 IP 且回调身份为空时，补回预期 IP，再调用原校验器。保留系统信任、证书链、有效期及身份验证；不改钥匙串信任设置。DNS/SNI 的原路径继续保留。

原生双架构构建通过 `scripts/prepare-desktop-tls-module.mjs` 使用临时 Tailscale 副本和临时 Go modfile；Go 1.27 禁止覆盖模块缓存的 overlay，因此没有修改缓存或仓库 go.mod。依赖版本和源文件 SHA 固定，变化即拒绝构建，后续升级依赖须重新审查补丁。

| 项目 | SHA-256 |
| --- | --- |
| 上游 dnscache.go | `94236cdfaf955f83cfb82ffaed8c913fa7d69b4b35c9549660fa600f5d4585df` |
| 本次原生构建补丁文件 | `d236f4f109304dcf1325763317fbec01b62d96e324cc3a5d15e9702b42324f57` |

真实上游模块 `go mod verify` 输出 `all modules verified`。构建后模块缓存中该文件 SHA 与上游值仍一致。

`TestDesktopTLSIPKeepsVerificationHost` 先在真实 TLS 连接上失败，错误为 `verification lost the expected IP hostname`；修复后通过。负向用例要求错误 IP 返回 `x509.HostnameError`、未受信任证书返回 `x509.UnknownAuthorityError`，并验证共享 TLS 配置未被修改。回归已进入 `scripts/verify.sh`。

对真实验收入口的只读 TLS 复现：修复前普通 Go HTTPS 返回 200，组网校验报未知颁发者；修复后两条路径均返回 200，组网校验的目标身份为预期 IP，系统信任验证成功。此检查只验证 TLS，未消费入网密钥或替代真实设备接入验收。

## 正式发行

- 分支：`codex/multi-user-server`。
- 签名源码：`5e3908225ba2a0b4cdf8b2d7429016cc1a8dbb2a`。
- Hub / Agent：`0.1.5+6`，arm64 / x86_64，macOS 13.0 起。
- 服务端和网页继续使用 `566ad81fe28eadc3c80bf890a7f5646804678358`；本批只更新原生下载源。

唯一正式入口完整执行，exit 0，显式使用已验证的加速上传选项：

```bash
FLEET_RELEASE_CONFIG=<0600 私有发布配置> \
  bash scripts/release-fleet-agent.sh --native-candidate
```

| 产物 | Apple 状态 | 提交 ID |
| --- | --- | --- |
| Fleet Agent.app | Accepted | `a87601db-14db-4ce4-aad1-855c79d9b342` |
| Fleet Hub.app | Accepted | `881946d1-5321-43be-82e0-79df2dd4f4d1` |
| Fleet-Hub.dmg | Accepted | `ec549789-bc73-42f8-ada1-8c5fb11585e2` |

完整入口通过严格 Developer ID 验签、Gatekeeper、公证票据、升级签名、双架构及运行组件隔离验收后，上传到 abj 独立验收源。HTTPS 使用配置的 CA，实际下载 DMG / ZIP 并逐字节比较后才原子切换；清单和 appcast 再下载核对一致。

| 文件 | 字节数 | SHA-256 |
| --- | ---: | --- |
| Fleet-Hub.dmg | 59388593 | `136a12a80d607e3d7bd5d7d10a799a5ff0fae2148ac3ec5256bb388850ee74ca` |
| Fleet-Hub-update.zip | 52921624 | `4f943bf83c0ab24cf94288a47e346634257ec96b4ac9eb02a58ac8c1e9f27da3` |
| client-release.json | — | `52586ab4663315e8800319a0fbfdad17c132f52370ead6ad2bba0cddf481cce9` |
| appcast.xml | — | `076c19acc05e641cc60528ded79a472eca948d4425dc6a68268b71cadccae1f3` |

下载路径为 `${FLEET_CANDIDATE_WEB_BASE}/enroll/clients/6/Fleet-Hub.dmg`。线上原生指针为 `client-native-releases/6`，保留 build 5 及之前产物；服务端指针仍为 `releases/baseline-566ad81fe28eadc3c80bf890a7f5646804678358`。

2026-10-09 最终只读 HTTPS 与服务验证输出：

```text
/healthz HTTP 200
/readyz HTTP 200
/auth HTTP 200
/api/devices HTTP 401
Fleet-Hub.dmg: HTTP/2 200, content-length: 59388593
macfleet-saas-uat active
macfleet-saas-uat-web active
macfleet-saas-uat-headscale active
macfleet-saas-uat-mesh active
manifest_version=0.1.5+6
manifest_revision=5e3908225ba2a0b4cdf8b2d7429016cc1a8dbb2a
```

实际下载的 DMG 另行运行 `spctl --assess --type open --context context:primary-signature -vv`，结果 `accepted / Notarized Developer ID`；`stapler validate` 输出 `The validate action worked!`。

## 本机安装及账号验收边界

已在安装的 Hub 中发起“检查更新”，用户解锁后继续核实：内置更新器已回到 `0.1.4+5`，报告“原版本后台未通过健康检查，已保留恢复记录”。未完成的关联让后台停留在 `unbound`，旧健康条件只接受无关联或完整入网，因此恢复后台也被误判。原签名后台已从原启动定义恢复，PID `43927`、管理接口 HTTP 200，关联和设置保留。build 6 已上线，但未在本机成功安装；此恢复问题与新增界面反馈统一进入 build 7 修复。

新版安装后，仍需在 Hub 解除上一次未完成的关联，重新打开网页授权，由用户在浏览器完成确认，再核对真实组网和设备服务。没有代替用户操作账号撤销、输入密码/TOTP 或修改系统磁盘权限；不能把 TLS 检查及下载安装源成功记为真实设备完整入网成功。

后续已在 [build 7](native-build7-settings-release-2026-10-09.md) 修复恢复判断，正式发布并直接安装 `0.1.6+7`；原生当前下载源已切换 build 7，本机旧升级记录正常清除，未完成关联保留供用户重新授权。

## 验证与证据

源代码验证及正式入口内部验证均 exit 0。正式入口使用配置的 Sparkle 归档，JS 分组 21、342、15、22、4 项全部通过，64 项 Swift、全部 Go / Shell 层及新 IP TLS 回归通过，0 fail、0 skip。

- 正式构建及下载证据：`/private/tmp/fleet-native-release.ecFsg0`。
- 完整发行日志：`/private/tmp/fleet-hub-build6-ip-tls-release.log`。
- 真实 TLS 复现：`/private/tmp/fleet-ip-tls-live-probe.log`。
- 回归红/绿：`/private/tmp/fleet-ip-tls-red.log`、`/private/tmp/fleet-ip-tls-green.log`。
- 线上最终检查：`/private/tmp/fleet-hub-build6-online-receipt.log`。

私有发布配置、证书私钥、账号/设备 token 与升级私钥均未进入提交。
