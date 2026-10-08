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

## 独立的上传失败

凭据可读后第一次完整发布，在 agent 公证上传阶段返回 `abortedUpload` / `HTTPClientError.deadlineExceeded`，入口 exit 1；abj 客户端和服务指针仍为 build 3，未晋级未公证产物。这不是认证错误或凭据丢失。

`notarytool submit --help` 明确支持 `--no-s3-acceleration`。原生三项公证提交改用官方标准 S3 上传，保留 Developer ID、等待 Accepted、staple、升级签名和真实下载校验的完整链路。包装契约测试先失败，再验证三项提交都使用普通上传；重试仍从最新干净提交走唯一入口，不拆分手工提交或复用失败上传。

使用标准上传的完整唯一入口已 exit 0：agent、主应用和 DMG 三项 Accepted，签名、staple、更新签名、真实下载与 Gatekeeper 均通过。无需重新保存或轮换任何凭据。客户端已发布 build 4；Web 服务首轮校验误将私有 `/app.js` 的正确 303 当作字节不一致，触发恢复旧指针与服务。校验改为确认私有脚本匿名仍返回 303；另单独补齐公开登录动效资源路由，服务从新的服务器修复提交部署，客户端签名源码仍为 `3ba3ea2`，不伪称二者同一提交。
