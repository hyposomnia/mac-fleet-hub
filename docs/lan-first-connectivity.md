# LAN 优先、Tailscale 兜底连接规划

日期：2026-07-26 · 状态：Future / 暂缓，不进入当前实现

## 背景

当前 dashboard 中的终端、文件和 Claude/Codex 自绘会话都使用同源路径：

```text
浏览器 -> Fleet 公网 nginx -> Mac mesh IP -> ttyd / filebrowser / fleet-agent
```

Headscale 只承担控制面；Tailscale 数据面会优先建立 WireGuard 点对点连接，打洞失败时才使用 DERP。但浏览器本身不在 mesh 中，当前实现又固定经 Fleet 网关反代，所以当浏览器与目标 Mac 位于同一个局域网时，业务流量仍会绕行网关。

未来希望实现：

```text
首选：浏览器 -> Mac LAN HTTPS -> fleet-edge
兜底：浏览器 -> Fleet 网关 -> Tailscale -> fleet-edge
```

文件传输、ttyd 终端以及 Claude/Codex 会话 API、SSE 和媒体请求应使用同一条已选路径，避免不同功能各自选择线路而产生状态不一致。

## 目标

- 浏览器与 Mac 同 LAN 时，终端、文件和 Claude/Codex 优先走真实局域网。
- LAN 不可达时，无需用户改地址，自动回到现有网关 + Tailscale 路径。
- Tailscale 继续承担远程兜底、网关到节点的连接和节点在线探测。
- 不把当前依赖 Headscale ACL 保护的无鉴权服务直接暴露给局域网。
- 新 Mac 仍保持一行安装；LAN 能力由 bootstrap 自动配置。
- 网络切换或 LAN 断开不能丢失 Mac 上已有的 tmux、Claude 或 Codex 会话。

## 非目标

- 当前阶段不实现、不修改部署脚本、不改变现有监听地址。
- 不替换 Headscale/Tailscale，也不自行实现 NAT 穿透。
- 不让浏览器直接连接 Codex app-server 或 Claude 的本地控制面。
- 不通过 WebRTC/DataChannel 重写 ttyd、filebrowser 或 HTTP 协议栈。
- 不把通配符 TLS 私钥复制到各台 Mac。

## 两种部署场景

### 场景 A：Fleet 网关与 Mac 位于同一 LAN

这是成本最低的情况。浏览器仍访问现有 Fleet 网关，由 nginx 或网关侧代理选择上游：

```text
网关 -> Mac LAN IP（primary）
     -> Mac mesh IP（fallback）
```

该方案不需要浏览器跨域、额外 LAN TLS 或访问设备安装 CA。只有网关真实可路由到 Mac LAN IP 时才适用；公网 VPS 无法使用 Mac 的 RFC1918 地址。

### 场景 B：浏览器与 Mac 位于同一 LAN，网关位于公网

浏览器必须直接连接 Mac，才能避免绕行公网网关。由于 Fleet 页面本身使用 HTTPS，浏览器会阻止页面中的 `http://192.168.x.x` iframe、fetch 和 WebSocket，因此 Mac 必须提供可信的 LAN HTTPS 入口。

本规划主要针对场景 B。

## 推荐架构

不新增独立常驻 daemon，而是在现有 `fleet-agent` 进程中增加第二个监听器，并把它作为统一的 `fleet-edge`：

```text
100.x.x.x:7682    HTTP   mesh 入口，供网关通过 Tailscale 访问
0.0.0.0:7683      HTTPS  LAN 入口，供同网段浏览器直连

fleet-edge
  /api/           -> fleet-agent 自身 API
  /mN/term/       -> 127.0.0.1:7681 ttyd
  /mN/files/      -> 127.0.0.1:8080 filebrowser
```

ttyd 和 filebrowser 改为只监听 loopback。LAN 中只有经过 TLS 和授权校验的 `fleet-edge` 可访问它们。

保留 mesh HTTP 监听是为了兼容现有网关反代和 Headscale ACL。未来也可以统一成 HTTPS，但不应把证书主机名校验问题与第一阶段混在一起。

## TLS 方案

### 推荐：Fleet 私有 CA

- 网关首次部署时创建 Fleet CA。
- CA 私钥只保存在网关，不下发。
- 每台 Mac 本地生成私钥和 CSR，网关为其签发独立设备证书。
- 访问 Fleet 的手机或电脑安装并信任一次 Fleet CA。
- Mac 证书可使用稳定名称，例如 `m3.fleet.local`，具体命名需在实现前验证 mDNS、DNS 和各平台证书行为。

优点是无需公开每台 Mac 的 DNS 和 ACME 服务；代价是每个访问设备需要安装一次 CA。

### 备选：每台 Mac 使用公网可信证书

通过 DNS-01 为每台 Mac 签发独立证书，可以免除访问设备安装 CA，但需要公网 DNS 自动化、续期和私钥生命周期管理。不得把现有通配符证书私钥复制到所有 Mac。

## 身份认证与安全边界

当前 filebrowser 使用 `noauth`，fleet-agent API 也默认依赖 Headscale ACL，因此不能简单把三个服务绑定到 `0.0.0.0`。

LAN HTTPS 入口至少需要：

- 网关在用户通过 Authelia 后签发短期、限定 Mac 的访问凭证。
- `fleet-edge` 校验凭证的签名、过期时间、目标 Mac 和权限范围。
- 支持凭证撤销或足够短的有效期。
- 严格的 Origin 白名单。
- 正确处理 CORS 和浏览器 Private Network Access 预检。
- WebSocket、SSE、文件上传下载使用相同鉴权边界。
- token 不写入持久日志，不作为长期 URL 参数保存。
- LAN 探测端点只返回最少健康信息，不暴露文件、会话或主机详情。

iframe 和 WebSocket 不便设置任意 Authorization header。实现前需要在以下方案中选定一种并做浏览器实证：

1. Dashboard 先访问 LAN edge 的一次性 bootstrap URL，由 edge 设置 `Secure`、`HttpOnly` cookie。
2. 所有 LAN 资源使用短期签名 URL，并解决子资源、重连和日志泄漏问题。

默认倾向方案 1。

## 地址发现与节点注册

fleet-agent 自动枚举满足条件的 LAN IPv4 地址，并通过现有受保护链路向网关注册：

```json
{
  "macId": "m3",
  "meshIP": "100.64.0.3",
  "lanCandidates": ["192.168.1.23"],
  "edgeHost": "m3.fleet.local",
  "edgePort": 7683,
  "capabilities": ["lan-edge-v1"]
}
```

候选地址需要排除 loopback、link-local、虚拟机/容器接口以及明显不适合直连的接口。LAN IP 变化后应重新注册。网关只把当前用户有权访问的节点候选返回给 dashboard。

Tailscale 在线状态不能证明 LAN 可达。路径选择必须以浏览器对 LAN HTTPS 入口的实际探测结果为准。

## Dashboard 路径选择

dashboard 应集中维护每台 Mac 的 `routeBase`，所有节点级请求都从该函数生成地址，避免当前相对路径散落后只改到一部分功能。

推荐优先级：

```text
LAN direct -> gateway + mesh
```

流程：

1. 从网关取得节点候选和一次性 LAN bootstrap 凭证。
2. 以约 300-800ms 超时探测 LAN HTTPS health endpoint。
3. 探测成功后建立 LAN cookie/session，并把该 Mac 的 route 固定为 LAN。
4. 探测失败则继续使用当前同源 `/mN/...` 路径。
5. 网络切换、设备唤醒、连续失败或凭证过期时重新探测。

以下流量必须共同切换：

- fleet-agent REST API。
- Codex 自绘 SSE、输入、审批、附件和媒体。
- ttyd iframe 和 WebSocket。
- filebrowser iframe、WebSocket、上传和下载。

## 故障切换语义

- GET/health 等幂等探测可以自动重试。
- POST、上传、审批和终止操作不得在路径切换时盲目重放，因为浏览器无法确认服务端是否已执行。
- 活跃 ttyd 断开后可以重新选择路径并重新 attach；后台 tmux 进程不受影响。
- Codex SSE 断开后应通过既有 thread/history 状态恢复，再从新路径订阅。
- filebrowser 上传中断默认报告失败，由用户重试；第一版不实现跨路径断点续传。
- 路径切换应以 Mac 为粒度保持粘性，不对每个请求做竞速。

## 新安装流程

用户入口保持不变：

```bash
curl -fsSL https://<fleet-host>/enroll/bootstrap.sh | bash
```

内部增加：

1. 用户输入入网 TOTP，网关自动分配 `mN`。
2. 安装 Tailscale 并加入 Headscale，取得 mesh IP。
3. Mac 本地生成 edge 私钥和 CSR。
4. 网关为该 Mac 签发独立 edge 证书。
5. 自动发现 LAN 地址。
6. 安装 ttyd、filebrowser 和 fleet-agent。
7. ttyd/filebrowser 仅绑定 loopback。
8. fleet-agent 启动 mesh HTTP 与 LAN HTTPS 两个入口。
9. agent 向网关注册 mesh、LAN、证书名和能力版本。
10. 分别验证 loopback、mesh 和 LAN health endpoint。

首次使用 Fleet 的浏览器设备需要额外安装一次 Fleet CA；安装新 Mac 不需要重复安装 CA。

## 已有 Mac 升级

仅执行 `fleet-agent update` 不足以完成迁移，因为还涉及证书、plist、监听地址和 filebrowser 配置。未来应提供幂等升级入口，例如：

```bash
curl -fsSL https://<fleet-host>/enroll/upgrade-edge.sh | bash
```

升级必须满足：

- 先取得并验证证书，再修改监听方式。
- 新 edge 健康检查通过后，才把 ttyd/filebrowser 收口到 loopback。
- 任一步失败时保留原有 mesh 服务，不能让远程管理失联。
- 重跑不会重复签发无用证书或破坏现有 plist。

## 分阶段实施建议

### Phase 0：浏览器与证书 POC

- 验证 macOS、iOS、Android 和主流桌面浏览器对 Fleet CA 的信任流程。
- 验证 HTTPS 页面访问私网地址时的 CORS、Private Network Access、cookie、iframe、WebSocket 和 SSE 行为。
- 验证 `.local`/mDNS 命名；不可靠时改用受控 DNS 域名。

POC 未通过前不进入生产实现。

### Phase 1：网关与 Mac 同 LAN 的双上游

- 注册 LAN IP。
- 网关优先访问 LAN，失败回退 mesh。
- 不改变浏览器连接方式。

### Phase 2：fleet-edge

- fleet-agent 双监听。
- edge TLS、授权中间件和 ttyd/filebrowser 反代。
- 原始服务收口到 loopback。

### Phase 3：Dashboard LAN 路由

- 集中抽象 `routeBase`。
- LAN 探测、bootstrap 和按 Mac 粘性路由。
- 覆盖 REST、SSE、WebSocket、iframe、附件和媒体。
- 增加当前路径与延迟的诊断展示。

### Phase 4：升级与运维

- 新安装自动签证书。
- 已有 Mac 幂等迁移和失败回滚。
- 证书续期、撤销、LAN IP 漂移和日志诊断。

## 测试与验收基线

- 同 LAN 时，大文件传输和终端连接确认不经过公网网关。
- LAN 不可达时，在限定时间内回退现有网关路径。
- 局域网内未授权设备无法读取文件、列出会话或启动 Codex/Claude。
- LAN 路由中断不会杀死后台 tmux、Claude 或 Codex 进程。
- 路径切换不会重复执行 POST、审批、终止或上传操作。
- edge 证书过期、主机名不匹配或 token 无效时必须失败关闭，不能降级为无鉴权 HTTP。
- bootstrap 和升级脚本可重复执行，失败时保留可用的 mesh 路径。
- 修改 fleet-agent 后按项目约定重建 amd64/arm64 `dist`，并覆盖 Go 单测、shell 幂等检查和浏览器实测。

## 实现前待确认

1. TLS 采用 Fleet 私有 CA，还是每台 Mac 的公网可信证书。
2. LAN edge 的稳定主机名采用 mDNS 还是受控 DNS。
3. LAN 授权采用一次性 bootstrap + cookie，还是签名 URL。
4. 场景 A 的双上游是否值得先于完整浏览器直连单独落地。
5. 是否要求支持未安装 Fleet CA 的临时访问设备；若要求，需要重新评估证书方案。

在这些问题确认前，本文件仅作为未来设计方向，不应据此改变当前生产部署。
