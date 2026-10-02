# 客户端授权 SDD/TDD 实施计划

用户已批准同日 client-authorization-design；继续现有功能分支，不提交/发布混杂的并行改动。

1. 服务端：multiuser/device_auth.go 和 device_auth_test.go，新建 device_credentials 表，claim 返回私有凭据，complete 清理暂存 token。先验证 claim 凭据幂等且 device/status 只接受自己令牌，再实现；命令 `cd server/enroll && go test ./multiuser -run TestDevice`。
2. 网关：multiuser/proxy.go、application.go，将 term/files 改经 agent，注入指定设备凭据；preview 与嵌入写请求 Origin/Fetch Metadata 规则。以伪造头、跨 owner、无凭据与错误 Origin 测试先红后绿。
3. agent：新增 binding.go、device_access.go、binding_test.go、device_access_test.go；绑定文件、HTTP 客户端、lease 与 loopback 代理，main 只添加统一守卫及路由。`go test -run 'TestBinding|TestDeviceAccess'` 红绿后覆盖 HTTP/SSE/WS。
4. CLI：新增 login.go/login_test.go，selfcmd 命令路由；接口 pairClient.Login(context.Context,origin) error，注入 browser/join/setup 回调用于真实 HTTP 测试。先测试规范化 URL、browser 确认前不可入网、恢复配对、错误控制面及离线解绑。
5. 安装：bootstrap/install/setup/plist 与 tests/client-authorization_test.sh。只提示网页 origin；私有凭据不经环境变量；子服务 loopback；旧二进制 capabilities 不支持时拒绝。先写 shell 行为/渲染测试再实现。
6. 前端：enrollment preview 显示设备名称/当前账号，确认后明确完成；页面测试先红再绿。
7. 集成：server/enroll/client_integration_test.go 运行临时原生 agent/CLI、实际 SQLite HTTP 服务器与隔离入网适配器；双用户绑定、凭据、文件/终端/流、禁用、重启、解绑。执行 verify、两个模块 race/vet、Linux server 和临时 Mac 双架构开发构建；不覆盖正式签名 dist。更新 docs 与交付证据。

核心行为断言：设备 Bearer 只投影本设备，不能选择其他设备；agent 业务请求无 proxy_token、禁用或 lease 过期均返回 403，已有请求退出；用户 B GET 用户 A /mN/api/info 返回 404。设备 status 的无效令牌是 401，禁用 403，撤销 410。

## 实际交付

七项源码任务与隔离本地联调完成，见同日 client-authorization 验证报告。实际新增 agent 文件为 device_binding.go、device_access.go、device_login.go、device_update.go 及相应测试；原生进程集成在 multiuser/client_integration_test.go 和 agent 的测试进程 helper。可选 HEADSCALE_UAT_* 模式使用真实 Headscale/私有 userspace daemon 完成 CLI 入网，OS 服务安装仍为隔离适配器。正式签名、公证、生产迁移与真实 Mac Desktop/终端会话 UAT 未在本地冒充完成。
