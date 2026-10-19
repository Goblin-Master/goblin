# 使用 DeepSeek 原生 tool_calls，不自定义 JSON 协议

Agent 的工具决策使用 OpenAI 兼容的 `tools` + `tool_calls`：请求中发送工具名称、描述和 JSON Schema，模型返回结构化的 `tool_calls`；客户端执行后以 `role=tool` 回填。没有工具调用时，assistant content 就是最终回答。`thought` 不作为独立协议字段，`reasoning_content` 存在时仅用于展示和 trace，不回传模型；普通模型则把调用工具前的简短 content 当作可见意图。

## Considered Options

- **自定义 `{thought, tool, final}` JSON**：思考更显式，但模型容易输出无效 JSON，解析失败会把主流程卡死；还需要自己处理转义、代码块和多工具。
- **双协议兼容**：更稳，但实现与测试面翻倍，MVP 收益不足。
- **原生 tool_calls**：协议由 provider 保证，Schema 直接随请求发送，错误边界清晰；缺点是 reasoning 是否可见取决于模型。

## Consequences

- `deepseek-chat` 不保证返回独立 reasoning；system prompt 要求调用工具前写一句意图，保证普通模式也有可观察的 Thought。
- 完整 chain-of-thought 不进入上下文，避免污染和泄漏。
- 工具参数仍是字符串，Registry 必须校验 JSON object，不能假设模型一定输出合法参数。
- 未来的 provider 差异收敛在 `internal/llm` adapter，不泄漏到 Agent。
