# 真实客户端发行与独立网络准备

## 本次真实执行

- 已确认当前开发机就是私有配置指定的唯一签名构建机，存在 Developer ID Application，登录钥匙串可访问，notarytool history 预检成功。未读取、打印或保存 Apple 密码。
- 新增唯一发布入口的 --candidate / --candidate-check，候选流程要求特性分支干净提交、签名机、专属实例标记、全量验证、双架构正式签名、公证 JSON 明确 Accepted、精确产物提交推送，以及实际 HTTPS 下载与 SHA/签名验证。旧 agent-only 发布行为保留，不向旧服务或现有 Mac 自动 rollout。
- 安装器支持从完整包使用已签名 agent，不要求首次使用裸二进制去猜支持文件位置。页面从 Accepted 发行清单判断下载可用性，清单不存在时不生成下载入口；服务地址取 location.origin，安装命令使用 FLEET_ORIGIN，不包含实际部署 IP 或端口常量。
- abj 已下载并实际运行官方 Headscale 0.26.1；新增专属 headscale / userspace tailscaled systemd unit、状态、API key、IP 池及 nginx 控制面/下载路由。没有退出原 tailscaled，没有复用或修改旧网关的 Headscale/节点/ACL。
- 独立网关已真实注册，BackendState=Running。其动态 IPv4 为 100.96.0.2，IPv6 为 fd7a:115c:a1e0:1000::1；该地址来自专属 socket 的实际输出，不是客户端编号或硬编码归属依据。
- 新增可选 FLEET_MESH_PROXY，限定 loopback HTTP 代理；浏览器设备代理及逐用户消息调用都使用同一独立 mesh，仍先检查 owner/node 与设备凭据。默认部署继续直接访问 mesh，本地脚本默认不继承该代理。
- 独立后端二进制与页面已更新至 `/opt/macfleet-saas-uat/releases/uat-ready-PwH7nK`。二进制 SHA 为 `8ea07b47922b398bca092d4654e4f398e1e9b4c7d4aa7b1b5645e1cf85bd7c28`；account.js SHA 为 `e01e47cd96b7d9b8b9f986fdb61239af196879420d6ed1bf383a6e84ea7c2463`。账户脚本为 v167-uat，未带入并行开发的完整主面板。

## 修复与回滚证据

- 新 userspace daemon 初次因缺少 AF_NETLINK 失败，补齐该专属 unit 的地址族后成功；原 tailscaled 未重启。Headscale IPv6 前缀改为官方范围内的独立子网，重新注册的只是测试 gateway 节点。
- 新后端初次无法穿过 root 的 0700 配置目录读取 API key，自动恢复原二进制/current/env。随后将 key 安装到服务自身 0700 状态目录内，0600、属主为 macfleet-saas-uat，重试成功；未放宽原 /etc 权限。
- 后端替换前停止的仅为验收后端，并做一致状态备份；原数据库、加密 key、用户账号及会话沿用。备份在 `/var/backups/macfleet-saas-uat/backend-PwH7nK`，nginx/环境初始备份在 `network-PwH7nK`。

## 实际验证与未完成项

- 全量 `bash scripts/verify.sh` 通过：Go 两模块、前端 293 项、候选清单/配置 2 项、全部 Shell 层通过。真实 HTTP CLI 集成测试增加了明确终端 y 输入，不自动跳过用户确认；已有配对、取消/重试、跨用户访问测试通过。
- 经私有配置取出实际 origin，使用公有证书验证 HTTPS：healthz=200、readyz=200、未认证 api/devices=401；新版 account.js 与冻结发布快照逐字节一致。
- 四个验收 unit 与原有 fleet-enroll/nginx-fleet/cloudflared-fleet/nginx/tailscaled 均 active。新 proxy 仅 loopback，现有 Mac 与 Desktop 未改动。
- `enroll/release.json` 和安装包当前仍为 404，页面因此不展示伪下载入口。尚未执行新 agent 的正式签名公证、候选发布、用户真实设备安装或浏览器 owner 确认，不能把网络就绪当作这些步骤完成。
- 用户已明确回复“允许，自己全部做完”，授权相关源码提交推送、abj 候选签名发布及仅当前 Mac 的安装与网络切换；其他 Mac 和旧生产网关不在范围内。浏览器账号确认和本机 sudo 密码仍必须由用户本人输入。
- 候选私有参数保存在签名机 `~/.config/mac-fleet-hub/acceptance.env`（0600），origin 从 abj 私有 server.env 取得，不进入页面源码；未更改旧 release.env。
- 本机冻结快照、完整日志与部署脚本位于 `/private/tmp/macfleet-real-client.PwH7nK`；远端暂存位于 `/tmp/macfleet-real-client-PwH7nK`。未提交、推送、生成或分发未公证 agent。

## 授权后的源码快照

- 在独立克隆 `/private/tmp/macfleet-candidate-source.hktMn2/repo` 冻结相关源码，未暂存、删除或部署共享工作区中并行的 workspace/sidebar/preview/设备图标改动。主面板只提取账号鉴权、设备发现、退出清理、CSRF 与添加设备相关变更，保留 HEAD 已有会话功能。
- 该聚焦快照完整验证通过：前端 267 项、候选清单与网络模板 2 项，以及 Go 和全部 Shell 测试；真实日志为同级 `verify-focused.log`。
- 本机实查没有已安装的 fleet-agent 或 Fleet LaunchAgent，没有 47682 shared listener；不按前文推测当作旧 Fleet 客户端升级。已有 Tailscale 处于 Running，需要先备份并显式切换。`sudo -n true` 要求密码，自动化不能读取或代填密码。
