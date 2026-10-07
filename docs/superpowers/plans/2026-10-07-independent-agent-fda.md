# 独立 Fleet Agent 实现计划

用户已要求直接整改。只改源码与隔离验证，不覆盖当前安装、系统权限或生产网络。

1. 修改 `RuntimeTests.swift` 先验证启动路径不在 Hub 内；运行 `swift test --filter RuntimeTests` 观察失败，再拆分 `RuntimeLayout` 的发行载荷与实际后台路径。固定运行位置为 `~/.macfleet/desktop/runtime/Fleet Agent.app`。
2. 新建 `BackgroundRuntimeInstaller.swift` 与事务测试：验证载荷及拷贝、阻止并发部署/符号链接、空闲守卫前不替换、健康失败恢复旧包及启动定义；使用临时目录和异步适配器，不操作真实 launchd。
3. `NativeManagement.swift` 安装独立后台，首次启动与旧嵌套迁移走同一事务；完整应用更新恢复同步后台版本，解除/重启/卸载只管理 Fleet 自己的运行副本；状态请求复用私有控制端点。
4. 登录启动使用单独的无磁盘读取 launcher 身份，不再注册真正的 agent 为 Hub 的嵌套登录程序。新增 launcher 测试并更新打包及唯一签名脚本；后台单独公证 Accepted 和 stapler 校验后才签名主应用。
5. 更新设置页与 Web，定位实际后台；隔离 UAT 从载荷复制独立后台并运行真实二进制，确认保存/重启、版本一致与运行组件。不把此结果当作 FDA 真机成功。
6. 运行聚焦回归、`bash scripts/verify.sh`、Universal 开发构建和实际独立后台 UAT，保存日志并更新验证记录。正式签名部署与 TCC 实际归属/受保护访问另行取得证据。
