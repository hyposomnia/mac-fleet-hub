# 紧凑工作区 Tab 与共享输入框验证

日期：2026-10-02。源码已同步到 `codex/multi-user-server`，外壳资源版本 v165。本次没有推送 main、生产部署、服务重启或客户端替换。

## 实现

- 桌面 28px 图标 Tab 替代重复会话标题，栏高 42px；移动端保留 44px 触控区域。选中项使用主题主表面，其他项使用柔和底色，无装饰边框或 hover 变色。
- 悬停 350ms 后显示标题、至多四行摘要和路径；键盘焦点也可读预览，Escape 可关闭，切换和退出账号时清理。鼠标可进入预览卡，手机不弹 hover 卡。
- 文件预览标题栏只保留一行路径、换行和下载；没有返回按钮、设备、格式或大小标签。
- 文件 Tab 中保留原会话输入框、附件区、草稿及事件处理器；仅正文区域 inert。ResizeObserver 按输入区实际高度为文档预留空间。

## 自动验证

在开发 checkout 执行 `bash scripts/verify.sh`，退出码 0。原始日志见 [compact-tabs-verify.txt](compact-tabs-verify.txt)。实际输出摘要：

```text
ok  	fleet-agent	(cached)
ok  	fleet-enroll	(cached)
ok  	fleet-enroll/multiuser	(cached)
ℹ tests 291
ℹ pass 291
ℹ fail 0
tailscale-utils tests passed
setup-mac shared tests passed
client-authorization tests passed
codex-bin-resolve tests passed
codex-keeper-launch tests passed
uninstall-restore tests passed
check-codex-idle tests passed
check-fleet-update-safe tests passed
nginx-config tests passed
multiuser-server tests passed
bash-var-brace tests passed
migrate-agent-retry tests passed
==> 全部验证通过 ✓
```

## 浏览器验证

使用 Chrome 原生 UI 验证独立 loopback 页面 `http://127.0.0.1:8894/`，加载真实工作区模块、CSS、预览标题栏和输入框结构，文档内容及发送处理器为隔离测试数据。

- 桌面浅色/深色与 390×844 手机浅色/深色布局已检查，Tab 与路径栏统一，文件和输入框同时可见，无遮挡。
- 打开两个文件，切换会话和文件，草稿持续保留。
- 在文件 Tab 点击发送，测试页面显示 `本地发送已触发：继续检查接口文档`。此项验证本地输入交互，不代表向真实模型发送消息。
- 关闭第二个文件回到前一个文件；切换会话仍保留文档，输入区仍可用。
- 悬停/焦点预览展示标题、正文摘要和完整路径；文档返回按钮及 M1/Markdown/大小标签不再出现。

没有生产网关或真实设备的发布/UAT证据，不能据此报告已经上线。
