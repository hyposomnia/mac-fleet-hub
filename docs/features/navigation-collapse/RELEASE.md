# Web 导航 v161

发布范围：设备栏独立收起、会话列表图钉固定/取消固定与悬浮呼出，新建会话移至搜索框右侧。只发布生产兼容的 dashboard 静态资源；不包含多用户服务器、鉴权、Mac 客户端或网络配置。

提交前验证：`bash scripts/verify.sh`，真实完整输出见 `release-verify.txt`。覆盖 Go agent/enroll、dashboard JS、shell 工具及部署守卫。8 个新增侧栏测试覆盖独立偏好、浮层关闭、固定恢复、文件模式、移动断点、受限存储及栅格定位。

生产发布及浏览器证据完成后补充。
