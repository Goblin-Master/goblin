# 只保留一份结构化日志

Goblin 原先同时写 `goblin.log` 和 `trace.jsonl`：前者几乎没有内容，后者才是真正的运行事件流。两套文件让“日志”和“trace”语义重复，也让排障时需要同时看两个地方。我们决定只保留 `data/logs/goblin.log`，每行一个 JSON 事件，事件带 `session_id`、`trace_id`、`step`、`tool_call_id` 等关联字段。

## Considered Options

- **保留 `trace.jsonl` + 普通日志**：职责看似分离，但实际重复，且 `goblin.log` 长期为空。
- **纯文本日志**：人读方便，但无法稳定按字段过滤工具、耗时和 ID。
- **OpenTelemetry / 独立 trace 后端**：对单机 CLI MVP 过重，需要 collector、导出器和额外依赖。

## Consequences

- `internal/trace` 删除，改为 `internal/logging`；Agent 使用 `log/slog` 写事件。
- `run_id` 统一改名为 `trace_id`；一次用户输入对应一个 `trace_id`，`session_id` 跨输入不变。
- 默认控制台保持安静；`--verbose` 打印 `trace_id` 与工具摘要；`/last` 查看上次 Run。
- 日志级别、字段名一旦变化，README、spec、testing 和 `check-docs.ps1` 必须同步。
- 不引入日志轮转；MVP 使用单文件追加，后续需要时再加。
