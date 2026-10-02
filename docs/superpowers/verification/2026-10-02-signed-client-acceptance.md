# abj 真实签名客户端验收

## 已执行

- 用户明确授权特性分支提交推送、abj 独立验收发行及本机安装切换；未更新 main、旧生产网关或其他 Mac。
- 相关源码通过独立克隆聚焦提交。已发布安装包来自不可变提交 `af732061c169a9a8c0bad220a45ad9821c92a298`，版本 `8a8bbde-20261002`；共享工作区中的并行 UI 文件未覆盖或部署。
- 唯一签名构建机运行完整 `release-fleet-agent.sh --candidate`；两个架构均采用 Developer ID Application、固定身份 `com.macfleet.fleet-agent`、可信时间戳与严格验签。
- Apple 公证真实结果为 `Accepted`，提交 ID `daaccf1c-b84e-420e-a37e-24dffca4e3fc`；原始记录在 `/private/tmp/macfleet-candidate-release.Jdtpfm/notary.json`。
- 完整包真实下载至 `/Users/hjc/Downloads/macfleet-saas-uat/mac-bundle.tar.gz`，大小 7,387,334 字节，SHA256 `0e1894368c73894afcfbd3742fd8ab34e98db087af43e3422e704498ab17b049`，与服务器 Accepted 发行清单逐字节一致。双架构下载均通过 codesign 严格检查。
- 下载后的 arm64 客户端真实执行 capabilities，返回 `device_authorization=1`、`browser_pairing=1`、`agent_proxy=1`。没有分发开发构建或假下载入口。
- 浏览器页面使用同源路径与 location.origin；安装命令使用 FLEET_ORIGIN。实际入口来自签名机私有 acceptance.env，而不是页面硬编码。
- 回退保护经过先红后绿测试：跨控制面使用 tailscale login 新建 profile，保留旧 profile；同控制面用 up --force-reauth 消费本次账号密钥，不复用旧节点推断 owner。

## 命令证据

`bash scripts/verify.sh` 在干净候选快照通过：Go agent/server/multiuser，267 个前端测试，2 个发行清单/网络模板测试，以及所有 Shell 层。共享工作区保留并行 UI 后另外完整验证通过：293 个前端测试、2 个发行配置测试，Go 与 Shell 层均通过。日志分别为 `verify-profile.log` 与 `/private/tmp/macfleet-shared-post-release-verify.log`。

实际 HTTPS 请求使用该实例公有证书校验，没有 -k：

| 路径 | HTTP |
| --- | --- |
| /healthz | 200 |
| /readyz | 200 |
| /api/devices（未登录） | 401 |
| /enroll/release.json | 200 |
| /enroll/bootstrap.sh | 200 |
| /enroll/mac-bundle.tar.gz | 200 |
| /enroll/dist/fleet-agent-darwin-arm64 | 200 |
| /enroll/dist/fleet-agent-darwin-amd64 | 200 |

验收后端、验收 nginx、验收 Headscale、验收 userspace mesh 四个 unit 均 active；旧 fleet-enroll/nginx-fleet/cloudflared-fleet/nginx/tailscaled 仍 active。client-current 指向上述不可变提交的专属 client-releases 目录。

## 本机真实授权进度与边界

- 本机原本没有 Fleet agent/LaunchAgent，也没有 shared 47682 listener。现有 Tailscale 配置与用户文件私有备份在 `/Users/hjc/Downloads/macfleet-saas-uat-backup.dH6i8e`；旧 profile ID 为 `4e12`，未 logout 销毁。
- 真正的签名包安装器已执行验签并写入 0700 支持目录；签名 agent 通过原生 TLS 发起 /api/enrollment/start，成功取得服务配对链接与短码，并实际调用系统浏览器打开确认页。未读取浏览器 cookie、密码、TOTP 或恢复码。
- 为避免在共享工具日志输入本机密码，将终端等待转到原生 Terminal：已打开 `/Users/hjc/Downloads/macfleet-saas-uat/开始安装.command`。该入口读取独立 origin 文件、使用验收 CA 验证 readyz、在真实终端调用 sudo -v，并执行包内正式安装器。系统下载目录访问提示、sudo 密码、浏览器账号确认和终端 y 由用户完成。
- 此时尚未取得 owner 确认、claim/complete、真实 Mac 新 mesh 节点、已安装服务及设备页面代理的证据。binding 尚未签发，正式 ~/.local/bin/fleet-agent 尚未安装，原 mesh 仍选中。不得将发行成功或配对请求成功表述为设备已入网。
- 另一次重复钥匙串信任请求已取消：真实 Go agent 已能用原生平台 TLS 校验连接，没有新增跳过 TLS 的代码。curl 下载仍显式使用该验收 CA 校验。

## 回退方式

若本机随后切换后需要恢复旧网络，在本机真实终端执行 `sudo /opt/homebrew/bin/tailscale switch 4e12`。新 Fleet 服务与新设备授权另行撤销；不要把 profile 切换本身当作设备撤销或完整安装回滚。现有备份不包含 macOS Keychain 密钥导出，回退依赖保留的旧 profile，而不是伪称已经备份了整个系统身份。
