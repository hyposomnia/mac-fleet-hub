# 设置整改验证

范围：`codex/multi-user-server`；本批完成后统一发布 abj 独立验收服务。不替换现有 Mac 应用、launchd、Desktop 或其他服务器。

## 红 → 绿

- 原生路径规范化、裸地址 HTTPS、并发编辑不被旧保存响应覆盖、空服务器自动启动设置：新增用例先失败，最小实现后通过。
- 系统登录启动授权失败时恢复原后台配置；临时启动提供打开已安装应用并只携带有效 origin：新契约缺失的编译失败后补实现，Swift 全过。
- 账号与下载页分离、容器注入、注册返回登录、恢复码流程离开保护及独立账号 hash 导航：先失败，账号 31 项通过。
- 统一设置弹窗：页面选择、过期加载丢弃、未保存确认、恢复码保护、Escape/焦点及非阻塞内联确认，6 项通过。
- 登录鼠标探针：触屏/减少动画降级、单帧限流、页面隐藏及销毁清理，3 项通过。

## 本地真实浏览器

独立服务 `http://127.0.0.1:7099` 使用 `/private/tmp/fleet-settings-batch-local` 状态。仅生成合成测试账号，未使用真实用户密码、验证器或恢复码。

- 桌面浏览器真实打开根页面，空设备“添加设备”进入同一个弹窗；账号、添加设备、自动化、会话设置分别切换成功，账号安全表单完整。
- 输入框为 `rgb(227,233,239)`，账号区表面为 `rgb(247,249,251)`，不再混为同色。
- Chrome 390×844：统一横向菜单、独立内容滚动；文档宽度 390，无横向页面溢出。深色账号与添加设备可用。
- 登录/注册链接互返；表单之外是细网格，探针不读取输入框。细指针动画、粗指针/reduced-motion 降级由行为测试覆盖。
- 本地实例没有发行包，正确显示暂不提供下载；abj 下载另行验收，未伪造本地下载链接。
- 内置浏览器的原生确认框曾阻塞自动化控制；已改为统一弹窗内的 `alertdialog`，实际页面不再调用阻塞式原生确认。Chrome 复测“继续编辑”保留会话缓存数草稿 `5`，点击“放弃并离开”才关闭或切页，未保存输入不会被静默丢弃。
- 独立 `/account` 的“添加设备”链接实测切至 `/account#add-device`，点击“账号”恢复安全表单；不再只有 hash 变化而内容不变。

截图：`/private/tmp/fleet-settings-account-desktop.jpg`、`/private/tmp/fleet-settings-account-mobile-dark.jpg`、`/private/tmp/fleet-settings-device-mobile.jpg`、`/private/tmp/fleet-settings-inline-confirm.jpg`。这些只含合成账号，不提交真实私有凭据。最后一次合成会话已通过真实 logout API 撤销，测试 cookie 与私有夹具均已清理。

## 完整命令

`FLEET_SPARKLE_ARCHIVE="$HOME/Library/Caches/fleet-hub/Sparkle-2.10.0.zip" bash scripts/verify.sh`

最新日志：`/private/tmp/fleet-settings-batch-final-verify.log`。exit 0，Go agent/服务器、324 项 Dashboard + 9 项新增设置/动效测试，共 333 pass、0 fail；17 项发行/打包测试（0 skip）、58 项 Swift（0 failures）、全部 Shell 层通过。

## 原生开发构建与隔离运行

开发预览产物：`/private/tmp/fleet-settings-refinement-development/fleet-settings.caNRkh/Fleet Hub.app`，版本 `0.1.3+4`，Universal 架构 `x86_64 arm64`。构建日志 `/private/tmp/fleet-settings-development-build.log`，exit 0。这是开发包，不是正式签名公证发行包，不可用于下载分发。

`node scripts/settings-runtime-uat.mjs '/private/tmp/fleet-settings-refinement-development/fleet-settings.caNRkh/Fleet Hub.app'` exit 0，日志 `/private/tmp/fleet-settings-refinement-runtime-uat.log`：真实内置 agent 在私有临时控制 socket 下运行；版本一致、维护态拒绝设置、重启保留配置、旧二进制升级路径被拒绝；内置 filebrowser、ttyd、tmux 在独立文件与 loopback 下验证通过，不依赖 Homebrew。

GUI 实测：临时位置的“关联账号”输入 `fleet.example.test:7443` 后“打开网页授权”可点击；空地址仍正确禁用，页面无保存按钮。未点击安装或实际授权，测试输入已清空，临时应用已退出。截图 `/private/tmp/fleet-settings-native-account.png`。原有后台仍是 PID `73774`，来自 `/Applications/Fleet Hub.app`，未重启或替换。

## 正式发行阻塞

拟统一发布 `0.1.3+4` 与 Web v185；abj 仍保留 `0.1.2+3`，本批未上线。

唯一发行入口 `--native-candidate-check` 在公证凭据检查失败：`No Keychain password item found for profile: mac-fleet-hub-notary`。随后真实执行 `xcrun notarytool history --keychain-profile mac-fleet-hub-notary`，exit 69，仍为同一缺失；Developer ID 证书可见不代表公证凭据可用。

已请用户在本机终端执行 `xcrun notarytool store-credentials mac-fleet-hub-notary`，凭据仅在终端输入，不发送聊天或进入日志。恢复后从最新干净不可变提交运行完整唯一入口，完成签名、公证、更新签名、下载 SHA 及 abj 验收，不能用开发包或旧包冒充完成。

本地测试不证明真实 FDA、真实设备入网或升级后 TCC 保留。正式包的签名、公证、下载 SHA 与 abj 服务检查须以发行后的记录为准。
