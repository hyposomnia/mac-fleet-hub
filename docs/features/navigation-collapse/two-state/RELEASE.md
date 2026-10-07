# v172 生产发布验证

2026-10-07，用户明确要求上线。静态源提交 `77b255b` 已推送到 main，归档从不可变 Git 对象生成。发布入口 https://fleet.hyposomnia.top:20443/。

发布范围仅 dashboard 静态文件，不含多用户开发分支的服务端、原生客户端、认证、网络或设备迁移。发布前运行 `bash scripts/verify.sh`，退出码 0，Dashboard 250 项测试通过；完整输出见 verify.txt。

部署前旧版本 v171 的 105 个文件哈希全部匹配。备份后发布新版本，未重启服务。真实输出：

```text
backup=/var/backups/mac-fleet-hub/dashboard-before-v172-20261007T020244Z.tgz
static_sha256_matches=105
runtime_nodes_present
nginx=active
fleet-enroll=active
headscale=active
fleet-nodes.timer=active
const CACHE = 'fleet-shell-v172';
release=77b255b
```

未认证公网 `curl -I` 为 HTTP/2 302，正常跳转认证入口。

已登录 Chrome 实测加载 `style.css?v=172`：展开时侧栏宽 330px、position static、图标左侧填实；点击收起后宽 0px，入口图标左侧 fill none；再次点击恢复正常网格侧栏且左侧填实。页面无 backdrop 元素，展开后刷新保留状态。验证结束恢复用户原有展开状态，设备栏仍保持其原有收起偏好。

![展开时左侧实心](production-expanded-controls.png)

![收起时左侧空心](production-collapsed-controls.png)
