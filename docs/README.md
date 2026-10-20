# Goblin 文档

本目录只放项目自身的文档。DSH 技能目录、个人工具链说明不进入仓库。

## 文档地图

| 文档 | 作用 | 读者 |
|---|---|---|
| [../README.md](../README.md) | 项目定位、快速开始、命令、配置、Memory 摘要 | 第一次运行项目的人 |
| [spec.md](spec.md) | 行为规格、数据模型、模块边界、验收清单 | 维护者、评审者 |
| [testing.md](testing.md) | 测试矩阵、真实 API/MySQL 验收记录、已知缺口 | 维护者 |
| [../GLOSSARY.md](../GLOSSARY.md) | 领域术语：Session / Turn / Run / Summary Cursor 等 | 所有人 |
| [adr/0001-mysql-only-persistence.md](adr/0001-mysql-only-persistence.md) | 为什么 MySQL 是唯一存储 | 架构评审 |
| [adr/0002-native-tool-calls.md](adr/0002-native-tool-calls.md) | 为什么使用原生 `tool_calls` | 架构评审 |
| [adr/0003-viper-for-config.md](adr/0003-viper-for-config.md) | 为什么用 Viper 合并配置 | 配置维护者 |
| [adr/0004-cobra-for-cli.md](adr/0004-cobra-for-cli.md) | 为什么用 Cobra 组织 CLI | CLI 维护者 |
| [adr/0005-single-structured-log.md](adr/0005-single-structured-log.md) | 为什么只保留一份结构化日志 | 排障/维护者 |

`docs/skills.md` 已删除：它描述的是 DSH 用户级技能目录，不是 Goblin 的项目知识，容易随环境变化而过期。

## 维护规则

文档不是代码的逐行复制；**命令、配置名、默认值、Schema 必须与代码一致**。

### 什么时候更新什么

| 变更 | 必须更新 |
|---|---|
| CLI 命令、flag、交互命令 | `README.md`、`spec.md` 第 9 节、`testing.md` |
| 配置项、默认值、环境变量 | `README.md`、`config.example.yaml`、`spec.md` 第 11 节 |
| 工具名称、参数、行为 | `README.md`、`spec.md` 第 8 节、工具测试 |
| MySQL 表结构或写入语义 | `spec.md` 第 6 节、`internal/db/schema.sql`、ADR 0001 |
| Agent 循环、最大轮次、压缩策略 | `spec.md` 第 7/10 节、`README.md` Memory 段 |
| 新的领域术语或术语含义变化 | 立即更新 `GLOSSARY.md` |
| 难以回退的架构决策 | 新增 `docs/adr/NNNN-*.md`，不要改写已接受的 ADR |
| 外部技术选型（DB、CLI、配置、协议） | 新增 ADR，记录替代方案与代价 |
| 测试结果、覆盖率、已知缺口 | `testing.md`，写日期与环境 |
| 主循环、Memory 压缩、MySQL 关系图 | 同步更新 README 与 spec 中的 Mermaid 图 |
| 日志字段、trace_id、控制台输出 | 更新 README、spec、testing 和 ADR 0005 |
| 删除或移动文档 | 同步所有相对链接和 `docs/README.md` 地图 |

### 文档层级

- `spec.md`：**行为契约**。实现与它有冲突时，先改 spec 或明确记 ADR。
- `README.md`：**可运行入口**。不追求覆盖所有边界，但命令必须能直接复制执行。
- `testing.md`：**证据**。只写实际跑过的命令、结果和日期。
- `GLOSSARY.md`：**语言**。只放领域概念，不写实现细节。
- `adr/`：**决策**。只记录难以回退、且有真实取舍的决定。

### 提交前检查

```powershell
.\scripts\check-docs.ps1
go test ./...
```

`check-docs.ps1` 会检查 Markdown 相对链接、常见过期术语和 `.env` 忽略状态。

## 最近核对

- 日期：2026-10-20
- 对应提交：DSML 解析防护与最后一步收尾之后
- 结论：README、spec、testing、ADR、术语表与当前代码一致
