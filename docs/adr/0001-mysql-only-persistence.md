# MySQL 作为唯一持久化后端

Goblin 需要让多个 Session 在进程重启后继续，并让消息、摘要和待办保持一致。我们决定只使用 MySQL 8.4：`sessions`、`messages`、`todos` 三张表，启动时执行内嵌 `schema.sql`，不再提供 JSON/file 运行时适配器。原因是单用户多 Session 的数据关系明确，事务和外键能避免文件方案的半写、并发覆盖和跨文件一致性问题；Docker Compose 已能把本地启动成本压到一条命令。

## Considered Options

- **每 Session JSON 文件**：零依赖、易查看，但跨文件事务、并发写和待办一致性都要自己实现，最终会滑向自制数据库。
- **SQLite**：单文件且支持事务，但用户明确选择 MySQL，且 MySQL 便于演示连接池、schema 和容器化。
- **MySQL + file 双驱动**：保留离线能力，但两套持久化语义会迫使 Agent/CLI 处理分叉，违背最小实现。

## Consequences

- `go test ./...` 不能依赖 MySQL；MySQL 行为放在 `integration` tag，测试使用 Store 接口 fake。
- 本地开发必须先启动 Docker Desktop 的 MySQL；`scripts/dev.ps1` 负责定位 CLI 和等待端口。
- MVP 的 `Save` 使用事务内整段替换 messages；数据量增长后再评估 append-only。
- 不在本版引入迁移框架；`schema.sql` 只做幂等建表，后续变更必须显式、幂等、非破坏。
