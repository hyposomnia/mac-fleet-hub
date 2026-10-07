# 设置整改验证

范围：`codex/multi-user-server`；本批完成后统一发布 abj 独立验收服务。不替换现有 Mac 应用、launchd、Desktop 或其他服务器。

## 红 → 绿

- 原生路径规范化、裸地址 HTTPS、并发编辑不被旧保存响应覆盖、空服务器自动启动设置：新增用例先失败，最小实现后通过。
- 系统登录启动授权失败时恢复原后台配置；临时启动提供打开已安装应用并只携带有效 origin：新契约缺失的编译失败后补实现，Swift 全过。
- 账号与下载页分离、容器注入、注册返回登录、恢复码流程离开保护：先失败，账号 30 项通过。
- 统一设置弹窗：页面选择、过期加载丢弃、未保存确认、恢复码保护、Escape/焦点，5 项通过。
- 登录鼠标探针：触屏/减少动画降级、单帧限流、页面隐藏及销毁清理，3 项通过。

## 本地真实浏览器

独立服务 `http://127.0.0.1:7099` 使用 `/private/tmp/fleet-settings-batch-local` 状态。仅生成合成测试账号，未使用真实用户密码、验证器或恢复码。

- 桌面浏览器真实打开根页面，空设备“添加设备”进入同一个弹窗；账号、添加设备、自动化、会话设置分别切换成功，账号安全表单完整。
- 输入框为 `rgb(227,233,239)`，账号区表面为 `rgb(247,249,251)`，不再混为同色。
- Chrome 390×844：统一横向菜单、独立内容滚动；文档宽度 390，无横向页面溢出。深色账号与添加设备可用。
- 登录/注册链接互返；表单之外是细网格，探针不读取输入框。细指针动画、粗指针/reduced-motion 降级由行为测试覆盖。
- 本地实例没有发行包，正确显示暂不提供下载；abj 下载另行验收，未伪造本地下载链接。
- 内置浏览器的原生确认框阻塞了自动化控制，未把这一分支报作实测成功。未保存确认的否决语义有行为测试，其他可见路径在真实 Chrome 复验。

截图：`/private/tmp/fleet-settings-account-desktop.jpg`、`/private/tmp/fleet-settings-account-mobile-dark.jpg`、`/private/tmp/fleet-settings-device-mobile.jpg`。这些只含合成账号，不提交真实私有凭据。

## 完整命令

`FLEET_SPARKLE_ARCHIVE="$HOME/Library/Caches/fleet-hub/Sparkle-2.10.0.zip" bash scripts/verify.sh`

日志：`/private/tmp/fleet-settings-batch-verify.log`。exit 0，Go agent/服务器、323 项 Dashboard + 8 项新增设置/动效测试、17 项发行/打包测试（0 skip）、58 项 Swift（0 failures）、全部 Shell 层通过。另执行 `node --test server/dashboard/*.test.mjs`，331 pass、0 fail。

本地测试不证明真实 FDA、真实设备入网或升级后 TCC 保留。正式包的签名、公证、下载 SHA 与 abj 服务检查须以发行后的记录为准。
