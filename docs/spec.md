# Goblin：最小可用 Agent 规格说明（v3）

- 状态：已实现并验收（v3.2，2026-10-20）
- 目标仓库：<https://github.com/Goblin-Master/goblin>
- 语言：Go 1.25
- 模型：DeepSeek `deepseek-chat`（OpenAI 兼容 Chat Completions）
- 运行形态：CLI，一个进程绑定一个 Session
- 持久化：MySQL 8.4（唯一存储后端）
- 会话 ID：UUID v4（`github.com/google/uuid`）
- 工具：`calculator`、`search`（mock）、`read_docs`、`todo`
- 配套文档：[GLOSSARY.md](../GLOSSARY.md)、[ADR 0001](adr/0001-mysql-only-persistence.md)、[ADR 0002](adr/0002-native-tool-calls.md)

> 本版取代 v2。v2 中“每会话 JSON 文件”“file/mysql 双驱动”“可读 session id”的决定全部废止。

---

## 1. 已确认决策

| # | 决策 | 结论 |
|---|---|---|
| 1 | 交互形态 | CLI；一个进程一个窗口，窗口通过 Session 隔离 |
| 2 | 存储 | MySQL 8.4 唯一后端；不提供 file 运行时适配器 |
| 3 | 会话 ID | 新建一律 UUID v4；`--session` 只接受 UUID |
| 4 | 打开语义 | `--session` 必须已存在；不存在的 UUID 报错，不静默新建 |
| 5 | 新建语义 | 不带 `--session`、或使用 `--new` 时新建并打印 UUID |
| 6 | 工具协议 | DeepSeek 原生 `tool_calls`；兼容 `reasoning_content`，不伪造 CoT |
| 7 | 工具集合 | calculator / search / read_docs / todo |
| 8 | 待办存储 | 与 sessions/messages 同一个 MySQL schema |
| 9 | Schema | 启动时执行内嵌 `schema.sql` 的 `CREATE TABLE IF NOT EXISTS` |
| 10 | 上下文压缩 | 超预算时 LLM 摘要，失败降级为截断 |
| 11 | 测试 | 单测用接口 fake；MySQL 与真实 API 走 `integration` tag |
| 12 | Docker | Docker Desktop + `docker compose`；提供 `scripts/dev.ps1` |
| 13 | 提交 | 按可运行垂直切片分阶段提交，不攒大提交 |

---

## 2. 目标与非目标

### 2.1 目标

1. 从零实现 Agent Runtime：输入、决策、工具执行、结果回填、继续/结束。
2. 用工具注册表向 LLM 暴露名称、描述、JSON Schema。
3. 解析 `tool_calls`、可见意图、最终回答；工具错误可回喂。
4. Session 可创建、恢复、列出；两个窗口互不影响。
5. 支持纯对话追问、带工具追问、跨重启继续。
6. 有最大轮次、上下文预算、摘要压缩。
7. 有异常处理、工具调用 trace、单元/集成测试。
8. 用真实 DeepSeek API 完成一次工具闭环。
9. 代码、README、spec、测试、AI 问题记录可交付并推送到 GitHub。

### 2.2 非目标

- 不做 Web UI、桌面端、多用户鉴权、服务端部署。
- 不做向量检索、Embedding、长期用户画像。
- 不做多 Agent 协作、计划树、MCP。
- 不做流式输出、工具并行执行。
- 不做跨进程写同一 Session 的强并发控制。
- 不使用 LangGraph、OpenHands、OpenClaw、PI 等 Agent 框架。
- 不把 MySQL 包装成通用 Repository 框架；只实现本项目的三张表。
- 不支持 file 驱动、SQLite、内存持久化作为运行时后端。

---

## 3. 典型场景

### 3.1 两个窗口

```bash
# 窗口 1：新建，输出 UUID
goblin chat
# Session: 7f2c3a5e-...

# 窗口 2：再新建一个
goblin chat
# Session: b91e4d70-...

# 恢复窗口 1
goblin chat --session 7f2c3a5e-...
```

窗口 1 与窗口 2 的 messages、summary、todos 均按 Session ID 隔离。关闭进程后重新用同一 UUID 打开即可继续。

### 3.2 追问

```text
你> docs/spec.md 里上下文压缩的默认阈值是多少？
Agent> 我先读文档……（read_docs）
你> 那 keep_recent 呢？
Agent> keep_recent 默认 12；如果上一轮结果已被压缩，我会重新读文档确认。
```

### 3.3 长会话

超过上下文预算时，较早的 messages 被压成 summary；最近 messages 原样保留；MySQL 中的完整历史不删除。

---

## 4. 架构

```text
┌──────────────────────────────────────────────────────────────┐
│ cmd/goblin                                                   │
│ CLI：chat / run / sessions；UUID 生成；信号处理               │
└───────────────────────────┬──────────────────────────────────┘
                            │ Engine.Run(ctx, sessionID, input)
┌───────────────────────────▼──────────────────────────────────┐
│ internal/agent                                               │
│ Engine：主循环、决策解析、工具编排、保存时机                   │
│ parser：Response -> Decision{Thought, ToolCalls, Reply}      │
│ prompt：system prompt                                        │
└───────┬───────────────────┬───────────────────┬──────────────┘
        │                   │                   │
┌───────▼────────┐  ┌───────▼────────┐  ┌───────▼──────────────┐
│ internal/llm   │  │ internal/tools │  │ internal/session     │
│ DeepSeek HTTP  │  │ Registry + 4 工具│ │ MySQL Store + Model  │
└───────┬────────┘  └───────┬────────┘  └───────┬──────────────┘
        │                   │                   │
        │           ┌───────▼────────┐  ┌───────▼──────────────┐
        │           │ calculator     │  │ MySQL 8.4            │
        │           │ search (mock)  │  │ sessions/messages/   │
        │           │ read_docs      │  │ todos                │
        │           │ todo           │  └──────────────────────┘
        │           └───────┬────────┘
        │                   │
        │           ┌───────▼────────┐
        │           │ Todo MySQL Store│
        │           └────────────────┘
        │
┌───────▼───────────────────┐  ┌────────────────────────────┐
│ internal/contextmgr       │  │ internal/trace             │
│ token 估算 / 摘要 / 裁剪   │  │ JSONL trace + 控制台输出    │
└───────────────────────────┘  └────────────────────────────┘
```

### 4.1 目录

```text
goblin/
├── cmd/goblin/main.go
├── internal/
│   ├── config/            # Viper、.env、校验
│   ├── db/                # 打开 MySQL、连接池、执行内嵌 schema.sql
│   ├── llm/               # ChatClient 接口、DeepSeek adapter
│   ├── agent/             # Engine、parser、prompt
│   ├── tools/             # Tool、Registry、四工具、Todo MySQL Store
│   ├── session/           # 模型、MySQL Session Store
│   ├── contextmgr/        # token 估算、Build、MaybeCompact
│   ├── trace/             # trace 事件、JSONL Recorder
│   └── cli/               # Cobra 命令树、REPL、one-shot
├── docker-compose.yml     # MySQL 8.4
├── scripts/dev.ps1        # Docker Desktop 辅助脚本
├── config.example.yaml
├── .env.example
├── GLOSSARY.md
├── docs/
│   ├── README.md              # 文档索引与维护规则
│   ├── spec.md
│   ├── testing.md
│   ├── ai-prompts.md
│   └── adr/
└── README.md
```

依赖：Cobra（CLI 命令树）、Viper（配置）、`google/uuid`、`go-sql-driver/mysql`。其他用标准库。

### 4.2 深模块接口

```go
// Agent 高层入口：CLI 不需要知道循环、压缩、工具执行细节。
type Engine struct { /* ... */ }
func (e *Engine) Run(ctx context.Context, sessionID, input string) (Reply, error)

// LLM 接缝：真实 DeepSeek 与测试 fake 都实现它。
type ChatClient interface {
    Chat(ctx context.Context, req ChatRequest) (ChatResponse, error)
}

// 持久化接缝：MySQL 是唯一运行时 adapter；测试可用 fake。
type Store interface {
    Create(id string) (*Session, error)
    Load(id string) (*Session, error)
    Save(*Session) error
    List() ([]Meta, error)
    Exists(id string) bool
}

// 工具接缝。
type Tool interface {
    Definition() Definition
    Execute(ctx context.Context, call Call) (Result, error)
}

// 上下文接缝。
func (m *Manager) Build(s *Session, system string) []llm.Message
func (m *Manager) MaybeCompact(ctx context.Context, s *Session, system string) (bool, error)
```

`Store` 的 MySQL 实现隐藏连接、事务、JSON 列和 schema；Agent 不直接执行 SQL。

---

## 5. 领域模型

完整术语见 [GLOSSARY.md](../GLOSSARY.md)。核心区分：

- **Session**：一次可持续的对话容器，ID 是 UUID；窗口关闭后仍存在。
- **Window**：一个 CLI 进程与一个 Session 的绑定。
- **Turn**：一次用户输入到最终回答的完整过程。
- **Run**：Engine 的一次执行，对应一个 Turn；有独立 `run_id`。
- **Step**：一次 LLM 请求；一个 Run 最多 `max_steps` 个 Step。
- **Tool Call / Tool Result**：模型请求执行工具，以及执行后回填的结构化结果。
- **Memory Summary**：较早 messages 的压缩表示，放在 system context 中。
- **Todo**：属于某个 Session 的结构化待办，不放进 system prompt。

---

## 6. MySQL 设计

### 6.1 运行环境

- 镜像：`mysql:8.4`
- 容器名：`goblin-mysql`
- 端口：`127.0.0.1:3306`
- 数据库：`goblin`
- 开发账号：`goblin / 123456`（root 也是同一密码；仅本地）
- 数据卷：`goblin_mysql_data`
- 字符集：`utf8mb4`，排序规则 `utf8mb4_unicode_ci`
- DSN 必须带 `parseTime=true`；应用会补 `charset=utf8mb4` 和 `loc=Local`

Docker Desktop 的 `docker.exe` 可能不在 PATH；`scripts/dev.ps1` 负责定位并调用。Windows 侧已确认 Docker Desktop 29.9.1 可用。

### 6.2 Schema

`internal/db/schema.sql` 在启动时执行，全部使用 `CREATE TABLE IF NOT EXISTS`。

```sql
CREATE TABLE IF NOT EXISTS sessions (
  id CHAR(36) NOT NULL PRIMARY KEY,
  title VARCHAR(255) NOT NULL DEFAULT '',
  summary MEDIUMTEXT NOT NULL,
  summary_up_to INT NOT NULL DEFAULT 0,
  created_at DATETIME(6) NOT NULL,
  updated_at DATETIME(6) NOT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS messages (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
  session_id CHAR(36) NOT NULL,
  seq INT NOT NULL,
  role VARCHAR(16) NOT NULL,
  content MEDIUMTEXT NOT NULL,
  reasoning MEDIUMTEXT NOT NULL,
  tool_calls JSON NULL,
  tool_call_id VARCHAR(128) NOT NULL DEFAULT '',
  created_at DATETIME(6) NOT NULL,
  UNIQUE KEY uk_messages_session_seq (session_id, seq),
  CONSTRAINT fk_messages_session FOREIGN KEY (session_id)
    REFERENCES sessions(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS todos (
  session_id CHAR(36) NOT NULL,
  todo_id VARCHAR(32) NOT NULL,
  text VARCHAR(1000) NOT NULL,
  done BOOLEAN NOT NULL DEFAULT FALSE,
  created_at DATETIME(6) NOT NULL,
  PRIMARY KEY (session_id, todo_id),
  CONSTRAINT fk_todos_session FOREIGN KEY (session_id)
    REFERENCES sessions(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
```

### 6.3 写入语义

- `Save` 在一个事务内 `UPSERT sessions` + `DELETE messages WHERE session_id` + 按 `seq` 重插全部 messages。
- 选择整段替换而不是增量追加：MVP 数据量小，换来实现简单、不会出现半截消息；后续可优化为 append-only。
- `todos` 的 add/complete/remove 在事务内完成，并校验 Session 存在。
- `Load` 读 Session 与按 `seq` 排序的 messages；`tool_calls` 从 JSON 列反序列化。
- `List` 返回 `id/title/updated_at/message_count`，按 `updated_at DESC`。
- Session 删除通过外键级联；本版不提供 CLI 删除命令。
- 连接池：MaxOpen 8、MaxIdle 4、ConnMaxLifetime 30m。
- 启动时 Ping，失败给出可操作的 DSN / Docker 提示。

### 6.4 Schema 演进

本版不引入迁移框架。`schema.sql` 只做首次建表；字段变更必须新增 `ALTER TABLE` 语句并保证幂等，禁止破坏性变更。后续如需要版本化，再引入 migration 工具。

---

## 7. Agent 主循环

一次 `Run`：

```text
1. 校验 UUID；Load Session；不存在返回错误（不自动创建）
2. 追加 user message；若标题为空，取前 30 个字符
3. for step = 1 .. max_steps:
   a. MaybeCompact：估算 Build 后 token，超阈值则摘要旧消息
   b. 调用 LLM；非最后一步带工具 Schema，最后一步不带工具
   c. parser -> Decision{Thought, ToolCalls, Reply}
   d. 无 tool_calls 且有 Reply：追加 assistant，Save，返回
   e. 有 tool_calls：追加 assistant(tool_calls + 可见意图)
      - 串行执行每个工具
      - 追加 role=tool 消息
      - 记录 trace
      - 进入下一 step
4. 收尾请求仍无有效回答：返回“达到最大轮次”，并保存已完成消息
```

约定：

- `max_steps` 默认 8，限制单次 Run 的 LLM 请求数；最后一步不允许工具调用。
- 工具错误作为 `role=tool` 结果回喂，不让 Go error 冒泡到 CLI。
- 同一轮多个 tool_calls 全部执行，按返回顺序。
- Save 在 Turn 结束时执行；工具执行过程中不写半成品 Session。
- 同一 Engine 内按 Session ID 串行化，防止同进程并发写。
- CLI 的 Ctrl-C 取消当前 Run，尽量保存已完成消息。

### 7.1 决策解析

```go
type Decision struct {
    Thought   string
    ToolCalls []llm.ToolCall
    Reply     string
}
```

- 有 `reasoning_content`：作为 Thought。
- 否则有 tool_calls：assistant content 作为“调用前意图”。
- 无 tool_calls：content 作为 Reply。
- 两者都空：返回明确错误。
- `arguments` 保留原始字符串；Registry/工具负责 JSON 校验。
- 不从普通文本正则提取 tool call，避免误判。

### 7.2 System Prompt

- 角色：Goblin，简洁、诚实的中文助手。
- 需要计算、检索、文档内容、待办时调用工具；不编造工具结果。
- 调用工具前用一句短话说明意图。
- 工具报错时先读错误，避免重复相同参数超过两次。
- 最终回答基于工具结果；mock 数据要标注。
- 当前时间、summary、工具说明由程序注入。

---

## 8. 工具

### 8.1 calculator

- Schema：`expression: string`（必填）。
- 手写 tokenizer + 递归下降；支持 `+ - * / % ^`、括号、小数、一元正负号。
- 禁止 eval；除零、非法字符、括号不匹配返回错误。
- 输出：`{"ok":true,"expression":"...","result":42}`。

### 8.2 search（mock）

- Schema：`query: string`（必填）、`limit: int`（1–5，默认 3）。
- 内置 10–15 条 mock 文档，匹配标题/摘要，标题权重更高。
- 无结果返回空数组；输出含 `"source":"mock"`。

### 8.3 read_docs

- Schema：`action: list|read`、`path: string`。
- 只读 `docs/` 和根目录 `README.md`；拒绝绝对路径、`..`、目录外文件、符号链接逃逸。
- 单文件上限 64KB；`list` 只列 `docs/*.md`。
- 返回 `{"ok":true,"path":"...","content":"..."}`。

### 8.4 todo

- Schema：`action: add|list|complete|remove`、`text`、`id`。
- 数据在 MySQL `todos` 表，按 `session_id` 隔离。
- `add` 生成 `t1`、`t2`…；500 字符上限。
- 输出操作后的 items；未知 ID 返回结构化错误。

### 8.5 Registry

- 注册时校验：名称唯一、描述非空、Schema 是 JSON object。
- LLM 只看到 Registry 暴露的定义。
- 未知工具/非法参数/panic/超时都转为 `IsError=true` 的 JSON 结果。
- 每个工具默认 10s 超时（可配置）。

---

## 9. Session 与 CLI

CLI 由 Cobra 组织为 `goblin` 根命令和 `chat` / `run` / `sessions` 子命令；`--config`、`--env-file` 是持久 flag。

### 9.1 命令

```bash
goblin chat                         # 新建 Session，打印 UUID 与模型
goblin chat --session <uuid>        # 严格恢复；不存在报错
goblin chat --new                   # 强制新建
goblin run --session <uuid> "..."   # one-shot；Session 必须存在
goblin sessions                     # 列出 id / title / updated_at / message_count
```

- 新建一律 `uuid.NewString()`；`--session` 用 `uuid.Parse` 校验。
- `goblin chat` 不带 `--session` 等价于新建。
- `--new` 与 `--session` 同时出现时报错，避免歧义。
- 标题取首条 user message 的前 30 个字符，不额外调用 LLM。

交互命令：

| 命令 | 行为 |
|---|---|
| `/help` | 命令列表 |
| `/new` | 新建并切换 Session |
| `/sessions` | 列出 Session |
| `/switch <uuid>` | 严格切换到已存在 Session |
| `/history [n]` | 最近 n 条消息（默认 10） |
| `/compact` | 手动压缩 |
| `/trace` | 上次 Run 的工具 trace |
| `/quit` / `/exit` | 退出 |

### 9.2 Session 文件？不，MySQL

- 不写 JSON Session 文件。
- `data/` 只放日志与 trace：`data/logs/goblin.log`、`data/logs/trace.jsonl`。
- Session 的完整历史只在 MySQL；日志不记录 API key 或完整消息正文。

---

## 10. Context 与 Memory

### 10.1 构建时机

每次 LLM 调用前（包括工具调用后的下一次 Step）重新构建：

1. System prompt（角色、规则、当前时间）；
2. Memory Summary（若有，附在 system 末尾）；
3. 最近 messages（原生 role 序列，含 assistant tool_calls / tool results）；
4. 当前 user input（已在 messages 末尾）。

不做向量召回，不把完整历史全塞进去。

### 10.2 三层

| 层 | 内容 | 位置 | 生命周期 |
|---|---|---|---|
| 工作记忆 | 最近 messages、当前工具结果 | LLM messages | 当前 Run/窗口 |
| Memory Summary | 被压缩的较早对话 | system 尾部；MySQL `sessions.summary` | 持久化 |
| 结构化状态 | todo | MySQL `todos`，需要时调用工具 | 持久化 |

### 10.3 预算与压缩

| 配置 | 默认 |
|---|---|
| max_steps | 8 |
| max_context_tokens | 32000 |
| reserve_output_tokens | 2048 |
| compact_ratio | 0.75 |
| keep_recent | 12 |
| summary_max_tokens | 800 |
| tool_timeout | 10s |

- 输入预算 = `max_context_tokens - reserve_output_tokens`。
- token 估算：CJK 约 1 token，ASCII 约 0.25 token，加消息固定开销；不追求精确。
- 超过 `输入预算 * compact_ratio` 时压缩。
- 压缩：旧 summary + `messages[summary_up_to:cutoff]` 交给 LLM，生成新 summary；`summary_up_to = cutoff`。
- 切点必须对齐到 user message，不能拆散 assistant(tool_calls) 与对应 tool 结果。
- 摘要失败：保留旧 summary，注入“更早内容已省略”，不中断。
- 单条工具结果超过 16KB 时截断后发送；MySQL 中保留完整内容，trace 只留 2KB 预览。
- `reasoning_content` 不回传模型；只用于展示/trace。

### 10.4 追问

- 纯对话：最近 messages 在窗口内。
- 带工具：上一轮 tool call/result 仍在；也可重新调用工具。
- 跨重启：MySQL 恢复 messages + summary。
- 跨 Session：不支持，todos 也隔离。

---

## 11. 配置

### 11.1 优先级

命令行 flag > 进程环境变量 > `.env` > `config.yaml` > 默认值。

### 11.2 `.env`

- 本地 `.env` 已被 gitignore；`.env.example` 提交。
- 启动时加载工作目录 `.env`（或 `--env-file`）。
- 支持 `KEY=VALUE`、空行、`#` 注释、单双引号；不执行命令、不做变量展开。
- 不覆盖已存在的进程环境变量。

### 11.3 示例

```yaml
llm:
  base_url: "https://api.deepseek.com"
  model: "deepseek-chat"
  api_key: ""
  timeout: 90s
  max_retries: 2
  temperature: 0.2

agent:
  max_steps: 8
  tool_timeout: 10s
  max_context_tokens: 32000
  reserve_output_tokens: 2048
  compact_ratio: 0.75
  keep_recent: 12
  summary_max_tokens: 800

mysql:
  dsn: "goblin:123456@tcp(127.0.0.1:3306)/goblin?parseTime=true&charset=utf8mb4&loc=Local"

logging:
  level: "info"
  dir: "./data/logs"
  verbose: false
```

环境变量：

- `DEEPSEEK_API_KEY`（推荐，`.env` 默认字段）
- `GOBLIN_LLM_API_KEY`（别名）
- `GOBLIN_LLM_BASE_URL`
- `GOBLIN_LLM_MODEL`
- `GOBLIN_MYSQL_DSN`
- `GOBLIN_LOGGING_DIR`

API key 解析：`GOBLIN_LLM_API_KEY` > `DEEPSEEK_API_KEY` > 配置文件；优先环境变量。

### 11.4 启动校验

- API key 为空：报错，不请求 LLM。
- base_url 非 http/https：报错。
- max_steps/keep_recent/ratio/summary 边界非法：报错。
- MySQL DSN 为空：报错。
- MySQL 连接失败：提示 `scripts/dev.ps1 up` 与 DSN。
- 配置错误不打印完整配置，避免密钥泄漏。

---

## 12. Docker 与本地开发

`docker-compose.yml`：

- MySQL 8.4，数据库 `goblin`，开发账号 `goblin/123456`（root 同密码）；
- 端口 `3306:3306`，命名卷持久化；
- healthcheck 使用 `mysqladmin ping`。

`scripts/dev.ps1`：

```powershell
# 自动定位 Docker Desktop 的 docker.exe
.\scripts\dev.ps1 up      # 启动 MySQL 并等待 3306 就绪
.\scripts\dev.ps1 status  # 容器与端口状态
.\scripts\dev.ps1 down    # 停止容器（保留卷）
.\scripts\dev.ps1 reset   # 停止并删除卷（危险，需确认）
```

脚本只在开发环境使用；应用本身不负责启动容器。README 给出一条完整命令：

```powershell
.\scripts\dev.ps1 up
go run ./cmd/goblin chat
```

---

## 13. 异常处理

| 场景 | 行为 |
|---|---|
| API key 缺失 | 启动失败，提示 `.env` |
| MySQL 未启动 | 启动失败，提示 `scripts/dev.ps1 up` |
| API 401/403 | 不重试，提示检查 key |
| API 429/5xx/网络 | 指数退避重试 2 次 |
| 空 content 且无 tool_calls | 重试一次，仍空则报错 |
| 工具参数非法/未知工具 | 结构化错误回喂模型 |
| 工具 panic/超时 | recover/超时结果回喂模型 |
| max_steps 用尽 | 最后一步无工具收尾；失败则明确报错 |
| Session 不存在 | 严格报错，建议 `goblin sessions` 或 `goblin chat` |
| MySQL 写失败 | 返回错误；事务回滚 |
| 摘要失败 | 截断降级，记录 warning |
| Ctrl-C | 取消当前 Run，尽量保存 |

---

## 14. Trace 与日志

- `log/slog`；控制台只输出可读信息。
- 文件：`<logging.dir>/goblin.log`。
- trace：`<logging.dir>/trace.jsonl`，每行一个 JSON。
- 事件：`run.start`、`llm.request`、`llm.response`、`context.compact`、`tool.call`、`tool.result`、`run.end`、`error`。
- 每条含 `ts`、`run_id`、`session_id`、`step`、`event`。
- 参数/结果预览最多 2KB；禁止记录 Authorization、API key、完整配置。
- `--verbose` 控制台打印 `→ tool(args) 12ms ok`。

---

## 15. 测试

### 15.1 默认单测（不需要 Docker）

- calculator、search、read_docs 的安全路径；
- Registry 注册/未知工具/panic/超时；
- parser：纯文本、多 tool_calls、reasoning、空响应；
- contextmgr：token 估算、压缩切点、摘要失败降级；
- Agent 循环：直接回答、工具闭环、多工具、工具错误、max_steps、追问、取消；
- 使用 `FakeStore` / `FakeLLM`，只实现接口，不引入 file 运行时。

### 15.2 MySQL 集成测试（`integration` tag）

```powershell
.\scripts\dev.ps1 up
$env:GOBLIN_TEST_MYSQL_DSN = "goblin:123456@tcp(127.0.0.1:3306)/goblin?parseTime=true&charset=utf8mb4&loc=Local"
go test -tags=integration ./... -run MySQL -v
```

覆盖：

- Create/Load/Save/List/Exists；
- 两个 Session 隔离；
- messages 顺序与 tool_calls JSON；
- summary 持久化；
- todos CRUD 与级联删除；
- 事务回滚（可用重复主键或注入错误）；
- UUID 校验。

### 15.3 真实 DeepSeek 集成测试（`integration` tag）

- 使用 `.env` 的 `DEEPSEEK_API_KEY`；
- 第一问 calculator，第二问追问计算，第三问 read_docs；
- 验证工具调用、最终回答、上下文延续；
- 不打印 key；缺少 key 时 skip。

### 15.4 手工验收

1. `scripts/dev.ps1 up`；
2. 新建两个 Session，分别记录待办；
3. 重启 CLI，严格恢复两个 UUID；
4. 追问、触发压缩、查看 trace；
5. 停止 MySQL，确认启动错误可读；
6. 错误 API key，确认 401 提示；
7. `git grep -i "sk-"` 与 `git status` 确认无密钥。

### 15.5 门槛

- `go test ./...` 通过；
- `go test -race ./...` 通过；
- `go vet ./...` 通过；
- MySQL 与真实 API 集成测试各跑通一次；
- 核心包覆盖率 ≥ 70%。

---

## 16. 交付与提交

交付：

1. Go 源码；
2. `README.md`：运行、Docker、配置、架构、Memory、测试；
3. `docs/spec.md`（本文件）、`GLOSSARY.md`、ADR；
4. `docs/testing.md`、`docs/ai-prompts.md`；
5. `config.example.yaml`、`.env.example`；
6. GitHub `main` 分支。

提交策略：按垂直切片提交。主要提交：

1. `chore: 初始化 Go 工程与 Viper 配置`
2. `feat(llm): 接入 DeepSeek Chat Completions`
3. `feat(tools): 实现工具注册表与四个工具`
4. `feat(session): 添加会话持久化与 UUID`
5. `docs: 重写 MySQL 版 Agent 规格`
6. `feat(db): 添加 MySQL schema 与 Docker 启动脚本`
7. `refactor(session): 改为 MySQL 唯一存储`
8. `refactor(todo): 待办迁移到 MySQL`
9. `feat(context): 添加上下文预算与摘要压缩`
10. `feat(trace): 添加 JSONL 执行追踪`
11. `feat(agent): 实现主循环与决策解析`
12. `feat(cli): 添加交互式 CLI 与日志`
13. `docs: 补 README、测试记录与 AI 问题记录`
14. `refactor(cli): 使用 Cobra 组织命令`
15. `chore(db): 统一 MySQL 开发密码`
16. `docs: 整理文档索引与维护规则`（本次）

---

## 17. 验收清单

- [x] 新建 Session 输出 UUID；`--session` 严格恢复。
- [x] 两个 Session 的 messages/summary/todos 完全隔离。
- [x] MySQL 启动时自动建表。
- [x] 无 file 驱动、无 JSON Session 文件。
- [x] 四个工具可由 LLM 自主选择。
- [x] 主循环包含工具回填、错误回喂、max_steps。
- [x] 追问与跨重启继续可用。
- [x] 超预算触发摘要；失败降级。
- [x] trace 记录每次工具调用。
- [x] `go test ./...`、`-race`、`vet` 通过。
- [x] MySQL 集成测试通过。
- [x] 真实 DeepSeek 工具闭环通过。
- [x] README 说明 Memory 召回与放置。
- [x] 无密钥，GitHub 已推送。

---

## 18. 工程默认值（不再作为开放问题）

1. **messages 写入**：MVP 使用事务内整段替换；数据量小时优先保证一致性，append-only 留作后续优化。
2. **MySQL 开发账号**：默认 `goblin/123456`，root 同密码；仅本地开发使用，不把生产凭据写进仓库。
3. **删除 Session**：本版不提供 CLI 删除命令；需要时直接执行 SQL，外键会级联清理。
4. **Docker PATH**：不要求用户改 PATH；`scripts/dev.ps1` 自动定位 Docker Desktop 的 `docker.exe`。

v3.2 的设计树已无未决分支。若这四项中的任何一项需要改变，再开新 ADR，不直接修改本规格。
