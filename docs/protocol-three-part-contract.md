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

`nativeWhen` 可以只读本次请求的 `runtime.clientOutput`，据客户端展示偏好选择原生交付或语义编码；不能读取其他运行时元数据、流状态或交付位置。usage 收集是网关策略，客户端是否展示 usage 是独立意图。完整请求转换统一填充收集设置及缺省展示设置，显式的 `includeUsage:false` 不被缺省值覆盖；单阶段预览不重复执行完整请求转换。

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

本阶段实现稳定分区、普通容器合并和受限角色更新；相邻分组、一般容器展开、文本合并、引用偏移及复杂 SSE 布局仍未闭合。

## 认证续传（dev.109）

Responses 的 include 原表示由 `ClientOutput.RawResponsesInclude` 持有；`RequireContinuationState` 表示产生受保护状态时客户端必须获得可继续使用的表示。目标规则只写 `ResponsesIncludeResolved`，不能改写来源原值或撤销续传要求。Agent 可声明原值和要求，本地收集器直接保留资源进入后续轮次。

`mapping.continuation` 声明 `extract`、`encode_response` 和 `encode_event`。提取返回 token 及真实原文位置，不在解码前删除数组成员；成员映射通过 `is_carrier` 消费已选载体，后续 Source 下标不变。宿主认证 Owner、Scope、历史及节点摘要后确认该请求；未经认证的载体不能通过删除根 Native 绕过编码检查。

出站程序只读取宿主封装的 token/ID 和已有流交付位置。只读 `emitted` 从最终输出观察 token，与最终 Spec 校验共享输入视图；它不能读取待发 token 集合并自报成功。流式写入及 Flush 成功后推进交付；JSON 等 HTTP completion。没有待发 token 时不执行载体程序。

`conversionDefaults[].recovery` 只声明 signature/encrypted_content 的保留条件及无恢复通道时的处理，仍按稳定规则 ID 覆盖或禁用。加密及认证由宿主实现，配置和普通 context 不能伪造恢复证明。服务端 Persist 与客户端交付分开；明确的客户端要求不能仅靠 Persist 满足。已识别保留前缀的认证失败是请求错误，不作为兼容省略处理。

上述为当前实现合同，验收状态见实施记录；不表示生产预置已经切换。

## 新版本边界

新版本使用隔离的新数据库验收，不提供旧格式、旧绑定或旧任务的升级迁移。最终生产链只执行当前包格式；现有数据库、日志和私人正文不删除、不修改。新版本自身的修订、热更新和激活事务继续保留。

切换必须等待四协议实际调用、配置化策略和性能门槛通过；不在线双跑，不失败换引擎。跨平台构建与实际平台运行分别记录。
