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
- 用户指定本机 Mac mini 签名，并授权迁移既有 Sparkle 更新密钥。导入后公钥与已安装正式应用一致；两端临时私钥文件均已删除，没有生成或轮换密钥。
- 正式入口 `scripts/release-fleet-agent.sh --native-candidate` 完整执行成功；源码 `50a42b616402f27bf3682bf7915279a3170a0ee8`，版本 `0.1.10`、build `11`。本机私有 `native-pm-release.env` 固定构建机身份、Developer ID 和 pm 目标，不入库。
- 修复两个真实发布阻断：跳板机上传使用 SSH 文件流；Swift `@main` 入口文件重命名以通过新版 Xcode 双架构 release 构建。修复后全量验证再次通过，正式脚本也重新执行全套验证。
- arm64 / x86_64 构建及隔离运行 UAT 通过，实际捆绑的 Agent、filebrowser、ttyd、tmux 均验证。`codesign --verify --deep --strict` 通过。
- 三项 Apple 公证均为 `Accepted`，均完成 staple / validate：
  - Agent：`93616261-69fc-4840-ba7e-ce47c28cfde8`
  - 设置应用：`1f1fd93d-3b9e-451d-8f10-fe8519bfa4d6`
  - DMG：`9c79bbc7-1eb0-44e1-9d4a-6b97789c04fe`
- pm `/opt/macfleet-saas-uat/client-native-current` 已原子切换到 `client-native-releases/11`；旧 build 10 保留。公网安装清单与 appcast 下载逐字节一致，安装包及更新包从公网实际下载并与构建产物比较通过。
- DMG SHA-256：`3bf20adfac956db58fe49366b3cb9f1b2addb7fa46e4e5b43524c1131bd2406f`。
- 更新 ZIP SHA-256：`5cf60b559f913defbab756443bbbf5d506c5c9cbf235f78137780a628d929fb2`，Sparkle 更新签名验证通过。
- 本机构建、公证与下载验证证据：`/private/tmp/fleet-native-release.FG4TzK`；完整发布日志：`/tmp/fleet-native-build11-release-retry.log`。
- 未安装或替换任何当前 Mac 客户端；真实用户从旧版升级的验收尚未执行。
