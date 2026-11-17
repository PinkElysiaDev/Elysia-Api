# Agent Notes

这里存放一类设计文档。**Agent Note** 记录影响本代码库的决策或提案：代码和文档无法承载的*为什么*以及*放弃了什么*。本文件规定 Agent Note 存放在哪里、何时需要写一份，以及[文件内格式](#文件格式)。

本机制沿用 [deepseek-ai/deepseek-harness](https://github.com/deepseek-ai/deepseek-harness)（DSH）的 `.agents/notes/` 体系，按本仓库规模裁剪：不设冻结归档，暂无 CI 门禁脚本，其余规则保持一致。双语以 zh/en 对侧文件实现。

`docs/` 仍是对外接口与使用说明的权威位置；Agent Note 记录决策依据，不替代 `docs/` 中的契约文档。

## 布局与命名

每份 Agent Note 有两个维度，都编码在其**路径**中：`{lifecycle}/{class}/yyyy-mm-dd-topic-title.{zh|en}.md`。

**每份 Agent Note 是 zh/en 对侧文件对**：`yyyy-mm-dd-topic-title.zh.md`（中文）与 `yyyy-mm-dd-topic-title.en.md`（英文）必须成对存在、内容互为翻译。规则文档（本 README、`implemented/AGENTS.md`）不在此列。

- **生命周期**（顶层文件夹）是 Agent Note 的状态，Agent Note 随状态变化在文件夹之间移动：
  - **`proposed/`**：实施前评审的提案；尚未构建（或仅部分构建）。
  - **`implemented/`**：决策已交付。文件记录做了什么决定、否决了什么，并**与实际交付的内容保持同步**：当代码后续移动文件、重命名包或更改键名/默认值时，Agent Note 在同一个变更中同步更新（仅限事实——路径、名称、结构——而非决策本身）。见 [implemented/AGENTS.md](implemented/AGENTS.md)。
  - **`rejected/`**：提案经过讨论后被否决。仅当其决策依据仍能避免一种诱人且影响重大的错误时保留；否则删除。
- **类别**（嵌套文件夹）是决策的*种类*——见下方[分类](#分类)。

文件名中的日期是该主题**首次提出**的时间（以 git 历史为准）。Agent Note 之间的交叉引用使用相对 Markdown 链接，从不使用纯文字或编号，这样既可机械检查，也能在文件夹间移动时保持有效。

活跃生命周期目录树就是工作清单：浏览其生命周期/类别文件夹，或搜索仓库即可。请勿添加集中式 `INDEX.md`。

## 分类

每份 Agent Note 属于下列封闭集合中的一个路径编码类别。

| 类别             | 覆盖范围                                                                        |
| ---------------- | ------------------------------------------------------------------------------- |
| `feature`        | 面向用户或模型的新能力。                                                        |
| `bug-fix`        | 修正缺陷或弥补事故复盘（postmortem）发现的缺口。                                |
| `performance`    | 以可测量的 CPU、延迟、分配或内存收益为目标、不改变可观察协议语义的变更。        |
| `simplification` | 在不增加能力的前提下移除代码、行为或对外范围。                                  |
| `architecture`   | 关于**交付源码**的结构性决策：包之间的关系、运行时词汇。                        |
| `process`        | 代码**周边**的工具、策略或工作流——门禁、包管理器、vendor 化——不涉及运行时行为。 |
| `testing`        | 测试基础设施与策略。                                                            |

`architecture` 与 `process` 的界线：**architecture** 关乎我们交付的源码；**process** 关乎围绕源码的工具与工作流。`performance` 变更若同时改变了包结构，选择其主要动机所对应的类别，并在正文中说明另一面。

## 何时需要写一份

每个非平凡变更都必须在同一变更中新增或更新至少一份 Agent Note。如果变更修改了行为、架构、跨文件或跨包约定、流程或工具、测试策略、磁盘存储格式、协议格式（wire format）、配置格式或性能特征，或者维护者可能合理重新审视的其他决策，就属于非平凡变更。对未来重大工作的提案从 `proposed/` 开始；已经做出的决策从 `implemented/` 开始。

更新已经拥有该决策的 Agent Note 即可满足规则；不要创建重复记录。只有不涉及行为、约定、结构、流程或理由变化的纯机械性或局部编辑才可豁免。Agent Note 永远不会被编辑为一个*不同的决策*：用新 Agent Note 取代旧记录，并让两个记录保持互相链接。

## 文件格式

### 中英双语（强制）

Agent Note 必须**中英双语**，且是**两份独立的对侧文件**：`yyyy-mm-dd-topic-title.zh.md`（中文文档）+ `yyyy-mm-dd-topic-title.en.md`（英文文档），内容互为翻译，路径除 `.zh`/`.en` 后缀外一致。

### 头部块

每份 Agent Note 的前三行严格为：

```markdown
# Agent Note: <title>

Status: <status>
```

后跟一个空行。`Status:` 的值有三种形式，且必须与文件所在的生命周期文件夹一致：

- `Status: proposed`
- `Status: implemented`
- `Status: rejected — <why, in one line>`

状态行不带日期、不带括号补充说明：文件名记录首次提出日期，git 记录其余一切。

### 正文骨架

每份 Agent Note 的正文以 `## Problem` 开头：动机，写法上不依赖解决方案即可独立成文。固定章节使用以下规范名称且仅限这些名称（中文文件同样使用英文章节名，便于机械检查），而真正独特的技术章节在必需章节之间可自由组织。

#### `proposed/`

```markdown
## Problem

## Proposal

…bespoke sections…

## Alternatives considered

## Acceptance criteria

## Risks
```

#### `implemented/`

```markdown
## Problem

## Decision

…bespoke sections…

## Alternatives considered

## Consequences
```

`## Decision` 以现在时态描述已交付的现实。`## Consequences` 记录权衡的代价**与**收益。`## Proposal`、`## Plan`、`## Migration plan` 和 `## Acceptance criteria` 不得出现在 implemented Agent Note 中。`## Testing`、`## Measurements`、`## Deferred` 或 `## Related` 章节在陈述现在时态的事实时是允许的。性能类 Agent Note 应在 `## Measurements` 中给出前后对比的命令与数值，并注明测量环境。

#### `rejected/`

被否决的 Agent Note 是冻结的提案：保留提案时的所有章节，结论写在 `Status:` 行上。

### 曾考虑的替代方案——必需

每份 Agent Note 都必须包含 `## Alternatives considered` 章节：每个真实的替代方案及其落选原因，每个替代方案用一个加粗引导的段落。

### 在生命周期之间移动

将文件在生命周期文件夹之间移动意味着在同一个变更中更新 `Status:` 行并满足目标文件夹的骨架要求。`proposed/` → `implemented/` 将 `## Proposal` 改写为现在时态的 `## Decision`，将 `## Acceptance criteria` 和 `## Risks` 折入 `## Consequences`（或折入现在时态的 `## Testing`/`## Measurements` 章节），并用实际交付的内容替换计划。`proposed/` → `rejected/` 仅在 `Status:` 行添加原因并冻结文件。
