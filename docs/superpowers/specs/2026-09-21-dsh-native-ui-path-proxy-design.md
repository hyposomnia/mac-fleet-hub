# DeepSeek Harness 原生 UI 同域路径接入设计

日期：2026-09-21

## 目标

Fleet 在不新增域名和 DNS 解析的前提下，为每台 Mac 暴露受保护的 DSH 原生 Web UI：

- 用户选择具体 Mac 后，通过官方黑色鲸鱼按钮打开 `/mN/dsh/`。
- DSH Desktop 未安装、未运行、目标 Mac 离线或 agent 版本不支持时，按钮置灰并给出原因。
- 原生 UI 在新页面打开，Fleet 自绘 DeepSeek 会话入口保持不变。
- Sub Agent 状态不属于本功能，后续单独设计。

## 数据流与信任边界

```text
browser
  -> https://fleet-host/mN/dsh/          (Authelia)
  -> nginx /mN/dsh/
  -> http://mac-mesh-ip:7682/api/dsh-native/
  -> fleet-agent
  -> http://127.0.0.1:<dynamic-dsh-port>/ (DSH Desktop Harness)
```

浏览器只持有 Fleet/Authelia 会话。fleet-agent 继续通过现有 DSH discovery 获取 loopback endpoint
与认证 cookie，在反向代理请求中服务端注入；DSH 的 cookie、token 和 secret 不返回浏览器。
DSH 仍只监听 loopback，mesh 和公网都不能直接访问 Harness。

## 路径兼容

当前 DSH Web UI 假设部署在站点根路径：HTML 没有可配置的 base path，图标与运行时代码会访问
`/api/*`、`/plugins/*`、`/assets/*` 和 `/api/remote.mux`。代理因此执行两层兼容：

1. HTML/manifest 静态改写，把 base、图标、插件和 manifest 地址收口到 `/mN/dsh/`。
2. 首屏注入最小路径 shim，改写运行期 fetch、WebSocket、XHR、EventSource、sendBeacon 和
   DSH 文件上传 hook；同源但不属于 DSH 的地址不改写。

nginx 关闭该路径的 buffering/cache，保留 WebSocket Upgrade，并把 body 上限与 agent 文件上传
上限对齐。PWA service worker 已把所有 `/mN/` 路径视为敏感实时请求，不缓存 DSH 内容。

## 可用性判定

`/api/info` 增加 `dsh.nativeUI=true`，并在响应前主动探测 DSH manifest。按钮只有在以下条件全部满足
时才可用：当前是会话页、选择具体 `mN`、节点在线、DSH enabled/installed、nativeUI 能力存在、
hostRunning 且未 degraded。点击通过同步 `window.open` 打开新页，避免异步探测触发弹窗拦截。

## 验证

- Go 单测覆盖凭据不泄漏、HTML/manifest 改写、API 透传、WebSocket 和不可用状态。
- dashboard 静态契约测试覆盖两个响应式入口、官方图形、黑色/置灰样式、能力门禁和目标路径。
- nginx 回归测试覆盖 Authelia、路径映射、WebSocket 与上传上限。
- 发布沿用唯一签名构建机的正式 agent 发布流程；不得用本地未签名二进制替换生产 agent。
