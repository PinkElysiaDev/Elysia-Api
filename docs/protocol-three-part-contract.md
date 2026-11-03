# 协议包 v4 与 DSL v2 契约

本规范取代迁移期间对旧引擎内部行为的等价要求。正确性依据是外部协议语义和具名客户端兼容声明；旧引擎只作为独立性能参考。实现进度见[实施记录](protocol-three-part-implementation-2026-11-01.md)，本规范中的目标不代表已经全部上线。

## 三部分与发布单位

- DSL 提供读取、结构操作、关联、增量、状态、编码、计量和诊断原语，不根据供应商名选择语义。
- `spec.json` 描述外部端点、文档、实体关系、事件顺序和生命周期；不负责核心映射或跨协议取舍。
- `mapping.json` 声明双向核心映射、来源规范化、目标投影、模型策略和样例。

两份源以 `{ "schemaVersion": 4, "spec": ..., "mapping": ... }` 形成单一发布包。Mapping 的 `dslVersion` 为 2，`coreVersion` 为 2；转换策略的 `schemaVersion` 为 2。保存、验证、激活以整包为单位，共同确定修订哈希；请求固定编译快照，不解析配置。

Spec 和 Mapping 各有自己的 `expressions` 符号表。命名程序按静态输入、词法槽位和权限编译复用，执行值与可变状态不共享。引用闭包进入实际使用方向的指纹。循环、未定义或不可达引用，以及深度超限，在编译时拒绝。

Spec 的 `constraints` 是独立的具名断言组。文档或事件用 `{ "ref": "名称", "path": "/子文档" }` 调用；省略 path 表示当前输入，root 始终指向原文档。相同静态作用域共享已编译检查，不展开整份 JSON，也不共享执行状态。引用不能覆盖组内检查；循环、未定义、不可达和超深引用在编译时拒绝。约束及其表达式依赖共同进入使用文档的指纹。

结构断言可省略 `code`，由真实边界归因：入站请求为 `invalid_input`，上游响应／事件为 `upstream_contract_violation`，出站为 `conversion_rejected`。显式声明的关联、能力及其他业务诊断保留；错误位置始终包含调用处的实际数组索引和子路径。这样同一外部结构可用于请求历史、JSON 响应及流快照，无须复制不同错误码的整套 schema。

## 预置端点与基地址

新四预置以供应商 origin 作为 `baseURL` 示例，Spec 声明包含 API 版本的完整路径。同一个 API 的 JSON 和 SSE 使用同一路径。

| 预置 | baseURL 示例 | 生成路径 | 模型目录路径 |
|---|---|---|---|
| Chat Completions | `https://api.openai.com` | `/v1/chat/completions` | `/v1/models` |
| Responses | `https://api.openai.com` | `/v1/responses` | `/v1/models` |
| Anthropic | `https://api.anthropic.com` | `/v1/messages` | `/v1/models` |
| Gemini | `https://generativelanguage.googleapis.com` | `/v1beta/models/{model}:generateContent`；流式为 `:streamGenerateContent` | `/v1beta/models` |

通用传输仍按配置的 `baseURL + operation.path` 拼接，不自动删除或去重 `/v1`、`/v1beta`。自定义网关前缀由其显式配置决定。已有 baseURL 包含 API 版本前缀的来源，必须在选择新修订时同步明确新基地址或自定义路径；不自动改写旧配置、绑定或任务。旧预置的相对 API-root 路径约定继续属于旧修订历史。

## 检查与所有权

执行链为读取与外部校验、核心映射、目标规则、编码与必要最终校验、交付。

一次不可变输入可共享有界字段索引和只读视图。同一输入、同一合同和限额的成功校验可以复用；失败与取消不能缓存成成功，帧间不保留读取缓存。同步校验可借用字符串，进入核心或流状态的小字段必须取得独立所有权。

外部生命周期与转换后的核心生命周期检查不同对象，不能互相替代。配置编译能够证明的静态条件不在执行时重复验证；用户修改的输出根据影响范围验证，无法确定范围时执行一次完整最终校验。内部已证明条件直接使用；外部输入、配置、权限、资源和 I/O 错误返回结构化诊断。

流状态归属单一流及 Scope。声明槽位在编译时绑定；有序 `state_update` 的右侧读取同一前置快照，后续动作可读取其结果，帧失败不提交槽位。工具参数、文本和数组按实际需要增量保存；不反复编码完整前缀。交付位置只在对应帧成功发送后推进。

Spec 的流合同声明外部 `namespaces`、`entities` 及父子关系；实体载荷可以是文本、JSON 或数组，不以核心 NodeKind/EventReplay 充当外部合同。数组增量只检查新增元素，完整快照校对已观察前缀。合同失败后该实例不可继续接收或提交交付。相同文档可用 `base` 复用已编译结构与生命周期，仅附加断言；不能覆盖基文档，也不能循环引用。

Mapping 的 `buffer_text`／`buffer_array` 按固定通道和项目身份保存必要增量，支持 append、snapshot、read、length、release。append 返回新增部分；snapshot 拒绝改变既有前缀；read 才物化完整结果。文本状态不逐帧保存微小片段对象，数组状态保留独立元素并共享不可变输出叶子。

只需识别已发送数组前缀时，使用 `track_array`：delta 追加成员摘要，snapshot 校对既有语义前缀并返回新增后缀。状态只保存固定摘要和成员数，不保存完整数组；JSON 数值按精确十进制比较，不转为浮点数。它不能代替需要最终完整数组的收集器。

`resume_text`／`resume_tool` 从实际已交付的外部实体接续语义输出，核对当前 Scope 和开放实体，不从普通上下文 JSON 恢复发送位置。Spec 可将 JSON 实体的初始载荷显式声明为 `placeholder`；第一次真实参数更新替换该占位，空增量不会清除它，失败的帧不提交替换。

原生复合帧由 `nativeReplay: "original_frame"` 声明动态字段属于原帧，配套 observer 只同步后续重建需要的状态。`nativeWhen` 可进一步限定哪些合法来源帧适合原样交付；不满足时按正常语义重建，不以目标校验失败作为回退条件。原生回放仍检查双向映射指纹、Scope 与语义摘要。输出批次使用实际已交付位置及当前批次偏移计算序号；计算序号不提前提交交付。

`nativeWhen` 可以只读本次请求的 `runtime.clientOutput`，据客户端展示偏好选择原生交付或语义编码。dev.132 另开放只读 `runtime.encoding.hasRendered`：本方向此前是否执行过语义编码，用于声明聚合帧的重放取舍；它不是成功交付或正文等价证明。不能读取其他运行时元数据、任意流状态或交付位置。usage 收集是网关策略，客户端是否展示 usage 是独立意图。完整请求转换统一填充收集设置及缺省展示设置，显式的 `includeUsage:false` 不被缺省值覆盖；单阶段预览不重复执行完整请求转换。

`encode_request.finalCheck` 是最终 wire 与客户端意图的只读检查。它读取 `input/root` 和 `runtime.clientOutput`，不能访问流状态或执行身份。在原生协调、请求 wire 规则之后，由 `ValidateRequestOutput` 与最终 Spec 共用当前输入视图执行一次；基础 `EncodeRequestValue` 只返回编码结果。网关、Agent 收集、预览及独立／组合验证共同使用该边界。实际 wire 修改后的路由权限复核仍独立保留，普通请求不额外解码。

`ClientOutput.ResponsesStorage` 是 API 对象存储意图的唯一所有者。来源写 Raw、Requested、RequestedUnknown 和 SourceCodec，目标规则仅写 Effective／EffectiveUnknown。Chat 缺省 false，Responses 缺省 true，显式 null 保持未知；两者来源解码时目标决定尚未知。Anthropic／Gemini 无对应对象存储请求，来源声明 false；它不表示供应商不保留日志。同合同直接编码保留 Raw，已明确改变的有效决定覆盖 Raw；跨合同的有效决定必须由所选规则处理，编码器不重复执行被禁用的降级。未知 `wire:*.store` 仍是扩展，不能混同于已识别意图。

`mapping.agent.clientOutput` 使用固定字段名到 Expression 的映射，可声明 includeUsage、rawStreamOptions、rawResponsesInclude 和 responsesStorage，不能写宿主计量 CollectUsage。完整 Agent 准备保留未声明的调用者字段，声明的 storage 替换为新的完整意图；本地 Agent 的 storage 为 false，SourceCodec 留空。样例的 `expectedClientOutput` 检查完整结果，包含宿主生成的流式 CollectUsage；v4 Agent 预览返回 parameters 和 clientOutput 两部分。参数单独读取不执行客户端输出策略。

需要生成输出身份的 Mapping 从 `/operation/id`、`/operation/startedAt` 读取调用者绑定的一轮执行元数据。网关复用请求记录；离线验证使用明确的合成值。DSL 不产生随机身份，也不使用缺失时的常量兜底。

任务 Mapping 仅获准读取上述两项执行事实；持久任务复用本地任务 ID 和创建时间，轮询、恢复及读取结果不会重新生成身份。health 预检与实际发送共享同一次执行事实。纯字段读取、模型目录解析和 Agent 参数策略不为此制造调用身份。

## 关联与资源

`check_links` 读取有序关联事实：`define` 注册、`resolve` 消费、`barrier` 要求此前调用已经解决。显式身份按解码后的字符串比较。`key` 要求显式匹配；`unique_group` 允许缺失身份通过唯一待处理组匹配，歧义拒绝。null 不等于缺失。重复身份、重复结果、孤立结果和前向引用拒绝。

统一资源目标：

| 限额 | 计量对象 |
|---|---|
| Depth / Nodes | 输入文档、单帧和实际物化结果 |
| StateItems | 动态关联条目；固定槽位、别名引用和重复观察不算新增语义实体 |
| BufferBytes | 单份输入/输出，以及流内实际保留的逻辑载荷；同一持有资产只计一次 |

身份只有一个持有者；usage 只累积已声明计算所需的计数。完成、失败、取消与发送结束释放相应状态。这些规则正在替换旧分散预算；未完成的路径在实施记录中单列，不能靠放大测试限额宣称通过。

## 语义取舍与诊断

已知语义进入核心。未知字段携带命名空间、来源和原始位置；满足 Native 条件时可同协议保留，跨协议没有明确映射则拒绝。默认兼容模式只允许配置声明的降级并记录诊断；严格模式拒绝语义损失。正文、工具关联、实际 usage、终态和权限约束不得静默丢弃。

默认规则由目标 Mapping 编译；用户按稳定 ID 整条替换，禁用不产生隐式替代规则。同阶段顺序冲突在编译期拒绝。规则不能关闭外部结构、权限和资源硬约束。签名、加密内容和续传控制继续受来源及 Scope 限制。

`conversionDefaults[].use` 显式绑定 `request_projection` 或 `model_policy`。前者引用 encode_request 的目标处置 effects，后者引用 modelPolicy 的目标步骤；均为独立 request 规则，不能与 steps、recovery 或辅助事件处置混用。ID、order、enabled 直接来自声明。省略声明不运行该程序，编码所需的最终约束仍然生效。来源模型规范化只在显式绑定的模型程序中接入。

有限值映射的 valueMapping 由声明核心路径和规范值决定，余项保存在独立 parameters 字段中；编译证明识别分支完整保留规范值、非识别分支完整保留原值。普通标量检查实际核心类型，Value 字段保留精确 JSON；来源、Native、资源、客户端意图以及整容器替换不属于此原语。historyRetention 只是现有默认包的一种使用，不是原语内置的唯一字段。

`core_controls` 的 `each.select` 遍历核心中已定义的 node、metadata、usage 或请求 tools。局部 `input` 指向当前条目，`root` 仍指向完整核心对象；不通过任意反射查询或重新解析完整正文执行。省略前检查原值，诊断可引用真实来源路径。规则不能改写 Source、Scope、Native 或私有来源凭据。普通 metadata 与辅助帧共用 `event-metadata` 默认规则的完整覆盖／禁用边界。

`tool` 选择仅用于请求，允许更新 `options.strict`、`options.strictDefault` 和 `options.eager_input_streaming`。来源缺省 strict 行为由 Mapping 写入 automatic／disabled，不能从 codec 名称推断。目标 `tool-options` 规则显式解决缺省差异和流式提示；严格模式拒绝已声明的损失，禁用后编码器不补做处置。工具名字、schema 和 Native 不在此选择器的写权限内。

`tool_result` 复用节点遍历，只授予 payload、status、显式消费 children，以及通过 `childrenPrefix` 插入文本前缀的权限。children 不能被任意替换；名字、callId、Native、资源和属性不可写。`tool-results / 150` 声明对象序列化、文本包装及错误标记。Responses／Anthropic 保留可表达的媒体块；Chat 只接受工具文本，Gemini 当前对象结果不消费媒体或受保护子节点。合并纯文本块前检查其附加语义，不能借拼接删除缓存、引用或扩展。

Gemini 的 error／output 包络由 Mapping 双向表达，独立错误状态在编码时无损进入 error；带保留键的成功对象进入 output，普通文本的 result 包装则必须先执行目标规则。编码不尝试解析 JSON 字符串。开发组合比较规范化已声明的 disabled 与 strict:false；automatic、true 和未知工具选项继续参与比较，不整体忽略 Options。

需要补齐目标首帧计数的事件规则显式声明 `frameUsage`。调用入口从当前原始事件批次绑定只读数字观察，按批次顺序合并；未绑定与本帧没有计数是两种状态。观察不保存原始帧、attribution 或 billing 对象。交付占位计数标为 `placeholder`，不能覆盖原始上游用量账本，也不能重新引入此前明确省略的细分字段。

来源流将普通事件、辅助事件、状态事件和纯传输控制分开声明。辅助／状态事件的权限凭据由成功的外部合同校验产生，绑定来源帧、方向和 Scope，不能通过用户 JSON 伪造。纯传输控制不生成核心事件；辅助 metadata 通过验证后不进入正文收集器；状态通过通用 `control.updated` 保留类型及原始载荷。跨协议省略只能由目标配置列出的 `auxiliary_event`／`control_event` 规则授权，并遵守兼容／严格模式；未知生成载荷不能借此省略。

核心流的 `collection` 区分 reasoning 的 summary 与 reasoning_content，保留父子关系而不伪造消息节点。metadata 的 `update: "append"` 是增量指令，收集器按段追加数组，在需要完整结果时物化；最终节点和响应不得遗留该指令。来源位置、帧序号属于输入事实，开发语义对照不将其视为正文变化；真实凭据仍核验这些事实。

核心版本 2 的 `logprobs` 使用 token 数组，归属于对应的正文或拒绝节点；Chat 的 content/refusal 外层对象只在 wire 编码时建立。Codec 标签不决定核心容器形状。缺失概率字段与显式空数组分开保留；Responses 现有客户端样例允许缺失的行为属于具名兼容范围，不称为官方字段定义要求。未识别的概率对象成员继续受原始扩展保护。

`server_tool` 表示执行责任在上游的活动，独立于需要客户端执行的 `tool_call`。已知输入、状态及产物进入对应核心字段，文件摘录不冒充完整文档，容器身份保留自身资源类型与 Scope。进度帧可使用 `item.delta` 的小型 item 头更新状态，只允许 kind、id、name、status、source，不携带文本 delta 或结构产物；文本输入继续增量追加。进度中的 completed 不结束 item，真实 `item.finished` 才释放其生命周期责任。

收集器保留服务端活动及产物供后续历史使用，不把代码、日志或检索摘录当作助手正文，不触发本地工具执行。服务端调用次数由 Mapping 写入独立的 `Usage.Details`；根据完成项推断的计数标为 inferred，上游显式报告标为 observed，包含零的显式计数优先。工具调用次数不能累加进模型 token 总量。

诊断使用真实来源位置、统一阶段和业务错误码，不包含秘密值。不保留旧错误文字、多故障首错顺序、重复身份计费或旧 renderer 的检查时点。下列旧差分要求重新分类：

| 分类 | 内容 | 新验收依据 |
|---|---|---|
| 真实缺陷 | 工具关联遗漏、隐式关联歧义、流内容/usage 丢失、终态或 EOF 错误、修改输出漏验 | 明确成功结果或结构化拒绝 |
| 客户端兼容 | 请求 reasoning `content:null`、Responses 辅助 metadata、已批准的 Native 后修改接续 | 具名声明和代表性样例 |
| 废弃要求 | 旧错误文字与首错树、模拟 `/message/0` 等非原文位置、重复身份计费、旧 logprobs 检查点 | 不再作为迁移门槛 |
| 性能要求 | 双方正确完成且工作量一致的负载 | 同机配对实测；不能比较旧版提前失败耗时 |

## 请求外部合同（dev.116）

默认 Spec 的具名约束覆盖 Gemini safetySettings、Responses truncation／previous_response_id、Anthropic metadata 以及 OpenAI metadata／user；来源、重建与最终出站引用相同合同。Gemini 类别使用解码后的字符串身份去重，同协议未知枚举和扩展保留，跨协议的可表达性仍由目标 Mapping 决定。最终用户 wire 修改由既有 ValidateRequestOutput 检查，不重新解码核心请求。

核心版本 2 不再通过共享 Go 检查这些供应商字段。默认 Mapping 在省略源追踪载体前检查其形状；自定义包可以在同名参数位置声明不同的外部类型。工具选项、关联、资源作用域与 reasoning 的核心约束继续保留。旧 codec 的校验仅留在旧核心版本路径，随旧链退役一起删除。

raw 编码不再接受数字／对象用户标识或静默忽略非对象 tracking metadata。旧错误文字及非原文位置不作为迁移门槛；非法字段的来源检查和最终出站检查分别使用输入错误和转换拒绝。

## 请求字段目标处置（dev.115）

request_field 的位置可以是独立 parameters 字段或 reasoning 的 Value 叶子。default 声明目标等价的值，只有选中值与该默认值一致时才可清除；命中条件但值不同会拒绝。reject 不声明默认值，按条件拒绝。路径和清除动作在配置编译时绑定，不能改写来源、权限、资源、客户端意图或整个容器。现有 Gemini 历史保留声明复用此通用能力，不存在供应商对之间的特例。

禁用投影规则后编码器只检查未解决的字段，不替规则执行省略。同协议 Native 仍需证明删除合同；仅清除当前核心字段不能授权恢复原始字段。没有相关字段与缓存意图时不分配执行状态。

## 缓存提示目标处置（dev.118）

四份 Mapping 通过 encode_request.effects 的 cache_intents 声明目标处置，并由显式 request-projection 规则运行。Anthropic 保留 breakpoint，OpenAI 两协议保留 request key／retention，Gemini 保留有 Scope 的资源引用。目标没有对应表示时，兼容模式只允许省略已声明的 request key、in_memory／24h retention，以及 type:ephemeral、TTL 缺失／null／5m／1h 的 breakpoint，并产生 info 诊断；严格模式拒绝这些损失。明确的 null 提示可按目标声明归一化，缺失不生成意图。

资源引用、未知策略、额外策略成员及未声明的 TTL 不进入提示省略规则。不扩大 mode、options.ttl、prewarm 的现有能力范围。禁用或替换投影规则后，编码器只拒绝残留意图，不替规则执行省略。

缓存检查与字段检查、Native 协调共享请求局部的重放准备结果。私有工作区仅为精确匹配时的复用凭据；缺失或改变时使用已有解码流程重新核验原始 wire，Scope、外部合同和认证续传约束继续生效。原始基线仍参与删除检查，不能通过清除当前 Cache 恢复已省略字段，也不能把任意政策修改作为 Native 删除证明。

## Metadata 配置边界（dev.114）

dev.117 补齐派生文本一致性：四份目标 Mapping 在保留或省略 Responses output_text 前，通过 equal_text 将其与当前核心可见文本段比较；终态事件比较完整响应，不将文本 delta 与完整派生值比较。Responses Spec 的具名 response-output-text 约束同时用于来源、重建和最终客户端输出。比较不构造正文拼接字符串，refusal、reasoning 和工具参数不混入可见文本。

核心版本 2 的普通 metadata 不查询 Go 供应商白名单；核心仍检查 logprobs、增量形式及资源边界。外部字段类型由 Spec 定义，目标 Mapping 在处置前显式选择 codec、location、name 和允许形状。四个默认包未声明的字段（包括空值）拒绝，不能落入统一兼容省略；自定义包可仅修改两份配置声明自己的字段。

enum 可声明 otherwise，只在字符串未命中时执行；省略该分支仍报未映射错误，非字符串不会转入该分支。该分支已用于默认 metadata 字段目录。编译器、schema、操作目录和编辑器使用同一声明。

## 错误字段目标处置（dev.119）

each.select:error 读取完整响应、流错误及终态响应中的当前错误对象，只允许更新 message、code、param、details、providerType。category、响应状态、事件类型、来源和扩展不可写；不能替换整份错误或把失败改成成功。错误字段选择复用控制步骤及诊断，已存在错误才读取局部对象，普通响应不触发此选择。

Anthropic／Gemini Mapping 的 response-error-details、event-error-details（顺序 225）声明跨协议已知 code／param 及 new_api_error wrapper 的处置。字符串细节保留到消息并报告结构损失，严格模式拒绝；空字符串按声明归一化。非法类型、其他 wrapper 和未知扩展不静默删除。OpenAI 目标沿用已有结构化字段编码，同协议保留原表示。禁用规则后目标编码仍拒绝未解决字段，不隐式重做降级。HTTP 状态与流生命周期由宿主和协议合同保留。

这不代表所有错误扩展都已可跨协议：未知 type、details 结构和命名空间扩展必须有明确映射，否则继续拒绝。

## 对象聚合所有权（dev.120）

aggregate.layout 按声明的请求核心路径绑定对象类型。remainder 和 fields 必须分别对应不同的 Value 成员，labels 必须对应类型一致的标量字段；不能写入来源、Native、资源或客户端意图。外部字段名及变体由 Mapping 指定，不要求 cache、ttl 或特定标签值。

编译器证明对象拆分、null 保留及无覆盖重建，追踪命名程序、局部绑定、稳定数组位置和互斥字段合并。无关条件的两个分支必须分别成立；不能用 deep merge 给已归属对象追加未声明成员。动态数组选择尚不属于此证明能力。

编码前检查剩余核心成员均有表示、标签匹配、余项为对象或 null、分拆字段不重复、固定数组所有者后没有未表示成员。成员目录及顺序在编译时建立；运行时不反射，也不重新解码整份核心。默认 Anthropic 根缓存使用该声明，wire 扩展不能重新定义 cache_control。

## 指令布局（dev.121）

ProtocolControlStep.layout 支持请求及完整响应 /content 的 stable_partition 和 combine_children。配置选择根节点并以普通控制步骤诊断、修改角色；Go 只移动既有节点或合并普通容器子节点，并处理 Native 所有权。pathBinding 是当前核心位置，movedBinding 表示相对选中组的位置改变。编译期禁止局部结构选择、整对象替换和受保护字段写入。combine_children 要求相邻同 kind、同 role，容器除 children、来源和 Native 外不得有未表示字段。

Anthropic／Gemini Mapping 显式提供 system-instruction-hoist／140。system 在对话之后上提、developer 角色降为 system、多个根进入同一系统包络均诊断并在严格模式拒绝。容器附属字段没有明确目标表达时拒绝，叶子仍保留缓存、资源及来源。已确认的 Anthropic 同合同内联 system 消息保留；其位置分类来自 Mapping。编码前检查布局已满足，禁用规则后不会隐式再次上提。

dev.123 增加 group_runs，按声明 role 将相邻选中根放进一个 message；普通 message 容器可展开，独立叶子直接移动，未选中根截断。需要展开的容器附属语义拒绝，不复制整树 JSON。单个原 message 保持不变；实际分组首步必须声明边界损失，严格模式拒绝。

Anthropic 的 assistant-history／165 选择相邻 assistant message、reasoning、tool_call，保持 thinking／文本／工具顺序及工具结果边界。编码前拒绝未解决的裸 reasoning／tool_call；禁用规则后不暗中组合。已完成的指令分区与普通容器合并继续使用原声明；Chat 一般混合布局、完整响应多根、文本合并、引用偏移和复杂 SSE 布局仍未闭合。

dev.124 的 Anthropic／Gemini response-layout／240 复用 group_runs，将完整响应中的有序 message、text、reasoning、tool_call 根放入单个 assistant 包络；实际改变诊断边界损失。目标编码 effect 检查零个或一个 assistant message，不能在规则禁用后隐式再次布局。候选附带 choiceIndex 等容器属性时拒绝组合，不将独立候选拼接。Gemini Spec 的 single-candidate 命名约束同时检查来源和最终输出，只支持零个或一个候选。Chat 多根及 SSE 状态衔接仍独立待闭合。

## 工具结果包络（dev.122）

工具结果沿用 payload 与 status，不增加供应商专属核心类型。status.payloadForm=result_envelope 表示 payload 是带独立 error／output 成员及其余数据的结果包络；isError 与 error 成员是否存在一致，显式 null 仍为存在。普通 payload 中恰有同名键不自动获得包络语义；必须由来源 Mapping 声明。未知状态成员或不一致标志拒绝。

Gemini Mapping 将单独 error／output 解包到普通 payload；两者并存或带额外成员时保留完整对象及包络标志。编码按标志直接重建对象，不挑选一个成员，也不猜测字符串里的 JSON。functionResponse 的未知成员仍属于带来源扩展，重建时与已映射字段独占合并，不能覆盖 id、name 或 response。

其他三个默认目标的 tool-results／150 明确将包络保留为序列化 payload，并延续已有错误标志／文本标记处置，诊断结构损失；严格模式拒绝，禁用规则后编码器拒绝未解决表示。Gemini 目标不需要该降级，原生和删除 Native 后重建保留 error、output、其余成员及 null。整个处置由 Mapping 表达，未新增 Go 供应商分派。

## 认证续传（dev.109）

Responses 的 include 原表示由 `ClientOutput.RawResponsesInclude` 持有；`RequireContinuationState` 表示产生受保护状态时客户端必须获得可继续使用的表示。目标规则只写 `ResponsesIncludeResolved`，不能改写来源原值或撤销续传要求。Agent 可声明原值和要求，本地收集器直接保留资源进入后续轮次。

`mapping.continuation` 声明 `extract`、`encode_response` 和 `encode_event`。提取返回 token 及真实原文位置，不在解码前删除数组成员；成员映射通过 `is_carrier` 消费已选载体，后续 Source 下标不变。宿主认证 Owner、Scope、历史及节点摘要后确认该请求；未经认证的载体不能通过删除根 Native 绕过编码检查。

出站程序只读取宿主封装的 token/ID 和已有流交付位置。只读 `emitted` 从最终输出观察 token，与最终 Spec 校验共享输入视图；它不能读取待发 token 集合并自报成功。流式写入及 Flush 成功后推进交付；JSON 等 HTTP completion。没有待发 token 时不执行载体程序。

`conversionDefaults[].recovery` 只声明 signature/encrypted_content 的保留条件及无恢复通道时的处理，仍按稳定规则 ID 覆盖或禁用。加密及认证由宿主实现，配置和普通 context 不能伪造恢复证明。服务端 Persist 与客户端交付分开；明确的客户端要求不能仅靠 Persist 满足。已识别保留前缀的认证失败是请求错误，不作为兼容省略处理。

上述为当前实现合同，验收状态见实施记录；不表示生产预置已经切换。

## 新版本边界

新版本使用隔离的新数据库验收，不提供旧格式、旧绑定或旧任务的升级迁移。最终生产链只执行当前包格式；现有数据库、日志和私人正文不删除、不修改。新版本自身的修订、热更新和激活事务继续保留。

切换必须等待四协议实际调用、配置化策略和性能门槛通过；不在线双跑，不失败换引擎。跨平台构建与实际平台运行分别记录。

## Chat 声明式布局（dev.125）

默认 Mapping 的 tool-result-history／235、assistant-history／240、history-text／250
明确处理混合工具历史和同类文本、reasoning、refusal。response-layout／240 和
response-text／250 明确处理完整响应多根及文本合并。编码器不再隐式分组、展开或选择
第一个 refusal；JSON 独立候选保持独立，空响应保留一个空 assistant choice。

通用 unfold_children、merge_text 与 utf16_length 不解释供应商字段。引用结构、长度
口径、附属数据聚合及损失诊断由 Mapping 声明。未处理的身份、资源、缓存及扩展拒绝合并。
annotations 的外部类型由 Spec 声明，已知值进入核心 metadata；输出保留原文位置、
所属内容、条目顺序以及已声明的 null／空数组形式。

回归使用显式目标规则和手写合法输出期望，替代旧测试对隐式分组、非法响应数组和旧错误
文字的要求。仍直接断言关联、引用、Native 重建、输入隔离及最终 wire 合同。
这批只闭合 JSON 布局；复杂 SSE、生产默认切换及性能门槛继续独立验收。

## 流式位置区间（dev.126）

track_position 与 project_range 只保存来源位置到输出位置的对应区间；长度单位、来源身份、
输出通道和引用字段均由 Mapping 声明。Chat 使用 utf16_length 计量 track_text 返回的新后缀，
原生 observer 与重建共用相同程序。去重使用原始 metadata，只有实际编码时换算引用位置。
连续追加合并区间，拒绝超过已观察正文或横跨其他 item 输出的引用。

本批修正已有顺序 item 输出的引用偏移，不放开交错 item、独立 message 或多候选边界。
这些结构的损失授权和严格／禁用行为仍需下一切片闭合。区间事务成功不代表网络发送成功，
实际交付位置仍由原有 Write／Flush 成功边界提交。

## 流式布局处置（dev.127）

Mapping 的 `streamDisposition` 和目标事件 `require_disposition` 将流中实际发生的损失
交给已解析策略授权。规则按稳定 ID 整条覆盖，禁用不会回填默认规则。声明在编译时绑定，
执行只查询当前事务请求的规则；输出编码前解析授权，成功诊断随事务提交。

Chat 的 `event-layout / 310` 允许兼容模式合并同一回答的多个 message 和同类内容块，
保留事件到达顺序、工具关联和语义通道。strict 或禁用在首次实际边界损失时拒绝。
未处理的容器 metadata、缓存、资源、独立候选和无法精确表示的引用范围仍拒绝。
这项授权不改变 Spec 的原始事件检查或 Write／Flush 后才提交的实际交付位置。

## 完整响应的引用交付证明（dev.128）

Mapping 的 `directions.encode_response.deliveryRanges` 通过已有 Expression 声明文本及引用关联。
每个声明包含唯一 `name`、字符串计量 `measure`、`texts` 和 `ranges`。文本记录为
`key/text/mode/path`，引用记录为 `key/id/start/end/path`；key 绑定同一文本，id 区分该文本的引用。
`mode` 为 append 或 snapshot，完整响应按一份完整文本检查。选择程序只读当前 wire，不能读取
宿主 operation、修改状态或取得资源证明。外部字段类型仍由 Spec 检查。

Chat 和 Responses 使用 `utf16_length`，供应商字段及引用索引路径全部由各自 Mapping 指定。
宿主 `ValidateResponseOutput(before, after, options)` 统一最终 Spec、引用和续传载体检查，
共享当前不可变输入视图。网关对新包 wire 修改后的完整语义再解码已移除；组合验证和 wire
响应预览调用同一边界。纯映射样例仍只验证其所选方向的构造合同，不提前套用交付合同。
验证成功仍不代表已完成网络交付。

实际改写只保留 Unicode 字符边界上相同的前缀与后缀，中间不做通用 diff。
等长不能证明内容相同；引用跨改写区、跨插入内容或失去稳定关联时拒绝。
长度变化后的未改写后缀保留确定偏移，但 wire 规则必须已给出正确最终索引，边界不会自动修正。
没有引用的响应不读取文本观察记录；借用字符串只用于同步比较，结果不持有正文副本。

dev.128 仅接通完整 JSON 响应。dev.130 接通下述 SSE 边界；语义事件修改事实仍待完成，不能据此宣称
全部文本改写边界或功能完整版本 B 已完成。

## 失败交付关闭（dev.129）

`WireStreamValidation.AbortDelivery` 和 `ContinuationBinding.AbortDelivery` 由宿主在 Write／Flush
失败时调用，撤销本批待提交状态，关闭后续消费、恢复和提交。已成功提交的前序位置与认证资源
持久化事实不回滚。网关保留首次传输失败，再次 emit 直接返回同一失败，不再次调用写入或 Flush。
终止标记的写入及最终 Flush 同样进入这一边界；不增加发送或 Flush 次数。

Flush 成功后先提交交付事实，即使随后观察到取消也不撤销。失败关闭不替代最终检查；调用者
仍必须按既有顺序验证、发送、Flush、CommitDelivery。源失败、交付失败及后续结算错误继续
使用各自分类和已有主次因处理。完整多帧载体批次和 Native 接续组合仍待后续验收。

## SSE 最终输出的引用交付证明（dev.130）

`directions.encode_event.deliveryRanges` 与 JSON 共用只读声明。Chat 选择 delta content／annotations，
Responses 选择可见文本增量、完成快照、content part、message 和 terminal 中的文本及引用；reasoning
与其他附属语义不被猜测为可见文本。供应商路径和 UTF-16 口径均在 Mapping 中。

`ConsumeTransformed(before, after, options)` 共用实际帧输入视图完成最终 Spec、生命周期、引用和续传
检查。网关、组合验证及完整 wire 事件预览使用同一函数；预览显式模拟 CommitDelivery 并检查终态。
新包网关不再在 wire 规则后完整 DecodeFrame，也不再为该重复解码创建另一份映射状态。

来源和目标的已交付文本用摘要及位置保存。未改写路径共用摘要和隐式连续区间；改写只保留字符边界上
相同前后片段。每批仅暂存新增文本和可变尾区间，历史区间不复制。晚到或重复引用按原 key／id
寻找已证明区间，最终索引必须匹配；snapshot 不允许改写已交付前缀，无引用增量仍可改写。

宿主在所有 Write 及 Flush 成功后调用 CommitDelivery。失败关闭当前输出，不提交本批证明；Release
释放观察。续传扩展在同一 sealed batch 和相同已提交位置上应用同一个只读程序，获得逐帧 before／after
配对；无修改只执行一次，不假设原始帧位于载体批次末尾。wire 修改改变载体边界或输出数量时拒绝。

dev.133 增加下述语义事件证明。静态 wire 影响路径分析、旧生产链退役、B／C 归档及正式性能
仍待后续批次，不能把本切片标记为总目标完成。

## 语义事件引用证明（dev.133）

`directions.encode_event.semanticRanges` 使用同一 name／measure／texts／ranges 声明，绑定受限核心
事件投影。只能读取类型、关联标识、delta、metadata 及 item 的 kind／payload／metadata；不能读取
Source、资源、权限或流状态。只允许 frameBatch 目标事件程序，schema 和编辑器帮助同步提供字段。

Conversion.Event 在相关字段实际改变时保留不可由 JSON 提供的原始投影；EncodeConvertedFrame
也可从原 frame 与一对一事件取得这一事实。目标重建与 Native 观察共用编码事务和未改写区间算法，
正文不变时不创建原始副本，改写时只比较当前片段。事件编码后仅保留摘要、区间和独立身份，
不持有完整原帧或已发送正文。最终 wire 检查与成功 Flush 后的 CommitDelivery 继续独立负责交付事实。

Chat／Responses 配置选择引用所属核心 item 并使用 UTF-16。等长替换、跨改写区引用和未修正的后缀
索引拒绝；未改写前后缀可保留，无引用改写允许。

dev.137 扩展到 `encode_response.semanticRanges`：响应投影包含 id、metadata 和 content 树，
节点仅暴露 id、kind、payload、metadata、children、reasoningContent。实际修改才保存宿主投影，
连续转换沿用最初投影；新增完整响应及无法关联的批次不能把新增内容作为自己的修改证明。
`ValidateResponseReferences`／`ValidateEventReferences` 供不编码 wire 的核心消费者使用，
Agent 的 JSON／SSE 收集已接线。事件检查复用编码事务的区间状态，但不执行目标编码及槽位更新。
编码入口自身执行同一检查，调用方不应在同一状态上再独立调用事件检查。

Chat 的完整响应选择器按单个有序正文解释文本合并及引用偏移；局部索引一次累计长度和引用顺序，
不保留跨帧状态、不重复遍历前缀。Responses 按稳定节点身份检查文本重排；身份丢失明确拒绝。
最终 wire 合同、核心权限及成功交付仍是独立边界；组合资源预算和静态修改影响分析尚待后续批次。

## 早期错误与固定入口（dev.131）

public 别名注册表显式声明协议 ID；通用 gateway 路径取其已注册的 protocolId。鉴权中间件在读取
凭据及正文前固定注册表视图和 ingress 编译修订，正文处理和错误出口复用该快照。一次选择失败也
固定到当前请求，不因并发激活改选协议。public 路由只选择协议，不解释其字段或错误形状。

宿主将本地失败表示为通用 category、message、code、param 及 HTTP 状态，错误核心使用所选包的
CoreVersion。Mapping 的 encode_response 构造正文，ValidateResponseOutput 检查最终 Spec 合同；
同一边界用于上游 HTTP 错误。鉴权、未知模型、资源拒绝和普通网关失败不再调用旧 renderer。

无所选协议、修订不可用或错误映射自身失败时，只返回固定
`{"error":{"code":"protocol_gateway_error","message":"原主因"}}`，保留原 HTTP 状态。
映射失败记录为次因，不尝试另一协议。内部诊断保存在结算日志，不额外塞入供应商错误正文。
非法上游错误体保留真实 HTTP 状态并使用通用描述，不通过专用字段猜错误类别，也不向客户端泄露原始正文。

任务控制等宿主管理出口仍使用其自身合同；本切片不宣称全部入口统一或旧链已退役。新包 public
入口直接使用 Spec 完整路径；仅旧核心的基地址前缀适配仍留待最终删除。

## 客户端模型目录（dev.134）

已有 `models` operation 继续使用 `response`／`decodeModels` 进行上游发现。可选的
`Spec.operations.<name>.models.clientDocument` 指定客户端 JSON 合同，必须与
`Mapping.operations.<name>.encodeModels` 同时声明。编码器读取通用 ModelPage（models 中的
id／name／maxTokens 及分页信息），执行共享 operation plan，并在返回前验证 clientDocument。
不新增 HTTP 端点，不把目录包装成生成响应，不通过名称选择供应商字段。

`modelSamples.direction` 省略或 decode_response 验证发现，encode_response 验证客户端输出；
发现终页及分页证据不能替代编码证据。管理／CLI 的 models 预览使用同一方向和编译结果，编辑器
可选择两个方向。两份源继续共同形成修订和验证哈希。

现有 `/v1/models` 与 `/v1beta/models` 的新包路径，以及通用 gateway 中匹配的 models 操作，均
使用鉴权前固定的编译快照。宿主枚举已启用模型组，Mapping 决定外部字段、固定标签和默认 token
数值；空目录编码为空数组。默认编码器明确拒绝分页输入，不静默丢弃 cursor。没有 encoder 或
最终合同失败时，使用已选协议的错误出口；不尝试旧格式。

本批保持既有目录成员策略，没有改变 token 的模型组过滤合同。只给现有 Chat／Gemini 客户端
目录添加默认编码声明；Responses／Anthropic 继续只有已支持的上游发现。旧生产默认仍保留其
既有目录 handler，按核心版本在执行前选择，待生产切换统一删除；新包失败不会回退到它。

## 上游输入计数声明（dev.135）

Spec 的 `count_tokens` 操作声明 POST、HTTP JSON、独立的 request/response 文档和完整路径。
Mapping 的 `operations.<name>.countTokens` 包含 request 下的 decode_request／encode_request、
decodeResponse／encodeResponse 及只用于请求的 conversionDefaults。请求复用核心 Request、表达式、
语义效果证明和 Native 规则，但拥有独立映射指纹，不继承生成端点的预算或模型默认参数。
结果为 `TokenCount { inputTokens: int64 }`，接受零，拒绝负数、非整数、缺失和溢出；它不是 Usage。

Anthropic 声明 `/v1/messages/count_tokens`，不要求也不接受 max_tokens、stream 等生成字段。
Gemini 声明 `/v1beta/models/{model}:countTokens`：输入在 contents 与 generateContentRequest
中二选一；工具、系统指令等输入通过 generateContentRequest 保留，内部 model 必须匹配端点模型。
Mapping 使用已有 with_input 及共享请求表达式，不添加供应商 Go 分支或提高配置限额。

`tokenCountSamples` 必须明确 operation，分别证明四个请求／响应方向；请求样例还验证语义往返。
修改样例、计数程序或合同会改变对应修订证据，生成样例不能替代计数证据。计数预览使用
`mode: "count_tokens"`、operation、direction 和 input；路径中的模型通过公开的
`conversionContext.model` 提供。CLI 对应 `--model`，宿主内部 Scope 不接受 JSON 伪造。

本批仅完成编译、样例和离线预览。实际 HTTP 路由将在下一批接线：只选择声明计数操作的候选，
不能回退本地估算，计数结果不能写入生成 token 账本。生产默认切换和正式性能验收尚未完成。
