# 多用户 SDD+TDD 实施计划

用户已授权按已呈现设计开发和本地部署，规格见同日 multi-user-design.md。

当前范围以用户后续更新为准：分支 `codex/multi-user-server`，本阶段只交付服务器及独立本地验证。Mac 客户端安装、正常逐用户授权和迁移接入留到服务器完成后的下一阶段；本阶段不迁移当前客户端、不发布生产。以下红绿检查点是实施与验收要求，执行结果另附真实证据。

## 文件与接口

- server/enroll/multiuser/store.go：SQLite migrations、User/Device/Grant/Node，持久状态。
- server/enroll/multiuser/auth.go：Argon2id/TOTP、Cookie 会话、CSRF、恢复、安全管理。
- server/enroll/multiuser/server.go：Options、New、ServeHTTP、管理/元信息/偏好。
- server/enroll/multiuser/enrollment.go：Network 接口及绑定状态机。
- server/enroll/multiuser/proxy.go：授权后动态反代、取消连接。
- server/enroll/multiuser/network.go：Headscale API 与 policy adapter。
- server/enroll/multiuser/*_test.go：以真实 SQLite 与 httptest 断言行为。
- server/enroll/application.go/main.go/message_api.go/message_worker.go：复用每用户消息实例，守卫每次实际投递，切换统一入口。
- server/dashboard/auth-client.js/account.js/account.css/auth.html/account.html/admin.html/enroll.html：公共认证客户端及账号页面组件。
- server/dashboard/app.js/index.html/sw.js：认证 bootstrap、CSRF、缓存和菜单。
- server/nginx/fleet.conf、server/systemd/fleet-enroll.service、scripts/setup-server.sh：统一服务入口和配置。
- server/enroll/bootstrap.sh：现有旧全局入网 TOTP 客户端，本阶段不改造；对新服务 `/enroll/join` 返回 410。浏览器短码配对及领取/完成的 HTTP 协议由服务器实现，新安装器集成留后续阶段。
- scripts/run-local-server.sh、scripts/local-multiuser-smoke.mjs：本地独立运行与 HTTP 验收。

统一接口：`New(Options) (*Server,error)`；`Options.Application func(User) http.Handler`，`PublicAPI http.Handler`；`Users() ([]User,error)`，`Devices(int64) ([]Device,error)`，`AuthorizedDevice(int64,string) (Device,error)`，`ScopeContext(int64,string) context.Context`。Network 为 `Issue(context.Context,int64,int)(Grant,error)`、`Discover(context.Context,Grant)(Node,error)`、`Reconcile(context.Context,[]Device,[]User) error`、`Revoke(context.Context,string) error`。

## 红绿检查点

1. SQLite/注册：写 `TestRegistrationRequiresTOTP`，以 POST register 后 GET me 不可访问为核心；运行 `go test ./multiuser -run TestRegistration` 必须失败；实现用户唯一、口令哈希、待绑定会话再同命令通过。
2. 认证：写重放及绝对到期测试，固定 clock；`go test ./multiuser -run 'TestAuth|TestRecovery|TestSession|TestSuccessfulCredentials|TestSecurityOperations'` 红后实现 TOTP 消费、恢复与会话撤销再绿。成功认证清除对应失败预算，限速 map 最多 4096 个身份、Argon2id 工作槽位最多 4 个，超限返回 429。
3. 授权/代理：两用户真实 session 和 httptest agent，用户 B GET 用户 A /m1/api/info 必须 404；写长流撤销与伪造头测试；`go test ./multiuser -run 'TestIsolation|TestProxy|TestAdmin'` 红绿。
4. 绑定/网络：start/confirm/claim/complete 并发重试不改 owner；Headscale httptest 验 key ID 关联，生成 policy 不出现跨用户规则；`go test ./multiuser -run 'TestEnrollment|TestHeadscale|TestReadiness'` 红绿。`/readyz` 仅在最近 45 秒实际成功完成 Network.Reconcile 时为 200，失败立即清空就绪状态，未配置/从未成功/过期均为 503；不以配置存在判定就绪。
5. 消息：两个用户独立 key/job store，以原 message API 测试扩展 owner transport 守卫；`go test . -run 'TestUserApplication|TestAutomation'` 红绿。
6. 前端：`node --test server/dashboard/auth.test.mjs server/dashboard/account_pages.test.mjs server/dashboard/auth_integration.test.mjs` 验注册三字段、认证后初始化、CSRF、SW 隔离，先红再写公共页面与集成再绿。测试范围是 Fleet 自身页面及请求；filebrowser / DSH 嵌入应用的写操作与 CSRF 兼容性需后续新客户端集成实测，不据此宣称客户端完整支持。
7. 服务器安装/迁移：`bash tests/multiuser-server_test.sh` 验统一 nginx 无旁路、服务配置及本地部署不继承现有服务状态；`go test ./multiuser -run TestImportLegacy` 验显式 owner 与迁移幂等，先红再实现再绿。服务器拒绝旧 `/join` 和 `/enroll/join`，不把旧 bootstrap 改为本阶段交付；当前客户端不迁移。
8. 交付：完整 verify、`go test -race ./...`，`GOOS=linux GOARCH=amd64 go build` 和 arm64，loopback 服务真实 HTTP 双用户注册/验证器/后台/绑定/越权/撤销/重启持久化验收。核 diff 仅本任务文件，写 CHANGELOG、README 和验证报告，不推送生产。

行为测试示例：

```go
request := httptest.NewRequest(http.MethodGet, "/m1/api/info", nil)
request.AddCookie(otherUserCookie)
response := httptest.NewRecorder()
server.ServeHTTP(response, request)
if response.Code != http.StatusNotFound { t.Fatalf("cross-user status=%d", response.Code) }
```

本地验收命令：`bash scripts/run-local-server.sh /private/tmp/macfleet-multiuser-local`。预期 loopback healthz 200，未登录 private API 401，普通用户 admin 403，跨用户设备 404，服务端协议绑定后仅 owner 可见；网络未配置时绑定签发及 readyz 为 503，成功 Reconcile 的 45 秒窗口与失败立即失效分别验证。此验收不代表现有 Mac 已安装或迁移。

## 客户端阶段交接

服务器完成后，在后续正常逐用户授权下再集成 bootstrap 的 start/浏览器 confirm/claim/complete，保留已签名公证 agent 的安装与独立更新规则。现有 bootstrap 仍是旧全局 TOTP 流程，新服务返回 410，尚未提供可用新安装器。filebrowser / DSH 原生嵌入界面的登录、写操作、上传和 WebSocket 等流程需使用集成后的客户端实测，尤其确认 Origin、Fleet CSRF 与上游自身认证/CSRF 的兼容性；服务器代理及 Fleet 页面测试不替代这些客户端验收。
