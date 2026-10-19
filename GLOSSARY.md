# Goblin

Goblin 是一个单用户、多 Session 的 CLI Agent；它的核心对象是持续对话、单次执行和可回放的证据。

## Language

**Session**：
一段可持久化、可恢复的独立对话，由 UUID 标识，拥有自己的消息、摘要和待办。
_Avoid_: conversation、聊天记录、窗口

**Window**：
一个 CLI 进程与一个 Session 的绑定；窗口关闭不代表 Session 结束。
_Avoid_: tab、页面、进程

**Turn**：
一次用户输入到最终回答之间的完整交互，包含零次或多次工具调用。
_Avoid_: 轮次、对话轮

**Run**：
Engine 对一次 Turn 的单次执行，拥有独立 `run_id` 和 trace。
_Avoid_: 请求、job

**Step**：
一个 Run 中的一次 LLM 请求；受 `max_steps` 限制。
_Avoid_: 轮、iteration

**Tool Call**：
模型基于工具 Schema 请求执行某个工具及其参数。
_Avoid_: function、action

**Tool Result**：
工具执行后回填给模型的 `role=tool` 结构化结果，包含成功或错误。
_Avoid_: observation、返回值

**Memory Summary**：
被压缩的较早消息的摘要，放在 system context 中，用于跨窗口和长对话保持连续性。
_Avoid_: 记忆、摘要缓存

**Todo**：
属于某个 Session 的结构化待办，只通过 `todo` 工具读写，不进入 system prompt。
_Avoid_: task、备忘

**Trace**：
一次 Run 中 LLM 请求、工具调用、压缩和错误的可回放记录，用于调试与测试。
_Avoid_: log、审计
