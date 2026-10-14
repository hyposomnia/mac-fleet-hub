# DeepSeek 模型目录

Fleet 的 DeepSeek 模型菜单读取当前 DSH host 的 `session/modelCatalog`，覆盖所有提供商和模型，不以会话历史中的单个模型代替目录。目录身份由提供商和模型共同确定。

| 需求 | 行为 | 验证 / 实现 |
|---|---|---|
| DSM-1 | 新建和恢复会话均返回完整目录、显示名称、提供商、推理档位及默认值；同名模型的不同提供商保留独立选项 | dsh_models_test.go / dsh_models.go、dsh_chat.go |
| DSM-2 | 恢复优先显示会话 next 选择；历史消息仍使用 lastUsed 的真实模型，不将选项身份写入模型使用记录 | Go 与 chat_model.test.mjs / dsh_models.go、app.js |
| DSM-3 | 用户改选后，在投递消息前调用 session/selectModel，准确传递 provider/model/reasoningEffort；未知选择、目录失败或 host 拒绝均阻止该次投递 | dsh_models_test.go / dsh_models.go、dsh_chat.go |
| DSM-4 | 目录失败不阻止读取会话正文，明确显示读取失败并禁用不可用的模型选择；部分提供商失败保留成功选项并提示失败提供商 | Go、JS 测试 / dsh_models.go、app.js |

`session/modelCatalog` 参数为 `{}`，返回 `{default,groups,failures}`；每组包含 id/name/models，模型可携带 reasoning.efforts/defaultEffort。`session/selectModel` 参数为 `{request:{sessionId,provider,model,reasoningEffort?}}`，返回 `{selected}`。契约已对照已安装 DSH 的生成 schema 与真实只读目录响应。选项 value 使用分别转义的 provider/model；发送给 DSH 的模型 ID 保持原值。

不启动第二个 DSH host。Mac 产物只通过唯一签名构建机的正式脚本发布、签名、公证和 rollout；Web 单独发布。回滚恢复上一个已公证 agent 和静态资源，不覆盖 DSH 设置、凭据或会话。
