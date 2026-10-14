# SaaS 发布核验（2026-10-15）

## Web

- 核验源码：`2d6c6352457dca66dd4d33ff2bb08819add12a30`，分支 `codex/multi-user-server`。
- 上线前仅 `server/dashboard/style.css` 未同步；服务端源码相对已部署的 `85fa8d7` 无变化。
- 使用 `git archive HEAD:server/dashboard` 生成完整静态目录，避免 macOS AppleDouble 元数据进入部署包。
- pm 当前发布目录：`/opt/macfleet-saas-uat/releases/web-2d6c635`。前一目录记录在该目录的 `PREVIOUS_RELEASE`，通过 `current` 原子切换；服务未重启。
- 140 个 dashboard 文件逐一 SHA-256 匹配源码，且无额外文件；公网 `style.css` 下载后逐字节一致。
- 沿用已部署服务端二进制，SHA-256：`ffe0ba7880a4f2c38e881f3f08ef4539f3b1394361418c7dbcc009e216e531d6`。
- `macfleet-saas-uat.service`、`macfleet-saas-uat-web.service` 均为 `active`；公网 `/healthz` 和 `/readyz` 返回 `ok`。
- `bash scripts/verify.sh` 退出 0，末行 `==> 全部验证通过 ✓`。

## Fleet Agent Settings

- 本次授权构建、Developer ID 签名、公证并发布到 pm；不自动替换当前 Mac 客户端。
- 已在唯一签名机构建工作树快进至上述提交，工作树干净。使用既有私有 `native-pm-release.env`，目标版本 `0.1.10`、build `11`。
- 正式入口 `scripts/release-fleet-agent.sh --native-candidate-check` 因当前 SSH 会话无法访问公证钥匙串而停止；尚未开始构建或上传。需要在签名机解锁已有钥匙串，不创建或轮换凭据。
- 当前公网安装清单仍为 `0.1.9`、build `10`。本记录不代表 build 11 已发布。
