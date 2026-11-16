# 协议包 v4 与 DSL v2 契约

本规范定义当前唯一支持的协议包与执行模型。正确性依据是外部协议语义和具名客户端兼容声明；旧引擎只作为隔离的性能参考，不进入生产执行或测试运行时。当前切换与验收状态见[三分切换记录](protocol-three-part-cutover-2026-11-14.md)，迁移历史见[实施记录](protocol-three-part-implementation-2026-11-01.md)。

下文按版本记录的实施范围是历史状态，当前执行合同以最新版本说明及三分切换记录为准。

## 来源开场与传输证明（dev.157）

`conversionDefaults.sourcePrelude` 只用于 `source_event`，独占该规则的执行内容。`first`／`next`
各有对象 schema 和可选 `when`；配对 `when` 与有序 `set:[{path,value}]` 只允许纯局部表达式，
读取 `input`（下一帧）和 `root`（首帧），不得读宿主或状态。更新路径不能是根或互相重叠，
只替换双方均已存在的值。理由必须明确；源 decoder 指纹、身份和资源限制共同绑定规则。

执行器最多暂存一帧及其复制的传输元数据，计入共享流预算。原帧与修正帧通过独立来源 Spec 后，
才交给原生命周期解码器。实际改写产生来源路径的 lossy_compatible 诊断，strict 拒绝；禁用、
不匹配或改写谓词为 false 时不获得规范化。干净 EOF 交付暂存原帧，取消和传输错误释放状态。
原始 provider 记录只记录一次且保持原字节，不能用修正帧覆盖审计证据。

组合报告可含 `transport:http_json`，表示只验证普通 HTTP JSON 请求／响应。它与协议修订、
策略、父绑定、样例哈希一起持久化；路由时不能匹配 SSE、NDJSON 或会话请求。只有完整证明及
通用能力档案均未通过时，才另行计算该受限证明。完整报告和原始流拒绝仍保留，不贡献正向覆盖。

Spec 的共享 `WireAssertion.ref` 可带调用者的 `when`，在选择 `path` 之前求值；命名约束内部
仍在所选值作用域执行，不能由调用者覆盖 schema、check 或诊断。纯错误信封借此跳过成功元数据
约束，不复制整套约束或放宽原有字段检查。

## 来源余项、部分用量与失败边界（dev.157）

Chat 和 Anthropic 的完整响应将未知根字段、未知 usage 字段及计量子对象余项保存到自身
`attributes["wire:<family>"]`。自身 Mapping 在无根 Native 时仍能重建这些余项；已识别计数由当前核心值
生成，余项与映射结果冲突时拒绝。其他协议只检查所有者，不能解析外部余项或静默删除它们。
Anthropic 空消息的 children 在核心中可省略，自身 Mapping 明确重建为空 content 数组。

Anthropic 来源 Spec 接受单独到达的缓存及思考计数，用共享 `usage-counters` 约束检查类型、非负性和 int64
范围。来源 Mapping 在自身流状态中累计已识别的原始计数，合并分批到达的 TTL 桶后重新计算核心 input、
cacheCreation 和 total。只有一个 TTL 桶时不推断缺失桶为零；显式零覆盖先前观测值。usage-only 尾帧无需
捏造 delta。最终客户端的起始帧与完整响应仍保留必需计数校验，不能用来源的部分快照合同替代最终输出合同。

可选 `failureObservation` 只观察被来源 Spec 拒绝的失败帧，原始拒绝仍成立；它不能读宿主或流状态，也不能
授权重放。`Event.UnmappedPaths` 是来源 Mapping 声明的原始路径证据，核心不再识别厂商帧中的扩展布局。
`warn` effect 只记录声明的警告，不改变输入、输出或放宽校验。

HTTP 错误响应投影不携带仅适用于事件的续传转换绑定。流中真实 operation.failed/cancelled 会释放未完成的
续传收集器，再继续正常的失败转换、最终 Spec 校验和交付；已封装的续传状态及关联校验保持。

## 中心语义与来源所有权（dev.156）

实际匹配的停止字符串使用响应 `attributes.stopSequence`，未命中时不产生该属性。
Anthropic Mapping 将其映射到自身 `stop_sequence` 和 `stop_reason:"stop_sequence"`，JSON 与 SSE
遵守同一规则；未命中时由自身 Mapping 构造必需的 null。其他目标不能表示此属性时明确拒绝。
核心语义比较不再识别或删除厂商专用的停止序列属性。

Chat 和 Responses 的响应及终态事件编码只接受目标已声明的响应属性，未知核心属性不能静默省略。
Chat 对尚未投影的 summary/structured reasoning 在请求、响应和事件中保持一致拒绝。
Responses 自身的 summary/content 文本成员通过同一所有者重建方法合并扩展字段；冲突仍拒绝。
源 instructions 字符串对应的消息或文本节点被加入无法表达的属性时，编码拒绝，不能只取第一个 payload。

Responses 的 moderation 由来源 Mapping 生成具有来源路径的元数据，不再同时留在 wire 余项中。
null、空对象可跨协议规范化并记诊断；非空状态需要目标等价表示。同源重建无需根 Native。
派生 output_text 的一致性检查发生在同源元数据保留判断之前，当前正文编辑不能绕过这项检查。

Spec 的实体完成动作可声明 `requireSealedPayload:true`，要求此前有 `sealPayload:true` 的快照；
该声明仅允许有载荷实体的 `entity_finish`。检查发生在身份、关系、属性及正文写入之前。
未声明此条件的实体仍按原合同完成。Responses 的 reasoning_text 内容 part 使用该条件，
普通文本及 summary 的完成边界继续由各自 Spec 独立声明。

Gemini Spec 对 safetySettings 的 category 解码后检查唯一性，等价 Unicode 转义不能规避重复检查。
该集合约束失败定位到 `/safetySettings`，核心 safety 校验仍可定位具体重复成员。
Gemini part 先校验对象及已出现字段的类型，再检查是否存在支持的载荷或签名，首错位置稳定。

以上修改没有提高节点数或深度限制。Responses 复用重复条件，并将仅返回字面量的选择分支写为 enum；
不改变匹配值、默认分支或未知值拒绝行为。

请求的身份使用 `parameters.user`；安全配置使用 `parameters.safety = null | [{category, threshold}]`。
category 为 `harassment`、`hate-speech`、`sexual-content`、`dangerous-content`、`civic-integrity`；threshold 为
`off`、`none`、`low`、`medium`、`high`、`default`。来源 Mapping 解释自己的枚举，目标只读取这些核心值。
无法识别的来源结构整体保留在自身 `wire:<family>` 中，不授予核心语义，也不由其他协议解读内部字段。

推理参数只使用 `ReasoningIntent`。旧 `reasoning_effort`、`responses_reasoning`、`anthropic_thinking`、
`anthropic_output_config`、`gemini_thinking` 核心参数包装在编码和来源归一化边界整体拒绝。
各协议 Mapping 将自身推理余项放入 `parameters["wire:<family>"]`，自身编码合并这些余项和当前核心控制，
相撞字段拒绝。null、空对象及未知成员能够在移除 Native 后同源重建。跨协议时，可省略的空余项由**来源**
`modelPolicy.normalize` 处理并记录规范化诊断；目标不会解析其他协议的推理字段。禁用这条来源处理所在的
规则后，剩余外部所有者仍由目标编码拒绝，编码器不能兜底删除。

Responses 的 include 选择在来源 Mapping 转成 `ClientOutput.RequireContinuationState`。
唯一的已知选择器完全由该布尔值表达；null、空数组、未知选择器和需要保留的自身布局放入自身 wire 余项。
自身编码服从当前核心布尔值，不能由原始选择器恢复已经取消的续传要求。目标协议只消费核心交付意图；
未知来源选择器跨协议拒绝。核心已移除 `rawResponsesInclude`、`responsesIncludeResolved`，预置 Agent 也只
生成核心续传意图。最终 Responses wire 校验会拒绝后续编辑对续传选择的增加或删除。

上下文截断使用 `parameters.contextTruncation = null | "auto" | "disabled"`。没有等价行为的目标拒绝此约束，
规则禁用时编码守卫仍拒绝。未恢复的历史使用核心 session resource 表达。当前 Responses Spec 明确拒绝非空
previous_response_id；null 的同源布局保存在自身 wire 余项，并由来源声明跨协议省略。

`Usage.Attribution` 的核心形状为 `{items:{name:bucket}, requestFields:{name:counts}}`。
counts 可含 `input`、`output`、`cacheRead`、`cacheCreation`；bucket 还可含 `content:[counts]`。
计数必须为非负 int64，缓存读取与创建之和不得超过已提供的 input，子项累计不得超过已提供的父项小计。
`Usage.CacheBilling` 为 `{basis:"cache-allocation", billed, observed, policyRevision, ruleId}`；
三个数字为非负 int64，ruleId 非空。billed 和 observed 是独立证据，不替代模型 token 计数。
两种账本均区分缺失、null、空对象；未知来源账本保留为来源所有者的整体余项。
Responses Spec 独立检查外部账本类型、范围和小计，Mapping 双向改写字段，Go 核心只检查上述中立算术。

加密续传资源与普通签名分别声明恢复规则。预置 `*-encrypted-state` 规则在缺少已认证恢复通道时拒绝，
compatible 模式也不能删除加密状态。普通签名的既有取舍继续由其自身规则声明。

通用 DSL 编译器把空数组构造器和空数组字面量视为相同的空分支证明；非空构造或 opaque 值不能因此获得
Native 权限。`project` 的字段缺失证明可沿原始来源继续推导，仍不能覆盖未证明的字段写入。
流 finalizer 允许 `with_input` 绑定来自 state 的值；其 source 及未绑定 input/root 仍不得读取当前事件。
这些规则适用于全部协议，不包含厂商分支。

## 三部分与发布单位

- DSL 提供读取、结构操作、关联、增量、状态、编码、计量和诊断原语，不根据供应商名选择语义。
- `spec.json` 描述外部端点、文档、实体关系、事件顺序和生命周期；不负责核心映射或跨协议取舍。
- `mapping.json` 只声明本协议与大自在天的双向映射，以及本协议的来源规范化、目标投影、模型策略和样例。

大自在天（core2）是整个转换拓扑的中心。A → B 是 `Mapping A: A → 大自在天` 与 `Mapping B: 大自在天 → B` 的组合，不存在绕过核心的 A/B 专用转换器。目标 Mapping 读取大自在天语义，不解析其他供应商的 wire 对象。协议对用于选择转换策略，不改变中心拓扑。Native 回放、签名和续传仍受核心来源、作用域、语义和交付证明约束。

Spec、Mapping 是两份共同发布的配置；配置说明解释两者及 DSL 的实际行为。不能把“说明”当作不参与执行的 Mapping，也不能将旧 codec 包装为一个 DSL 原语。

两份源以 `{ "schemaVersion": 4, "spec": ..., "mapping": ... }` 形成单一发布包。Mapping 的 `dslVersion` 为 2，`coreVersion` 为 2；转换策略的 `schemaVersion` 为 2。保存、验证、激活以整包为单位，共同确定修订哈希；请求固定编译快照，不解析配置。

Spec 和 Mapping 各有自己的 `expressions` 符号表。命名程序按静态输入、词法槽位和权限编译复用，执行值与可变状态不共享。引用闭包进入实际使用方向的指纹。循环、未定义或不可达引用，以及深度超限，在编译时拒绝。

Spec 的 `constraints` 是独立的具名断言组。文档或事件用 `{ "ref": "名称", "path": "/子文档" }` 调用；省略 path 表示当前输入，root 始终指向原文档。调用可声明 `when`，它在调用者作用域中求值，成立后才选择 path 并执行整组检查；条件不成立时不读取该子文档。相同静态作用域共享已编译检查，不展开整份 JSON，也不共享执行状态。引用不能覆盖组内检查；循环、未定义、不可达和超深引用在编译时拒绝。调用条件、约束及其表达式依赖共同进入使用文档的指纹，条件不能绕过取消和资源限额。

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

编码方向的 `effects` 可声明 `scope: "metadata"`、`kind: "reject"`。条件只读取当前核心元数据记录；执行器遍历响应、历史节点、事件节点及终态快照的实际所有者。该检查不修改数据，也不代替转换规则：禁用目标投影后，残留的不可表达字段必须拒绝，不能由编码器再次降级或静默遗漏。诊断优先使用记录的原文路径，没有原文路径时使用核心所有者路径。外部字段类型仍由本协议 Spec 声明。

`scope: "node"` 的拒绝条件读取当前核心节点，诊断 `path` 相对于该节点的实际位置。四份 Mapping 用它检查节点上的 `wire:` 扩展是否属于自身命名空间，不读取其他协议扩展的内部字段。例如 Anthropic Spec 校验工具 `caller` 的结构，Mapping 仅将精确的 `{"type":"direct"}` 识别为普通客户端调用；其他 caller 仍由原协议持有，未经映射不能在跨协议编码中丢失。

`each.select: "node"` 可以设置当前节点的 `payload`，但必须在同一步声明 `normalize` 或 `degrade` 诊断，不能修改来源、Native、资源、节点种类或子节点所有权。Anthropic 和 Gemini 的目标 Mapping 使用该能力，将无内容、无受保护资源的空可见思考规范化为空字符串；已存在的内容、显式 null 和结构化思考不进入这条规则。禁用规则后，最终 Spec 拒绝缺少必需内容字段的输出。Gemini 的 JSON 和 SSE 共用 `part-content` 约束，思考标记本身不能替代内容或签名。

执行链为读取与外部校验、核心映射、目标规则、编码与必要最终校验、交付。

一次不可变输入可共享有界字段索引和只读视图。同一输入、同一合同和限额的成功校验可以复用；失败与取消不能缓存成成功，帧间不保留读取缓存。源流式帧的视图同时绑定宿主提供的载体上下文，Spec 断言、外部生命周期和 Mapping 读取相同的 `context`；相同 JSON 正文配上不同的 SSE 事件名不能复用旧校验证明。上下文不保存在跨帧状态中。同步校验可借用字符串，进入核心或流状态的小字段必须取得独立所有权。

请求身份只有一个核心所有者 `parameters.user`，值为字符串或显式 null。来源 Mapping 提取自身身份字段，例如 Anthropic 的 `metadata.user_id`；目标只读取核心身份，不读取 `anthropic_metadata` 等旧包装。来源元数据中的未知成员仍属于自身 `wire:` 扩展，不能冒充通用标签或在跨协议时静默丢弃。`parameters.metadata` 单独表示字符串值的通用跟踪标签；它与用户身份不是同一字段。

外部生命周期与转换后的核心生命周期检查不同对象，不能互相替代。配置编译能够证明的静态条件不在执行时重复验证；用户修改的输出根据影响范围验证，无法确定范围时执行一次完整最终校验。内部已证明条件直接使用；外部输入、配置、权限、资源和 I/O 错误返回结构化诊断。

流状态归属单一流及 Scope。声明槽位在编译时绑定；有序 `state_update` 的右侧读取同一前置快照，后续动作可读取其结果，帧失败不提交槽位。工具参数、文本和数组按实际需要增量保存；不反复编码完整前缀。交付位置只在对应帧成功发送后推进。

节点元数据只有显式 `update: "append"` 才追加数组；未声明时替换快照。来源 Mapping 必须区分增量和快照，核心不再根据供应商名称或 `logprobs` 字段名推断追加。追加标记只属于节点增量事件，不能进入已收集的快照、响应级元数据或传输帧字段。

不透明会话状态统一使用 `control.updated` 和 `ControlState`，已退役 `ResponsesTurnState` 专用模型及转换器。Spec 校验外部状态帧，Mapping 捕获其实际载荷位置；核心收据绑定来源、作用域、帧序号和编解码指纹。原生状态帧可以先于响应出现，不能建立响应身份或进入正文。获准省略的状态及辅助事件通过 `EncodeFrames` 直接编码时也产生空帧列表，且仍检查收据归属和取消。降级诊断保留实际来源帧序号，不包含状态内容。

Responses 的 `obfuscation` 只在六类文本、拒绝、工具参数和思考 delta 中作为字符串 padding 识别。同协议编码将它绑定到原 delta 种类，不移动到终态或历史；跨协议按显式目标规则降级，严格模式和禁用规则保留拒绝边界。其他位置的同名字段仍是未知扩展。

Spec 的流合同声明外部 `namespaces`、`entities` 及父子关系；实体载荷可以是文本、JSON 或数组，不以核心 NodeKind/EventReplay 充当外部合同。数组增量只检查新增元素，完整快照校对已观察前缀。合同失败后该实例不可继续接收或提交交付。相同文档可用 `base` 复用已编译结构与生命周期，仅附加断言；不能覆盖基文档，也不能循环引用。

Mapping 的 `buffer_text`／`buffer_array` 按固定通道和项目身份保存必要增量，支持 append、snapshot、read、length、release。append 返回新增部分；snapshot 拒绝改变既有前缀；read 才物化完整结果。文本状态不逐帧保存微小片段对象，数组状态保留独立元素并共享不可变输出叶子。

只需识别已发送数组前缀时，使用 `track_array`：delta 追加成员摘要，snapshot 校对既有语义前缀并返回新增后缀。状态只保存固定摘要和成员数，不保存完整数组；JSON 数值按精确十进制比较，不转为浮点数。它不能代替需要最终完整数组的收集器。

`resume_text`／`resume_tool` 从实际已交付的外部实体接续语义输出，核对当前 Scope 和开放实体，不从普通上下文 JSON 恢复发送位置。Spec 可将 JSON 实体的初始载荷显式声明为 `placeholder`；第一次真实参数更新替换该占位，空增量不会清除它，失败的帧不提交替换。

原生复合帧由 `nativeReplay: "original_frame"` 声明动态字段属于原帧，配套 observer 只同步后续重建需要的状态。`nativeWhen` 可进一步限定哪些合法来源帧适合原样交付；不满足时按正常语义重建，不以目标校验失败作为回退条件。原生回放仍检查双向映射指纹、Scope 与语义摘要。输出批次使用实际已交付位置及当前批次偏移计算序号；计算序号不提前提交交付。

`nativeWhen` 可以只读本次请求的 `runtime.clientOutput`，据客户端展示偏好选择原生交付或语义编码。`runtime.encoding.hasRendered` 表示本方向此前是否执行过语义编码，用于声明聚合帧的重放取舍；它不是成功交付或正文等价证明。dev.150 允许读取与目标修订及 Scope 绑定的 `/stream/*`、`/delivered/maxItemIndex` 和当前批次的 `/emission/offset`；交付位置只在成功写入并 Flush 后提交。不能读取任意运行时元数据或 Mapping 状态。usage 收集是网关策略，客户端是否展示 usage 是独立意图。完整请求转换统一填充收集设置及缺省展示设置，显式的 `includeUsage:false` 不被缺省值覆盖；单阶段预览不重复执行完整请求转换。

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

dev.138 将同一 EvaluationState 内全部 Mapping 的索引、动态身份、跟踪器和区间纳入组合计项；
固定跟踪通道及同一规范身份的别名不重复计项。每个 wire 验证器将实体、独立身份绑定及交付区间
合并计量，并累计其保留载荷。`stream.identities[].namespace` 可将 keyed 身份关联到已有实体
命名空间：实体出现前由别名持有一项，出现后归实体持有；别名的值、唯一性和字节仍分别检查。
交付中止释放动态状态，保留已成功交付的位置事实，不能继续提交或重发。

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

核心版本 2 不再通过共享 Go 检查这些供应商字段。默认 Mapping 在省略源追踪载体前检查其形状；自定义包可以在同名参数位置声明不同的外部类型。工具选项、关联、资源作用域与 reasoning 的核心约束继续保留。dev.116 阶段暂留的旧 codec 校验已随生产切换退役，当前没有旧核心版本执行路径。

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

Chat 的 history-order／255 在核心中显式排列消息子节点，使其顺序与独立的 reasoning、content、
tool_calls、refusal 字段一致。位置改变产生降级诊断，严格模式拒绝；禁用规则后，编码阶段拒绝
尚未处理的交错顺序。自身已成形的 Chat 消息不再被 assistant-history 无条件合并。

通用 unfold_children、merge_text 与 utf16_length 不解释供应商字段。引用结构、长度
口径、附属数据聚合及损失诊断由 Mapping 声明。未处理的身份、资源、缓存及扩展拒绝合并。
annotations 的外部类型由 Spec 声明，已知值进入核心 metadata；输出保留原文位置、
所属内容、条目顺序以及已声明的 null／空数组形式。

来源 Mapping 通过 metadata.meaning 声明中立语义：empty、information、state、resources、
service-tier、token-probabilities、url-references、visible-text、buffering、padding。
目标仅根据核心语义进行投影；codec、name 和 value 保留来源自身的重建信息，目标不能用它们
解析其他供应商。URL 引用另存为 references 数组，每项为 url、title、start、end；位置采用
UTF-16 code unit。来源 Spec 检查外部结构，来源 Mapping 提取规范形式，目标 Mapping 编写自己的
外部形式。未知引用扩展保留资源语义，不能因为含 URL 而丢弃其余字段。
文本合并同时声明 value 与 references 的更新；最终引用关联校验只读取规范引用，保留
原内容归属和实际交付证明。缺失核心语义的外来元数据仍拒绝，不由目标猜测分类。

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

任务控制等宿主管理出口使用其自身合同。dev.131 切片当时尚未完成全部入口切换；当前 public
入口统一使用 Spec 完整路径，旧核心基地址前缀适配已删除。基地址与版本路径按本规范显式配置。

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
目录添加默认编码声明；Responses／Anthropic 继续只有已支持的上游发现。dev.134 阶段暂留的旧
目录 handler 已随生产切换删除，当前模型目录只执行所选包的声明程序。

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

dev.135 切片当时仅完成编译、样例和离线预览。当前实际 HTTP 计数入口已接入所选包，
只选择声明计数操作的候选，不能回退本地估算，计数结果不能写入生成 token 账本。
生产默认已切换至三分执行；正式性能验收仍独立记录在[切换记录](protocol-three-part-cutover-2026-11-14.md)。


## 请求头控制声明（dev.150）

`Mapping.headerControls` 包含 `headers` 和 `program`。前者是非空、无重复的小写 HTTP 头名列表；宿主只读取这些头。程序输入是按头名索引的 token 数组：以逗号分隔、去除两端空白、忽略空项，token 本身区分大小写。程序必须返回且仅返回 `{"interleavedThinking": boolean}`。程序没有运行时状态权限，执行遵守包的资源限制。

入站使用实际收到的头，出站在编译时使用 `Spec.operations.<name>.headers`。默认 Anthropic Mapping 明确匹配 `anthropic-beta` 中完整的 `interleaved-thinking-2025-05-14` token；Go 宿主不识别协议名称、beta 头名或具体开关值。修改 Spec 的头部或 Mapping 的判断都会改变修订哈希与验证证据。未声明 `headerControls` 的协议不启用该控制，也不会读取头部。
## 请求范围证据

目标 Mapping 可以通过 `verificationScopes` 声明需要独立验证的核心请求范围。通用 DSL 执行只读谓词，Spec 的外部合同保持独立；每份通过证据仍需完整重放。配置、支持子集、身份和范围外行为见[请求范围与组合验证](protocol-verification-scopes.md)。

## 响应观测与缓存保留期明细

所有转换遵循 `Spec wire ↔ 大自在天/Core2 ↔ Spec wire`。Responses Spec 校验外部形状，Responses Mapping 解释并重建外部字段；通用核心仅校验中立语义。其他目标 Mapping 只识别下列 meaning 和用量明细键，不读取 Responses 原始字段。JSON 与 SSE 共用观测编解码表达式。

`ResponseMetadata` 的以下 meaning 必须属于 `location:"response"`：

| meaning | canonical Value | 含义 |
| --- | --- | --- |
| `image-context-compression` | `true`、`false` 或 `null` | 来源本次响应已生效的图像上下文压缩观测；null 表示显式未报告 |
| `input-transformations` | 有序记录数组或 `null` | 来源执行时移除输入的历史观测；[] 表示没有列出的移除，null 表示显式未报告 |

输入变换记录为 `{"sourceItemId":"source-input","kind":"reasoning","action":"remove","reason":"model-mismatch"}`。reason 仅允许 `organization-mismatch`、`end-user-mismatch`、`model-mismatch`、`prefix-mismatch`、`turn-boundary`。顺序和来源输入 ID 均保留；重复流快照替换该观测，不追加记录。ID 不提供跨账户资源权限，不是输出 item 引用，不授权恢复历史或执行新的移除。图像观测也不会变成下一次请求的压缩控制。

字段缺失不制造 metadata；false 不等于缺失或 null。同源去除 Native 后仍由 Mapping 重建字段，修改 canonical Value 会改变输出。Responses 的 image、变换记录和保留期对象不接受尚无中立解释的额外成员，Spec 在来源边界准确拒绝，不能丢弃成员后声称已完整转换。

Chat、Anthropic、Gemini 的 `response-metadata`、`event-metadata` 及历史响应规则对非 null 图像观测采用具名有损投影：compatible 报告 `conversion_degraded`，strict 拒绝。精确的空变换数组可报告 `conversion_normalized` 后省略；null 图像、null 或非空变换数组要求保留，当前跨目标拒绝。关闭规则后所有者守卫仍生效。诊断带来源 path、规则 ID 和策略哈希，不修改原始来源对象。

`Usage.Details` 的 `cache_creation.30m_tokens`、`cache_creation.12h_tokens` 是写缓存保留期分项，计量与流快照边界见[缓存用量契约](cache-usage-contracts.md)。这些扩展不新增厂商 DSL 操作，不改变现有 JSON 节点或表达式深度上限；共享表达式抽取须同时验证展开语义、引用深度及编译结果。

## DSL 读取与配置编码

Expression 在可选读取位置支持 `"$input/model"` 等 [read 简写](protocol-dsl-primitives.md#read-简写)。该能力属于通用 DSL：Spec 继续声明外部协议合同，Mapping 继续声明本协议与大自在天／Core2 之间的双向转换。简写不会增加 Spec 间的直接转换，也不会解析 literal、enum 或样例中的不透明 JSON。配置编码保留声明形式与精确数值；声明变化继续受修订指纹和重新验证约束。

`matches_charset` 只判断字符串是否由声明的字符集合组成，协议名称的字符集合与长度上限由各自 Spec／Mapping 配置。Expression 编码器根据真实字段声明生成，在一棵表达式树内共用输出缓冲区；字段顺序、缺省省略及非法编辑的拒绝行为保持原合同。这些实现不提供额外协议权限，也不代表正式性能验收已经通过。

## 执行账本与输出约束

大自在天 Usage 的 `executionSpeed`／`executions` 表达来源的执行事实。Anthropic Spec 定义自身字段结构，Mapping 负责与中立账本双向转换；其他目标通过具名规则报告披露损失。内部来源记录先于客户端投影保存，分项不会再次加入父 token 计数，未知内容保留来源所有权。JSON 与 SSE 的支持范围分别声明，SSE 的显式 iterations 仍拒绝未确定的更新语义。字段、配置、精度与持久化合同见[执行速度与分次执行用量](execution-usage-contract.md)。

请求 `parameters.outputConstraint` 是独立的中立输出约束。各协议 Mapping 只连接自身格式与该核心字段，Spec 约束自身格式和名称；通用 DSL 不识别厂商字段。未知格式仍归来源所有，不能与核心形成双重所有权。名称／描述的具名取舍、strict 拒绝、禁用规则后的编码守卫以及独立的 `output.constraints` 能力证据见[结构化输出约束](protocol-output-constraints.md)。较窄的文本组合报告不能替代结构化输出证明。
