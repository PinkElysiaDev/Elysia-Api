# DSL 能力迁移清单（dev.100）

核对基线：`f06648a8`，实现提交 `15f5fa07`，编译器 `dev.100`。dev.101 的已验证增量单列于下方。**生产四预置仍走旧执行链，尚未达到默认切换条件。**

“已验证”仅指列出的样例和边界；“未闭合”表示仍须迁移或取得证据。119 个 JSON 样例是组件及声明证据，不是完整转换能力、组合调用或性能验收。过程与原始记录见[阶段报告](protocol-dsl-closure-2026-10-31.md)。

## dev.101 增量

Responses 源已新增普通 `keepalive`、`response.failed` 和扁平/嵌套 `error`；复用 JSON 错误载荷解码，保留错误前置次序、未知字段及真实终态。见[控制与失败源回归](../backend/protocol/builtin/responses_control_package_test.go)。四目标错误重建仍使用旧目标作为对照，不能解读为新 Responses 目标已实现。Spec 的 `sequenceReason` 保留旧序号错误文字，不新增序号状态。

本增量的全后端测试与 vet 通过；预算探针使用真实来源能力后仍在新 wire 的 `/state` 提前拒绝。`SSE-Keep-Alive:true` 哨兵、完整流/策略与正式性能门槛没有因此关闭。下表继续保留 dev.100 的逐项基线，避免把新增源切片外推成完整目标能力。

## 四协议请求与响应

旧入口共用 [module.decodeRequest / encodeRequestOutput](../backend/protocol/builtin/request.go) 和 [module.decodeResponse / encodeResponse](../backend/protocol/builtin/response.go)，再按旧 codec 选择处理。下表中的 `decode_* / encode_*` 均指 Mapping 的 `directions`；Spec 的 `upstream-request / client-response` 约束实际最终正文，不等同于构造文档。

| 范围与旧入口 | 新两源声明及关键表达式 | 已验证的主要证据 | 未闭合 |
|---|---|---|---|
| Chat 请求：`decodeRequest / encodeRequestOutput` | [Spec][cs] 的 `request / generation-request / upstream-request`；[Mapping][cm] 的 `decode-request-message / encode-messages / decode-reasoning / encode-user`，请求缓存及工具声明 | [Chat 迁移][ct]：历史、媒体、工具、只改模型及无根 Native 编码；[最终请求][frt]：工具邻接、角色、stream_options、max-token 别名冲突及局部首错 | 全树解码后调用 ID 去重、深层内容及其余最终动态约束；根 owner 拒绝诊断尚不等价 |
| Chat 响应：`decodeResponse / encodeResponse` | [Spec][cs] 的 `provider-response / response-candidate / client-response`；[Mapping][cm] 的 `decode-response-message / response-groups / decode-usage / encode-generated-response / encode-failure` | [Chat 迁移][ct] 的响应分组；[错误包络][eft]；[最终响应][fst] 的类型、整数拼写、顺序和错误包络 | 全量 metadata/未知 owner 组合、全部多故障首错及底层错误树不能由这些样例推定闭合 |
| Responses 请求：`decodeRequest / encodeRequestOutput` | [Spec][rs] 的请求及最终文档；[Mapping][rm] 的 `decode-request-item / encode-request-item / decode-reasoning / encode-reasoning / encode-user` | [Responses 迁移][rt]：分组历史、reasoning、媒体和重建；[最终请求][frt]：input 形态、角色、历史 reasoning `content:null` | 全树工具调用/结果关联、参数与媒体等深层最终约束；来源变化与未闭合诊断须分别处理 |
| Responses 响应：`decodeResponse / encodeResponse` | [Spec][rs] 的响应及最终文档；[Mapping][rm] 的 `decode-response-document / decode-response-item / decode-response-metadata / decode-tool-usage-details / encode-response-envelope / encode-failure` | [Responses 迁移][rt]：metadata 在 Native 关闭时的来源、派生文本；[工具 usage][rut]：八类 hosted-tool 计数及余项；[错误包络][eft]、[最终响应][fst] | 全部 metadata/error 组合首错、非法类型底层错误文字/链仍有差异；不把 JSON 能力外推至流终态 |
| Anthropic 请求：`decodeRequest / encodeRequestOutput` | [Spec][as] 的请求及最终文档；[Mapping][am] 的 `decode-request-content / encode-messages / decode-request-reasoning / encode-thinking / encode-output-config / decode-history-retention` | [Anthropic 迁移][at]：30 个历史 JSON 样例、3 MiB 与约 1,800 节点夹具、无根 Native 重建；[历史 owner][aht]；[最终请求][frt] 的 thinking/output_config 局部约束 | 全树工具关联、其余深层最终约束；historyRetention 只识别已声明的完整 keep-all 形态，不能吞掉其他 context_management |
| Anthropic 响应：`decodeResponse / encodeResponse` | [Spec][as] 的响应及最终文档；[Mapping][am] 的 `decode-response-content / encode-response-content / encode-bound-usage / encode-failure` | [Anthropic 迁移][at]、[块协议错误][bet]、[最终响应][fst]；完整交付样例包含 usage 和必要 thinking 签名 | 完整未知字段组合、错误链和多故障次序仍需逐项核对；组件能编码不等于最终可交付 |
| Gemini 请求：`decodeRequest / encodeRequestOutput` | [Spec][gs] 的请求及最终文档；[Mapping][gm] 的 `decode-messages / decode-system / decode-tools / encode-contents / encode-system-envelope / encode-thinking` | [Gemini 迁移][gt]：工具分区、systemInstruction 未知包装、thinkingConfig、原生/改模型/重建 | 最终文档仍缺隐式工具身份、同名歧义和全树关联；完整参数/媒体约束及首错未闭合 |
| Gemini 响应：`decodeResponse / encodeResponse` | [Spec][gs] 的响应及最终文档；[Mapping][gm] 的 `decode-response-content / encode-candidates / decode-usage / encode-failure` | [Gemini 迁移][gt]、[块协议错误][bet]、[最终响应][fst]：候选、parts、签名、错误包络 | 来源表示批准不覆盖其他诊断差异；尚无完整组合能力及真实流验收 |

最终请求的四个**已实证差异**保存在 `TestPackageFinalRequestRemainingConstraints`：Chat 跨轮复用解码后 ID、Responses/Anthropic 孤立工具结果、Gemini 无匹配调用的结果，旧最终校验拒绝而新结构文档暂时接受。测试记录这些缺口，并没有使其成为可接受的生产行为。详细边界见[最终请求](../backend/protocol/builtin/packages/json-final-request-contracts.md)和[最终响应](../backend/protocol/builtin/packages/json-final-response-contracts.md)说明。

## 真实 SSE

旧源入口为 `streamModule.DecodeEvents` 后的各 `decode*Frame`；旧目标入口为 [EncodeEventFrames](../backend/protocol/builtin/stream.go) → [encodeEvent / encodeItem / encodeDelta](../backend/protocol/builtin/stream_render.go) 及 [renderMetadata](../backend/protocol/builtin/stream_metadata.go)。新源通过 Spec 当前帧合同及 Mapping `decode_event`，新目标通过 `encode_event`、`frameBatch` 和最终 wire 合同。两边的状态与验收职责不能互相替代。

| 范围与旧入口 | 新两源声明及关键事件/表达式 | 已验证的主要证据 | 未闭合 |
|---|---|---|---|
| Chat 源：[decodeChatFrame](../backend/protocol/builtin/stream.go) | [Spec][cs] `provider-event.stream` 的 `failure / chunk`、数组动作、隐式开始、稀疏身份；[Mapping][cm] `decode_event`、`stream-decode-usage` | [Chat 流][cst] `TestChatPackageSourceStreamMigration`：原四组源样例、metadata/概率输入、晚到 usage 和工具身份 | 完整异常首错、所有 metadata 保留及组合预算边界 |
| Chat 目标：`encodeEvent` 的 Chat 分支 | [Spec][cs] `client-event`；[Mapping][cm] `encode-chat-stream-item / encode-chat-stream-metadata / encode-chat-stream-base`、finalize/observe | [Chat 流][cst] 的目标、ClientOutput、annotations 原始 JSON 去重、native_finish_reason、一次原生回放 | **目标 logprobs 仍显式拒绝**；旧 metadata 检查时点与容量尚未迁完；Native 到修改后的所有混合轨迹未闭合 |
| Anthropic 源：[decodeAnthropicFrame](../backend/protocol/builtin/stream.go) | [Spec][as] `provider-event` 的 message/block/delta/stop/ping；[Mapping][am] `decode_event`、保存来源的索引、增量状态 | [Anthropic 源流][ast]：文本、工具、thinking、加密块、稀疏身份及分片签名的真实可信来源 | 完整源 metadata/usage、复杂未知容器来源、异常组合首错 |
| Anthropic 目标：`encodeEvent` 的 Anthropic 分支 | [Spec][as] `event-candidate / client-event`；[Mapping][am] `encode-stream-item / encode-stream-metadata / prepare-stream-item / read-resumed-stream-item` | [Anthropic 目标流][att]：五组轨迹、晚到工具身份、文本快照只补后缀、自动结束工具、usage/metadata；[原生接续][ntt] | 工具/thinking 的 Native 混合接续未启用；完整多故障与极端索引边界未闭合 |
| Responses 源：[decodeResponsesFrame](../backend/protocol/builtin/stream_responses.go) | [Spec][rs] `provider-event` 的 created、output_item、function_call_arguments、content_part、output_text、refusal、completed、普通 in_progress；[Mapping][rm] `decode-stream-content-part / finish-stream-message / merge-progress-usage`，`progressUsage` 槽 | [工具流][rst]、[文本流][rtt]、[普通进度][rpt]：父子关系、快照、空消息、真实终态、晚到/工具 usage、未知余项与替换 | safety_buffering、派生 output_text 完整响应校验、billing/attribution、完整 metadata/turn-state、reasoning、annotation/logprobs；组合资源边界及终态多故障首错已知不等价 |
| Responses 目标：`encodeEvent` 的 Responses 分支 | [Spec][rs] 尚无 `client-event`；[Mapping][rm] 尚无 `encode_event` | 无目标迁移验收；工具源转 Chat 的对照不代表 Responses 目标完成 | **真实目标编码尚未迁移**，含动态序号、metadata、工具/reasoning 输出与终态 |
| Gemini 源：[decodeGeminiFrame](../backend/protocol/builtin/stream_gemini.go) | [Spec][gs] 尚无事件文档/stream operation；[Mapping][gm] 尚无 `decode_event` | JSON 及通用流原语测试均不能充当 Gemini SSE 证据 | **真实 SSE 声明未迁移**；错误优先级、身份变化、缺失/null、usage 尾帧、工具/签名与 reasoning 交错均待核对 |
| Gemini 目标：`encodeEvent` 的 Gemini 分支 | [Spec][gs] 尚无 `client-event`；[Mapping][gm] 尚无 `encode_event` | 无目标迁移验收 | **真实目标编码尚未迁移**；文本、工具、签名、usage 和终态均需接通 |

## 策略与实际调用入口

| 旧入口/职责 | 已接入的新声明或入口 | 证据与剩余边界 |
|---|---|---|
| 四协议 `reasoningControls`、模型能力解析与输出上限 | 四份 Mapping 的 `modelPolicy`、`model-control-*`、`model-normalize-*`，通用 `model_controls`；保留 `reasoning-controls` 规则 ID、顺序 175、用户覆盖和最终检查 | [模型策略差分][mpt] 覆盖 63 个模型分支、52 个输出限额场景及 thinking 投影/后置规则。不是所有转换策略均已配置化 |
| `requestProjection` 的缓存与历史处置 | 四份 Mapping 的缓存双向分区、effects；Anthropic `decode-history-retention`，Gemini default-all，Chat/Responses reject | [历史投影][hpt]、[Anthropic owner][aht] 及通用缓存测试；见[历史说明](../backend/protocol/builtin/packages/history-target-projection.md)。历史矩阵隔离 request-projection，不能代替完整默认策略组合 |
| 旧 renderer 的未知协议字段保护 | 四份 Mapping 的 `has-foreign-wire-owner` / `foreign-wire-owner` | [owner 测试][fot] 只证明根 parameters 拒绝。新 `conversion_rejected / mapping.effects / /parameters` 与旧码/位置/首错不同；节点、工具、响应/事件 owner 仍须分别闭合 |
| 四协议 Agent 请求准备、工具第二轮、模型目录 | Mapping `agent / modelSamples / operations.models`；Spec 对应端点；离线回译接入真实默认转换策略 | [Agent 与模型目录][agt] 对照参数、工具历史、输入隔离与回译；[Agent 管理](../backend/server/agent_protocol_v2_test.go)。真实流 Agent、续传、任务、双向会话的四协议组合未完整验收 |
| 管理保存/验证/激活、CLI、预览 | 统一编译 `{schemaVersion:3,spec,mapping}`；`CompileSources` 与整包身份一致；两源共同产生一个修订/报告/CAS 激活；WebUI 两源编辑及整包保存 | [源编译](../backend/protocol/package_sources_test.go)、[真实 SQLite 生命周期](../backend/storage/protocol_package_test.go)、[WebUI](../packages/webui/tests/protocol-v2.spec.ts)。管理通路已接入，但四预置没有切换；旧格式仍可执行，最终退役尚未实施 |

剩余供应商取舍不能仅凭已有 modelPolicy 判为迁完。例如 `conversion_context.go`、`conversion_projection.go`、`conversion_gemini_safety.go`、`conversion_response_model.go` 及事件 metadata 路径仍须按实际入口核对迁移；已有通用 task/session 夹具也不能证明四预置支持了它们。不得为满足清单新增旧版本本不支持的供应商 API。

## 保真、拒绝与资源门槛

| 门槛 | 当前证据 | 下一步关闭条件 |
|---|---|---|
| 原生与重建 | 四 JSON 迁移测试覆盖原 Native、只改模型、删除根 Native；Chat/Gemini 来源经批准改为真实原文位置和片段；通用来源/Native 测试限制 scope、映射身份及所有权 | 所有新增字段/流同时验证重建与原生路径；修订/scope/映射变化不能借旧快照绕过处置。保留来源表示差异，不把其他错误变化归入批准项 |
| Anthropic 原生文本接续 | dev.99 获批的窄变化：绑定同一目标、scope、映射且前缀已成功交付时，读取真实块位置及摘要后接续修改文本；测试保留索引 7，随后新项为 8。未绑定编码器仍保留旧失败 | 不能外推到工具/thinking 混合接续；不能取消最终 wire 校验或在发送失败时推进交付位置 |
| 首错与诊断 | Chat 邻接/根 controls、JSON 最终响应及 Responses 普通进度已有顺序对照；错误包络保留未闭合错误树差异 | Responses 终态同时改变 phase 与 text，旧先查 message metadata、新先查子内容；完整 metadata/error、多 owner 与深层请求首错仍须关闭。来源表示批准不授权调整业务首错 |
| 条目容量 | Anthropic 按需接续对照 dev.96：默认预算下 4095/4096 块成功，4097 同在源 `state_items` 拒绝；64 条 Native-only 回归。Responses StateItems=64 的 48 个空消息对照 | 这些是指定负载边界，不是全流容量等价；未接入的 Chat logprobs 计量草稿不计为已完成 |
| Responses 字节容量 | **实测阻断**：相同 8 MiB 限额，2 MiB 合成响应 ID + 5 MiB 未知 usage，旧源解码、语义重放、真实终态及 EOF 成功；新独立 wire 校验在 `/state` 提前拒绝 | 对齐身份与正文的原独立预算及所有权；不能调大限额或跳过 wire。累计槽节点限制与旧 usage 字节限制的差别目前仅为代码推断，仍需验证 |
| 正式性能与回收 | dev.100 尚无功能完整版本的五对 HTTP/组件结果、正式火焰图与全矩阵保留量验收 | 完成流及组合后再测三版本；确认稳定回退阻止切换。旧 12 秒 profile、历史组件收益和开始时的六目标产物不充当本版本结论 |

## 已有验证与切换前清单

dev.100 的 `closure-dev100-go-all` 全后端、vet、相关 race 通过。固定 staticcheck v0.8.2 仍以既有 45 条诊断退出 1，与 dev.99 归一化对比新增/移除均为零；不能写成 staticcheck 全净。管理阶段页面记录为 3 通过、2 个依赖真实后端的既有用例跳过；本阶段没有新增 WebUI 运行验收。

- [ ] 闭合四协议请求最终动态约束、未批准诊断差异和多故障首错。
- [ ] 补齐 Chat 目标 logprobs、Anthropic 剩余来源/metadata、Responses 源及目标、Gemini 双向真实流。
- [ ] 对齐已知资源边界，验证原生/改写、取消、慢交付与完整组合调用。
- [ ] 取得功能完整版本与优化版本的可复现配对数据、匹配二进制 profile 和保留量结果。
- [ ] 完成最终审计、前端/页面、六目标构建及平台运行范围记录。
- [ ] 四包证据满足后显式切换预置；保留历史可读性、禁止旧格式执行，删除旧生产 codec 路径。不得在线双跑或失败后换引擎。

[cs]: ../backend/protocol/builtin/packages/openai-chat/spec.json
[cm]: ../backend/protocol/builtin/packages/openai-chat/mapping.json
[rs]: ../backend/protocol/builtin/packages/openai-responses/spec.json
[rm]: ../backend/protocol/builtin/packages/openai-responses/mapping.json
[as]: ../backend/protocol/builtin/packages/anthropic/spec.json
[am]: ../backend/protocol/builtin/packages/anthropic/mapping.json
[gs]: ../backend/protocol/builtin/packages/google-generate-content/spec.json
[gm]: ../backend/protocol/builtin/packages/google-generate-content/mapping.json
[ct]: ../backend/protocol/builtin/chat_package_migration_test.go
[rt]: ../backend/protocol/builtin/responses_package_migration_test.go
[at]: ../backend/protocol/builtin/anthropic_package_migration_test.go
[gt]: ../backend/protocol/builtin/gemini_package_migration_test.go
[frt]: ../backend/protocol/builtin/package_final_request_test.go
[fst]: ../backend/protocol/builtin/package_final_response_test.go
[eft]: ../backend/protocol/builtin/responses_package_failure_test.go
[bet]: ../backend/protocol/builtin/block_package_failure_test.go
[rut]: ../backend/protocol/builtin/responses_package_tool_usage_test.go
[aht]: ../backend/protocol/builtin/anthropic_history_package_migration_test.go
[cst]: ../backend/protocol/builtin/chat_stream_package_migration_test.go
[ast]: ../backend/protocol/builtin/anthropic_stream_package_migration_test.go
[att]: ../backend/protocol/builtin/anthropic_stream_target_migration_test.go
[ntt]: ../backend/protocol/builtin/native_transition_migration_test.go
[rst]: ../backend/protocol/builtin/responses_stream_package_migration_test.go
[rtt]: ../backend/protocol/builtin/responses_text_stream_package_test.go
[rpt]: ../backend/protocol/builtin/responses_progress_package_test.go
[mpt]: ../backend/protocol/builtin/model_policy_package_migration_test.go
[hpt]: ../backend/protocol/builtin/history_package_projection_test.go
[fot]: ../backend/protocol/builtin/package_foreign_owner_test.go
[agt]: ../backend/protocol/builtin/agent_package_migration_test.go
