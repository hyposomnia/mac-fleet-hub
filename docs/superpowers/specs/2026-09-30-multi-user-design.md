# 多用户统一服务设计

日期：2026-09-30。授权：用户审阅完整设计后要求 `$dev` 按 SDD+TDD 开发并本地部署验证；本文件将该设计落为实施契约。

后续范围更新：2026-09-30 用户要求在新分支 `codex/multi-user-server` 开发，先交付服务端；Mac 客户端正常按用户授权、安装及迁移接入留待服务端开发完成后继续。本阶段只测试服务端入网协议，不更新现有 Mac 安装脚本、LaunchAgent 或已签名 agent，不将旧客户端安装入口描述为已支持新协议。

## 产品与边界

只有用户、设备、管理员三个概念。一套公开源码和部署流程，一个用户与多个用户走相同逻辑。注册仅邮箱、密码、确认密码，邮箱为未验证的登录标识；普通注册不能获得管理员权限。每台 Mac 仅归属一个用户，不提供共享、组织或计费。管理员只查看账号和设备元信息，不获得其他人的文件、终端或聊天正文。

## 认证契约

`POST /api/auth/register` 接受 email/password/confirm_password，返回 setup 状态及 TOTP URI，创建短时待绑定会话。`POST /api/auth/login` 接受 email/password，返回 challenge 或未完成账号的 setup。`POST /api/auth/verify` 校验六位 TOTP，首次激活返回一次性恢复码；正式 Cookie 会话固定有效 30 天。密码 Argon2id，TOTP AES-GCM 加密，恢复码和会话令牌仅存 SHA256 哈希。验证码时间窗原子消费，防并发重放。恢复要求密码及未使用恢复码，撤销全部原会话，进入重新绑定。

`GET /api/auth/me` 返回 user 和 csrf_token；未认证返回 401。账号写操作检查精确 Origin 和 CSRF；登录注册也检查 Origin。Cookie HttpOnly/SameSite=Lax，HTTPS 部署 Secure。本地只允许显式 loopback HTTP origin。GET sessions、POST logout/logout-others/password/totp/start/totp/confirm/recovery-codes 提供会话及安全管理。验证码和口令失败按来源与账号限速，响应不泄露内部错误或凭据。

认证限速按来源 IP 与身份保存失败预算；成功凭据认证、TOTP 验证及账号安全操作清除各自对应的预算，正常成功操作不持续消耗失败额度。限速 map 最多保存 4096 个身份，清理过期项后容量仍满时拒绝新增身份；每个身份 15 分钟内最多 8 次未被成功清除的尝试，超限返回 429。Argon2id 相关认证及账号安全操作共用 4 个工作槽位，槽位用尽立即返回 429，不排入无界队列。

## 设备与网络

`POST /api/enrollment/start` 返回 code、request_id、claim_token、verification_url、expires_at。浏览器 POST confirm {code} 确定 owner；Mac POST claim {request_id,claim_token} 领取一次性 Headscale 密钥及全局 mac index；POST complete 根据该预授权 key 的 ID 与 Headscale node 的关联确认节点，不接受客户端报来的 IP 或 owner。短码不是领取凭据。数据库登记 grant，重试不能重新绑定或重复签发；期限 10 分钟。已确认未完成安装、失败、撤销明确可见。

Network 接口 Issue/Discover/Reconcile/Revoke；Headscale 每用户一个内部 user，但隔离真正来自显式 policy。ACL 仅放行网关 IP 到有效设备的服务端口，以及同一 active 用户设备间互通；跨账号默认拒绝。失效、disabled、revoked 设备无规则。数据库状态先封锁网页/API，再执行网络撤销；失败以 503 和待收敛状态返回，后台重试，不冒充完成。

设备 ID 保留 mN，编号不复用。`GET /api/devices` 和兼容 `/api/nodes.json` 仅返回 owner 的设备。GET/POST names、settings 按用户保存，设备撤销不隐藏或清除 owner 已保存的外观偏好。修改设备外观仍检查当前归属和有效状态。DELETE devices/mN 关闭活动连接、撤销网络及自动化授权，不删除本地文件或终止 Desktop turn。

## 路由及自动化

nginx 只负责 TLS 和入口，所有私有请求经 Go 认证授权，动态映射 /mN/term、files、api、dsh。保留原上游 prefix，支持上传、Range、SSE 和 WebSocket。上游只来自受信登记，移除浏览器 Cookie、Authorization、伪造身份及转发头，拒绝跨设备重定向；长连接随会话到期、用户禁用或设备撤销关闭。

上述为服务端代理契约，不能据此认定 filebrowser / DSH 嵌入应用已经完整支持新认证。代理入口的写请求检查 Fleet Origin 与 CSRF，Fleet CSRF 不转发给上游；嵌入应用自身的凭据、CSRF token 与请求形状需在后续新客户端集成后逐项实测登录、写操作、上传及实时连接。Fleet 页面测试与模拟上游代理测试只验证各自范围，不能替代真实嵌入应用及客户端验收。

复用现有 messageAPI 的持久队列和回调逻辑，每用户独立应用实例与私有数据路径；数据库保存用户/设备/偏好真相，既有 JSON 自动化数据通过 owner 专属目录保存，防止重写复杂消息状态机。向设备发起操作时重验有效 owner 和 device。已保存的结果及记录由有效 owner 和当前 API key 范围决定可见性，设备撤销或身份变更不隐藏该 owner 的历史；相同幂等请求返回既有 message_id，不再次投递。公开 Bearer key 仅在其 owner 应用内有效。后台禁止查询其他用户的消息正文。

## 数据及运维

SQLite 保存 users、sessions、totp_recovery、devices、enrollments、preferences、audit_events 和迁移 marker；foreign keys、事务、WAL、busy_timeout、0600 文件、单服务实例。数据库 migrations 原子、幂等。加密 key 在仓库外独立 0600 文件；重启必须沿用原 key。

管理员通过本地命令提升指定邮箱，绝不能首个注册者自动 admin。服务默认拒绝没有配置的 Headscale 集成，健康区分存活/就绪。旧数据迁移需显式 owner，备份再将 MAC_IPS 与 names/settings/keys/jobs 归属导入，校验全部节点关联，标记幂等；仅设备名称不能作为归属凭据。未知节点默认拒绝。

`/healthz` 检查服务与数据库存活；`/readyz` 必须有最近 45 秒内一次实际成功的 `Network.Reconcile`，仅配置 Network 或 API key 不足以就绪。未配置网络、从未同步成功或上次成功超过 45 秒均返回 503；策略同步失败或后台网络同步退出立即清空成功标记，下一次成功前保持 503。后台网络策略同步周期为 15 秒，故障后重试不能保留旧的就绪判断。

## 前端与后台

/auth 注册、登录、绑定、恢复；/account 密码、验证器、恢复码、会话；/admin 概览、用户分页搜索、用户详情、禁用启用、撤销会话、设备搜索撤销；/enroll/confirm 浏览器确认。继承既有无框架风格。所有私有请求 no-store；SW 不缓存 auth/account/admin/enroll/API/设备路径，包括无尾斜杠路径。浏览器私有状态按 user ID 分区，切换与退出清理连接、内容及缓存。

## 验收与本地部署

先看到测试失败再实现，每轮独立红绿。覆盖邮箱唯一/确认密码、强制 TOTP、验证码重放、30 天绝对到期、恢复码单次、CSRF、admin 边界、双用户设备/名称/设置/消息/密钥隔离、跨账号 ACL、绑定凭据归属/并发幂等、动态反代 SSE/WS 撤销、失败可见及迁移幂等。执行完整 `bash scripts/verify.sh`，race、Linux 双架构构建，保存真实证据。本地使用独立临时目录及 loopback 端口，绝不覆盖当前 Fleet/ChatGPT 运行配置；真实 Headscale 能启动则联调，否则明确列出未验证外部步骤。生产发布不在本次授权范围。

补充覆盖 readyz 的成功/过期/失败立即失效、成功认证清除失败预算、身份 map 上限 4096 及 Argon 工作槽位 4 的资源边界。前端验证入口为 `node --test server/dashboard/auth.test.mjs server/dashboard/account_pages.test.mjs server/dashboard/auth_integration.test.mjs`，分别覆盖公共认证客户端、账号页面及 dashboard/SW 集成；这些检查要求不等于已取得通过结果。

本阶段只验证服务器设备授权 HTTP 协议，不迁移当前 Mac。现有 `server/enroll/bootstrap.sh` 仍是旧全局入网 TOTP 客户端，新服务对 `/join` 与 `/enroll/join` 返回 410；新安装器、正常逐用户授权接入和 filebrowser / DSH 嵌入应用 CSRF 兼容实测留后续客户端阶段，不宣称已部署可用或客户端完整支持。
