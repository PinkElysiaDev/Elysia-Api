# 三分切换的测试迁移记录

默认协议现在由 `Packages()` 加载每个目录的 `spec.json` 与 `mapping.json`。旧 Go Module、TypedModule、供应商流式 codec 及其辅助 API 已移除。旧实现不再作为运行入口，也不能作为新实现的测试 oracle。

## 迁移原则

测试使用实际发布包与大自在天 core 2。Spec 负责源输入与最终输出的边界，Mapping 负责自身 wire 与 core 的转换，宿主提供操作标识、作用域、认证续接证据、帧用量观察及交付确认。

- 旧 Module/Definition 安装测试应改为独立 Spec/Mapping 安装测试；不能仅因旧 helper 消失而丢弃其输入反例。
- 差分测试应改为独立预期。旧输出的错误文字、JSON 拼写及旧 core 1 布局不具有规范效力。
- 直接构造大自在天事件必须带当前 schemaVersion。core 2 保留消息容器，工具断言应检查容器内的工具语义及关联。
- 流式转换使用 `convertFixtureStream`，执行源 Spec/Mapping、目标声明取舍、core 生命周期、目标 Mapping/Spec 和交付提交。
- 禁用规则按稳定的声明 ID 选择；不能继续匹配已经移除的供应商 Go action 名。
- 有损布局变化必须同时覆盖 compatible 的诊断和 strict/disabled 的拒绝。真实协议缺口只能在所属层修复。

## 已确认的独立覆盖

| 原有契约 | 当前独立覆盖 |
|---|---|
| 默认包编译与激活 | `TestBuiltinPackageEvidence` |
| 最终请求结构、操作约束、工具关联 | `package_final_request_test.go` |
| 最终响应结构与错误投影 | `package_final_response_test.go`、`package_error_projection_test.go` |
| 四协议流式文本、用量尾帧、工具精度与关联 | `TestBuiltinStreamMatrixPreservesTextAndUsageTails`、`TestBuiltinStreamToolMatrix` |
| Responses 源工具生命周期、身份唯一性、等价转义、乱序序号 | `TestResponsesPackageToolSourceStream`、`TestResponsesPackageToolWireIdentityBoundaries` |
| Responses 流重建、原生帧、消息阶段、推理集合、文本工具、托管工具 | `TestResponsesPackageStreamRebuildAndNativeDelivery` |
| 消息边界、布局取舍及原生位置 | `package_instruction_layout_test.go`、`package_assistant_layout_test.go`、`chat_message_stream_package_test.go` |
| Spec/Mapping 切换后 Anthropic 错误帧 | `TestAnthropicPackageSourceFailureLifecycle`，包括开始前及活动块中的失败 |
| 嵌套工具转换为 Responses 独立 output item | `TestResponsesPackageNestedToolDisposition`，包括 compatible、strict 和禁用规则 |
| Chat 完整工具索引与 refusal 类型 | `TestChatCompleteToolIndexesAreValidatedRedundancy`、`TestChatRefusalTypeAndContentRemainProtected` |
| 缓存意图取舍、空值与未知字段 | `package_cache_projection_test.go` |
| 客户端存储/include 的唯一所有者 | `package_storage_test.go`、`package_continuation_openai_test.go` |
| 认证续接与作用域隔离 | `responses_encrypted_continuation_test.go`、`anthropic_carrier_package_test.go`；测试实际 SealState/BindContinuation，不模拟可恢复标志 |
| 工具选项和工具结果 | `package_tool_options_test.go`、`package_tool_results_test.go` |

这些是相关覆盖入口，不代表每个删除的函数已逐条完成等价覆盖审查。

## 尚未完成的审查

dev.156 的 16 方向历史请求测试先按来源声明准备核心布局，再经过来源编码/解码、实际目标转换和目标编码/解码。
原文本、函数关联、超大整数及工具结果保留；文本进入 Gemini 使用声明的 result 包装，strict 与禁用 tool-results
仍必须拒绝。流测试补齐真实来源起始帧、帧用量观察、目标流绑定与交付确认，没有删除原错误输入。

存储表按各来源当前声明区分缺失与 null；加密续传断言绑定独立的 response-encrypted-state 规则，
compatible 缺少认证载体也必须拒绝。拒绝候选的诊断从错误读取，不要求失败事务向成功诊断 sink 发布数据。
上下文截断及指令提升的诊断改用当前核心路径。Gemini safety 的集合唯一性失败使用 `/safetySettings`，
具体成员的类型错误与核心重复成员路径继续单独验证。

本轮实际修复由原负例和新增来源重建回归验证：停止字符串、instructions 属性、结构化推理扩展、moderation、
派生 output_text、安全类别转义以及推理正文完成顺序。`TestPackageStreamRequiresDeclaredPayloadSeal` 验证通用
实体完成前置条件与拒绝时不写入实体；普通文本无需该条件的正例仍保留。

dev.155 将旧推理包装归一化改为整体拒绝；`TestPackageSourceRejectsRetiredReasoningControls` 保留原 legacy、
history、空/null、规范字段冲突、错误类型等输入，检查拒绝不解析或改写原值。真实来源的 enum/类型反例由各
Spec 测试保留；`TestReasoningRemaindersStayWithTheirSource` 验证来源到核心、无 Native 重建、核心编辑和跨源拒绝。
旧账本 wire fixture 原样保留，验证改走实际 DecodeResponse 和独立 Spec；核心算术另由
`TestCanonicalUsageAccounting` 覆盖 int64、缓存和子项溢出、unknown、null/empty 及诊断路径。

旧 include/context 规则诊断改为来源归一化或中立 `request-context`，不要求目标解析外部字段才能产生旧路径。
测试继续验证 unknown、禁用来源处理、输入不变和最终 wire 改写拒绝。已知单一续传选择现在直接成为核心布尔值，
没有发生投影时不再要求生成虚假的规范化诊断。`TestAnthropicLateContentMetadataCannotDisappear` 保留晚到元数据
的拒绝，并确认拒绝回滚后仍能正常完成原始流。

[retired-tests.json](testdata/retired-tests.json) 记录机械删除时涉及的每个声明及原始版本。`mechanicalReason` 只解释删除工具遇到的依赖，**不是允许删除测试的理由**。`restored-on-package-contract` 表示同名声明已恢复；`review-required` 仍需对照基线输入补全独立断言或记录具体替代。旧辅助类型和方法的删除也不能自动证明其调用测试已覆盖。

独立包测试及四协议流式矩阵通过，不能替代全量功能、资源及性能门槛。完整回归和发布验收以最终切换记录为准。

## dev.157 用量回归恢复

恢复 11 个原有测试声明及其原始输入，改用实际来源 Spec/Mapping、目标转换和编码边界，见退役清单各条目的
`coverage`。覆盖 Gemini 推理总量推断、Chat 缓存别名、缓存创建缺失/零/非零及冲突、Anthropic 思考子计数、
分批 TTL 桶和 late snapshot。新增 40 个非法部分计数在独立来源 Spec 边界拒绝，覆盖 null、负数、字符串、
小数和 int64 溢出。完整客户端响应的必需 usage 校验独立保留。

恢复过程中发现 Chat/Anthropic 未知 usage 余项仅依赖 Native 保存，现由来源 Mapping 显式归属。测试移除
根 Native 后检查长整数、null、false、当前核心值编辑及同源冲突；其他目标仍必须拒绝未知来源所有者。
Anthropic 来源 usage-only 尾帧不要求 delta，计数状态按自身声明累积；单个 TTL 桶不补默认零。

这些专项及相关通用合同已通过 race；完整回归和其他退役条目的逐项审查仍以最终切换记录为准。

## 目标流与自定义映射的退役审查补充

`target_migration_audit_test.go` 恢复 Chat 的三类原始目标流及四种用量交付偏好、三类元数据更新，
以及 Anthropic 的原始文本、工具、无签名/有签名思考、加密思考和七类续接变体。断言直接检查外部帧、
原始工具整数、文本累积、元数据所有者与终态次数，实际执行目标 Spec 和交付确认；旧 codec 不再参与。
目标流辅助方法共用一个验证和交付执行器，去掉仅服务于旧 Module 的运行分支。

`custom_mapping_audit_test.go` 恢复自定义协议身份、显式系统指令提升、绑定样本、错误输出、模型缺失修复
及辅助元数据的原始输入。显式提升由目标 Mapping 的核心布局和外部布局选择器共同声明；破坏字段语义的
自定义程序必须先通过编译期 effect 证明。辅助元数据的省略授权来自独立 Spec 的真实观察凭据，序列化核心
事件不能制造该凭据；旧 `/metadata` 路径标记不再作为授权依据。原 4096 字节限制保持不变。

元数据所有者测试的同名恢复已对照基线核验。Responses 九类控制流输入也已核验；其中失败信封携带标量
usage、失败后继续生成两个输入最初暴露真实回归，在主代理修复后通过，原输入与断言未删除或放宽。

本轮十项审查的组合专项与 race 已通过；随后对四个 `DraftMigration` 的原 helper 进行了下述逐条核对。

## DraftMigration 原始输入核对

`draft_*_audit_test.go` 使用原 helper 的输入字面量和当前发布包，独立检查所有者合并与冲突、
原生字段编辑、缓存和推理余项、tracking/media、计量及资源容量。没有调用旧 Module 或把旧运行输出
作为预期。四个同名入口原有的 119 个静态 wire 样本和 27 个边界样本继续保留。

- Anthropic：14 个原始有效推理输入、9 个非法输入、3 类原生字段编辑、3 种原始 owner 值，以及
  3 MiB 媒体／1,800 块 native-free 重建。新增预算／effort 同值冲突与空／null output_config 对称边界。
- Chat：4 种缓存余项及字段编辑、4 种 owner 合并、13 个 typed/legacy 推理组合、16 个 cache usage
  输入、9 种收集选项、原始具名工具选择与 5 个媒体 URI 边界。
- Gemini：11 个有效／5 个非法推理输入、2 种核心编辑、2 个核心冲突、4 种缓存余项与 system owner、
  3 种 owner 容器，以及 6 种 generationConfig 合并／冲突。
- Responses：3 种响应扩展 × 3 种输出方式、3 个非法元数据类型、15 种 model/owner 组合、3 种非法
  owner 容器、4 种缓存余项及模型编辑，以及原始 base64 图片。
- Chat／Responses 共用的 16 个 tracking 输入和带空白的 null 也保留。退役的跨协议包装在 typed 和 raw
  入口均明确拒绝；当前 canonical 身份的有效输入由现有 tracking identity 测试独立覆盖。

表示变化有明确边界：Responses 输入字符串、Anthropic 消息字符串在去掉根 Native 后采用核心消息
布局；Gemini systemInstruction 的冗余 role 按既有来源声明省略，vendor 元数据保留。原生未改写输出
仍保留原字节。Anthropic 的未知推理余项现在只有 `wire:claude` 一个所有者，替换该 owner 是编辑；
不再制造旧双份 wrapper 的虚假碰撞，但重复 canonical 字段仍拒绝。非法 model 值不能通过最终请求
Spec，这与单独调用早期编码器接受任意 JSON 的旧比较方式区分开来。

本轮输入恢复暴露了真实 Mapping 回归：空／null prompt_cache_options 丢失、Gemini generationConfig
owner 被静默忽略，以及 Anthropic 空 thinking/output_config 与已知控制共存时丢失。主代理在各自
Mapping 修复，新增断言保留这些原始输入。Responses 静态边界的重建预期同步保留原输入的缓存标记，
没有改变原 wire 或科学计数法的拼写。

四个 DraftMigration、全部新增审查测试及其 tracking／system role／大请求关联测试通过，专项 race
通过（28.717s）。退役清单现在为 112 项恢复、9 项独立替代、35 项辅助声明移除，待审项为零。
辅助声明的移除理由仍与其调用测试覆盖分别记录。容量基准的 128／512／2,048 原负载已由主代理验证；
这些结果不替代完整工程、server race 和 D0 性能验收。
