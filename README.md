# Goblin

> 一个智能体（AI Agent）项目。

## 状态

🚧 **v3 规格待确认** —— 已按访谈重写 MySQL 版本，规格见 [docs/spec.md](docs/spec.md)。

## 技术选型

- 语言：Go
- 配置：Viper + `.env`
- 模型：DeepSeek（OpenAI 兼容 API）
- 持久化：MySQL 8.4（Docker Desktop）
- 会话 ID：UUID v4
- 运行形态：CLI，一个进程对应一个独立 Session
- 工具：calculator / search（mock）/ read_docs / todo
- 不依赖 Agent 框架，主循环、工具注册、上下文管理均自行实现

## 待实现

- [ ] LLM 客户端与配置装配
- [ ] 工具注册机制与 calculator / search / read_docs / todo
- [ ] Agent 主循环、输出解析、最大轮次
- [ ] 多会话持久化、追问、上下文压缩
- [ ] trace、异常处理、测试与真实 API 验收

## 目录结构

```text
goblin/
├── README.md          # 项目说明
├── .env.example       # 本地密钥模板（复制为 .env）
├── docker-compose.yml # MySQL 8.4
├── config.example.yaml
├── internal/db/schema.sql
├── docs/
│   ├── spec.md        # 最小可用 Agent 规格说明
│   └── adr/           # 架构决策记录
├── GLOSSARY.md
└── .gitignore
```

## 开始使用

```bash
git clone git@github.com:Goblin-Master/goblin.git
cd goblin
```

## 开发约定

- 默认分支：`main`
- 提交信息使用简洁的祈使句，例如 `feat: 添加工具调用骨架`
