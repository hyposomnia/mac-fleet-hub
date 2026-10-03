# Fleet Hub 原生设置应用实现计划

用户已确认完整设计与完全磁盘访问。开发使用现有特性分支的隔离源码快照，保留共享工作区并行 UI 改动。正式下载只能来自唯一签名机的签名公证发行。

## 文件和契约

- `mac/fleet-agent/desktop_control.go`、`desktop_control_test.go`：`desktopSettings{schema,origin,auto_start}` 的原子持久化、地址校验、状态 JSON、固定文件权限探测、受限管理操作。后台状态探测只经 0600 Unix socket，不开放 TCP 管理端点。
- `mac/fleet-agent/desktop_runtime.go`、`desktop_runtime_test.go`：launchd 运行定义与登录启动分离、实际服务启动/停止/重启、依赖路径和后台健康、同 UID 文件边界。令牌仍在现有私有 binding 文件，状态输出不包含它们。
- `mac/settings-app/Sources` 与 `Tests`：原生设置窗口、独立状态模型、机器可读管理调用、系统设置/Finder 入口、复制到 Applications、浏览器授权与应用内明确确认、升级和卸载系统界面。
- `scripts/build-settings-app.sh` 与 `release-fleet-agent.sh` 候选扩展：正式嵌套应用、架构、依赖、签名与公证、DMG、完整应用升级包、同源 feed/清单；没有实际依赖或未通过公证时不发布。
- `server/dashboard/account.js`、`account_pages.test.mjs`：只消费新的应用发行清单，以 DMG 和 GUI/FDA 步骤替换源码推荐安装入口。
- `scripts/verify.sh`：在现有 Go/JS/Shell 层增加原生状态模型与打包契约验证，不删除原有测试。

## 测试驱动步骤

1. [ ] 写配置/FDA 分类失败测试；运行 `cd mac/fleet-agent && go test -run 'TestDesktop'` 观察缺少实现的失败；实现最小配置与探测，跑到绿。覆盖错误 origin、schema、原子保存、权限拒绝、目标不存在、无内容读取、令牌不泄漏。
2. [ ] 写私有 socket/运行状态与生命周期失败测试；加入受限本地接口，验证实际后台探测与 UI 进程探测不混淆。测试只用临时目录与隔离 OS 适配器。
3. [ ] 原生模型测试先覆盖保存失败/旧状态保留、授权未确认不入网、关窗口不停止服务、FDA 待验证与拒绝提示；实现 SwiftUI 操作和系统入口，使用 `swift test` 验证模型，`xcodebuild`/`swiftc` 验证应用编译。
4. [ ] 添加登录启动/首次安装/卸载清单、root 操作限制、依赖完整性、安装目录与繁忙守卫测试，再实现对应原生流程；不以中文输出解析或自动 yes 模拟 GUI 授权。
5. [ ] 添加应用清单/升级策略测试，拒绝跨 origin、错误身份、错误签名、降级及未 Accepted 发行；集成完整应用升级事务，验证失败不替换运行版本。
6. [ ] Web 测试先要求 DMG、当前 origin 复制和 FDA 步骤，禁止推荐 tar/裸二进制/终端安装；实现并验证缺失发行时没有假链接。
7. [ ] 运行 `bash scripts/verify.sh` 完整验证，再通过唯一签名候选入口完成签名/公证/真实下载，最后更新 abj 独立页面及下载源。

## 实机证据

分别记录复制到 Applications、系统后台审批、浏览器 owner 确认、真实 Headscale 节点、关闭 UI 后 daemon PID、FDA 实际后台探测、登录启动、新应用版本升级及回滚、卸载清理。macOS 密码/TOTP/FDA 开关由用户在系统或浏览器内操作，不记录秘密；未完成的人机步骤如实报告。

## 2026-10-03 续做记录

- 配置、FDA 分类、私有 socket、浏览器配对、独立 userspace mesh、原生模型、Web DMG 说明与应用打包已有实现和测试；步骤 1/2/3/6 的核心测试已通过，但不能代替 launchd 与真实入网验收。
- 步骤 4/5 已加入原子安装、独立登录项、繁忙维护守卫、完整应用升级入口和私有备份恢复；卸载异常路径、实际升级/回滚与全新 Mac 的 Codex shared 配置仍未完成验收。
- 修复实际运行测试发现的 macOS 新 SDK `pipe2` 弱链接崩溃；重新构建 Universal 依赖后，真实 ttyd HTTP、filebrowser 文件列表、tmux 独立会话均通过。最低系统版本声明仍待旧版 macOS 实测。
- 原生窗口已实际打开检查；未点击安装、FDA 开关或登录后台权限。开发包只留在临时目录。
- 唯一签名机已执行原生候选发布入口；应用签名、公证 Accepted 和 Gatekeeper 评估通过。abj 服务端已更新，原生安装源发布和本机安装仍未完成；步骤 7 尚未完成。
- 操作说明与当前证据见 `docs/native-settings-client.md`。不得将上述开发构建上架为正式下载。

### 主工作区续做

- 原生客户端源码已合回 `codex/multi-user-server` 工作区；保留并行 Web UI 修改与已有三项新增 JS 测试，验证入口追加原生打包契约与 Darwin Swift 测试。
- 新增红→绿回归：维护期间拒绝修改设置/发起及确认配对；不再复用被篡改的 SDK 解压缓存；取消升级不继续停止服务；新后台需匹配应用版本和构建号，已绑定设备不能以临时 unbound 误报健康。
- 新增停止态卸载准备测试：临时启动、重读关联、空闲检查；运行态不重复启动，启动失败或繁忙则中止。
- 实际开发包运行测试增加版本一致、维护期间保存被拒、SIGTERM 正常退出、重启后配置保留；仍只使用临时 HOME 与直接子进程，不操作本机 launchd。
- 2026-10-03 已将最新 main 合入 `codex/multi-user-server` 并推送 `78840d060c52212f240e0ca4c55d3cd00f36ec5c`，完成 abj 服务端一致性备份、替换和 HTTP/systemd 验证；注册状态和加密密钥保留。应用公证 ID `3b65a0d7-60ad-4a5c-91e0-dbc7385e0b11` 为 Accepted，发布等待用户解锁 Mac 并允许升级签名工具访问钥匙串。DMG 公证、真实下载、本机安装和设备归属确认仍待完成；不将开发包作为官网下载。
