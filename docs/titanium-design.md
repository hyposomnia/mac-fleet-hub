# 精密钛银落地规范

设计来源：用户选定的 `http://127.0.0.1:8876/variants/titanium/` 浅色精修规范及 SVG 母版。

## 共用基础

`server/dashboard/titanium.json` 是 Web / iOS 共用的色彩、圆角、字号与导航尺寸来源。Web 使用生成的 `titanium.css`；iOS 在构建时将同一 JSON 打包为只读资源，由 `TitaniumStyle` 转为系统动态颜色和尺寸。不要分别修改 CSS 与 Swift 的颜色、字号或栏宽。

```sh
node scripts/titanium-design.mjs --write
node scripts/titanium-design.mjs --check
bash scripts/verify.sh
```

当前已跟进 Web v170 的统一画布：浅色页面、导航、内容均为白色，深色均为 #10141B；控件填色与钢蓝操作建立层次。容器无常驻描边；选中使用填色与字重，聚焦保留 2px 钢蓝指示。加载状态环、媒体背景、状态点与头像保留功能性形状。设备颜色同样来自共用 JSON，不在 Swift 与 CSS 分别维护。

圆角统一为小控件 8px、标准控件 12px、内容卡片 16px、输入器 / 弹层 20px。手机主要操作至少 44px；系统控件、状态点和头像不强行改形状。

## 主题与兼容

新用户默认浅色；已保存的浅色 / 深色选择继续有效。跟随系统作为显式偏好保存，避免与默认浅色混淆。Web 与 ttyd 使用当前设计令牌；App 通过现有版本 1 快照的可选主题字段同步原生栏，不新建 WebView、不重启会话。旧快照缺少主题字段时采用浅色。

网页与 App 均以实际可用宽度 860 为单栏 / 三栏断点；861–1180 的栏宽为 220 / 310，更宽时为 260 / 330，收起设备栏为 72。手机采用 H5 的文字标题切换、设备选择面板和 44px 操作；宽屏采用 Web 的栏内标题、36px 操作和图钉，无全局重复顶部导航。App 的 WebView 内部可能窄于 860，因此用独立的 native-layout 标记决定会话头部，不拿内嵌窗口宽度误判外部导航。无业务 API、设备授权或生产网络调整。

## 标识

标识复用规范三折面空心六角 SVG：`server/dashboard/icons/logo.svg`。Web 安装图标与 iOS AppIcon 从 `icons/icon.svg` 同一母版导出；图标 PNG 保持不透明方形，不预裁切四角。iOS 深色图标使用规范指定的 #16202A / #B4CCE0。

PWA 缓存版本与资源引用同步升级，包含共用主题与标识。网页端不依赖 iOS 构建工具即可继续迭代。

本地模拟后端与模拟器测试不代替真实网关登录、真实终端 WebSocket 或 Duo 硬件验证。
