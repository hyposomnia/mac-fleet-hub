# 客户端逐用户授权设计

用户已确认：填写服务网页地址，自动打开浏览器，登录并明确确认关联，随后自动入网与登记。本阶段在 codex/multi-user-server 完成源码及隔离本地验证；不改生产网络、不替换当前 Desktop/launchd，不发布未经 Developer ID 签名与公证的 Mac 产物。

## 流程与身份

只有用户、设备、管理员。fleet-agent login [服务网页 origin] 打开 /enroll/confirm?code=，浏览器显示当前账号与设备名称，明确点击关联。未传 origin 时 CLI 在终端询问完整服务网页地址。浏览器确认并领取授权后，终端显示服务、归属账号和设备编号，只有输入 y/yes 才继续本机入网和安装；回车、拒绝或 EOF 均停止，不写入正式 binding 或改变本机网络，保留私有配对记录供 login 重试或 logout 撤销。已完成绑定不重复要求授权。CLI 不收集邮箱密码、TOTP 或浏览器 Cookie。使用 start/confirm/claim/complete，编号与控制面来自服务器，不复用 hostname 猜编号。

claim 为已确认的设备返回两种独立随机凭据：device_token 仅用于设备访问服务器的 status/binding 接口；proxy_token 仅用于网关访问 agent。服务器只保存 device_token 哈希，配对重试所需原 token 暂时加密保存，complete 后删除；proxy_token 加密保存。不在公开 JSON、日志、plist、shell 环境或浏览器中暴露秘密。客户端 binding.json 与配对恢复文件位于 0700 私有目录，0600 原子写入，拒绝符号链接、宽权限及不属于当前用户的文件。

浏览器会话仍为绝对 30 天。设备授权持续到解绑/撤销/禁用；Mac 重启恢复授权，不因浏览器退出而解绑。换账号或服务器必须先解绑。原有 mesh 节点不得凭 hostname 自动认领；同控制面重新认证使用本次一次性 key；不同控制面切换需要显式 --replace-tailnet。

## 客户端访问边界

ttyd/filebrowser 仅监听 127.0.0.1。fleet-agent 是唯一 mesh 服务入口，验证 X-Fleet-Device-ID 及 X-Fleet-Device-Token（proxy_token）；所有会话、文件、DSH、终端和 WebSocket 都经过该守卫。无绑定、错误凭据、被禁用或撤销不允许业务访问。健康端点只提供存活结果，无业务数据。

agent 使用 device_token 获取 45 秒授权 lease，15 秒重新校验；收到禁用/撤销响应立即取消当前请求上下文；本地解绑先原子锁定，后续请求立即拒绝，存量请求由 100ms 文件观察周期关闭。网络错误或服务端 5xx/429 最多沿用尚未到期的 lease，过期 fail closed。网关账号/设备撤销立即关闭代理连接；设备离线时客户端独立授权撤销的收敛上限为 45 秒，不宣称网络断开仍能瞬时通知设备。取消访问不删除用户文件、不终止 Desktop turn。

网关只向正确 owner 的正确设备注入 proxy_token，删除浏览器伪造的 Fleet 头。代理及自动化每次实际请求都重验归属。未取得新凭据的旧设备不允许业务代理，要求显式升级关联。嵌入 filebrowser/DSH 写请求使用精确 Origin 加浏览器 Sec-Fetch-Site=same-origin 防 CSRF；其它 API 继续要求 CSRF token。任何缺失/外域 Origin 均拒绝。

## 接口

- GET /api/enrollment/preview?code=：完整登录后返回设备名称和请求状态，不含 claim token。
- claim：除 authKey/loginServer/index/state 返回 device_id、owner_email、device_token、proxy_token、agent_port、terminal_port、files_port。
- GET /api/device/status：device_token Bearer，仅返回自己的 device_id、owner_email、状态、lease_until、idleSec；installing 202、active 200、禁用 403、撤销 410。idleSec 沿用该 owner 的 autoCloseMinutes × 60，默认 1800 秒；agent 在授权响应有效时同步，读取设置失败或 idleSec 非正数时保留当前回收时长，不影响设备授权。已绑定 agent 不再从公开的全局配置接口覆盖该值。
- DELETE /api/device/binding：device_token Bearer；先撤销本设备和所有相关访问，再收敛网络。网络失败 503 且 access_revoked=true，不冒充成功。
- fleet-agent login/status/logout/capabilities：CLI。status 不打印凭据；logout 先本地锁定访问，再远端撤销，成功删除凭据，离线保留锁定状态供重试。

## 安装与验证

bootstrap 下载客户端包调用 install；install 只询问服务网页地址，安装依赖及已签名、支持新授权协议的 agent，再由 login 完成关联、入网与本地 setup。setup 仍复用原有服务配置与 Codex 逻辑，不增加第二种部署模式。旧签名产物若不支持新协议必须明确失败，不能继续安装无鉴权版本。

TDD 覆盖凭据隔离/幂等/不泄露、preview、禁用/解绑、代理注入、嵌入 CSRF、CLI 浏览器打开与重试、私有文件、控制面保护、授权 lease 到期与长连接取消、loopback 子服务。真实本地 HTTP/SQLite 与临时 agent 进程联调；正式 Mac 签名与公证发布遵守唯一构建机 runbook。
