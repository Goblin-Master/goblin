# Goblin

Goblin 是一个单用户、多 Session 的 CLI Agent；它的核心对象是持续对话、单次执行和可回放的证据。

## Language

**Session**：
一段可持久化、可恢复的独立对话，由 UUID 标识，拥有自己的消息、摘要、摘要游标和待办。
_Avoid_: conversation、聊天记录、窗口

**Summary Cursor**：
Session 消息序列中已被 Memory Summary 覆盖的前缀长度；值为 `n` 表示 `messages[0:n]` 已摘要，构建上下文时从 `messages[n:]` 开始；等于消息总数时没有可压缩内容。
_Avoid_: token 水位、消息 ID、时间戳

**Compaction**：
把较早消息折叠进 Memory Summary 的动作；不删除 Session 消息，只推进 Summary Cursor。
_Avoid_: 截断、清理、清空上下文

**Auto Compaction**：
Run 的每个 Step 前按 token 预算触发的 Compaction；允许 Fallback Summary，不阻断对话。
_Avoid_: 后台压缩、自动清理

**Manual Compaction**：
用户在 Window 内执行 `/compact` 触发的 Compaction；忽略预算阈值，折叠到 Summary Cursor 抵达消息末尾；失败时严格回退，不推进游标。
_Avoid_: force compact、强制截断

**Fallback Summary**：
摘要调用失败时由旧摘要和“更早的对话内容已省略”拼成的降级 Memory Summary。
_Avoid_: 空摘要、占位符

**Window**：
一个 CLI 进程与一个 Session 的绑定；窗口关闭不代表 Session 结束。
_Avoid_: tab、页面、进程

**Turn**：
一次用户输入到最终回答之间的完整交互，包含零次或多次工具调用。
_Avoid_: 轮次、对话轮

**Run**：
Engine 对一次 Turn 的单次执行，拥有独立 `trace_id`。
_Avoid_: 请求、job

**Trace ID**：
一次 Run 的唯一标识，写入该 Run 的每条结构化日志；不是分布式追踪系统的 trace。
_Avoid_: run_id、request_id

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

**Log Event**：
`goblin.log` 中的一行 JSON，记录一次 run、LLM 调用、工具调用、压缩或错误。
_Avoid_: trace、span、审计
