# 用 Viper 管理配置

Goblin 需要同时支持 `config.yaml`、进程环境变量、`.env` 和命令行覆盖。我们选择 Viper 作为配置合并器，统一 key 优先级、嵌套结构和 `time.Duration` 解码；配置加载仍然是一个显式的 `config.Load`，并在最后执行 `Validate`，不把业务校验交给 Viper 的零值。

## Considered Options

- **只用环境变量**：部署简单，但本地开发需要大量 `export`，也无法用 YAML 表达默认值和注释。
- **手写 YAML + env 合并**：依赖少，但优先级、类型转换、嵌套 key 和 duration 解析都要自己维护，容易和文档漂移。
- **koanf**：更轻，但项目明确要求 Viper，且当前配置规模不需要再引入第二套选型。

## Consequences

- 多一棵 Viper 依赖树；启动时需要显式 `Validate`，不能依赖默认零值。
- Viper 不原生读取 `.env`，因此在 `Load` 前用内置最小解析器补齐环境变量；已存在的进程环境变量优先，不被 `.env` 覆盖。
- API key 只从环境变量或本地 `.env` 读取，不写进 `config.yaml`，降低误提交风险。
- 配置名、默认值、环境变量一旦变化，README、`config.example.yaml` 和 spec 必须同步更新。
