# 2026-11-13 审验报告复核与三分修复计划

复核对象：`29318400217b2a91afc085373d29ee468fc1cc70` 的历史代码，以及持续验收的三分工作区。三分已成为唯一执行链；用户已确认报告修复与性能验收并行。复核探针与结果位于 `.tmp-dev/three-part-cutover`；下文区分已实施并验证的修复、能力边界及尚未通过的性能验收。

dev.173 按本轮优先级修复一项已复现的 Gemini 流式误拒绝：同一工具调用的后续帧省略 `args` 时，自身 Mapping 误将已有参数覆盖为空对象。现在保留已关联调用的精确参数；显式空对象仍由核心快照守卫检查，首次省略仍允许空对象。Spec 已允许该字段缺省，因此无需修改 Spec、DSL 或核心保护。签名和参数关联规则见[配置说明](protocol-gemini-stream-arguments.md)。它不能证明历史 F03 第二轮 400 的根因。

实际入口复查未发现可达旧模型协议执行器，当前文档同步为 package4／DSL2／core2。输出约束测试合并了重复初始化，完整96个网关请求组合及陈旧证据断言保留；新增测试入口按改动范围选择一次普通或竞态运行，默认两组任务并行。性能候选继续隔离，正式 D0 未通过；本批证据归档于 `.tmp-dev/three-part-cutover/conversion-production-173/`。

2026-11-15 补充：最新 SDK 验证发现的 Responses 进度快照丢失属于第一阶段迁移回归，并非原报告
F01 的 image 字段问题。修复落在 Responses Mapping 的来源快照、目标重建及交付跟踪，以及 Spec
的客户端完整进度信封；没有增加厂商 DSL 原语。完整 builtin、专项 race 和原真实网关失败用例
已通过。候选 `29318400-source-f4dbd176afdb` 的 server 补测与 84 份 SDK 新捕获已完成：
回放 159 项通过、13 项失败，13 项均已核对为 SDK 将准确 wire 数值 9007199254740993 舍入，
不能计为通过，也没有其他已确认网关失败。后续 dev.163a 候选
`29318400-source-983f618ca208` 已重新生成完整 160 份捕获（32 份文本、64 份单工具、64 份并行工具），
三组真实网关测试通过，捕获身份无缺项或额外项。SDK 回放为 300 项通过、20 项失败；独立精确整数
解析逐项确认这 20 项均为客户端将准确 wire 数值舍入，没有其他失败类型。原失败结果保持，
不能宣称全部 SDK 兼容。证据见 `f11-sdk163a/report.json`、`replay.json` 和 `precision-review.json`。
正式 D0 的 160 个窗口检出 147 项稳定性能退化，第一阶段尚未验收。通用 DSL 优化正在进行，
dev.162 的组合正确性、相关竞态及固定负载诊断已完成；局部保留堆和分配有所减少，CPU 仍有明显
差距，尚不具备正式性能复验的依据。各批次收益、代价及证据见三分切换记录，后续正式性能验收
仍须独立完成。
F01 的 image、input_transformations 和缓存 TTL 明细已在 dev.166 单独合入；完整现场JSON／9帧SSE
的离线重放及四入口实际HTTP网关验证通过，保留无法表达的非空／null观察的拒绝边界。
dev.167 已将 F02 执行用量和 F06 中立输出约束合入实际三分执行链，同时保留 dev.165 启动编译复用及原197条协议样例；该阶段样例数量见 dev.168 段落；最新数量见 dev.170 段落。完整 builtin 回归、protocol 全量竞态、相关网关竞态、六平台构建及 Windows 隔离启动／刷新／重启通过。F02 的 SSE iterations、未声明语义和 F06 首批 Schema 子集之外的约束仍有明确拒绝边界；这些结果不代表全部原生格式均已互通，也不替代尚未通过的正式性能验收。

dev.168 后续仅合入通用 DSL 子缓存存储优化，Spec／Mapping 语义与上述报告修复不变。该阶段完整配置含207条协议操作样例及48条 Agent 样例，共255条；最新数量见 dev.170 段落。原197条协议样例逐项保持。固定流式分配约减少2.60%，扩容路径的额外分配及耗时代价已记录；来源复用候选因未解释的流式成本继续隔离。正式D0仍未通过，详见[三分切换记录](protocol-three-part-cutover-2026-11-14.md)。

dev.169 已接入修订后的通用响应来源复用，报告修复的Spec／Mapping及中立核心合同保持。完整普通回归、相关竞态和固定配对测量通过；短请求分配减少6.89%，无重复读取的额外成本及流式时延／CPU上升观察仍如实保留，不构成正式D0通过。第一版来源复用继续作为被替代的隔离证据保存。最终 dev.169 完整 protocol 竞态831项通过／1项原可选跳过，六平台构建及 Windows 首次启动、刷新、重启验证通过；精确应用证据见 `.tmp-dev/three-part-cutover/d0-memo-production-169/final-verification.json`。

dev.170 将 F06 的数值 enum 扩展纳入 DSL／Mapping／配置说明三分：通用 `is_integral` 不改变 `is_integer` 的 int64 解码合同；四份 Mapping 各自处理自身协议与大自在天核心，支持原样保存大整数、指数及整数形式小数。新增8条样例，现为215条协议操作样例及48条 Agent 样例，共263条；原197条协议样例保持。组合专项23项、真实网关 JSON／SSE 两组、完整 builtin612项及 vet 已通过；最终版本的竞态、生成器、构建和启动结果见 `f06-enum-production-170/final-verification.json`。const 与非数值 enum 未因此获得跨协议支持，Responses 元数据 union 收紧仍在独立验证中，正式 D0 仍未通过。

dev.171 在实际三分配置中收紧 Responses 的 instructions／tool_choice 响应元数据联合类型，并修复验证过程中确认的两个 Spec 连接缺口：最终 JSON 客户端文档未引用该合同，response.failed 事件的元数据断言误选了帧根部。客户端 JSON 仍在最终交付边界校验；SSE 客户端继承来源文档和事件动作，不重复加校验。失败事件现在选中 /response，因此该事件已有的其他元数据类型检查也随之作用到正确容器。配置与边界说明见[Responses 响应元数据](protocol-responses-metadata.md)。这不是对历史 F01 触发原因的新断言。

四方 Mapping 仍只连接自身协议与大自在天核心。为容纳完整联合定义，Responses Mapping 将17个仅转发 item 字段的别名替换为既有读取简写，保留有实际语义的命名逻辑和全部263条样例；源节点限额没有提高。编译共享与保留量的独立测量、完整组件及发布验证见 `responses-union-production-171/acceptance.md` 和 `final-verification.json`。首次错误测试入口、后续真实 Spec 缺口以及夹具缺少序列号的失败记录均保留。

性能方面，惰性诊断路径第二版的8个固定配对窗口已完成：流式分配字节减少1.44%、次数减少3.72%，但短请求耗时、工作集及流式 P99 上升，候选继续隔离。每侧只有两个样本，不宣称稳定无回退；第三版正针对已证实的额外工作区初始化和跳过断言写入另行验证。正式 D0 及历史147项稳定退化仍未关闭。

dev.172 将 F06 的精确字符串 enum 子集接入 Chat、Responses、Gemini 自身 Mapping 与大自在天核心之间。Unicode、大小写、空白、空字符串、顺序和重复项保持；Anthropic 来源保留自身格式，目标继续明确拒绝精确字符串枚举。既有数值 enum 与独立名称、提示取舍、strict、能力及计数请求边界保持。DSL、Spec 和核心参数不因本批新增分支，配置说明见[输出约束合同](protocol-output-constraints.md)。

本批提取相关重复校验并移除同一边界上的冗余守卫，保留原263条样例并增加6条到269条；原197条仍逐项保持。首次完整 builtin 回归有6个自定义包因节点上限失败，失败版本与日志保留；修订继续整理配置并验证原自定义包，没有提高上限、删除样例或缩小测试。最终组件与发布证据见 `.tmp-dev/three-part-cutover/f06-string-enum-production-172/final-verification.json`。这不是缺失的原报告24份请求全部通过的证明，const 等边界仍保留。

容量修订后，网关回归进一步发现新增字符串样例会使 Anthropic 的整个输出约束组合报告失败，阻断原本支持的普通 Schema 和数值枚举。最终修订在 Anthropic 自身 Mapping 中增加默认 `output-constraint-schema` 声明式拒绝，复用原有精确 Schema 判定；字符串拒绝只形成负面证据，能力仍由实际通过的正例证明。该规则先于名称／描述取舍执行；strict 仍拒绝，禁用规则不能授权不支持的 Schema，直接编码入口保留自身校验。正常 Schema 多一次判定的开销被明确保留，不记为性能优化。原网关失败记录不改写为通过。

性能侧惰性诊断路径第三版也保持隔离：固定8窗口中，短请求分配约减少1.09%，流式约减少1.40%，但流式 CPU 中位数上升约4.48%；每侧仅两个样本，不宣称稳定回退或无回退。实际 dev.171 的四次 CPU／内存采样已经完成，用于后续归因，不替代正式 D0。历史147项稳定退化与正式性能验收仍未关闭。

## 结论与证据边界

报告总体区分了真实兼容性阻断、保守拒绝、上游失败、归因不明和验证未完成，并没有证明存在 14 个生产代码 bug。不能把 123 项矩阵未通过直接等同于 123 个网关缺陷，也不能把明确拒绝记作能力已支持。

交付包 README 明确说明不包含 HTTP 原始证据、日志、trace、数据库、配置或复现脚本。报告中的逐请求链接无法在此包打开。本次能验证代码机制、构造本地最小输入，但无法独立核验所有真实上游次数、当时的激活修订、失败原始载荷和供应商行为。

本次发现并修复了一项迁移回归：Anthropic/Gemini Mapping 编码图片时曾静默丢失核心 `payload.detail`，strict 模式也不拒绝。先恢复了明确拒绝；dev.163 再通过目标 Mapping 的具名规则支持 compatible 对显式 auto 的可诊断取舍。strict、规则禁用、其他 detail 及 count_tokens 仍拒绝，范围与验证结果见 F05 条目。

同时，F04 的解析器归因需要修正：历史 `content.go:299` 已有标准 base64 data URI 的 MIME 提取，当前最小输入也能生成正确 Gemini inlineData。进一步的本地网关测试复现了另一条拒绝路径：默认来源的 `image-decode` 验证样例是没有 MIME 的普通 URL，使 Gemini 的 images 组合证据失败；路由随后把该失败报告用于拒绝实际带 MIME 的 data URI 请求。因此“缺少 data URI MIME 解析”的推断与源码不符，能力证据范围过粗是已复现的问题；它是否解释原报告的具体请求，仍需当时的输入与激活修订。

## 唯一执行与分层前提

- 大自在天 core2 是中心。每个协议的 Mapping 只连接自身 Spec 与 core，不读取其他协议 `wire:*` 内部结构，也不直接完成 A → B 的供应商转换。
- DSL 只增加可证明通用且被现有组合能力无法表达的结构操作、状态、计量、诊断等能力。不得添加供应商名称分派或新旧引擎回退。
- Spec JSON 负责外部字段、类型、枚举、端点、事件关系与生命周期。Spec 不删除字段来放行转换，不承担核心语义归一化。
- Mapping 负责来源解码、来源所有权、核心意图、目标表达与明确取舍。兼容性取舍需要具名规则、可禁用、诊断和 strict 行为；未知字段继续拒绝。
- 文档需与修订一同更新，说明核心合同、字段归属、默认规则及损失。文档不能替代实际运行时配置。
- 当前三分唯一执行链已实际切换。2026-11-15 用户确认报告修复与性能验收并行，随后要求本轮优先转换修复、三分清理与测试提效；最终性能门槛保持，历史阶段顺序不再作为报告修复的前置。不得将功能通过记作整体验收通过。

## F01–F14 逐项判断

| 项目 | 历史报告判断与源码核对 | 当前状态 | 修复归属与决策 |
| --- | --- | --- | --- |
| F01 Responses context_compression | 未声明扩展触发跨协议拒绝的机制成立。历史 `builtin/json.go:115–127 preserveExtensions` 拒绝不同 family 的 wire 余项；该行是拒绝点，不是字段语义定义。48 次只按报告计。 | 授权现场 JSON/SSE 确认上游200、Chat入口502；input_transformations与TTL明细也独立阻断。dev.166已接入中立观察／明细，同源无Native重建、具名兼容取舍、strict／禁用、快照撤回及冲突检查通过；完整现场载荷经四入口JSON/SSE网关通过。 | Responses Spec定义字段合同，自身Mapping完成wire↔core；其他目标只处理中立语义。通用核心验证观察／TTL明细，不新增厂商DSL原语。缓存写入不重复计费，不能把具名降级或明确拒绝记作无损支持。 |
| F02 Anthropic iterations/speed | usage 新字段与失败相关的机制可信。`capability.go:160` 是泛化的 Native/能力守卫，不能仅据此推断 usage 格式错误。24 次只按报告计。 | dev.167 已支持 JSON 执行账本、message_start 的速度观察、同源重建及具名客户端披露取舍；源用量在投影前记录，日志与导出保留精确数值。SSE iterations 仍拒绝未确定的更新语义；晚到 speed 和显式 null 明细保留来源帧，无法重建时拒绝。现场仍只有权限403证据，不记为成功复现。 | 已修 Anthropic Spec／Mapping、中立 Usage 合同与记录预算；其他目标 Mapping 只处理中立字段。分次用量不再次加入父计数，standard／null／空账本不当作无损默认值。配置见[执行用量合同](execution-usage-contract.md)。 |
| F03 Gemini signature | 确有第二轮 400 观察，根因未确认。报告自己的摘要对照在签名及参数不变时分别得到 200/524/400，不能推出网关丢签名。 | 当前存在认证续传、签名、工具第二轮测试和 provider_signature 已有签名分支修复。dev.173 另修复已复现的同一工具调用省略 args 被误写为空对象的问题，保留精确参数及签名关联；不等于历史真实失败已复现或解决。 | 本次只改 Gemini 自身 Mapping，Spec 缺省合同及核心快照守卫保持。历史问题仍需阶段证据；认证/scope/执行生命周期问题归通用核心，wire 事件合同错误才改 Spec。不得补造签名或复用不匹配 scope 的签名。 |
| F04 PNG MIME | 报告指出 400 `/media/mime`，但定位只证明目标缺少 MIME。历史解析器已解析标准 base64 URI；已复现问题是组合证据范围过粗。 | dev.163 已加入目标 Mapping 的只读范围声明、通用组合验证及完整请求路由检查。Chat／Responses→Gemini 的 JSON/SSE data URI 正例通过；URL 与两种混合顺序均拒绝且上游调用为0。原报告具体请求的因果关系仍未核实。 | 通用 DSL/验证证据机制 + Mapping 声明/正例 + 配置说明；Spec 不放宽，不猜 MIME、不删除 URL 样例。原完整失败报告保留，范围摘要包含引用表达式，失败执行不能当范围外跳过。 |
| F05 detail:auto | 历史拒绝符合保守契约；不同供应商默认预处理并非已证明等价。 | dev.163 在 Anthropic/Gemini 目标 Mapping 加入可禁用的 image-default-preprocessing，compatible 对显式 auto 输出具名有损诊断，strict／禁用仍拒绝；定向测试通过。count_tokens 仍拒绝 detail，Gemini 工具结果媒体限制保持。 | 复用现有节点遍历、project 和 core_controls DSL；Spec 不改。仅去除显式 auto，保留 MIME/数据/其余成员和原始核心对象；high/low/original/null 不转换。严格 Responses 网关独立的 parentId 布局证据拒绝仍保留。 |
| F06 原生结构化输出 | 缺少通用参数合同、12 跨方向明确拒绝的描述合理，是能力缺口。旧 request.go:501 是无目标参数映射的拒绝点。 | 首批子集部分完成：dev.167 已接入 parameters.outputConstraint，由四份 Mapping 各自连接 core2；首批精确 Schema 子集、名称合同、来源所有权和独立 output.constraints 能力证据已验证。缺少 OpenAI 目标必需名称时拒绝，不创造名称；dev.170 补入显式 number／integer 的数值 enum（单元素可表达固定值），数学整数判断保留原数值字节；dev.172 再补入 Chat／Responses／Gemini 的精确字符串 enum；Anthropic 的来源所有权与目标拒绝保持。const 等仍在子集外，合法原生格式继续归来源所有，不能宣称原报告24项已通过。 | 已修四方 Spec／Mapping与通用核心入口。DSL 增加通用 matches_charset、is_integral 和紧凑 read 配置形式；字符集合、64字符上限、格式语义均留在协议配置。name／description 的兼容取舍有具名诊断，strict／禁用规则仍拒绝。见[输出约束合同](protocol-output-constraints.md)。 |
| F07 upstream 503 | 直连对照支持渠道/模型不可用，不是网关协议错误的证据。 | 当前工作无法改变真实供应商渠道。 | 不修改 DSL/Spec/Mapping。保留上游状态及调用记录，修渠道配置/可用性；测试按依赖不可用单列，不能算转换通过。 |
| F08 红图识别 | 输入红图及错误回答是观察；直连 Responses 也答错，网关归因不足。256px 7/8 结果不证明网关修复。 | 无原始出站载荷，无法独立核对图片被改写与否。 | 不根据颜色答案改转换器。先比较输入/核心/出站图片字节摘要、MIME 与尺寸，区分模型语义评估和传输保真。确有字节或 MIME 改变再修负责 Mapping。 |
| F09 等待/504/524 | 超时真实存在，报告未断言全由供应商导致。参数拒绝和长等待不能合并成同一根因。 | 无失败阶段日志，根因仍未确定。 | 先量化 DNS/connect/headers/首帧/流间隔/转换/终态/客户端交付阶段；参数合同错误按 Spec 或 Mapping 修；通用取消/预算错误才修 runtime。不能统一延长 180s 或增加重试掩盖问题。 |
| F10 登录恢复动画 | 报告保留间歇性失败、没有宣称根因，合理。trace 不在交付包。 | 本次有限本地三对照均通过：原流程、等待呈现帧流程均达到 cinematic ready；故意阻断帧回调触发现有900ms video frame timeout。未复现历史失败，不能据此确定测试竞态。hidden、WebGL异常与总watchdog仍有各自边界，产品fallback与超时未修改。 | 前端/测试，不属于协议三分。已保存 finish reason、seek/frame 时间线，见 `f10-local-diagnostic-169/result.md`；历史失败仍需对应时序证据后再修。不得直接移除 fallback 或放大超时。 |
| F11 审计脚本漂移 | 当前源码独立确认多个问题；原报告的具体适配清单缺失。 | suite/local-tools/plugin 已共用真实绑定流程；dev.163a 的 160 份完整捕获覆盖全部16协议方向、JSON/SSE及两轮工具交互。SDK 回放300项通过、20项大整数精度失败；原始wire均准确，未发现其他失败类型。 | 测试基础设施修复，不改生产协议来迁就旧脚本。覆盖缺口已补齐；20项客户端精度失败保持失败状态，不宣称全部客户端兼容。历史候选证据继续保留。 |
| F12 Agent retry | 一次超时后 3/3 通过，原根因未证实。源码确认测试将15s轮询加速成100ms后，自动成功可能先于点击并删除按钮。 | 已用受控时钟修复该测试竞态：单次触发失败轮询后暂停，分别验证手动重试与下一次自动轮询；两例都检查请求数量和历史保留。 | 仅改测试，不改产品轮询或使用自动重跑掩盖失败。Edge Chromium 单 worker 两例通过（7.5s）；原报告那次失败的 trace 仍缺失。 |
| F13 取消归因 | 报告明确 blocked、有意不将未知 canceled/EPIPE 都标499，判断正确。 | `http_delivery.go:143–194` 区分下游拥有者写失败、观察失败、deadline、shutdown、泛化取消；只有断连证据给499。 | 不放宽归因来刷通过率。仅在复现拥有者证据丢失后修通用交付/取消路径。协议取消事件合同错误才归 Spec/Mapping。不能承诺供应商停止生成或计费。 |
| F14 server race | 全局45m超时且没有 data race 警告，表示验证未完成；不能定位为正在运行53s的 CountTokens 单测死锁。 | 完整603项冻结基线清单已跑完；3个失败及11个中断缺项在最终157g源码补测通过，另加9个修改路径检查。真实数据库重开竞态属于测试夹具，已排空HTTP回调后复测通过。原17个可选跳过和失败日志保留。 | 验证已闭合，证据见server-race-completion-157g.json；不宣称最终源码全量重跑603项，也不将SDK或D0待验收记作通过。 |

## 第一阶段阻断：图片 detail 静默丢失

复现文件：`report-triage-media-probe.go`；原始结果：`report-triage-media-probe.jsonl`。从 backend 执行：

```powershell
go run ../.tmp-dev/three-part-cutover/report-triage-media-probe.go
```

探针使用 `builtin.Packages → NewCompiler → DecodeRequest → DefaultConversionPolicy/ResolveConversion.Request → EncodeRequest`。每次删除根 Native；Responses 明确 `store:false`，避免无关的响应存储取舍使 strict 提前拒绝。使用两种入口、两种目标、detail 缺失/auto/high、compatible/strict 共24组合。

修复前结果：24项均编码成功。8个 detail 缺失的正例 MIME/data 正确。16个带 detail 的输入在来源 core 中仍有准确的 detail，但目标 wire 无此字段、无替代约束、无诊断；其中8个 strict 输入也通过。Chat 的 `decode-block`、Responses 的对应 image decoder 均正确保留 detail，丢失发生在 Anthropic `encode-block` 与 Gemini `encode-part` 的目标表达边界。原始结果保留在 `report-triage-media-probe.jsonl`，不是在线供应商复现。

修复步骤：

1. 在 Anthropic/Gemini Mapping 中为目标不支持的 image payload 成员声明校验与拒绝，拒绝路径指向核心成员；生成与 count_tokens 请求复用本包节点谓词，覆盖工具结果内的嵌套图片。完整响应与事件入口原本就拒绝不支持的图片，不为这次请求修复重复添加守卫。
2. 先恢复 detail present 时明确拒绝，不在迁移恢复中顺带宣称 auto 等价。Spec 中没有可用字段就不伪造目标字段。
3. 用本次16个反例固定预期，另保留8个 MIME 正例；添加 low、未知值、显式 null、同源重建、去 Native 与规则禁用反例。对于不合法值，让负责外部输入的 Spec 提供准确拒绝。
4. 按实际服务路由执行 JSON/SSE 请求验收，确认禁用兼容规则也无法绕过目标编码守卫。随后补阶段一受影响回归和冻结证据。

这里的通用 core 类型校验与目标 Mapping 可表示性校验分别证明不同条件，不应误判为重复保护。需要合并的是同一边界上重复的 payload 解析、同一拒绝理由的连续守卫。

上述请求守卫已实施。`has-image-detail` 先确认真实核心节点为 image，再用 exists 检查 detail，因此 null 也拒绝；它不遍历工具参数或工具结果中的不透明 JSON。修复后同一24组合探针得到16项明确拒绝与8项 MIME/data 保真正例。新增测试覆盖 low、未知值、null、嵌套图片、无 Native、兼容/严格及媒体规则禁用；Responses 的独立存储意图解析仍保留。

builtin 完整回归通过（28.262s），媒体专项 race 的 builtin/server 分别通过（14.909s／80.578s）。实际 Anthropic 网关16项 JSON/SSE 检查中，4个无 detail 输入保留 MIME／数据／usage，12个 detail 输入拒绝且不调用上游。Gemini 默认网关仍受 F04 组合证据问题阻断；其16项失败实验完整保留在 `media-detail-gateway-unresolved.log`，不计入成功验收。两份 Mapping 变动后的最终发行构建、完整 race 补测和 D0 仍须另行完成。

## 第二阶段具体工作包

以下保留诊断与实施过程中的设计记录。当前状态以 F01–F14 表为准：F02 的首批核心扩展、F04/F05、F11 矩阵及 Responses 官方元数据 union 已实施；SSE iterations、F06 当前精确子集之外的约束仍有明确边界。历史失败及中间测量不改记为成功。

### A. F01/F02：来源扩展的明确处置

已查到这三个字段的当前官方合同，并保存公开资料及 SHA-256；仍需最小脱敏原始 JSON/SSE、beta header 和激活修订，核对报告渠道是否遵循相同版本。不能继续把字段统称为未公开私有扩展，也不能据此默认允许丢弃。

- OpenAI Python SDK 3.51.0，固定提交 [`3dae6090cc31cc64a437fe0bcee740b6d81ed2cc`](https://github.com/openai/openai-python/commit/3dae6090cc31cc64a437fe0bcee740b6d81ed2cc)：Response 根部 `image.context_compression` 是已生效策略 `enabled|disabled`；SSE 位于完整 response 对象内。请求同路径另支持 `auto`，且默认 auto，因此请求 disabled 与省略不等价。
- Anthropic Python SDK 2.8.0，固定提交 [`5139cf2e7d07aa618a6c30b7e0c712f10ccb5ad9`](https://github.com/anthropics/anthropic-sdk-python/commit/5139cf2e7d07aa618a6c30b7e0c712f10ccb5ad9)：usage.speed 是实际 `standard|fast|null`；[Fast mode](https://platform.claude.com/docs/en/build-with-claude/fast-mode) 明确其计价与缓存隔离影响。不能从请求回填或映射成未经证明等价的 service_tier。
- 同版 BetaIterationsUsage 按 type 区分 message、compaction、advisor_message、fallback_message。[Compaction](https://platform.claude.com/docs/en/build-with-claude/compaction-threshold#understanding-usage)、[Advisor](https://platform.claude.com/docs/en/agents-and-tools/tool-use/advisor-tool#usage-and-billing)、[Fallback](https://platform.claude.com/docs/en/build-with-claude/refusals-and-fallback#billing-and-rate-limits) 文档说明不同变体与顶层用量的包含关系不同，且可能按不同模型计价。全部求和会造成重复或错误计量。message_delta 的顶层计数为累计值，但 iterations 数组每次更新究竟是完整快照还是新增项，现有公开证据不足以统一判断。

资料快照与固定 SDK 原文位于 `.tmp-dev/three-part-cutover/extension-contracts`，清单含抓取时间、最终 URL 与哈希。这些公共合同支持本地设计与测试，不能替代原报告 HTTP 证据。

用户随后授权从项目配置读取真实凭据并保留完整请求体现场测试。独立实例的 Responses JSON/SSE 请求均收到上游200，真实正文分别在 `/image/context_compression` 与 `/response/image/context_compression` 出现 disabled，Chat 入口返回502。原始四段正文、激活身份和日志保存于 `.tmp-dev/three-part-cutover/live-f01-f02-20261114`；正文捕获显式开启，未修改现有运行数据库或配置。部分日志 JSON 会重排键，因而完整性同时核对所有字段与无截断标识，原始网络字节另存，不把语义相等误称为逐字相同。

请求体完整保留已逐条证明。响应体需另作区分：Responses SSE 在第一帧即被网关拒绝，网关日志只记录已消费的1帧（1317字节），不是完整上游流；独立采集代理继续读完并保存9帧原文（6674字节）。日志的 truncated=false 只表明未碰到捕获字节上限，不能单独证明流已读到 EOF。完整响应证据应引用独立原始文件，同时保留网关消费边界。

现场 JSON 的离线因果对照进一步确认 F01 不止一个阻断项。Responses 核心自有余项中包含 image、input_transformations、instructions、previous_response_id、tool_choice，以及 usage.input_tokens_details.cache_write_tokens_details 的 TTL 分项。仅去掉 image 后三种目标仍拒绝；分别只保留上述任一余项时仍各自拒绝。清空所有余项的诊断对照可编码，但这种删字段只用于定位，绝不是修复策略。结果保存在 `live-responses-isolation-summary.json`，27个对照不产生新的上游请求。

因此 F01 的实施包应逐项明确来源语义：有效媒体策略与输入变换观测、请求参数回显、缓存写入计量明细不能合并成“忽略未知字段”规则。确证为回显且不增加响应语义的精确值，也必须以来源 Mapping 的声明及反例验证处置；未知值继续保留或拒绝。TTL 分项需要独立核对单位、总量包含关系和同源保真。所有目标仍只读取中立核心，不解析 Responses 私有对象。

进一步源码核对把其中三项缩小为明确的 Mapping 接线缺口：`has-valid-owned-metadata` 已声明 instructions、previous_response_id、tool_choice，`read-owned-metadata-meaning` 已区分 empty／state／information，但 `decode-response-metadata` 的提取名单与 `decode-response-remainder` 的排除名单均漏掉它们。历史 HEAD 的 `builtin/response_metadata.go:extractMetadata` 会提取类型表中所有已识别字段，JSON 响应与 SSE 的 `/response` 都调用该方法；这三项在旧类型表中均存在。因此它们是第一阶段必须补回的迁移回归，不能推迟为第二阶段新能力。应成对接入共享来源提取，避免 metadata 和 wire 余项双重所有权；已有 JSON、progress 和流信封共用该表达式。null 的精确归一化、非空父响应的 state/scope 拒绝、tool_choice 响应记录的具名取舍分别验收；Spec 仍需按外部 union 精确检查，不能用宽泛 container 放过任意对象。旧冻结全量 race 继续保留，修复后需新的候选与受影响补测，尚未测量的 D0 不得使用过时候选。

同版官方 SDK 的 input_transformations 是有序的 reasoning 输入移除记录，包含原 item ID、removed、reason 与 reasoning type；非空会涉及组织、用户、模型或历史边界。来源 Mapping 可归入独立的中立状态观察，不能直接套用默认可省略的信息意义。显式空数组、缺失与 null 也不可未经合同证明一概合并。

三项旧有元数据的迁移接线已恢复，Responses 专项 race 通过（10.443s）。历史根字段集合比对另确认 Gemini JSON createTime 提取遗漏，已通过共享来源提取与自身编码恢复，专项 race 通过（9.262s）。两项修复合并后的 builtin 全量通过（21.820s），实际网关补测仍排队。原现场正文的18项离线复测中，三项已不再进入未知余项；剩余 image、input_transformations、usage 内 TTL 明细仍独立拒绝。旧27项诊断结果完整保留，不把诊断删字段的三个成功对照当作生产修复成功。

[官方 Prompt caching 合同](https://developers.openai.com/api/docs/guides/prompt-caching#monitor-cache-performance) 明确30分钟与12小时写缓存计数互不重叠，合计等于 cache_write_tokens，且已包含于 input_tokens。来源已有 CacheCreation 总量解码，只遗漏 TTL 明细。优先用现有 Counter／Usage.Details 建立中立分项并写清包含关系，Spec 验证实际出现的总分约束，Mapping 重建自身 TTL 对象；不能再加到 input/total，也不能把其他协议5分钟／1小时改名冒充。客户端省略明细需明确规则与诊断，内部计量保留；费率不硬编码进转换器。完整响应与流式部分快照分开验证，不能因缺字段补0。

现场最终共8次生成 HTTP 请求、1次模型发现请求，全部独立实例已关闭。其中 Responses 两次上游成功；Anthropic 六次生成和一次发现均为403分组权限拒绝，尚未得到生成用量，不能证明 iterations/speed 缺失或记作 F02 已复现。曾测试审计配置的 Haiku4.5、报告的 Haiku5.5，以及原测试实例中只读取得的配置凭据；事后比较证明最后一项与审计凭据相同，两次无新增权限的重复调用也完整计数，未隐去。88个证据文件的摘要及逐请求正文完整性证明位于现场目录的 `summary.json`。

Responses 修改点：`packages/openai-responses/mapping.json` 的 `decode-response-remainder`、对应流事件余项、来源字段观察与 conversionDefaults。Anthropic 修改点：自身 Mapping 的 `decode-usage-remainder`、usage 累计/完整响应和流式尾帧处理。

决策规则：

- 若字段是行为约束或计费事实，建立有文档的中立 core 表示，由来源写入、目标消费；目标不具备等价行为则拒绝。
- 响应的有效策略等观测优先使用具有明确中立 Meaning 的 metadata 和受控投影机制。同源必须能去 Native 重建；跨协议的省略需要具名规则和路径诊断。现有 fieldObservations 只覆盖特定 HTTPJSON 根 Value 字段，不能直接宣称它已支持这些嵌套字段和 SSE。
- `context_compression:"disabled"` 不自动等同于“无语义”；只能在外部合同证实其与省略的关系后对精确值建立规则，不匹配的新值继续拒绝。
- `iterations` 不自动当成 token 总和；`speed` 不自动丢弃。若 iterations 含分项用量，要区分 observation 与 accounting，避免重复计费；精确整数、零、部分输入/输出、缓存与最终快照必须保持。
- 现有 Usage.Attribution 明确表示已含在模型总量内的分项，不能承载额外 compaction/advisor/fallback 账目。若新增核心执行账本，需明确顺序、用途、模型、计数及相对顶层总量的“包含／额外／未知”关系；内部计量事实独立于客户端 usage 投影保存。未确定的 SSE 数组更新关系继续拒绝或保留来源观察，不猜测 append。
- 目标只读中立事实或检查所有者，不解析另一家的 usage/image 对象。

进一步沿落库路径核对，`updateRecordProtocolUsage` 仅接收 Usage，普通响应 metadata 不进入内部 ProtocolUsage；原始正文又可能关闭或截断。因此 F02 的 speed／iterations 应使用 Usage 中明确的中立执行事实，而非只加入可省略 metadata 名单。现有 Counter、MergeUsage 与 JSON 记录可复用；新增字段还须进入核心读写、克隆、语义比较、资源预算和用量队列保留成本。客户端投影之前保存源事实，原顶层计数和统计列不累加执行分项。

首批边界应明确：完整 JSON 的已知四类 iterations 可以独立记录包含关系；speed 可支持 JSON 及 message_start，后续缺失不清空已观察值。SSE 中显式 iterations 的更新语义未证实，继续保留来源所有权或明确拒绝跨协议，不猜 snapshot／append。fallback_message 只标记返回消息的最后一次 sampling，不能据模型名或位置划分全部 attempt；关系无法证明的 message 项使用 unknown，不强算总量。排除于顶层也不等于额外收费，速度、价格修订和 credit 未齐备时不计算推测金额。具体修改点和验证清单位于 `.tmp-dev/three-part-cutover/f02-implementation-notes.md`，上述首批核心扩展已于 dev.167 实施；SSE iterations 等剩余边界见[执行用量合同](execution-usage-contract.md)。

验收：JSON/SSE 首帧、增量、尾帧与完整响应；同源去 Native 保留；跨三目标 compatible/strict/禁用；未知邻接字段与未知枚举仍拒绝；计量不被改变；不含目标行为语义的来源扩展不能神秘变成 target 参数。

### B. F04/F05：媒体保真与 detail 策略

先完成上述迁移拒绝恢复。F04 需要取得原始 `request-0252/request-0253` 输入及激活配置，核对是否命中本地已复现的组合证据问题。`matchGatewayCombination` 在实际 EncodeRequest 之前返回保存的失败报告；报告里的 `/payload/mime` 因而可能描述验证样例，而非当前请求。不能仅据该路径认定当前图片失去 MIME。

这一已复现问题的第二阶段修复涉及通用验证运行时与 Mapping 的核心范围声明。保留无 MIME 普通 URL 的失败检查及完整报告；为 MIME 已明确、目标可表达的媒体建立单独验证的范围，验证记录绑定范围、双方修订、样例、策略和编译器身份。真实请求必须整体满足该范围，包含工具结果中的嵌套媒体；不能拼接多个局部通过项充当完整证据。范围条件使用中立 core 字段，配置归属各自 Mapping，运行时不加入“Chat 到 Gemini”的分派。无正例、范围不符、策略变化或陈旧证据继续拒绝，完整失败样例不计作成功覆盖。只有现有通用表达能力不足时才扩展验证 DSL／合同，不改外部 Spec 来伪造支持。

验收保留两组同时成立的条件：Chat/Responses 的合法 data URI 经默认真实网关到 Gemini，JSON/SSE 均成功且出站 MIME／字节一致；无 MIME 的普通 URL、未知 detail 和失效证据仍明确拒绝且不调用上游。现阶段的网关日志是问题复现，尚不是上述修复的通过证明。

若原始请求指向其他阶段，再按数据追踪归属：来源未解析 → 来源 Mapping；核心变换删除 → 声明的变换规则；目标没有使用 core.mime → 目标 Mapping；只有通用字符串原语行为错误才修改 `plan_data_uri.go`。

当前来源 `decode-media-url` 与 `split_data_uri` 已实现标准容器拆分，不应新增 png 默认值、猜测 URL 后缀或网络读取媒体。新增测试须保持 payload 的 base64 字节与 MIME，不以模型颜色答案替代传输断言。非 base64 URI、空 MIME、普通 URL、额外 media 参数分别按已声明合同处理。

若第二阶段支持 detail:auto：先定义其核心含义与目标能力证据。可以省略时，来源将默认偏好归一化为明确的核心意图，或者目标对中立 detail 声明带诊断的兼容取舍；不能在 Anthropic 中写“来自 Responses 时删除 detail”。high/low 或未知含义保持拒绝，strict 按是否有损决定，不借用无关的媒体 capability 放行。

### C. F06：结构化输出的中心合同

dev.167 已实现 `parameters.outputConstraint` 的版本化中立合同，首批子集和固定值约束缺口见[输出约束合同](protocol-output-constraints.md)。以下保留原设计要求：输出 kind、Schema、可选名称/描述、strict 意图与明确 Schema 方言。字段名以项目现有规范确定，不能只把 `response_format` 换一个名字却继续按供应商分支解释。缺失/null/空对象与 JSON-only、JSON Schema 是不同语义。

四方独立完成：

- Chat `response_format` ↔ core 输出约束。
- Responses `text.format` ↔ core 输出约束；text 内其他余项继续由 Responses 所有。
- Anthropic 自己实际支持的 structured-output 字段 ↔ core；不能把已有 output_config.effort 当结构化输出。最终字段以固定外部版本文档与真实载荷为据。
- Gemini `generationConfig.responseMimeType/responseSchema`（或该版本明确支持的其他 Schema 字段）↔ core；已知原生参数逐步移除双重表示，避免 source-owned 余项与 core 冲突。

Spec 验证外部结构及事件合同；Mapping 完成 Schema 方言与核心之间的显式等价转换；核心通用校验负责中立类型/资源预算。优先使用已有递归/结构原语，确有不足才设计通用 Schema 结构操作，不能添加 `convert_openai_schema_to_gemini`。

最小可发布子集必须写清楚：object/array/scalar、required、enum/const、additionalProperties、nullable、数值/字符串限制、组合/引用支持范围。不得静默删除 additionalProperties:false、required、const 或 strict。不能表达就准确拒绝并指出具体 Schema 路径；名称/描述若按合同可省略也应保留取舍证据。

公开合同已另行固定并保存在 `.tmp-dev/three-part-cutover/output-contracts`：Anthropic 当前字段为 `output_config.format`，旧 `output_format` 另需 beta header；Google python-genai 3.3.0 的 `GenerateContentConfig` 明确区分 OpenAPI 子集 `responseSchema` 与 JSON Schema 子集 `responseJsonSchema`，后者要求 MIME，且二者互斥。Google 的普通 `oneOf` 会按 `anyOf` 解释，部分约束会被忽略；Anthropic 不支持部分数值、字符串和数组约束。不能把“请求被接受”当作严格约束等价。优先用既有 walk/recur 与结构原语表达各自的有限合同，未知或不等价约束继续拒绝；不能补 required、将可缺失字段改成必需 nullable、或把限制搬进描述后宣称无损。

验收分两层：先各方 wire ↔ core 独立合同（去 Native、编辑 core 后自身重建），再16方向 × JSON/SSE 组合。不把符合 Schema 的单次模型回答视为约束已生效；加入与 Schema 冲突的提示词及出站载荷检查，供应商拒绝/超时另记。

### D. F03/F07/F08/F09/F13：补足因果证据后再动生产代码

统一每次请求记录入口/上游修订与策略、来源路径、时间阶段、错误拥有者；签名/加密续传只记录摘要与 scope 标识，避免泄露原值。F03 比较签名所在 part、顺序、关联工具 ID、参数、第二轮 history 和 account/model/session scope，不能仅比较 signature 字符串。

F13 的 `backend/server/http_delivery_test.go` 已覆盖下游拥有者、上游失败、deadline、shutdown、未知 EOF/EPIPE、取消清理及 panic 优先级。实际断连阶段及 goroutine/reader/collector 释放仍需对应证据，不能把一般取消归因客户端。缺少下游证据时503可以是符合合同的结果；上游 EPIPE 不允许归因客户端。实际供应商是否停止计费不属于网关可证明内容。

这些问题没有足够证据时不实施推测性“修复”；明确待验证点比修改协议错误层更有价值。

### E. F10/F11/F12/F14：测试与工程验证

F11 已将重复的建 source/group/binding 流程提取到共享审计辅助方法。ModelSource 本体不接受虚构 protocolBinding 字段；先创建 source，再通过实际 `/api/admin/protocols/bindings` 的 PUT 单独绑定修订。读取 runtime 激活修订、Spec 端点与 bindings 交叉核对。可参照已运行的 `scripts/performance-versions.py` 中 production 配置流程，但不要在审计脚本复制旧 platform。

配置部分已实施于 `scripts/protocol-audit/src/bindings.mjs`，准确 DTO 为 `{kind:"source", sourceId, binding:{protocolId, revisionHash, capabilities, transports}}`。三个审计入口共用它，检查当前加载、存储和组合证据身份，并按 Spec 选取端点。绑定后 API 展示的 `custom:<protocolId>` 必须随后续完整来源更新保留；省略会显式解绑，不能将这一有效绑定标识与已退役的 platform 预设混淆。22项本地检查通过；真实临时后端验证四协议配置、更新后绑定和端点派生通过，模拟上游调用数为0。第一次临时配置因未显式允许 loopback 而被出站校验拒绝，记录保留，随后仅在独立测试配置允许 loopback；未修改真实配置。

SDK 回放从 SDK 返回值/事件中按顺序聚合可见文本，验证 finish 与工具 ID/参数；不对整个序列化对象搜索 hi/lookup，避免跨块漏报或元数据假阳性。工具矩阵显式比较预期方向/模式/SDK/轮次集合。授权之前拒绝的请求不伪造 generation 记录，取消预期按证据分支。

SDK 语义断言已实施于 `scripts/sdk-audit`：原生四协议与 Vercel v2/v4 分别解析实际返回结构，检查文本增量、工具关联、完整参数和终态；Responses 同时核对增量与最终快照。15项纯 Node 检查通过。历史并行工具捕获的128项客户端检查通过；更旧捕获得到236通过／84失败，其中64项捕获本身的参数已为空对象，20项为 SDK 将大整数舍入。失败完整保留，不能据此声称当前网关存在相同问题或将期望整数改小。这是 dev.163a 前的中间结果；后续已完成160份完整捕获与回放，300项通过、20项客户端整数精度失败，见顶部与 F11 条目。

覆盖报告按文件身份列出当前生产器与完整组合的缺项。dev.163a 之前的三个生产器曾产生32个文本、36个单工具、16个并行工具捕获，共84个；原说明“160个完整捕获”已修正。历史更广的捕获不冒充当前测试覆盖。客户端已将数值对象舍入时，测试报告客户端保真限制；JSON 参数字符串用精确整数解析，网关不得为客户端通过而改写数字。

报告提出的“工具矩阵选择不完整”已在 dev.163a 补齐：单工具和并行工具生产器覆盖全部16方向、JSON/SSE及两轮，与文本捕获合计160份，精确集合验证见 `f11-sdk163a/report.json`。此前84份的缺口记录仍保留。原大整数、工具关联、签名和第二轮历史保持，失败完整报告；SDK 自身数值精度限制不能通过修改网关或缩小期望数值消除。

F12 的测试修正已实施：Playwright 控制时钟保留15秒实际间隔，将人工 retry 与自动恢复分成两个确定性用例，分别断言失败后只有一次对应恢复请求、错误消失及历史保留。首次运行因本机缺少 Playwright Chromium headless shell 未能启动浏览器，随后使用已安装 Edge 完成两例验证；环境失败日志保留，不混入通过数量。未修改产品行为。F10 先观测 seek 完成、实际 video frame callback、trace ready 与 renderer finish reason；适合把纯状态转换提取成可注入时钟/帧事件的短函数，但不因测试失败就重构整个渲染器。6500/900 等仅在实际修改该处时提为具名常量，保持默认行为。

F14 保留完整测试清单，分组集合严格等于该清单，检查无漏项/重项、每组 exit code、race 告警及冻结哈希。不能以增加单次总超时替代进度定位，也不能用定向通过覆盖完整超时事实。

## 修改点代码风格验收

只约束新增或修改点，不扫描未改区域作风格性重构。

1. 同目的的来源解码、目标编码、校验表达式邻近放置；重复逻辑提取为语义清晰的命名表达式/函数。跨协议共性只抽成通用原语，不把 provider 字段集中在 Go switch。
2. 对每个新增/保留 fallback 记录可到达输入。前置 Spec 已保证类型时，Mapping 不再重复保护同一类型；但外部字段合法与目标可表示性是不同边界，不能删掉后者。相同拒绝理由可合并条件；不同诊断路径不要为了少行数合并。
3. 不把缺失当 null、不把未知枚举当默认、不把未知计量当0。没有等价目标表示时明确拒绝，不能用空串/空数组让编码成功。
4. 布尔值/谓词使用 is/has/can/should；函数使用动词+名词；变量表达具体作用。新增公共 Go API 使用项目风格文档注释，公共 TS/JS API 使用 JSDoc。删去只重复代码动作的注释，保留解释语义、所有权、取舍和预算的注释。
5. 删除被替代且无调用的辅助方法与重复规则；复杂 decode/encode 按媒体、用量、Schema、生命周期责任拆分。优先提前返回、合并相同条件，避免层层默认值。
6. Go/TS 中的时间、容量和枚举等参数使用文件头部具名常量；协议 JSON 本身就是配置，按现有 DSL 引用/命名表达式复用，不额外发明配置层或把协议常量硬编码进宿主。
7. 验证只补真实失败输入、边界与策略反例，不为纯重命名、排版或机械抽取写镜像测试。变更需要的检查通过后进入下一步，不无理由重复全套。

## 本复核已执行验证

- `go test ./protocol/builtin -run '^(TestDraftMediaURIInputs|TestNestedMediaExtensionsPreserveEditsAndRejectCrossProtocol|TestPackageDraftForeignWireOwners)$' -count=1 -timeout=90s`：通过，0.651s。验证 URI 同源保留/扩展所有权，不等同于完整图片跨协议验收。
- `report-triage-media-probe.go`：运行成功，24条实际三分编译/转换结果写入 jsonl；发现16个 detail 静默丢失反例，其中8个 strict。生成成功不是通过断言，反例是最初发现并已用于后续回归的历史失败输入。
- 上述最初离线复核未调用真实模型、未修改生产代码、未变更数据库或协议绑定；其后的用户授权现场采样与迁移修复分别见本文对应记录。完整 server race、D0 与发行构建以主任务冻结验收证据为准。
