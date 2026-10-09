# 双分支维护约定

2026-10-09 起，本地与 origin 只保留以下两条长期分支，仓库默认分支保持 `main`。

| 分支 | 用途 | 协议边界 |
| --- | --- | --- |
| `main` | 当前自部署功能迭代与正常使用 | 保留现有 Authelia、节点清单、网关代理与客户端安装协议 |
| `codex/multi-user-server` | SaaS 化功能改造与独立验收 | 多用户账号、owner 隔离、动态设备路由、OAuth 和逐设备授权、原生客户端 |

## 日常开发

- 自部署问题与通用功能在 `main` 完成并验证。
- SaaS 专属改造在 `codex/multi-user-server` 完成并验证。
- SaaS 分支不自动跟随 `main`。需要同步时，由维护者手动合并；冲突中保留 SaaS 的账号、授权、设备及客户端协议。
- SaaS 分支不得整体反向合回 `main`。确需回填的独立通用修复应单独审查与验证。
- 需要隔离工作区时可使用 detached worktree，完成后将提交合入对应长期分支，不保留额外功能或生产发布分支。
- 提交前执行当前分支的 `bash scripts/verify.sh`。源码测试、签名发布与真实部署分别报告；分支整理不授权部署。

## 手动同步自部署更新到 SaaS

确认工作区干净后运行：

```bash
git fetch origin
git switch main
git pull --ff-only origin main
git switch codex/multi-user-server
git pull --ff-only origin codex/multi-user-server
git merge --no-ff --no-commit main
# 如有冲突，保留 SaaS 协议并逐项解决，再 git add 相应文件。
bash scripts/verify.sh
git commit
git push origin codex/multi-user-server
```

没有新提交时 Git 会报告 Already up to date，此时不需要创建空合并提交。
任何验证失败都应先修复；需要放弃尚未提交的合并时使用 `git merge --abort`。
例行同步使用普通合并，不使用 `ours` 策略、整体覆盖文件或强制推送来掩盖冲突。

## 本次分离的基线

此前 `main` 已包含完整 SaaS 代码，不能仅通过删除分支名区分用途。本次保留完整提交历史，通过普通提交分离工作树内容：

- SaaS 保留原 `main` 的 `9018f8a`，整合 `c1f0d7b` 的配色/设备图标和 `e5a6103` 的文件标题栏搜索，保留较新的 35% 毛玻璃与标题行末尾状态图标。
- 自部署运行源码以最新兼容发布 `f5b4eb2` 为基线，包含配色、设备图标、毛玻璃、会话状态及会话恢复；补入响应式文件标题栏搜索。历史设计与验收文档继续保留。
- SaaS 对分离提交做一次有明确目的的基线合并：保留 SaaS 运行源码，接受 `main` 的历史和共同维护文档。该操作仅用于建立分离基线，避免下次合并把本次自部署协议恢复当成新功能同步到 SaaS。
- 删除旧分支前，逐个确认原分支顶端提交可从保留分支到达；已保存本地 Git bundle 和原始引用清单。旧工作树保留文件与 detached HEAD。

本次未改生产服务、Mac 安装、launchd、Desktop 或实际网络，也未构建、签名或发布新的二进制。
