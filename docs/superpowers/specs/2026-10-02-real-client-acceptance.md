# 真实客户端下载与独立验收链路

## 目标与边界

只更新 abj 的独立验收实例，提供真正的完整 Mac 安装包、双架构 Developer ID agent 与 Apple Accepted 公证证据。现有网关、其他 Mac、当前 Desktop 活动会话不变。本机切换与特性分支提交分别取得用户授权；不得将既有 agent-only 正式发布流程用于验收实例。

## 发布与下载

唯一入口仍是 release-fleet-agent.sh，新增 candidate/check 参数，显式指定特性分支、验收目标、专属根目录与实际 HTTPS origin。检查签名机、干净提交、远端专属实例标记后，拉取同一分支、全量验证、正式双架构签名公证、提交精确产物并推送，然后从不可变提交组装含签名 agent 和安装支持文件的完整包。

发布将全部下载产物与 Accepted 记录生成的 release.json 原子放到验收专属 client-current 下。通过实际 HTTPS 下载、逐字节 SHA 和签名检查后才报告完成。候选发布不执行旧网关替换或任何 Mac rollout；失败停止，保留可回滚的旧发行版与公证证据。

页面从 release.json 验证发行清单，清单缺失或不完整时不生成可点击下载入口。下载路径为同源相对路径；服务地址取 location.origin，复制命令使用 FLEET_ORIGIN 变量。部署实际 IP/端口、证书位置只在私有配置，不写进页面源码。

## 独立网络

在 abj 增加专属 Headscale 状态与用户空间 tailscaled，不退出或更改现有 tailscaled。通过同一验收 HTTPS origin 的控制面路由提供 key/ts2021/DERP；仅修改独立验收 nginx。原服务、节点、ACL 与数据库不复用。

统一 Go 服务可通过显式 FLEET_MESH_PROXY 使用 loopback 用户空间代理访问独立 mesh；默认仍直连 mesh。此配置同时覆盖逐设备浏览器代理与用户消息服务，目的地仍由数据库 owner/node 授权决定。代理配置禁止远程地址、凭据、路径和查询串，不允许客户端提供代理地址。该配置是网关网络接入细节，不新增产品用户/租户/部署模式概念。

## 安装与授权

用户下载完整包，在终端使用 FLEET_ORIGIN 运行 install.sh。安装器优先使用包内正式签名 agent，验证固定代码身份与授权协议；agent 发起 start，唤起浏览器登录/TOTP并明确确认 owner。领取后回终端核对 origin/owner/编号，输入 y 才开始入网与服务安装。已有其他 mesh 默认拒绝切换，需用户明确 replace-tailnet；保留备份与回滚路径。

## 验收证据

跨控制面切换使用 tailscale login 新建配置，不 logout 销毁旧配置。安装前记录旧 profile ID，回滚时停止新 Fleet 服务并用 tailscale switch 恢复旧网络；同控制面仍 force-reauth 确保使用本次密钥。

分别记录：全量测试、不可变源码/产物 SHA、公证 Accepted、实际 HTTPS 三个下载 200 与字节一致、独立 Headscale/gateway 节点、readyz 200、真实设备 claim/complete、owner/跨用户代理结果、logout 撤销。本机或签名授权未给出时只完成独立准备，不能冒充已安装/真实设备验收。
