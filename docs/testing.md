# 测试与验收记录

- 日期：2026-10-20
- 环境：Windows / Go 1.25.0 / Docker Desktop 29.9.1 / MySQL 8.4
- 模型：DeepSeek `deepseek-chat`
- 结论：单测、race、vet、MySQL 集成、真实 DeepSeek 工具闭环均通过

## 1. 测试接缝

| 接缝 | 覆盖内容 | 使用替身 |
|---|---|---|
| `llm.ChatClient` | 主循环、工具回填、追问、max_steps | fake LLM |
| `session.Store` | Session 生命周期、消息隔离 | fake / MySQL integration |
| `tools.Tool` + `Registry` | Schema、参数校验、panic、超时 | fake tool |
| `memory` + `ChatClient` | 预算、切点、摘要失败降级 | fake summarizer |
| `logging` | JSONL 字段、`trace_id`、预览 | 临时文件 |
| DeepSeek HTTP | 请求格式、tool_calls、重试、错误 | `httptest.Server` |
| 真实 DeepSeek | 计算、追问、read_docs | 真实 API |
| MySQL | 事务、JSON 列、级联、UUID | 真实容器 |

## 2. 默认自动化测试

不需要 Docker，也不需要 API key：

```powershell
go test ./... -timeout 90s
go test -race ./... -timeout 180s
go vet ./...
```

结果：

| 命令 | 结果 |
|---|---|
| `go test ./...` | PASS |
| `go test -race ./...` | PASS |
| `go vet ./...` | PASS |

覆盖的核心行为：

- calculator：四则、括号、幂、取模、除零、非法字符；
- search：命中、空结果、limit；
- read_docs：list/read、README、路径穿越、绝对路径、目录外文件、符号链接逃逸、大小上限；
- Registry：重复注册、非法 Schema、未知工具、panic、超时、参数校验；
- memory：token 估算、摘要注入、切点对齐、失败降级、工具结果截断；
- Agent：直接回答、单/多工具、工具错误回喂、未知工具、max_steps、追问、缺失 Session、非法 UUID；
- parser：纯回答、tool_calls、reasoning、空响应；
- config：默认值、环境变量、`.env` 优先级、非法值；
- CLI：Cobra 帮助、无参数帮助、未知命令、冲突 flag；
- logging：JSONL 行数、`ts` / `level` / `event` / `session_id` / `trace_id` 字段、预览。

## 3. MySQL 集成测试

```powershell
.\scripts\dev.ps1 up
$env:GOBLIN_TEST_MYSQL_DSN = "goblin:123456@tcp(127.0.0.1:3306)/goblin?parseTime=true&charset=utf8mb4&loc=Local"
go test -tags=integration ./internal/session ./internal/tools -v -timeout 60s
```

用例：

| 用例 | 断言 |
|---|---|
| `TestMySQLStoreRoundTrip` | Create / Save / Load / List / Exists；summary、tool_calls JSON、tool_call_id |
| `TestMySQLStoreIsolation` | 两个 Session 的 messages 不串 |
| `TestMySQLStoreMissingAndInvalid` | 缺失 Session / 非 UUID 拒绝 |
| `TestMySQLStoreJSONToolCalls` | JSON 列往返 |
| `TestMySQLTodoStoreLifecycle` | add / list / complete / remove / 未知 ID |
| `TestMySQLTodoStoreIsolation` | 两个 Session 的 todos 不串 |
| `TestMySQLTodoStoreRequiresSession` | 无 Session 时 add 返回不存在；非法 UUID 拒绝 |

结果：全部 PASS。

覆盖率（含 integration）：

| 包 | 覆盖率 |
|---|---|
| `internal/agent` | 70.0% |
| `internal/memory` | 76.9% |
| `internal/session` | 78.2% |
| `internal/tools` | 83.7% |

核心包达到 70% 门槛；CLI 主要为交互编排，覆盖较低，由手工验收补足。

## 4. 真实 DeepSeek 集成测试

```powershell
go test -tags=integration ./internal/agent -run TestRealDeepSeekToolLoop -v -timeout 180s
```

测试内容：

1. “请用计算器计算 (128+72)*3” -> 必须调用 `calculator`，最终包含 `600`；
2. “再加 100” -> 同一 Session 的追问，最终包含 `700`；
3. “读 README.md，告诉我项目使用什么数据库” -> 必须调用 `read_docs`，最终包含 `MySQL`。

结果：PASS（约 4 秒）。

真实 API 不输出 key；`.env` 只存在于本地。

## 5. 手工 CLI 验收

### 5.1 工具闭环

```powershell
'请只用 calculator 工具计算 (128+72)*3，然后告诉我结果。' |
  go run ./cmd/goblin chat --verbose
```

实际输出：

```text
Session: 9ab9c004-0d15-4944-9fc6-c35b174fab2d
Agent> (128+72)*3 = 600
  → calculator({"expression": "(128+72)*3"}) 0ms ok
```

### 5.2 追问

```powershell
go run ./cmd/goblin run --session 9ab9c004-0d15-4944-9fc6-c35b174fab2d --verbose "那再加 100 呢？直接给结果。"
```

输出 `700`，说明上一轮计算与问题被带入上下文。

### 5.3 read_docs + todo

```powershell
go run ./cmd/goblin run --session 9ab9c004-0d15-4944-9fc6-c35b174fab2d --verbose `
  "读一下 docs/spec.md 里 MySQL 的三张表，然后用 todo 记一条：补集成测试。"
```

实际调用：

```text
→ read_docs({"action":"read","path":"docs/spec.md"})
→ todo({"action":"add","text":"补集成测试"})
→ read_docs({"action":"read","path":"docs/spec.md"})
```

待办写入 MySQL；三张表回答为 `sessions`、`messages`、`todos`。早期 4KB 截断导致读不到 schema，已把上下文中的工具结果上限调整为 16KB，并重新验证通过。

### 5.4 两个 Session 隔离

```powershell
'请用 todo 记录：窗口二的待办。不要调用其他工具。' | go run ./cmd/goblin chat --verbose
go run ./cmd/goblin sessions
```

实际输出包含两个 Session：

```text
70a7473f-2af6-4f7d-9d70-2613965b50b5 ... 窗口二的待办
9ab9c004-0d15-4944-9fc6-c35b174fab2d ... 计算与 read_docs 会话
```

两个 Session 的 todo 分别从 `t1` 开始，互不可见。

### 5.5 严格恢复

```powershell
go run ./cmd/goblin chat --session 00000000-0000-4000-8000-000000000000
```

实际输出：

```text
错误: 会话 00000000-0000-4000-8000-000000000000 不存在；用 goblin sessions 查看，或运行 goblin chat 新建
```

退出码 1，没有静默新建。

### 5.6 上下文压缩

用低预算环境变量对同一 Session 触发压缩：

```powershell
$env:GOBLIN_AGENT_MAX_CONTEXT_TOKENS='1200'
$env:GOBLIN_AGENT_RESERVE_OUTPUT_TOKENS='200'
$env:GOBLIN_AGENT_COMPACT_RATIO='0.5'
go run ./cmd/goblin run --session <uuid> "用一句话总结我们之前聊过的内容。"
```

`goblin.log` 实际出现：

```json
{"ts":"...","level":"INFO","event":"context.compact","session_id":"...","trace_id":"...","tokens_before":19809,"tokens_after":308,"summary_up_to":17}
```

再次追问“我刚才提到的三件事分别是什么？”，回答仍能复述 calculator、read_docs、todo 三件事，说明 summary 已被召回。

## 6. 日志验收

`data/logs/goblin.log` 是唯一结构化日志文件，实际包含：

- `run.start`
- `llm.request` / `llm.response`（usage、request_id、duration）
- `tool.call` / `tool.result`（参数/结果预览、耗时、ok）
- `run.end`

示例：

```json
{"ts":"...","level":"INFO","event":"llm.request","session_id":"...","trace_id":"...","step":2,"messages":4,"tools":4}
{"ts":"...","level":"INFO","event":"tool.result","session_id":"...","trace_id":"...","step":2,"tool_call_id":"call_1","duration_ms":139,"ok":true,"tool":"todo"}
{"ts":"...","level":"INFO","event":"run.end","session_id":"...","trace_id":"...","ok":true,"steps":2,"tool_calls":1}
```

`data/logs/trace.jsonl` 已移除；日志不记录请求头与 key。

### 6.1 `/last` 命令

```powershell
@('用 calculator 计算 6*7，只回答数字。','/last','/quit') |
  go run ./cmd/goblin chat --verbose
```

实际输出包含同一 `trace_id` 和计算器调用，说明 `/last` 读取的是上次 Run 的 `Reply`，不是日志文件。

## 7. 数据库验收

```powershell
.\scripts\dev.ps1 status
```

结果：

```text
goblin-mysql  mysql:8.4  Up (healthy)  0.0.0.0:3306->3306/tcp
3306 reachable: True
```

启动时 `internal/db/schema.sql` 幂等建表；`sessions`、`messages`、`todos` 三张表可用。

## 8. 已知未覆盖

- 同 Session 跨进程并发写；
- 数千条消息下的整段替换性能；
- 摘要压缩的真实 API 长对话压力测试（单测已覆盖逻辑）；
- Windows 控制台 Ctrl-C 对阻塞 stdin 的即时打断；
- 超大文档分页读取（当前 64KB 上限，发送前 16KB 截断）。

## 9. 文档检查

```powershell
.\scripts\check-docs.ps1
```

结果：PASS。检查项：

- README、GLOSSARY、docs 下所有 Markdown 的相对链接可解析；
- 不再出现旧的 MySQL 开发密码、标准库 flag 命令行、已删除的 JSON Store 等过期术语；
- `.env` 存在时确认被 git 忽略；
- 文档索引 `docs/README.md` 与维护规则已更新。
