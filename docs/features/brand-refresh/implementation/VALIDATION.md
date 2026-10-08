# Titanium Web 实施验证

2026-10-01，在 `codex/multi-user-server` 上执行。独立本地服务 `http://127.0.0.1:7099`，私有状态 `/private/tmp/macfleet-titanium-20261001`。本次没有生产部署或现有 Mac 客户端迁移。

## 实施

实际 `server/dashboard` 已采用 Titanium 的浅色默认、深色适配、原生 SVG 品牌与安装图标、8/12/16/20px 语义圆角、无装饰边框表面。保留原有布局、移动滚动与缩放；菜单增加方向键、Escape 与焦点恢复。

共享 `theme.js` 在首屏同步加载，保留既有显式选择，system 独立持久化，支持系统变化、跨标签页同步和受限 storage。登录/账号/管理/配对页面共用主题。PWA 外壳为 v155，继续排除账号/API/设备/用户文件路径。

## 自动化证据

`bash scripts/verify.sh`，退出码 0。[完整真实输出](verify.log)。

```text
Go 测试：mac/fleet-agent (go test ./...) → ok fleet-agent
Go 测试：server/enroll (go test ./...) → ok fleet-enroll / fleet-enroll/multiuser
ℹ tests 264
ℹ pass 264
ℹ fail 0
==> 全部验证通过 ✓
```

新增主题默认、选择持久化、系统变化、跨标签页、storage 失败与无效选择测试；菜单方向键跳过 disabled 和 Escape 焦点恢复测试；公开主题资源可加载而 app.js 保持鉴权的 Go 回归测试。新增行为测试先取得缺失行为的失败，再实现通过。`git diff --check` 退出码 0。

本地账号 smoke：`node scripts/local-multiuser-smoke.mjs http://127.0.0.1:7099 /private/tmp/macfleet-titanium-20261001 --keep-session`，退出码 0。[真实输出](local-smoke.log)。随机合成测试账号的 session 私有保存，仅供浏览器验证；检查结束后已在实际账号页退出登录。

HTTP 验证：[完整 curl 输出](http.log)。

| 请求 | 实际状态 |
|---|---:|
| GET /healthz | 200 |
| GET /readyz | 503 |
| GET /api/devices，未认证 | 401 |
| POST /enroll/join | 410 |
| HEAD /theme.js?v=155，未认证 | 200 |

## 实际浏览器证据

使用实际 Go 服务与实际 Web 资源，通过浏览器 UI 操作。[计算样式和交互状态](browser-measurements.json)。

- 1440px 桌面：实际 grid `260px 330px 850px`，无横向溢出。
- 390px 手机：设备选择、菜单、排序、新建、搜索均 44px 高、12px 圆角、0px 边框，无横向溢出。
- 深色实际 `--bg: #10141B` / `--accent: #B8D9FF`，主题选择后菜单关闭，焦点返回 user-btn。
- Escape 后菜单 hidden=true、触发器 aria-expanded=false，焦点返回 user-btn。
- 账号页检查的 18 个表单/按钮均 12px 圆角、0px 边框，无横向溢出。
- 文件视图切换成功；管理页表格无分隔边框，页面无横向溢出。
- 退出登录返回 /auth，保留浅色预览。临时 viewport 已恢复。

最终截图：[桌面浅色](screenshots/desktop-light.png)、[桌面深色](screenshots/desktop-dark.png)、[手机工作台](screenshots/mobile-light.png)、[登录浅色](screenshots/auth-light.png)、[登录深色](screenshots/auth-dark.png)、[手机账号](screenshots/account-mobile.png)、[文件视图](screenshots/files-light.png)。

## 验证边界

独立本地实例未接入 Headscale 或真实 Mac，readyz 503 是实际结果。工作台与文件截图展示真实空设备状态，浏览器检查不证明聊天、终端或文件的真实设备联动。服务器配对 HTTP 接口、客户端安装器、实际设备验证是不同交付。本次未执行客户端安装或迁移，不声明新 bootstrap 已部署或可用；实际 Mac 联动待后续授权接入验证。
