# 设备设置页与账号内排序

## 产品契约

- **REQ-DEVICE-1**：设置导航显示“设备”。设备页先显示当前账号的设备列表，再显示新增设备说明；账号设置页只保留账号与登录安全。
- **REQ-DEVICE-2**：每台设备可通过既有 `/api/names` 与 `/api/settings` 契约修改名称、图标和颜色，不增加独立设备配置源。
- **REQ-DEVICE-3**：设备列表显示 owner 自己设备的 mesh IP、在线状态和浏览器到设备健康端点的实际往返延迟；其他账号不能读取或修改这些设备。
- **REQ-DEVICE-4**：拖动顺序保存到当前账号已有的 preferences JSON。`/api/devices` 按保存顺序返回，未进入偏好的新设备按原设备序追加；主会话窗口直接沿用响应顺序。
- **REQ-DEVICE-5**：新增设备区从 `/enroll/client-release.json` 读取当前已公证发布的 DMG 路径、版本、最低系统和字节数，界面名称统一为 Fleet Agent Settings。
- **REQ-DEVICE-6**：服务地址本身可点击，复制入口是紧随链接的图标按钮，并保留可访问名称。

## 数据与接口

`dashboardPreferences.deviceOrder` 是账号范围内的设备 ID 数组。`PATCH /api/settings` 接受 `{ "order": ["m2", "m1"] }`，只保存不重复且属于当前账号、未撤销的设备 ID。保存允许缺少后来新增的设备，读取列表时把这些设备追加到末尾。

`GET /api/devices` 只在 owner 响应中增加 `ip`，不改变管理员元数据、网络协调或设备授权模型。延迟直接请求既有 `/<device>/api/health` 账号隔离代理，不增加探测服务。

preferences 仍使用现有 JSON 行，无数据库 schema 迁移。回滚旧版本时新增字段会被旧结构忽略；恢复新版本后可继续读取。

## 验证矩阵

| 需求 | 自动验证 |
|---|---|
| REQ-DEVICE-1, 5, 6 | `server/dashboard/account_pages.test.mjs`, `settings_dialog.test.mjs` |
| REQ-DEVICE-2, 3 | `server/dashboard/account_pages.test.mjs`, `server/enroll/multiuser/device_appearance_test.go` |
| REQ-DEVICE-4 | `server/enroll/multiuser/device_appearance_test.go`, `server/dashboard/account_pages.test.mjs`, `auth_integration.test.mjs` |

发布沿用 SaaS 不可变源码快照与当前已签名公证的原生客户端发行目录；本变更不构建、不重签原生 Mac 应用。
