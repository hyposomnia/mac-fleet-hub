# 原生客户端待统一发布修复

用户要求：先修复本次问题，后续问题收齐后统一签名、打包和上线。本轮不发布新 DMG、不修改下载源、不替换本机应用或后台。

## 1. 后台签名校验误把规则当作文件路径

状态：源码已修复并验证，等待统一发行；当前安装的 `0.1.3+4` 尚未包含修复。

- 症状：运行状态显示 `No such file or directory / invalid requirement specification`，阻止后台安装或版本同步。
- 根因：`codesign -R` 的普通字符串参数被解释为规则文件路径；内联规则必须以 `=` 开头。
- 修复：共享 `BackgroundRuntimeVerifier` 给内联规则加 `=`，同时覆盖 Hub 后台同步与登录启动器的校验入口。保留严格验签、同团队 Developer ID、固定 identifier、版本一致性及 Gatekeeper 检查，未降低安全条件。
- 回归：新增精确参数契约，断言内联前缀及完整 Developer ID 规则，并防止空校验集合误通过。定向测试先失败（1 项、1 failure），修改后原生运行测试 7 项全部通过。
- 真实命令：临时 Swift 验收程序链接修复后的 FleetCore，调用真实共享 verifier 校验已安装 Hub 与其签名 Agent 载荷，exit 0，输出 `signed_background_verifier=PASS`；仅只读校验，没有启动或替换后台。

## 验证记录

基于 `5b42fc1` 的独立源码快照只加入本次两项 Swift 修改，运行 `FLEET_SPARKLE_ARCHIVE="$HOME/Library/Caches/fleet-hub/Sparkle-2.10.0.zip" bash scripts/verify.sh`，exit 0，输出 `全部验证通过 ✓`。Go agent/server、362 项 Web JS、18 项发行测试、4 项公证预检、59 项 Swift 和全部 Shell 层通过，0 fail、0 skip。并行进行中的网页整改未纳入本次源码验证，也未被修改或提交。

日志：`/private/tmp/fleet-inline-requirement-red.log`、`/private/tmp/fleet-inline-requirement-green.log`、`/private/tmp/fleet-inline-requirement-real-verifier.log`、`/private/tmp/fleet-inline-requirement-full-verify.log`。

后续统一包仍须走唯一正式签名公证入口；这次只读签名校验不替代更新包验收、实际后台安装、真实 TCC 或逐用户入网测试。
