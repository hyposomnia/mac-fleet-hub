# 原生预览资源与组网重连

## 预览资源契约

调试预览管理已安装的签名 Agent，但 Codex 启动模板、keeper、解析器和 Aqua helper 必须与预览源码配套。`RuntimeLayout.codexResources` 明确这两个资源来源：正常安装读取独立 Agent 资源；DEBUG 预览读取自己的 bundle。所有 Codex 生命周期步骤使用同一个目录，不混用版本。

运行 `bash scripts/run-settings-preview.sh` 编译 Swift 调试程序、复制当前 shared 资源及现有 Sparkle/图标并打开预览。不会构建 Agent、签名、公证或替换正式应用。打开预览只刷新状态，主动运行按钮仍调用原有 `NativeManagement`。预览启动的 launchd 定义引用其临时资源目录；该目录在 shared 服务使用期间必须保留。

截图错误来自新渲染器读取已安装 0.1.8+9 的旧模板，其中四个参数已不属于当前 main 模板。资源来源回归先复现相同的“Codex 启动定义包含未解析参数”，再验证当前模板与 Aqua helper 均来自预览资源，旧资源字节不变。未知参数仍须在服务或环境变更之前拒绝。

## 用户态 mesh 路由契约

原生 tsnet 不创建系统路由，应遵循 macOS 为目标选择的路由，包括到私有服务器的 VPN。Tailscale 默认将 TCP 绑定到物理默认接口；实机观察到服务器的系统路由指向 VPN，而 tsnet 连接使用 Wi-Fi 地址并停在 `SYN_SENT`。

`createDesktopMesh` 在 Darwin 上使用 Tailscale 原有 `SetDisableBindConnToInterface`，保留系统选路。其作用限于该用户态 Agent 进程，不改变系统 VPN、服务器地址、账号、证书验证、mesh 身份或 Codex 接入协议。IPv4/IPv6 的 socket 边界回归先失败，再验证不再写入接口绑定；接入完整验证入口。

## 实机证据与交付边界

临时诊断使用现有 mesh 状态、同版本依赖和既有 TLS 修补。每次先通过原空闲守卫停止 Agent，诊断退出后用原签名 CLI 恢复它。

```text
默认接口绑定：tsnet.Up: context deadline exceeded
遵循系统路由：UP_OK state=Running
网关 HTTP 代理 → 临时 mesh 诊断端点：fleet-mesh-route-diagnostic
```

最后一项证明修正后的双向 mesh TCP 数据路径，临时端点只返回诊断标识，不代表正式 Agent 的文件、终端或聊天验收。

预览使用相同管理 API 实测启动，原签名 Agent 保持 0.1.8+9，shared listener 与 Unix proxy 就绪，Aqua 环境指向原共享 WebSocket。CUA 观察到“后台运行中”；随后重启确认 Agent PID 更换，旧签名二进制再次进入 connecting。它尚未包含路由修复，网关经真实用户态 HTTP 代理访问其健康端点仍超时，因此不能报告网页连接已恢复。

正式 Hub、Agent 二进制与 settings、binding、Agent plist、登录启动偏好 SHA 保持原样。Codex plist 按当前 main 模板重建，keeper PID 与监听地址实际核对；现有 Desktop app-server 未被终止。最终调试预览为 `/private/tmp/fleet-hub-preview.9Qi19D/Fleet Hub Preview.app`。正式 Agent 更新继续等待用户后续发行指令。

验证命令 `bash scripts/verify.sh` 退出 0：Go agent/原生路由与 TLS/server 检查、447 项 JS（446 通过、1 项既有 SDK 条件跳过）、94 项 Swift 和 Shell 检查通过。原始输出：`/private/tmp/fleet-preview-resources-final-verify-20261011.log`。最后的预览脚本元数据调整另外通过 bash 语法/变量守卫检查与实际重开验证。

需求与验证对应：

| 需求 | 验证 | 实现 |
| --- | --- | --- |
| REQ-RESOURCE-1 配套预览资源，保持签名安装 | CodexSharedRuntimeTests 的 preview 资源回归及 SHA 核对 | RuntimeLayout、FleetHubApp、run-settings-preview.sh |
| REQ-RESOURCE-2 未解析参数拒绝，不改服务/环境 | CodexSharedRuntimeTests 的 unknown parameter 回归 | CodexSharedRuntime |
| REQ-ROUTE-1 用户态 mesh 遵循 macOS 系统路由 | TestDesktopMeshUsesSystemRouteWithoutBindingPhysicalInterface；实机 tsnet 与网关诊断请求 | desktop_tsnet.go |
| REQ-LIFECYCLE-1 沿用守卫与共享服务 | 原 NativeRuntime/SharedRuntime 回归；相同 API 启动、停止及重启；readyz/Unix/Aqua | NativeManagement、CodexSharedRuntime |
