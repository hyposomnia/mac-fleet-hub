# 公证凭据读取差异

## 实际证据

- 上一次 build 3 使用 `mac-fleet-hub-notary` 完成 agent、Hub 和 DMG 公证，三项 Accepted 记录与用户终端返回的历史一致。
- 本轮自动执行曾返回 `No Keychain password item found`，默认读取与显式 login 钥匙串读取均 exit 69；当时为同一用户的 Aqua 会话，不是 SSH。
- 用户直接在终端运行 `xcrun notarytool history --keychain-profile mac-fleet-hub-notary`，成功获取原有历史，没有重新保存凭据。
- 随后自动执行同一命令也 exit 0，JSON 中有 31 条原有提交记录。日志 `/private/tmp/fleet-notary-context-recheck.log`。没有修改、导出、删除或重建钥匙串条目，没有更换 Apple 凭据。

这证明“当前进程查不到”不等于“凭据被删除”。已定位到读取/访问状态与执行上下文的差异，但尚无证据确定 macOS 内部哪一项权限或缓存状态发生变化，不能将再次保存凭据作为每轮发布的固定步骤。

## 发布入口修正

`scripts/release-fleet-agent.sh` 保留 `notarytool history` 的退出码：仅成功才确认凭据可用。无法查到 profile 时提示先在用户图形终端核对已有凭据，不再默认要求 `store-credentials`；锁定或未知认证/网络失败均停止，包含只读预检，避免 `|| true` 将未知失败误报成可用。

新增 `scripts/notary-preflight.test.mjs` 四项行为测试，首次 1 pass / 3 fail，修正后 4 pass / 0 fail；纳入 `scripts/verify.sh`。测试只使用隔离的命令适配器，不读取真实钥匙串。

后续若再次出现差异，应对照用户终端与发布执行环境；不能凭一条 lookup 错误要求删除、重录或轮换凭据。真实签名、公证及上线仍通过唯一正式入口完成。
