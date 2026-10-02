# 会话窗口文件标签 v163

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
