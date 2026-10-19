# AI Prompt 与问题解决记录

本文记录开发 Goblin 时使用的关键 Prompt、AI 协作方式，以及真实遇到的问题和最终处理。不是逐字聊天记录，重点是让读者理解“为什么这样做”。

## 1. 项目 Brief（给 AI 的核心约束）

```text
从零实现一个最小可用 Agent，不能依赖 LangGraph/OpenHands/OpenClaw/PI 完成主流程。
需要：接收输入 -> 判断直接回答/调用工具 -> 执行工具 -> 回填结果 -> 继续或结束。
至少三个工具，带名称、描述、参数 Schema 和注册机制。
需要 Session 隔离、追问、最大轮次、上下文压缩、异常处理、工具 trace。
使用 Go + Viper + DeepSeek，真实 API，不泄露密钥，提交到 GitHub。
```

## 2. 协作流程 Prompt

### 2.1 先规格，后实现

```text
先不要写代码。把需求拆成 spec：目标、非目标、架构、模块接口、数据模型、
主循环、工具、Session、Memory、异常、测试、验收。
注意不要过度设计。
```

结果：先产出 v1/v2 规格，再进入实现。

### 2.2 用 grill-with-docs 做设计访谈

```text
用 @grill-me-docs 重新过一遍 spec，我们明确一下，不然会混乱。
```

通过两轮 UI 选择确认：

- CLI 双进程，一个进程一个 Session；
- 原生 `tool_calls`；
- MySQL 唯一存储；
- 全部 UUID；
- todo 进 MySQL；
- 启动自动建表；
- fake 单测 + MySQL 集成；
- `--session` 严格恢复；
- Docker Desktop + `scripts/dev.ps1`。

### 2.3 TDD / 垂直切片

```text
按可运行垂直切片实现，每个阶段先测试再补实现；
每个阶段单独提交，不要攒一个大提交。
```

实际提交顺序：

1. 配置与 `.env`；
2. DeepSeek 客户端；
3. 工具与 Registry；
4. 会话存储；
5. MySQL schema 与 Docker；
6. MySQL Session；
7. MySQL todo；
8. 上下文压缩；
9. trace；
10. Agent 主循环；
11. CLI。

## 3. Agent System Prompt（实现中的版本）

```text
你是 Goblin，一个简洁、诚实的中文 CLI Agent。

当前时间：<runtime>

规则：
1. 需要计算、检索资料、读取项目文档或管理待办时，优先调用工具，不要编造工具结果。
2. 调用工具前，用一句简短中文说明你要做什么。
3. 工具报错时先阅读错误；不要用完全相同的参数重复调用超过两次。
4. 最终回答基于工具结果；mock 数据要明确标注。
5. 回答保持简洁；没有工具需要时直接回答。
```

设计取舍：

- 不要求模型输出完整 chain-of-thought；
- `reasoning_content` 只展示/trace，不回传；
- 普通模型用“调用前一句意图”提供可观察的 Thought。

## 4. 关键问题与解决

### 4.1 文件存储与 MySQL 的冲突

**问题**：初版 spec 选 JSON 文件，后来用户要求 MySQL；如果同时保留 file 驱动，会形成两套持久化语义。

**处理**：

- MySQL 唯一运行时存储；
- 删除 JSON store 和 fileutil；
- 测试用 `Store` 接口 fake，MySQL 行为放 `integration` tag；
- 用 ADR 0001 记录该决定。

### 4.2 Session ID 与严格恢复

**问题**：可读 ID 方便，但用户明确要求 Google UUID；UUID 输错时静默新建会造成空会话。

**处理**：

- `uuid.NewString()` 生成；
- `ParseID` 校验并规范化；
- `--session` 必须存在，`--new` 才新建；
- CLI 启动打印 UUID，`goblin sessions` 可找回。

### 4.3 Docker Desktop CLI 不在 PATH

**问题**：`docker --version` 在 Windows 失败，但 Docker Desktop 实际已安装并运行。

**处理**：

- 定位 `%LOCALAPPDATA%\Programs\DockerDesktop\resources\bin\docker.exe`；
- `scripts/dev.ps1` 自动加入临时 PATH、启动 compose、等待 3306；
- 不要求用户永久修改 PATH。

### 4.4 MySQL 拒绝零值时间

**问题**：集成测试插入消息时 `CreatedAt` 为零值，MySQL `DATETIME` 报 `1292 Incorrect datetime value`。

**处理**：

- `Save` 为 Session 和每条 Message 补当前时间；
- 测试覆盖无时间戳消息。

### 4.5 read_docs 内容被截断

**问题**：上下文层对单条工具结果限制 4KB，模型读不到 `docs/spec.md` 的 schema，重复读同一文档。

**处理**：

- 将工具结果上限提高到 16KB；
- 保留 64KB 文件上限；
- README/spec 明确“超大文档会截断”；
- 重新用真实 API 验证能读出三张表和 `messages` 字段。

### 4.6 摘要切点拆散工具调用

**问题**：如果按消息条数直接切，可能把 assistant tool_calls 和对应 tool result 分开。

**处理**：

- 切点向前对齐到 user message；
- 保留最近 `keep_recent` 条；
- 摘要失败时降级为“更早内容已省略”，不中断主流程。

### 4.7 原生 tool_calls vs 自定义 JSON

**问题**：自定义 `{thought, tool, final}` 看似更可控，但模型容易输出非法 JSON，解析失败会卡住主循环。

**处理**：

- 使用 DeepSeek 原生 tools/tool_calls；
- 参数非法时由 Registry 返回结构化错误并回喂；
- 用 ADR 0002 记录。

### 4.8 分阶段提交

**问题**：一次大提交难以 review，也不符合用户要求。

**处理**：

- 每个垂直切片单独 commit；
- 文档、schema、Session、todo、上下文、Agent、CLI 分开；
- 提交前跑对应包测试。

## 5. 密钥与安全

- 所有真实 key 只写本地 `.env`；
- `.env`、`config.yaml`、`data/` 在 `.gitignore`；
- README 只展示占位符；
- trace 不记录 Authorization header；
- 集成测试从 `.env` 读取 key，缺失时 skip；
- 提交前用 `git grep -i "sk-"` 与 `git status` 检查。

## 6. 复盘

做得好的：

- 先写 spec 和 ADR，再实现；
- 用 fake 测试覆盖主循环，用真实 MySQL/DeepSeek 做集成验收；
- 工具注册、Session store、LLM client 三个接缝清晰；
- 真实 API 工具闭环与追问均已跑通。

可以改进的：

- v1 过早选择了 JSON，后来整体切 MySQL，浪费了一次实现；
- `read_docs` 最初没有考虑上下文截断，导致真实模型读不到 schema；
- CLI 的 Ctrl-C 对阻塞 stdin 的即时打断仍有限；
- 消息整段替换在超长会话下不是最优。
