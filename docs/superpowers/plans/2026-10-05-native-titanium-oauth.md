# 原生客户端整改实现计划

1. 图标及 Titanium：在 `mac/settings-app/Resources` 保存指定母版，在 `scripts/render-settings-icon.swift` 生成 PNG 与 iconset，由打包脚本生成 `.icns`；用打包测试证明主/后台图标均被绑定。实现统一颜色、圆角、按钮和导航组件，检查四个界面浅/深色及最小窗口。
2. 磁盘权限：先补 `desktop_control_test.go` 的真实探测诊断与错误分类回归，再补结果字段及当前 TCC 授权对象定位；UI 状态旁刷新且有 accessibility label，不显示未验证授权。2026-10-07 实机发现后台权限被归到 Hub，用户明确拒绝主应用授权规避方案；后续必须整改独立后台的安装/启动/升级链路并验证真实 TCC 归属，而不是只改定位按钮或说明。
3. OAuth 服务端：新增 `multiuser/oauth_test.go` 覆盖标准参数、loopback callback、PKCE、登录/CSRF、两分钟有效期、一次性兑换与拒绝；新增 OAuth 存储与路由，复用现有设备入网 owner 和 claim/complete 边界。网页授权 UI 使用独立 OAuth 分支，旧 CLI 保持兼容。
4. OAuth 后台：新增 `desktop_oauth_test.go` 真实 loopback callback 测试，验证 state、PKCE、超时、拒绝和 listener 关闭；接入 desktop coordinator，每次新建请求并取消旧等待，不能复用旧 pairing.json。原有终端配对流程不变。
5. 设置交互：扩展 Swift 模型回归测试，关联页保存地址并发起新的网页授权，取消/过期后仍可重新发起；首页未关联可跳转；网页确认后的本机确认行为按用户答复实施。同步 Web 原生安装说明。
6. 验证交付：聚焦 Go/Swift/JS 测试、Go race、全量 verify；使用隔离 runtime 打包并运行内置服务 UAT，核对图标、窗口和安全状态。正式发布需唯一签名入口、递增构建号、完整公证与可信下载；不得覆盖现有 App 来假装验证。
