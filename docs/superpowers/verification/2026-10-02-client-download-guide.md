# 客户端下载与终端授权说明验证

## 实现边界

- 账户添加设备区新增完整安装包、Apple Silicon 与 Intel 二进制下载入口；使用现有 nginx 分发路径，不新增下载服务、不修改签名发布机制。
- 首次使用完整包运行安装器，安装器验证正式 agent 并准备支持文件后调用 `fleet-agent login` 发起配对。裸二进制只适用于已有支持文件的设备。网页不调用 enrollment/start，只允许打开 agent 已生成短码的确认页。
- `fleet-agent login` 不传地址时询问完整服务网页 origin，自动打开浏览器；浏览器授权并领取后，终端显示服务、owner 与编号，用户输入 y/yes 才继续写 binding、入网及安装。拒绝、回车或 EOF 停止，私有 pairing 保留供重试或 logout。已完成绑定不重复确认。
- 保持无参数 agent 的原有前台服务语义；授权入口是 login 子命令。邮箱、密码、TOTP 与令牌不进入 CLI 输入或确认输出。
- 仅源码与隔离本地验证；未更新 abj、正式 dist、现有客户端、launchd、Desktop 或实际网络。未提交、推送、签名、公证或正式发布。

## 实际结果

- 更新账户回归测试后先确认下载入口缺失导致失败；新增 Go 输入/确认测试首先因尚无实现编译失败。
- 实现后账户页 23 项测试全部通过；Go 输入、确认、拒绝后重试、既有浏览器配对测试通过。完整验证入口的 fleet-agent、fleet-enroll 及 multiuser Go 测试均通过。
- `bash tests/client-authorization_test.sh` 通过；`git diff --check` 通过。
- `bash scripts/verify.sh` 本次退出码 1：前端 288 项中 287 通过，1 项失败。失败为并行开发的工作区标签清理引入 `window.FleetWorkspaceTabs?.reset()` 后，既有 `auth_integration.test.mjs` 沙箱没有 window，报 `ReferenceError: window is not defined`。本次没有修改该行为或测试夹具；全量入口因此未执行后续全部 Shell 层，不能报告全量通过。
- 本地独立服务 7099 的 account.js?v=164 和 account.css?v=164 返回内容与工作区逐字节一致。实测 healthz=200、readyz=503；安装包与 arm64 下载请求为 404，符合 Go 本地实例不分发正式客户端的当前边界。
- 下载入口不代表新客户端已签名公证或可安装；页面明确提示尚未发布/协议不支持时停止，不回退旧版或关闭 TLS/签名检查。未进行真实安装、浏览器授权端到端或新页面截图验证。

日志：本机 `/private/tmp/macfleet-download-account.log`、`/private/tmp/macfleet-download-focused.log`、`/private/tmp/macfleet-download-verify.log`。
