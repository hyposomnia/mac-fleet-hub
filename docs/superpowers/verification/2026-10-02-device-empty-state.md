# 设备列表空态修订验证

- 在 `codex/multi-user-server` 修改设备列表空态：显示“暂无已入网的设备”，下方提供“添加设备”链接，目标为 `/account#add-device`。会话与文件模式共用该渲染分支；网关故障不显示空列表添加入口。
- 更新现有测试后先确认两项失败；实现后 `node --test server/dashboard/gateway_down.test.mjs` 为 11 项通过、0 失败。
- `bash scripts/verify.sh` 最终退出码 0：Go 两模块通过，前端 281 项通过、0 失败，Shell 层全部通过。首次外壳版本检查失败后统一主面板资源版本为 v163，再次全量通过。
- 使用 `bash scripts/run-local-server.sh /private/tmp/macfleet-device-empty.pHzqng/local` 启动独立本地实例；实测 `/healthz=200`、`/readyz=503`、未认证 `/api/devices=401`。未配置 Headscale，不能宣称真实设备入网成功。
- 未登录请求 `/app.js?v=163` 实际为 303 跳转登录，未取得已认证页面截图，不将接口存活或源码测试当作视觉验证。
- 按当前项目授权只做源码与独立本地验证。本次未更新 abj 静态资源，未修改旧服务、现有 Mac、launchd、Desktop、数据库或密钥；未提交或推送。
- 命令日志保存在本机私有临时目录 `/private/tmp/macfleet-device-empty.pHzqng/verify.log`。
