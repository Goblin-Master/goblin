# 用 Cobra 组织 CLI

Goblin 有 `chat`、`run`、`sessions` 三个子命令，以及公共的 `--config` / `--env-file` 和子命令自己的 `--session` / `--new` / `--verbose`。我们选择 Cobra 管理命令树、flag 继承、帮助和未知命令错误，而不是继续用标准库 `flag` 加手写 `switch args[0]`。

## Considered Options

- **标准库 `flag` + 手写分发**：零依赖，但子命令、公共 flag、帮助和错误处理都要自己维护，命令一多就重复。
- **urfave/cli**：更轻量，但项目明确要求 Cobra，且 Cobra 的 PersistentFlags 正好匹配公共配置参数。
- **Cobra**：命令树、help、completion、参数校验和测试接缝成熟；代价是新增依赖，帮助文本偏英文。

## Consequences

- `internal/cli` 只保留薄命令层；业务逻辑仍在 `App` 和 `Engine`，`Main(args, stdout, stderr) int` 继续作为测试接缝。
- 公共参数放 `PersistentFlags`，避免每个子命令重复定义；`chat` / `run` 的私有 flag 放各自 `Flags`。
- Cobra 的默认英文错误和 help 会出现在 CLI；跨平台终端行为需要手工验收。
- 新增命令或 flag 时，README、spec 的 CLI 章节和 `cli_test.go` 必须同步更新。
