# 原生客户端待统一发布修复

用户最初要求先收集问题、统一发行；追加授权本批改完后重新打包并直接替换本机。范围仅 Fleet Hub 与其独立后台，不变更现有 Tailscale、Codex Desktop 或其他生产 Mac，保留设备关联与设置；实际发行与安装结果另行记录。

## 1. 后台签名校验误把规则当作文件路径

状态：已于 2026-10-09 统一发行并通过内置更新器安装 `0.1.4+5`，本机 Hub 与实际独立后台均已包含修复。完整证据见 [build 5 发行记录](native-build5-test-release-2026-10-09.md)。

- 症状：运行状态显示 `No such file or directory / invalid requirement specification`，阻止后台安装或版本同步。
- 根因：`codesign -R` 的普通字符串参数被解释为规则文件路径；内联规则必须以 `=` 开头。
- 修复：共享 `BackgroundRuntimeVerifier` 给内联规则加 `=`，同时覆盖 Hub 后台同步与登录启动器的校验入口。保留严格验签、同团队 Developer ID、固定 identifier、版本一致性及 Gatekeeper 检查，未降低安全条件。
- 回归：新增精确参数契约，断言内联前缀及完整 Developer ID 规则，并防止空校验集合误通过。定向测试先失败（1 项、1 failure），修改后原生运行测试 7 项全部通过。
- 真实命令：临时 Swift 验收程序链接修复后的 FleetCore，调用真实共享 verifier 校验已安装 Hub 与其签名 Agent 载荷，exit 0，输出 `signed_background_verifier=PASS`；仅只读校验，没有启动或替换后台。

## 验证记录

基于 `5b42fc1` 的独立源码快照只加入本次两项 Swift 修改，运行 `FLEET_SPARKLE_ARCHIVE="$HOME/Library/Caches/fleet-hub/Sparkle-2.10.0.zip" bash scripts/verify.sh`，exit 0，输出 `全部验证通过 ✓`。Go agent/server、362 项 Web JS、18 项发行测试、4 项公证预检、59 项 Swift 和全部 Shell 层通过，0 fail、0 skip。并行进行中的网页整改未纳入本次源码验证，也未被修改或提交。

日志：`/private/tmp/fleet-inline-requirement-red.log`、`/private/tmp/fleet-inline-requirement-green.log`、`/private/tmp/fleet-inline-requirement-real-verifier.log`、`/private/tmp/fleet-inline-requirement-full-verify.log`。

后续统一包仍须走唯一正式签名公证入口；这次只读签名校验不替代更新包验收、实际后台安装、真实 TCC 或逐用户入网测试。

## 2. 网页授权返回 HTTP 410

- 本机只读 status 确认 Hub 为 `0.1.3+4`，实际后台仍是 `0.1.0+1`、PID `73774`，独立运行副本不存在；旧配对记录存在且未领取 grant。前一项验签错误阻止了后台版本同步，新 Hub 随后把授权请求发给旧后台，继续旧配对而非新 OAuth。
- `pair-start` 现在先执行完整后台安装/版本同步和健康验证；失败即停止，不向旧后台发送授权。确认与取消不触发版本替换，避免打断进行中的授权。
- 新后台继续每次新建 OAuth + PKCE，不恢复未领取 grant 的旧配对链接。未输入用户账号、密码或 TOTP，实际账号确认由用户验收。

## 3. 磁盘权限入口与运行详情

- 磁盘权限页始终使用“授权磁盘访问”。点击后先准备独立 Fleet Agent，再打开完全磁盘访问系统设置与可拖拽 Agent 的浮动引导；准备失败不打开系统设置、不伪报权限。
- 从未安装位置发起时，安装后的新实例继续磁盘授权引导，不把 Hub 或内嵌载荷作为拖拽目标。系统开关由用户操作。
- 运行详情直接展示后台版本与 PID，不使用折叠控件。

本批先红后绿的原生、磁盘引导及页面契约测试已通过；完整 `bash scripts/verify.sh` exit 0，64 项 Swift、20 项发行测试与其余 Go/JS/Shell 层全部通过，0 fail、0 skip。日志 `/private/tmp/fleet-native-build5-final-verify.log`。当时准备发行 `0.1.4+5`；后续三项 Accepted、真实下载及本机内置更新均已完成，详见发行记录。

## 4. IP 测试入口的组网 TLS 验证

用户在 `0.1.4+5` 完成网页授权后，界面持续显示“正在接入”。后台已取得账号和设备编号，但内置 Tailscale TLS 校验丢失 IP 身份，无法匹配钥匙串中限定该 IP 的 SSL 信任。普通 HTTPS 到同一入口可通过；内置组网仍报证书未知颁发者。

源码已修正为把预期目标 IP 交给原证书校验器，沿用现有系统信任。Tailscale 来源版本和 SHA 固定，通过临时模块副本构建双架构 Agent，未修改模块缓存。回归先失败于主机名丢失，修复后正确 IP 通过，错误 IP 和未受信任证书分别仍返回 HostnameError / UnknownAuthorityError；真实组网 HTTPS 校验已恢复到 200，全量验证 exit 0。

`0.1.5+6` 已完成三项 Accepted、真实下载校验并发布 abj。用户解锁后确认升级恢复因未完成关联被误判，本机仍为 `0.1.4+5`；原签名后台已恢复，管理接口 HTTP 200。详见 [build 6 发行记录](native-build6-ip-tls-release-2026-10-09.md)。

## 5. 本次新增界面反馈与升级恢复

- 磁盘权限直接沿用设置页的独立 Agent 拖拽图标，授权按钮只打开系统设置，删除重复的浮动窗口和“仅授权 Fleet Agent，设置应用无需磁盘权限”。网页安装说明同步。
- `m1` 来自服务器不复用的编号，不是电脑名称；运行和关联页改标“设备编号”。
- 卸载入口改为文字链接，明确卸载 Fleet Hub 设置程序和 Fleet Agent 后台。现有流程已包含两者，保留移除本机设置的独立选项与确认。
- 登录启动开关下不显示“已启用”，版本和进程直接排列，删除“运行详情”；需要系统批准等异常状态仍提示。
- 未完成且未锁定的关联在 `unbound` 状态下允许后台同步与升级恢复，保留未完成关联，不把它标为已联网；正在配对、已锁定、真实服务失败、版本/PID 不符继续拒绝。较新签名应用替换中断的旧升级时可沿用恢复记录，仍验证实际后台并保留回滚。

以上已统一发行 `0.1.6+7` 并直接替换本机 Hub 和独立 Agent。三项公证 Accepted、真实下载、全量验证和实际后台检查通过，旧关联与设置 SHA 保留，中断的更新记录已正常清除。五项界面整改已在本机 GUI 核对，实际设备完整接入仍由用户重新授权验收。详见 [build 7 发行记录](native-build7-settings-release-2026-10-09.md)。
