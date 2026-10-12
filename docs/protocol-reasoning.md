# 思考参数与存储意图转换

[文档索引](README.md) · [可配置转换](conversion-implementation.md) · [验证方法](protocol-release-readiness.md)

思考参数与存储意图在共享语义请求中表示，经转换规则适配实际目标模型。默认 `compatible` 允许有诊断的可接受近似，`strict` 额外在调用上游前拒绝所有有损候选。两者都必须保持明确的关闭思考意图和用户显式输出上限。

## 数据流和边界

源格式及组合校验 → `Request.Reasoning` → 每个候选独立解析模型能力 → 转换并判定损失 → 后续规则执行后的目标合法性复验 → 编码、操作输入检查 → 上游调用。

入口 `Request.Model` 表示模型组名，不能据此推断供应商模型能力；组名与已知模型重名也不改变请求是否合法。入口只检查类型、整数范围、互斥参数及协议格式；模型支持的模式、等级、预算和输出约束由候选阶段根据实际模型及绑定能力检查。旧参数归一化和 Native 重放遵循相同边界。

`ReasoningIntent` 分别保存模式、effort、effort 作用范围、数值预算、摘要显示偏好和手动交错思考标记。Anthropic effort 的作用范围是生成过程，其他协议的 effort 表示思考强度。因此合法的 `budget_tokens` + `output_config.effort`、`thinking:disabled` + effort 会完整保留；跨协议改变作用范围属于有损转换。只有目标不能共同表达时，才以预算优先于等级，并报告被舍弃的控制。

Native 重放使用源请求的解码上下文，并沿用首次授权得到的资源作用域；不重新路由或授权，也不把已移除的控制从原文恢复。

源端类型错误、整数溢出、非法组合全局拒绝。仅因目标能力、约束或严格模式产生的拒绝淘汰当前候选，继续检查其他候选；全部不可执行时返回汇总原因。操作员显式 `reject` 规则和 `responses_storage.onUnsupported:reject` 保留全局拒绝作用域。

先筛选可执行候选，再计算重试上限。每个候选克隆原始请求；限流预留采用重试窗口内有效请求估算的最大值，单次日志估算采用本次有效请求，上游观测用量仍为最终依据。同步、异步任务、会话和内置 Agent 均使用处理后的请求。

## 模型能力

`backend/protocol/reasoning_capabilities.go` 集中描述模型系列。编码器只输出已经解析的参数，不自行判断模型版本。相同协议也可能路由到不同能力的模型。

- Gemini 2.5 使用 `thinkingBudget`，Gemini 3 文本模型使用 `thinkingLevel`；各系列的可用等级、默认值和能否关闭独立描述。最低等级不等于关闭。
- Anthropic 区分手动预算与自适应模式，并保留与思考独立的生成 effort。手动交错约束必须同时满足模型支持、有工具、实际出站操作带对应 beta 头。
- OpenAI 采用模型支持的 effort 集合；不假设所有模型支持 `none`、`minimal`、`xhigh` 或 `max`。
- Chat / Responses 对 `gpt-4o`、`gpt-4o-mini`、`gpt-4.1`、`gpt-4.1-mini`、`gpt-4.1-nano` 及目录登记的 `gpt-4o-2024-05-13`、`gpt-4o-2024-08-06`、`gpt-4o-2024-11-20` 精确声明 `kind:none`。这些模型接受 Agent 默认关闭思考的意图，出站省略推理参数；不将此能力扩展到媒体变体或私有别名。
- 未知模型同协议保留原生控制；跨协议无法确认的控制在兼容模式下省略并诊断，严格模式拒绝；明确要求关闭思考而无法确认能否关闭时，两种模式均拒绝该候选。别名不能仅凭显示名称推断能力。内置 Agent 的设置并非原生请求，未知模型也必须诊断未能应用的控制，关闭思考无法确认时拒绝；需要保留这些设置时，应为别名声明绑定能力。

模型能力目录默认每 24 小时在线更新，模型列表刷新时回填基础能力；手动模型保留显式配置。目录不更新这里的推理档位、预算、默认值或端点限制。协议与绑定证据自动刷新依据随程序发布的定义和代码，不代表在线同步供应商规范。当前能力规则未完整表达所有模型、端点与工具组合限制。

必要时在已有协议绑定的 `reasoning` 字段完整覆盖能力，管理界面的“转换行为覆盖”提供编辑入口。配置来自已确认的模型/端点约束；省略则使用集中规则。例如：

```json
{
  "kind": "budget",
  "disable": true,
  "minBudget": 0,
  "maxBudget": 24576,
  "defaultMode": "auto",
  "source": "https://ai.google.dev/gemini-api/docs/generate-content/thinking"
}
```

`maxOutputTokens:0` 表示硬上限未知，不能用模型目录中的上下文窗口代替。此字段省略与零等价。模型、操作、能力覆盖、beta 条件或编译器变化使对应验证证据失效；刷新后重新验证。绑定证据保存能力快照，外部修改不能改变已生成证据。

## 裁决表

| 情况 | 处理 | 保真与严格模式 |
|---|---|---|
| 同名 effort，目标支持且作用范围一致 | 直接映射 | 保真，允许 |
| 不支持的 effort 档位 | 优先选择不超过请求的最高可用档位，否则最低档 | 有损，拒绝 |
| effort → Gemini 2.5 预算 | minimal/low=1024，medium=8192，high 及以上=24576；再校验目标范围 | 启发式近似，有损，拒绝 |
| effort → Anthropic 手动预算 | minimal/low/medium/high/xhigh/max 分别约为输出上限的 5/20/50/80/95/95%，受最低预算及输出约束限制 | 启发式近似，有损，拒绝 |
| 正预算 → effort | ≤1024 low，≤8192 medium，其余 high；再匹配支持档位 | 启发式近似，有损，拒绝 |
| 预算 0、-1、缺省、null | 分别保留关闭、动态预算、默认和空值语义；不进入正预算分档 | 跨目标无法表达时诊断损失 |
| 动态预算 → 固定 effort | 目标默认关闭时显式采用默认 effort；其他等级模型使用其默认 effort | 有损并诊断，严格模式拒绝此候选；同协议能表达 `-1` 时原样保留 |
| 预算超出目标范围 | 在可确认约束内调整 | 有损，拒绝 |
| 请求关闭，但目标无法关闭或无法确认能否关闭 | 拒绝当前候选，继续检查其他候选；不以最低档或省略代替关闭 | 两种模式均拒绝 |
| 无法表示的控制或摘要偏好 | 明确诊断后省略；摘要偏好本身不启用思考 | 有损，拒绝 |
| 显式输出上限超过目标硬上限，或无法容纳最低思考预算 | 保留上限，拒绝当前候选；不自动提高、降低上限或关闭思考 | 两种模式均拒绝 |
| 缺省输出上限，目标必填 | 系统默认 8192；有确认硬上限则受其约束 | 记录系统默认来源 |

用户显式输出上限始终保留；预算可在上限内有诊断地近似，严格模式拒绝这种损失。系统填入的默认上限可以在已确认硬上限内调整，并记录默认来源；无确认硬上限时，不自动提高输出上限。最低思考预算仍无法满足时，两种模式均拒绝候选。合法的零输出请求不会因思考转换被统一拒绝；启用手动思考仍必须满足预算约束。

摘要展示与工具续传签名分别处理。`display:omitted` / `includeThoughts:false` 不删除工具续传需要的签名；被转换或移除的参数不能由 Native 快照恢复。

Anthropic `thinking.display` 接受 `summarized`、`omitted`、Beta SDK 声明的 `updates` 及 null。同协议保留显示偏好；跨协议的近似或省略仍由 `reasoning-controls` 诊断，严格模式拒绝实际损失。支持该枚举不代表每个渠道都实现了 Beta 行为；操作的 Beta 头仍按已配置的出站操作处理。本次真实客户端及渠道证据见 [Claude Code 验收](protocol-anthropic-system-2026-10-12.md)。

## store 决策

`store` 是供应商 API 对象的保存/检索意图，不是提示缓存开关，也不控制网关日志或签名续传副本。

| 来源意图 | 相同协议 | 跨到 Chat / Responses | 跨到 Anthropic / Gemini |
|---|---|---|---|
| 显式 false | 保留 false | 显式 false | 去除不支持的字段，保持无对象保存意图 |
| 显式 true | 保留 true | true，并诊断原 API 检索契约不能保证 | 兼容模式无状态并诊断；严格拒绝 |
| Responses 缺省或 null | 保留来源默认开启语义 | 显式 true，并诊断检索契约变化 | 同 true |
| Chat 缺省或 null | 保留原值，默认行为标记未知 | 显式 false，并诊断未知来源默认值 | 无状态并诊断；严格拒绝 |
| 来源无 store 语义 | 原样 | 显式 false，避免目标默认启用保存 | 原样 |

“保持保存意图”不承诺供应商实际保存、不承诺原协议能够查询另一协议的对象，也不改变供应商的数据保留政策。

## 旧定义和验证

新内置定义声明 `conversion.reasoning.v1` 并使用统一语义。自定义定义按当前语义契约维护，不保证历史参数形状兼容。内置 Agent 设置也进入同一转换流程。Responses Agent 使用 `store:false` 并请求 `reasoning.encrypted_content`，在工具第二轮回传加密思考和完整工具历史。编译器版本变化后，已有证据必须重新验证。

覆盖包括四协议 16 个方向的 JSON/SSE 实际出站检查、合法独立控制、哨兵值、Native 防恢复、两种模式硬约束拒绝、零上游调用、候选顺序交换、预算及默认值、证据快照、Agent 绑定模型、缓存用量和签名工具续传。真实端点、SDK 与性能验证方法见[验证与发布检查](protocol-release-readiness.md)。

## 官方依据

约束核对日期：2026-10-11。预算换档比例是网关的有损近似策略，不是供应商声称的等价关系。

- [OpenAI reasoning](https://developers.openai.com/api/docs/guides/reasoning)
- [OpenAI Responses 迁移与存储行为](https://developers.openai.com/api/docs/guides/migrate-to-responses)
- [Gemini thinking](https://ai.google.dev/gemini-api/docs/generate-content/thinking)
- [Anthropic extended thinking](https://platform.claude.com/docs/en/build-with-claude/extended-thinking)
- [Anthropic adaptive thinking](https://platform.claude.com/docs/en/build-with-claude/adaptive-thinking)
- [Anthropic effort](https://platform.claude.com/docs/en/build-with-claude/effort)

模型和供应商代理的能力会变化；系列规则不能替代每个实际端点的验证。本文不承诺所有采样参数、媒体变体或供应商缓存行为均可跨协议等价表达。
