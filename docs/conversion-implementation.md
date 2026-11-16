# 可配置协议转换（dev.157）

[文档索引](README.md) · [三分契约](protocol-three-part-contract.md) · [切换验收记录](protocol-three-part-cutover-2026-11-14.md)

实际入口统一执行：来源 Spec 校验 → 来源 Mapping → 大自在天 core2 → 核心策略 → 目标 Mapping → 目标 Spec 校验。每份 Mapping 只理解自身协议与核心。默认预置直接加载四组 spec.json 和 mapping.json，旧 Module、Codec、after 及专用错误 renderer 已退出执行链。

## 修改落点

| 问题 | 修改位置 | 必须保留的验证 |
|---|---|---|
| 外部字段类型、必填项、枚举、身份或生命周期错误 | 对应协议 Spec | 原始合法／非法 wire，来源与最终输出的不同合同 |
| 自身 wire 与核心间丢失语义、所有者或布局 | 对应协议 Mapping | 来源解码、去 Native 重建、核心编辑、strict／禁用规则 |
| 多份 Mapping 需要相同执行能力 | 通用 DSL | 无供应商身份的小协议、资源、取消和事务反例 |
| 网关、Agent、错误或探测入口跳过声明 | 通用宿主 | 实际入口的固定修订、完整策略、Spec 和交付检查 |

配置说明随合同更新。不能通过提高资源限制、删除非法输入或恢复供应商 Go 分支修复问题。

## 声明与覆盖

默认规则来自 Mapping 的 conversionDefaults：请求按请求方向声明执行，响应／目标事件使用目标声明，source_event 使用上游来源声明。名称或 family 相似不授予兼容行为。改变源、样例或编译器语义后须重验修订证据。

compatible 仅执行明确声明的取舍并记录保真度；strict 拒绝实际损失。按 ID 禁用或替换规则后，编码器仍拒绝未处理字段，不隐式重复投影。常见规则包括：

| 规则 | 核心职责 |
|---|---|
| reasoning-controls／request-projection | 模型控制与请求提示；来源余项保持独立所有者 |
| responses-storage | ClientOutput.ResponsesStorage，区分默认、显式和未知意图 |
| tool-options／tool-results | 工具选项、结果类型与错误标记；保留 CallID 和参数精度 |
| image-default-preprocessing | Anthropic／Gemini 生成请求的显式图片 auto 取舍；采用目标默认预处理并记录降级 |
| system-instruction-hoist／assistant-history | 指令提升、助手历史分组及消息边界诊断 |
| response-metadata／event-metadata | 已知元数据；概率和引用绑定原始节点、区间 |
| response-shape／event-shape／输出布局 | 目标节点身份、状态与有序布局 |
| 签名、加密状态规则 | 使用认证恢复证据，不能伪造来源、作用域或交付事实 |
| response-usage-projection／event-usage-projection | 投影明确列举的计数细节，原始计量保持 |
| response／event-execution-speed-disclosure、execution-usage-disclosure | 具名省略目标无法展示的执行事实；源账本保存、strict／禁用拒绝 |
| Anthropic usage envelope | 客户端必填占位标记 origin:placeholder，strict 拒绝缺失计数 |

具体条件、顺序和路径以对应 Mapping 为准。规则 ID 不是供应商 Go 分派入口。

## 图片默认预处理

Chat／Responses 的来源 Mapping 将显式 `detail:"auto"` 保留为大自在天图片节点的 `payload.detail`。Anthropic／Gemini 的生成请求默认启用 `image-default-preprocessing`（顺序 149，在 `tool-results` 之前）：仅对真实图片节点的 `auto` 移除该成员，目标随后采用自身默认图片预处理。此操作不保证来源协议的缩放行为或 token 预算，因此 compatible 必须记录 `conversion_degraded`、`lossy_compatible`、规则 ID、策略修订及实际图片的 `/payload/detail` 路径。strict 在修改前拒绝这项损失。

可在现有转换策略中按 ID 单独禁用该规则；禁用或绕过转换时，目标 Mapping 的 `image-detail-target` 编码守卫仍拒绝存在的 detail。`high`、`low`、`original`、null 和未知值不会改成 auto。detail 缺失时不触发取舍，同源去 Native 重建继续保留显式 auto。通用 DSL 只遍历核心节点及其内容子节点，不进入工具参数或结果载荷中的不透明 JSON；投影保留 MIME、图片数据、URL／文件引用及其他载荷成员，不补 MIME、不取回 URL，也不缩放图片。

规则目录和配置预览使用现有 Mapping 默认规则机制。compatible 预览展示投影后的核心与具名诊断，strict 预览拒绝；禁用规则的预览保留原始 detail，其后目标编码和真实网关仍拒绝。预览只执行核心转换，不能将预览成功等同于目标可编码。

此声明只加入生成与流式生成的请求默认策略。`count_tokens` 使用独立的操作合同及默认规则，仍拒绝显式 detail，不能据此声称跨协议图片计量等价。真实工具结果子图片同样会被规则选中；Anthropic 支持其图片内容，Gemini 现有 `tool-results` 媒体边界仍独立拒绝，不能借本规则扩展该能力。

## 执行用量

大自在天 Usage 的 executionSpeed／executions 保存实际执行速度与有序执行账本。Anthropic Spec／Mapping 负责自身字段结构和双向映射，其他目标只按中立字段声明客户端披露取舍。compatible 记录具名损失，strict 和禁用规则拒绝；内部来源记录先于客户端投影保存，分项不增加父 tokens。JSON 账本与 SSE start-speed 支持范围不同，SSE 显式 iterations 仍拒绝未知更新语义。完整字段、计量及配置合同见[执行速度与分次执行用量](execution-usage-contract.md)。

## 客户端意图

Chat 的 stream_options.include_usage 由来源 Mapping 转为 ClientOutput，与上游用量请求和内部计量分别处理；没有真实计数不能生成 observed。eager_input_streaming 属于工具选项。

Responses 的单一 include:["reasoning.encrypted_content"] 直接成为 ClientOutput.requireContinuationState；这一步无信息损失，不生成虚假的规范化诊断。它选择 Elysia 认证续传，不冒充供应商原生加密内容。受保护状态缺少客户端交付计划时，服务端持久副本不能代替载体。

previous_response_id:null 保持来源自有余项，跨协议规范化须执行具名规则并记录原始路径。当前来源合同在路由前拒绝非空引用。合法 truncation 进入 parameters.contextTruncation，无法等价表达的目标继续拒绝，包括显式 null；禁用规范化不授权目标解析外部字段。

## 来源首帧与流顺序

Chat 的 source-opening-identity 使用通用 sourcePrelude，最多暂存一条 first 匹配帧。下一帧必须满足 next 和配对谓词，才能按有序 set 替换首帧已存在的非根、不重叠路径。input 是下一帧，root 是首帧；原始帧和修正帧分别通过来源 Spec，修正在生命周期提交前完成。

默认声明只处理同模型、ID 变化的精确空 assistant 开场对。正文、工具、非空计数、未知字段及后续身份改变不能借此放行。strict 拒绝改写，禁用后保留原输入交给身份校验。干净 EOF 释放原帧；取消或传输失败丢弃暂存帧。原始日志和传输元数据保持供应商观测值。

Responses 对外来源消息的流布局按叶子到达顺序生成独立输出，文本／refusal 包装由目标声明生成。原父容器保留身份和元数据，不能把后来的文本移到先前 reasoning／tool 之前。同源 Native 保持自身布局。验证只使用实际生效且修订匹配的布局 companion；禁用、替换或失败事务不能留下授权。

## 验证和失败

组合报告保留完整绑定诊断，能力子集各自验证，不拼接独立成功报告宣称组合支持。条件拒绝和跳过不贡献正向覆盖。visibleReasoningOnly 限制摘要／多段思考；transport:http_json 只证明普通 JSON，不能授权 SSE、NDJSON 或会话请求。完整流仍要求完整生命周期正例。

未知字段归属自己的 wire:<family>。无根 Native 时仍须保留已声明的自身余项，长整数、null、false 不被默认值覆盖；余项与核心结果冲突则拒绝。其他协议不能解析该余项。Chat 的 null／空 logprobs 也保留在自身选择项，不能移到根或静默消失。

错误信封只按自身正文成员识别。被 Spec 拒绝的失败帧可由只读 failureObservation 提供失败观察，原拒绝仍成立且不授权重放。流式失败／取消释放未完成续传收集状态，再执行真实错误转换和最终校验。

权限、Scope、Seal/Open、I/O、计量与交付确认仍属于通用宿主。运行、预览、验证执行同一声明；离线预览不写配置或持久续传副本。整体验收状态见切换记录。
