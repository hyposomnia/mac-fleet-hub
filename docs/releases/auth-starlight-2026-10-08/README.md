# 登录页稀疏星光 · 2026-10-08

用户在五方案评审后要求取消表单白色遮罩，并将较抢眼的跟随效果改成从屏幕四周随机飞向鼠标的星光。最终登录页采用小光点与淡光晕，最多六颗，转向平滑，靠近鼠标淡出；静止时降低生成频率，不在鼠标附近堆积。系统鼠标、输入框原有填充及账号流程保留。

## 实现

- `auth.html` 使用背景 Canvas；`account.css` 移除旧网格/准星样式，表单没有遮罩。
- `auth_effects.js` 按帧时间计算速度与转向，随机从四条屏幕边缘发射光点，寿命不超过 2.8 秒。光点半径 0.65–1.05px、光晕半径 6px、核心最大透明度 0.36。
- 仅 fine pointer 启用；触屏、减少动态效果、鼠标离开和页面隐藏会停止并清理动画。主题切换采用对应 accent 色。
- 认证页 CSS 和动效独立使用 v188 URL，避开旧缓存键；未改 PWA 外壳或其它资源引用。

## 验证证据

`bash scripts/verify.sh` 退出码 0，完整真实输出见 [verify.txt](verify.txt)。Go agent/server/multiuser 通过；JS 各组分别为 21、332、14、17、4 项通过，发布组另有 1 项平台相关跳过；Swift 应用 10 项与核心 48 项通过；所有 shell 验证通过，最终输出 `==> 全部验证通过 ✓`。

星光专门覆盖八个行为：触屏/减少动态效果、合并鼠标输入、四条边缘发射并趋向指针、静止时稀疏生成及数量上限、混合设备触摸输入、离开/隐藏取消、动态偏好改变取消、主题色更新及销毁。

独立真实 Go 服务 `http://127.0.0.1:8790/auth` 使用临时数据库和密钥。浏览器四种状态见 [browser-checks.json](browser-checks.json)：1440×900 和 390×844，均无横向溢出；表单背景透明、伪元素为 none，Canvas 不接收鼠标事件。截图为当前星光版本的 [浅色桌面](light-desktop.jpg)、[深色桌面](dark-desktop.jpg)、[浅色手机尺寸](light-mobile.jpg)、[深色手机尺寸](dark-mobile.jpg)。

实际 Canvas 像素读数非零，证明星光确实绘制；触屏与减少动态效果模拟均为 hidden/display:none，控制台 error/warn 为 `[]`，详见 [accessibility-checks.json](accessibility-checks.json)。手机结果来自浏览器模拟，不作为手机真机证据。

原始五方案及首次截图保留在 [调研档案](../../features/auth-pointer-lab/README.md)，不作为最终星光证据。被替换的丝带验证留在本地临时目录，未混入本次发布。

## 发布范围

目标为用户指定的独立验收服务 `https://10.17.74.92:7443/auth?next=%2Fadmin`。源码提交 `1e4debdf198d69b08f69b047b032847db8f959c4` 已快进合并并推送 `main`。2026-10-08 10:18 UTC 从该不可变提交归档发布，只替换 `dashboard/auth.html`、`dashboard/account.css`、`dashboard/auth_effects.js`；另更新快照 SHA256SUMS 并记录 WEB_REVISION，后端 REVISION 保留原值。

当前快照为 `/opt/macfleet-saas-uat/releases/web-starlight-1e4debdf198d69b08f69b047b032847db8f959c4`，旧快照 `server-0da0ae56d4e7389589b23c00998f372522bffe3a` 保留。发布先备份 dashboard 与 current 指针，复制当前快照并逐文件确认只有上述三个运行文件变化，再原子切换；脚本在切换后任何验证失败会恢复旧指针。备份路径见 [deploy.txt](deploy.txt)。无需重启服务。

远端完整快照 136 项 SHA 校验通过。本机及服务主机均使用从目标取得的公共证书执行 curl TLS 校验，没有跳过证书检查；HTTPS 下载的三个文件与源码提交逐字节相同，见 [public-check.txt](public-check.txt)。实际结果：`/auth` HTTP/2 200 且 no-store，`/healthz` 和 `/readyz` 均 200，未认证 `/api/devices` 为 401，私有 app/settings/sw 资源为 303；四项验收服务均 active。

后端 PID 前后均为 `597209`，二进制 SHA-256 保持 `aac8231a317451e3d96433776253c4b1d5391ef5a8e53ad504f2db86bb5ce51a`。直接远端浏览器因原有证书信任提示未做视觉验收；视觉证据来自本地相同前端源码，线上证据为证书校验、资源字节、HTTP 状态与服务状态。

服务端二进制、数据库、密钥、Headscale、网络、Mac 客户端和 Desktop 配置不属于本次发布。
