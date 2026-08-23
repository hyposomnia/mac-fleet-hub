# CLAUDE.md — mac-fleet-hub 项目入口

本项目的完整开发/部署规则以 [`AGENTS.md`](AGENTS.md) 为准。Claude Code 进入仓库后应先读完
`AGENTS.md`；项目人格 `/dev`、`/ui`、`/deploy` 的定义与记忆位于 `.claude/`，其中生产发布由
`/deploy` 负责。

## fleet-agent 唯一正式发布入口

fleet-agent 的正式产物只能在持有 Developer ID Application 私钥和 Apple 公证凭据的签名构建机
上生成。不要在其他电脑本地编译后直接替换网关或 Mac，也不要用 Apple Development、ad-hoc 或
未公证签名发布。

连接签名构建机后执行：

```bash
ssh hjc@100.64.0.2
cd ~/Git_Repositories/mac-fleet-hub

# 只读预检：证书、网关 SSH、所有 Mac SSH/PID/health
bash scripts/release-fleet-agent.sh --check

# 完整发布
bash scripts/release-fleet-agent.sh
```

完整脚本会按固定顺序执行：`git pull --ff-only` → `scripts/verify.sh` → 双架构构建 → 固定
`com.macfleet.fleet-agent` identifier 的 Developer ID 签名与可信时间戳 → Apple 公证 Accepted →
提交/push 产物 → 网关分发包备份替换 → 公网 SHA 校验 → 四台 Mac 逐台备份、更新、验签及
PID/health 验收。所有 SSH/SCP/远端验证输出均应保留给人和 AI 审阅；任一步失败必须停止，不能跳过。

真实网关与节点清单只保存在签名机的 `~/.config/mac-fleet-hub/release.env`（权限 `0600`），证书、
私钥、公证密码和真实配置不得提交。公开配置格式见 `scripts/release-fleet-agent.env.example`。
