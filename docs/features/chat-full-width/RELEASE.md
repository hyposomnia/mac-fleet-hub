# 聊天全宽 · v173

2026-10-07，静态源提交 `169d7cd` 已推送 main，并从不可变 Git 归档发布到 https://fleet.hyposomnia.top:20443/。

正文、输入框、顶部提问提示和浮动跳转栏取消 860px 居中限宽。桌面两侧保留 20px 留白；用户气泡的内容宽度、消息内容和交互不变。移动端继续使用正文 14px、输入区 10px 的原有间距。

发布前 `bash scripts/verify.sh` 退出码 0，Go agent / enroll、Dashboard 250 项测试及所有 shell 检查通过，完整输出见 verify.txt。

真实部署输出：

```text
backup=/var/backups/mac-fleet-hub/dashboard-before-v173-20261007T043156Z.tgz
static_sha256_matches=105
runtime_nodes_present
nginx=active
fleet-enroll=active
headscale=active
fleet-nodes.timer=active
const CACHE = 'fleet-shell-v173';
release=169d7cd
```

公网未认证入口为 HTTP/2 302，正常跳转登录页。发布仅更新 Dashboard 静态资源，保留运行时 api 目录，未重启服务，不含多用户、原生客户端或网络改动。

Chrome 已登录实际会话验证：

| 状态 | 聊天区宽度 | 正文宽度 | 输入框宽度 |
| --- | ---: | ---: | ---: |
| v172 原限宽 | 1141px | 860px | 860px |
| v173 会话列表展开 | 1141px | 1086px | 1101px |
| v173 会话列表收起 | 1471px | 1416px | 1431px |
| v173 390px 手机会话详情 | 390px | 347px | 370px |

桌面正文左侧 20px、右侧 20px 加 15px 滚动条；输入框两侧各 20px，max-width 为 none。手机实际间距仍为 14px / 10px，页面 scrollWidth 为 390px，无横向溢出。验证后恢复桌面视口和原会话列表展开状态；未发送消息或修改会话内容。
