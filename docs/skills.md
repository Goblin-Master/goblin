# 开发技能（skills）

本项目使用 DeepSeek Harness 的 skill 机制，技能安装在用户级目录 `~/.dsh/skills`，所有项目共享。
调用方式：在输入框输入 `/` 选择，或直接键入 `/技能名`。

## 已安装

来源：[mattpocock/skills](https://github.com/mattpocock/skills)（工程流），外加一个自建的 `commit`。

### 规格驱动（用户手动调用）

| 技能 | 作用 |
|---|---|
| `/grill-with-docs` | 反复追问把想法压成方案，同时产出 ADR 与术语表 |
| `/to-spec` | 把当前对话合成一份 spec，发布到 issue tracker |
| `/to-tickets` | 把 spec 拆成带依赖关系的 tracer-bullet tickets |
| `/implement-spec` | 按 spec + tickets 实现，用子代理并发推进 |
| `/implement` | 按 spec 或 tickets 实现一块工作 |

### 实现与评审

| 技能 | 作用 |
|---|---|
| `tdd` | 红-绿-重构，先写测试 |
| `code-review` | 以固定基点评审，Standards 与 Spec 两条轴并行 |
| `diagnosing-bugs` | 难 bug / 性能回归的诊断循环 |
| `codebase-design` | 深模块设计词汇（接口、接缝、可测试性） |
| `domain-modeling` | 维护 `GLOSSARY.md` 与 ADR |
| `research` | 针对高可信来源做调研，落成 Markdown 笔记 |
| `grilling` | 对某个决策做压力测试式追问 |

### 提交与 PR

| 技能 | 作用 |
|---|---|
| `commit` | 按仓库既有约定写 commit message（Conventional Commits） |
| `pr` | 按 Summary / Evidence / Merge Danger 结构写 PR 正文 |

## 推荐链路

```text
想法 → /grill-with-docs → /to-spec → /to-tickets → /implement-spec → code-review → /pr
```

小改动不必走全程：`tdd` → `commit` 即可。

## 首次使用前

工程流技能依赖仓库级配置（issue tracker、triage 标签、domain 文档布局）。
在新仓库第一次使用前，运行一次 `/setup-matt-pocock-skills`，它会在 `docs/agents/` 下写入配置，并在 `AGENTS.md`（或 `CLAUDE.md`）里加一段 `## Agent skills`。

## 维护

- 技能目录：`C:\Users\Administrator\.dsh\skills\<name>\SKILL.md`
- 格式：YAML frontmatter 必填 `name`（kebab-case）与 `description`；可选 `whenToUse`、`disable-model-invocation`、`user-invocable`、`metadata`
- `disable-model-invocation: true` 的技能不会出现在模型上下文里，只能由人 `/` 调用
- 增删 `SKILL.md` 后刷新页面即可，无需重启
