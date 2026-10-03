# 会话窗口文件标签

## v166 更新与生产验收（2026-10-02）

静态源码提交 `f0726da`，缓存 `fleet-shell-v166`。单会话常驻同一个图标 Tab 组件，打开/关闭文件不再切换旧标题栏；聊天初次打开与隐藏通过实际 pane 状态同步，终端及空态保留原有入口。

发布前 `bash scripts/verify.sh` 全部通过：Go agent/enroll 通过，Dashboard 252/252，全部 Shell 检查通过。生产 105/105 静态文件 SHA256 匹配；设备运行时数据非空，nginx、fleet-enroll、headscale、fleet-nodes.timer 均 active；公网入口 HTTP/2 302。备份：`/var/backups/mac-fleet-hub/dashboard-before-v166-20261002T075007Z.tgz`。没有重启服务、发布后端/客户端或修改认证与网络。

真实 Chrome 强制刷新后打开同一会话，对比单 Tab、打开 README.md 后的多 Tab、回到聊天、关闭最后一个文件四种状态，标题栏及聊天正文起始位置一致；文件下仍保留输入框，关闭后会话与输入框保留。未发送真实消息。

## v165 更新与生产验收（2026-10-02）

已发布静态源码提交 `eac8bbd`，缓存版本 `fleet-shell-v165`。桌面标签缩至 28px，合并标题栏高度 42px；选中标签与内容区连成同一表面，保留延迟悬停的标题、摘要和路径提示。文档头仅显示路径及必要操作，移除返回按钮和冗余类型标记。所有文件标签下保留原会话输入框及草稿。

生产从该不可变提交归档发布，105/105 个静态文件 SHA256 全部匹配，运行时 `api/nodes.json` 保留且非空。nginx、fleet-enroll、headscale、fleet-nodes.timer 均为 active；公网未认证入口实际返回 HTTP/2 302。发布没有重启服务或修改 Mac、认证、网络。发布前备份为 `/var/backups/mac-fleet-hub/dashboard-before-v165-20261002T061532Z.tgz`。

发布前 `bash scripts/verify.sh` 全部通过：Go agent/enroll 通过，Dashboard 251/251，全部 Shell 检查通过。真实 Chrome 生产页打开 README.md 文件标签，确认紧凑标签、简化文档头和可聚焦的原会话输入框；关闭文件标签回到同一会话，重新打开仍正常。未发送真实聊天消息。生产悬停提示未单独验收，已在隔离浏览器验证。

本次授权及发布仅覆盖 dashboard 静态 UI；未发布共享开发分支上的多用户服务器或客户端变更。

会话内文件链接在标题栏的独立标签预览。相同设备和路径复用标签，支持切换、关闭及键盘方向键/Home/End/Delete；关闭最后一个文件回到聊天。聊天 DOM、草稿和连接保留；切换其他会话或返回列表时回到聊天视图。标签仅在本次页面内保留。

复用现有 /view 文件查看器和逐设备文件接口；HTML 的脚本禁用、清洗和 sandbox 保持。外部链接、下载和带修饰键的点击保留浏览器原行为。文件管理器完整预览也进入会话窗口。非活动媒体暂停，关闭标签移除 iframe。

验证：`bash scripts/verify.sh`，Go agent/enroll 通过，Dashboard 248/248，Shell/部署守卫全部通过。完整输出见 verify.txt。新增 7 个测试覆盖路径身份、重复复用、多文件保留、关闭、事件拦截与排除、键盘和预览内链接。

## 生产发布

2026-10-02 11:47（Asia/Shanghai）首次发布文件标签 v162；同次交付随后修正悬停提示重复路径并发布 v163，最终不可变提交为 `c91c82a`。从 git archive 打包全部 dashboard 静态文件；生产结果见 production.txt，公网 HTTP 结果见 http.txt。

- 105/105 个静态文件 SHA256 一致，PWA 外壳为 fleet-shell-v163。
- nginx、fleet-enroll、headscale、fleet-nodes.timer 均 active，api/nodes.json 非空。
- 公网未登录入口为 HTTP/2 302 至原认证入口。
- 静态资源已备份；没有重启服务或修改 Mac/认证/网络。

## 真实浏览器验收

Chrome 已登录的真实生产页，在用户截图指定的“检查测试环境Film任务”会话完成：点击 film_proxy.py 在当前会话窗口打开文件标签，代码高亮与行号正常；返回会话后打开 service.py，两个文件标签同时显示；重复点击 film_proxy.py 复用已有标签，浏览器标签数未增加；返回会话后原聊天正文与输入框保留。最后的 v163 仅修正悬停提示文本。

Mac 随后锁屏，关闭按钮及最终提示文本未继续做人工浏览器检查；相关行为由 248/248 自动测试与最终文件哈希验证。未向真实会话发送消息。

## 共享开发分支

同一功能已同步到 codex/multi-user-server 工作区，并在账号失效时清理预览 iframe。此处对应 Dashboard 248/248 自动测试通过；完整 verify.sh 当前被并行客户端测试中缺失的 readLoginOrigin/confirmDeviceGrant 阻断。生产发布使用隔离的、完整 verify.sh 通过的发布分支，未夹带这些客户端改动。
