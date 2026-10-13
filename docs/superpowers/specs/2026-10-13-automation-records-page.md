# 自动化消息记录独立页面

消息记录从自动化弹窗中的文字链接打开浏览器新窗口，沿用网关 Authelia 登录态和配色。

| 需求 | 可执行验收 | 测试 / 实现 |
|---|---|---|
| REC-1 | 主弹窗只加载密钥；消息记录链接打开独立浏览器窗口，不创建会话内 Tab | Dashboard 入口检查、Chrome / index.html、app.js |
| REC-2 | 默认每页 25 条，最多 100 条；按创建时间及消息 ID 倒序游标翻页，同时间记录和翻页期间新增记录不造成重复 | message_records_test.go / message_records.go |
| REC-3 | 所有管理记录的结构化字段均可筛选，多条件取交集；字符串支持精确/包含，日期和数字支持范围，缺失值可筛选；正文支持关键词搜索 | message_records_test.go / 字段注册表、automation-records.js |
| REC-4 | 列表只返回摘要；展开时单独读取完整记录，不泄露访问密钥原文、摘要或回调签名密钥；不存在的记录返回 404 | message_records_test.go / 现有受保护 message-records 端点 |
| REC-5 | 未知字段、非法操作/值、页大小或游标返回 400；切换筛选后不能沿用旧游标 | message_records_test.go / 请求解析 |
| REC-6 | 筛选通过提交按钮请求；翻页只展示当前页，无轮询或自动全量加载；新请求取消旧请求，旧响应不能覆盖新结果，失败可重试 | automation-records.test.mjs、Chrome / 分页请求控制器 |

管理端点保持 `/api/automation/message-records`，不改变公网 Bearer API、Mac 协议或持久化格式。
新增 `view=summary`、`cursor`、`filters`（JSON 条件数组）、`q`、`include_filters=1`；完整详情使用同端点 `detail=<message_id>`。
字段、类型和允许的操作由服务端 `filter_fields` 描述，前端根据描述生成额外条件。筛选先于分页，游标绑定筛选条件；时间按 RFC3339 传输，本地日期输入转 UTC。
分页只复制当前页记录，返回 `next_cursor`、`has_more`；当前页摘要不含用户正文或 AI 回复，详情在本页缓存，不自动刷新。

交付：全量 verify → 聚焦提交/推送 → 网关 Go 服务及 Web 静态发布 → Chrome 线上验收。替换前备份网关二进制和静态目录；失败恢复备份。网关服务重启会从原 JSON 状态恢复任务，Mac Agent 不参与此变更。
