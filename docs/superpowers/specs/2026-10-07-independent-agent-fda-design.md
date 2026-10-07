# Fleet Agent 独立磁盘权限整改

状态：用户明确要求实施，源码、全量测试、双架构构建与独立后台隔离运行已完成；正式签名发布和真实 TCC 验收未执行。当前已安装应用不变。

## 产品边界

用户在 2026-10-07 明确要求：Fleet Hub 只是设置应用；需要完全磁盘访问的是 fleet-agent。不能要求 Hub 获权、扩大设置程序权限或反复要求用户重加已经授权的 agent 来规避应用缺陷。

Hub 仅管理服务器地址、浏览器授权、后台状态、重启、登录启动、卸载与升级，通过私有本机控制端点与后台通信，不读取受保护磁盘内容。Fleet Agent 是独立签名、公证的常驻后台，其 TCC 权限主体必须为 `com.macfleet.fleet-agent`。关闭 Hub 不停止后台。

## 已确认的根因

`RuntimeLayout.backgroundApplication` 指向 Hub 包内 `Contents/Library/LoginItems/Fleet Agent.app`，launchd 直接运行这个嵌套位置。实机 agent 许可已开启，重启后 TCC 日志仍将访问归到外层 `com.macfleet.fleet-hub` 并拒绝。只修改 probe、按钮或引导不能纠正权限主体。

## 整改方向

- 发行包携带完整、签名验证过的后台载荷；实际运行副本安装在 `~/.macfleet/desktop/runtime/Fleet Agent.app`，位于 Hub `.app` 包以外，不把发行包内嵌副本当作授权目标。
- launchd 指向独立后台副本，登录启动器仅负责启动服务，不能让后台依赖 Hub 进程、继承其权限或经 Hub 代理磁盘读取。设置页定位实际运行的 agent。
- 新安装和升级对主应用、后台载荷分别验证固定身份及版本；后台更换先经空闲守卫，原子替换、保留旧副本，健康失败恢复旧后台及启动定义。设备归属、令牌和现有用户网络不变。
- 后台升级保留固定 `com.macfleet.fleet-agent` 的 Developer ID 身份与 designated requirement，不通过绕过 TCC、修改数据库、临时签名或给 Hub 授权处理权限问题。
- 卸载停止并移除 Fleet 自己的独立后台及登录启动项；不删除用户文件、聊天、其他服务或系统网络。

实现使用独占安装锁和原子目录替换；验签在停止旧进程前完成，健康失败恢复原包与启动定义，恢复异常保留备份。设置程序启动只准备运行副本或迁移已运行的旧后台，不自动拉起用户已经停止的后台。登录启动器使用独立身份 `com.macfleet.desktop-login`，真正后台保持 `com.macfleet.fleet-agent`。后台探测额外拒绝处在外层 `.app` 内的进程，不把 Hub 授权当作 FDA 成功证据。

## 验证门槛

TDD 覆盖独立运行路径、载荷校验失败、迁移空闲守卫、升级与启动失败回滚、版本匹配、关闭设置窗口不停止后台、卸载范围及 UI/Web 不提示 Hub 授权。使用临时目录与隔离 OS 适配器执行，运行 `bash scripts/verify.sh`。

正式真机验收须用唯一签名入口生成 Developer ID、公证 Accepted 的产物；用户授权 agent 后，以 launchd 实际进程访问固定受保护目标，并核对 TCC `AUTHREQ_SUBJECT=com.macfleet.fleet-agent`。仅有测试通过、签名正确、授权表开关或开发进程读取成功不代表该架构已修复。当前阶段不覆盖已有 Applications、launchd 定义或生产网络。
