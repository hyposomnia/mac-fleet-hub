# 通过来源设备访问网页

会话正文和 Markdown 文件中的 HTTP(S) 链接保留来源设备。点击后由该设备的 fleet-agent 发起请求，使用该设备的 DNS、路由及 HTTP_PROXY / HTTPS_PROXY / NO_PROXY 配置。无需浏览器所在电脑能访问目标网络，也不靠 `localhost` / 私网地址名单判断：回环、LAN、IPv6、mDNS、内部域名及 VPN 域名使用同一流程。`0.0.0.0` / `[::]` 绑定地址在设备端连接对应回环地址。

现有 Fleet origin 的链接和本地文件预览保持原入口。预览顶部显示来源设备及完整目标地址，并提供“在当前浏览器打开”。不增加 DNS、TLS 入口或公网监听端口。

## 请求与隔离

`GET /host-view?mac=mN&url=...` 要求正式登录及设备 owner 授权。它生成最长八小时的随机预览凭据，数据库仅保存哈希，绑定 HttpOnly / Secure 浏览器 Cookie、原登录、设备节点/IP 和页面 origin。凭据不续期登录，退出账号、撤销设备、禁用账号后失效；流连接也受登录、设备撤销及预览到期限制。

网页运行于不包含 `allow-same-origin` 的 iframe 和响应 CSP sandbox 中；直接打开预览资源也保持隔离。第三方页面不能读取 Fleet 的账号页面、Cookie 或存储。仅 URL 凭据不足以复用预览，必须同时有原浏览器的 HttpOnly Cookie。凭据不缓存、不放入 Referer。设备代理凭据只用于网关到 agent，不发给目标网站。页面 Cookie 保存在 agent 内存中的独立预览 Cookie jar，退出或关闭后不作为长期登录保存。

资源与请求路径为 `/network-preview/<随机凭据>/<编码的目标origin>/<原路径>`。HTML、CSS、ES module import、表单、重定向和动态 fetch/XHR/WebSocket/EventSource 使用设备代理；保留路径编码、查询、方法和请求体。应用自己的 Authorization / CSRF 头可以传递，Fleet 凭据和转发头被移除。跨目标 origin 请求遵循目标服务的 CORS 声明，不能通过修改 URL 获取新的同源权限。

## 支持边界

这是设备网络网页预览，不是完整的远程浏览器。隔离环境不开放第三方 service worker、IndexedDB / localStorage、直接操作顶层 Location 的脚本导航、浏览器扩展、跨 origin 的匿名字体加载或设备浏览器中已有的登录 Cookie；依赖这些能力的网站可能不兼容。HTTP Set-Cookie、普通静态页面、开发服务器及常见接口/实时连接均走预览通道。HTTPS 继续验证证书，不绕过证书错误。

需要包含 `/api/network-preview/` 的新版正式签名 agent。旧 agent 会显示升级提示，不会回退到查看者的回环地址。源码测试不等于正式客户端已安装；发布和逐设备安装须分别报告。

## 验证

- `go test ./...`（agent、enroll）：网络地址类别、encoded path、凭据边界、应用 Cookie/写请求、重定向、CORS、WebSocket、SSE、跨 owner、节点变更、账号禁用及退出失效。
- `node --test server/dashboard/preview_parity.test.mjs`：会话来源设备路由与 Fleet 自身入口。
- 内置浏览器隔离联调：使用 `preview_uat` build tag 启动两个 `TestNetworkPreviewBrowserServer`。浏览器无法解析的 `design.internal:8788` 由设备侧测试路由映射；实际点击验证模块、API、WebSocket、SSE、POST 和父页面隔离。夹具仅监听 loopback，正式构建不包含测试登录入口。
