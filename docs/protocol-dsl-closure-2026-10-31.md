# DSL 功能闭合与性能验收记录

基线为 `d273ce96 / 2.0.0-dev.88`，分支 `refactor/protocol-dsl`。本轮先完成能力迁移，
再对功能等价的完整链路测量并优化；未通过门槛前不切换四预置，不移除生产旧入口。
仅本地提交，使用合成数据和模拟上游。

当前阶段为 dev.101：已补 Chat 请求工具邻接与根控制字段、Responses 普通文本/容器、进度及错误源流，
并独立修复有交付校验器的 Anthropic 原生文本接续。四协议 SSE、完整请求动态约束、
组合调用、正式性能测量及最终退役仍未全部完成，生产预置继续使用旧执行链。

## 基线归档

源码归档及六目标独立二进制、SHA-256、Go 构建信息保存在
`.tmp-dev/protocol-dsl-closure-2026-10-31/baseline/`。
六目标均使用 Go 1.27.2、CGO=0、PGO 关闭构建；Windows amd64 使用隔离数据库启动，
健康检查、四预置、编译器版本和内嵌 WebUI 均通过。其他五目标仅验证构建。
检查记录为 `.tmp-dev/protocol-dsl-2026-10-29/checks/closure-baseline-{six-targets,smoke}.*`。
此前 dev.81 产物不用于本轮对比。

## 功能覆盖与切换门槛

| 能力 | 基线状态 | 本轮验收依据 |
|---|---|---|
| 四协议 JSON | 有两源草案，仍有字段及拒绝差异 | 既有样例、无根 Native 重建、未知 owner、错误归因逐项闭合 |
| 模型与 Agent | 目录与参数声明已接通；模型策略未迁完 | 完整模型投影、工具第二轮与原调用次数 |
| SSE | 仅通用显式多动作，四包未配置 | 真实四协议帧、工具关联、usage、终态、取消与输出原字节 |
| 任务／双向会话 | 两源夹具已实现 | 管理验证、预览和调用入口接通；保持后台生命周期 |
| 管理与激活 | 仍按 v2 编译、保存和显示 | 一个 v3 包、一个修订／报告／激活事务，两源均参与哈希 |
| 性能 | 短 Responses 解码仍有约四倍组件回退 | 功能完整前后五对组件及完整 HTTP，独立 profile 与保留实验 |
| 旧生产引擎退役 | 尚未开始 | 功能与性能门槛满足后删除；旧数据可读且不自动重绑 |

历史字段清单见四包草案，已完成切片以本记录和
`protocol-dsl-completion-2026-10-30.md` 的较新记录为准；草案的早期未完成列表不能代替最终验收。

## 第一批：独立来源捕获（dev.89）

新增供应商无关原语 `capture_source`。它使用原文读取的位置证明，只构造 Provenance，
不保存正文快照，因此关闭 Native 保存后仍可给节点与 metadata 提供可信来源。
复用 `capture_native` 的位置推导和核心私有标记，不能通过普通对象或重排后的下标伪造来源。
原来的 Native 关闭行为不变；只有显式使用新原语才独立保留来源。

定向验证覆盖保存开关、授权仅调用一次、最终模型 scope、递归来源和构造容器拒绝；
记录为 `closure-source-capture`。本阶段没有测性能，不能宣称已经消除组件回退。

用户已批准 Chat/Gemini 来源改为真实原文位置及原文片段。这是表示变化，必须保留精确
旧／新对照并验证最终行为，不能把其他诊断差异归入此例外。现有静态错误／值账本继续保留。

## 第二批：来源变化的反向编码与 Gemini 工具分区

Chat/Gemini 已批准的来源差异在原迁移测试中单独标记为 `APPROVED REPRESENTATION`。
每项仍检查原先精确的差异路径和静态旧／新值，没有删除快照或扩大允许差异；其余拒绝诊断
继续标为 `NOT CLOSED`。复用这些输入增加原生重放、删除根 Native、修改模型后的反向编码。

对照实际发现 Gemini 混合工具桶的功能缺陷：函数声明已经解码为独立 function 工具，
而 hosted 工具的新完整 Native 又包含同一 `functionDeclarations`，删除根 Native 后重复输出。
Mapping 现在从 hosted 工具渲染中排除已由 function 工具承担的字段，保留原始快照和其他未知成员。
修复前记录 `closure-json-reencoding`，修复后全部 Chat/Gemini 迁移与新增反向对照通过，
见 `closure-source-reencoding`。不改变 Go 编码器，也不以来源表示批准覆盖这一实际功能差异。

## 性能证据边界

此前 12 秒 CPU／分配定位样本属于有界对象读取优化之前，且未保存匹配可执行文件。
其 Sonic 临时对象、计划递归和 GC 热点只作为候选线索。本轮功能完整后重新采集匹配二进制的
CPU、alloc_space/objects、inuse_space/objects，正式计时不启用 profiler 或主动 GC。
已有旧生产 HTTP 收益不当作新 DSL 的验收结果。

## 第三批：两源管理入口

统一编译入口现在识别 v3 包；保存从 `/spec/id` 核对身份，仍以完整包作为草稿编辑令牌，
验证生成包含编译器的不可变修订哈希。没有分开保存或激活两份源。会话预览、CLI、
可用协议目录、绑定默认能力和历史恢复均读取包元信息，避免继续通过旧 Definition 得到空数据。
管理 schema 同时提供 package/spec/mapping 契约；旧入口只是迁移期间保留，尚未执行最终退役。

WebUI 在原编辑器内提供两源页、导入导出及源内诊断定位，复用整包保存／验证／激活流程。
大整数和原文拼写不经 JavaScript 数字转换。编辑任一源即使原有证据不再适用于当前草稿。

真实 SQLite 的整包修订、CAS、激活隔离、重载、历史元信息测试通过
（`closure-package-lifecycle`）；Agent/CLI 管理与会话预览通过
（`closure-json-and-management` 中对应包通过，首次 builtin 测试助手编译错误另行保留）。
前端类型检查、定向 lint 通过。浏览器首次新增用例因重复按钮定位失败，修正定位后
`closure-package-ui-confirm` 为 3 通过、2 个既有依赖真实后端环境的用例跳过。
本批没有替换生产预置或自动更改旧绑定。

## 第四批：Responses 工具用量与独立 metadata 来源

Mapping 现在双向映射原有八类 hosted-tool 计数，保留未知余项以及 empty/null 区别。
Spec 按原顺序检查工具桶类型、非负 int64 和分项小计；错误包络沿用旧入口处理边界。
新增通用 WireAssertion 诊断声明，使 wire 选择位置和公开核心错误位置可以分别声明，
仅对实际失败的断言生效；非法声明在编译时拒绝，原始 cause 保留。
这里刻意保留旧小计错误的 `decode_event / stream` 归因，没有扩大已批准的诊断修正范围。

原本读取 `Native.source` 的 Responses 内容 metadata 现在有独立可信来源：Native 开启时
复用既有来源，关闭时使用 `capture_source`。不保存第二份原文，annotations/logprobs 路径和
scope 不再因关闭快照而丢失。

修改前草案放行了旧入口拒绝的九类非法工具用量，记录 `closure-tool-usage-before`。
修复后完整核心及无根 Native 编码、未知余项、类型/小计诊断、Native 开关及 metadata
重建通过，见 `closure-responses-tool-source`。中途编译证明和诊断包装失败均保留原日志。
本批没有迁完完整错误包络或所有 usage 扩展，也尚未测量这一切片的性能。

## 第五批：四包的实际 JSON 样例证据

四份 Mapping 不再保持空 samples：从原预置迁入 Anthropic 30、Chat 25、Responses 36、
Gemini 28 项 JSON 样例，共 119 项。用已有 ProtocolDocument 精确源片段编辑器搬移，
仅调整核心输入／预期根部的 schemaVersion 1→2，不经过数值转换、不重新生成预期输出。

`closure-authored-json-evidence` 对这些样例执行真实 Verify 流程；JSON 方向的输出、
往返和能力证据全部通过。测试明确只断言这 119 项，其他尚缺的流和 Agent 能力证据
仍影响完整报告，不能把本批通过写成四预置已满足激活要求。

## 第六批：真实帧所需的通用流执行（dev.90）

Spec 支持动作条件、动作自身的身份字段、显式缺失身份继承和整数 item 索引；
条件只读当前 wire，不能依赖 Mapping 隐藏源生命周期。使用 Anthropic 实际帧形状验证
message_start、文本／工具 block、usage 和 message_stop，null 身份仍不能充当缺失。

事件 Mapping 的命名 state 在编译期绑定槽位。每次更新读取同一旧快照，成功完成整个
事件边界才替换；类型、累计字节、节点和槽位数受原预算控制。跨帧值独立拥有正文，
不能让小片段保留整个输入帧；scope 不同不得复用状态。状态不授予 Native 来源权限。

frameBatch 将核心事件渲染为帧数组，逐帧直接编码与验证，不先编码整个数组再拆解。
finalize 只在观察真实核心终态且实际渲染过事件后执行；Native-only 不补帧，已交付终态
不重复输出，晚到 usage 仍可先合并。成功、失败和顶层请求结束均释放所属状态。
源与目标共享 EvaluationState 时，source.Finish 只释放自身方向，顶层结束才统一 Release，
避免清掉下游 finalizer。后台任务和会话仍在其真实生命周期结束时释放。

`closure-stream-state`、`closure-stream-frame-batch` 与 `closure-stream-lifecycle` 通过，
覆盖状态回滚、scope、取消、原生一次回放、批量输出、终态/usage/EOF，以及网关流与会话回归。
`closure-protocol-all` 在前一批量帧修改集上协议全量通过；完整工程与性能验收仍未执行。
四预置真实 SSE 声明继续迁移，不能将本机制提交称为四协议流功能已完成。

## 第七批：模型能力与输出上限声明（dev.91）

四包的 modelPolicy 声明已接入编译，模型名称归一化、具体型号优先级和例外来自配置；
字面量能力表在编译时验证，绑定的能力仍整份覆盖推断，不伪造旧 Codec 名称。
必要的通用字符串匹配和整数乘除／余数操作保持 Unicode 与 int64 规则，不借浮点数近似预算。

model_controls 复用默认规则 ID `reasoning-controls`、顺序 175 和用户覆盖机制。
投影只读写当前请求的 reasoning、单个 parameter 和输出上限来源，不序列化完整历史。
有序诊断先于对应字段更新；后续用户规则执行完仍使用同一目标约束检查结果。

本切片完整承接输出上限：Anthropic 默认值、绑定硬上限、显式／系统默认的区别，以及
合法 `-0` 的既有规范化。能力表 63 个模型分支和输出上限 52 个场景的结果、诊断、候选归因
对照通过；日志 `closure-model-capabilities-ready`、`closure-model-errors-indexes`。
后一组仅其他代理的错误包络用例失败，模型／索引对应检查无失败。
完整 reasoning 模式、预算与等级的投影仍在迁移，不以本批通过替代 Agent 全链路验收。

## 第八批：Responses 错误包络与有序拒绝（dev.92）

错误的类别、type/code 优先级、HTTP 状态、新 API 包装、未知 owner 和反向渲染迁入
Responses Mapping。纯错误包络继续保留不会作为生成参数校验的扩展。配置抽出共用
响应字段、正文 metadata 和错误表达式；不在 Go 中添加 Responses 策略分支。

`check_fields` 检查选中对象的静态允许字段／前缀，错误位置来自实际成员键或首个叶，
共用旧首叶定位实现。`reject` 在实际选中的分支中停止，用来维持类型、字段、类别、
owner 合并与 details 的检查顺序；不使用虚假字段或永不生效的规则模拟拒绝。
这些动作不解析完整请求历史，不把原文值放进诊断。

完整错误核心、原 Native 回放、删除 Native 后重建、无 HTTP 上下文、显式 null、未知
扩展及已覆盖负例的 code/direction/stage/path/reason 对照通过，见
`closure-responses-errors-final` 与 `closure-anthropic-source` 中的 Responses 检查。
无 HTTP 上下文的首版绑定错误和此前反向结果保留，未通过修改旧预期消除。

仍有明确边界：多个未知核心 error 字段在旧 Go map 遍历中的首错不稳定；非 null 非法
字符串类型的新静态原因与旧 Sonic 详细文字／内部错误树不同，已在测试中标记，不能称
逐字一致。普通响应中 metadata 和 error 同时非法的完整首错组合仍待补齐。

逐提交复核还发现分块暂存遗漏了编译器分支边界，已在 `19948419`、`77715520` 修正。
`closure-committed-protocol-fixed` 对独立的实际提交源码执行协议全量通过；此前只测工作区
不能作为这些提交自身通过的证据。后续最终整理将同时核验提交边界。

## 模型策略全量复核

`closure-stream-atoms-errors-model` 中的模型能力、源 alias 规范化、四向 thinking 投影、
后置用户规则及预算边界差分通过。声明保留默认 `reasoning-controls` 的顺序 175，
包括严格模式、绑定硬上限、未知 Agent 模型和渲染拒绝；性能尚未验收。

随后 `closure-integrated-stage` 暴露了两种不同问题：

- 旧草案的 `TestPackageDraftHistoryTargets` 用整份默认策略测试历史处置，却未提供
  目标模型。新接通模型规则后，unknown 模型会按旧生产策略省略 thinking 或严格拒绝；
  禁用历史处置后，未消费的 historyRetention 也会触发既有渲染拒绝。因此将该用例限定
  到实际声明的 `request-projection`，保留原有结果、禁用与诊断断言。完整默认策略的
  模型部分继续由独立的新旧差分检查，不能用这项隔离宣称默认组合全量通过。
- Agent 离线回译只对旧 Codec 运行默认策略，漏过 v3 声明的模型投影；Chat 草案的
  reasoning 解码又仅保存 effort，漏掉 mode 和 effortScope。前者改为统一使用真实
  默认转换入口，后者补齐声明。既有 Agent 差分增加回译后的 parameters/reasoning
  比较；没有放宽样例期望。缺少真实流声明的 Agent stream 样例仍是待迁移项。

以上修正已通过 `closure-model-full-paths`，包括真实默认策略入口、Agent 回译、历史投影、
Chat JSON、四包 119 个样例和用户自定义 Native schema 回归。原始失败日志保留。

## 第九批：Anthropic 实际流帧与通用增量状态（dev.93）

Anthropic 两份源增加 `stream` 传输、`provider-event` / `client-event` 文档和双向事件程序。
文本、工具参数、thinking、签名与加密块的五组现有目标样例比较完整帧字节；另外对照
晚到工具名称/调用 ID、文本快照只追加后缀、终态自动关闭工具以及无显式原因的工具终态。
这批对照通过 `closure-anthropic-continuations-ready`。原始编码样例没有初始 usage，
因此组件字节对照和使用原目标 usage 准备规则后的最终 wire 对照分开，未更改旧样例。

原有 JSON 与流 usage 渲染共用 `encode-bound-usage`，合并核心计数的 `merge_usage`
复用已有 `MergeUsage`。`resolve_item`、`track_text`、`track_tool`、`index_values`
只处理身份、增量和有界集合；供应商字段与帧顺序由 Mapping 声明。文本保留摘要和长度，
工具参数仅保留未完成 JSON 或尚未发送的内容；成功提交事件后才推进持久状态。

源端缺失/null index 的归零，以及缺失/null/空 response ID 的旧接受边界由显式 Spec
表达；目标文档不继承源端宽松身份选项。原 `visit` 不伪造数组 `/0` 路径，也不赋予
普通 JSON 来源权限。Native-only 保留整帧一次，不通过重建每个语义事件重复发送。

仍待闭合的项目如下；当前生产预置继续使用旧链，不把本阶段的样例成功当作完整迁移：

| 范围 | 已验证 | 尚缺 |
|---|---|---|
| Anthropic 源流 | 文本、工具、thinking、加密块的现有轨迹 | 分片签名的可信节点保存、未知字段多容器的真实来源表示、完整 metadata |
| Anthropic 目标流 | 五组既有轨迹及四类续传边界 | metadata 投影及首错顺序的完整覆盖、初始 usage 等最终发送约束独立绑定 |
| Chat / Responses / Gemini 流 | 通用状态与动作基础 | 真实数组帧、隐式开始、晚到身份及对应目标渲染尚未完整接通 |
| Native 到修改后流 | 已复现旧 `target item has no start` | 是否独立改变接受边界仍待确认；本阶段保留旧拒绝 |
| 管理及策略 | 整包管理、模型能力与 thinking 投影 | 四预置完整组合证据、流 Agent/续传/任务调用链 |
| 性能与默认切换 | 基线归档已完成 | 功能完整版本正式测量、火焰图、优化及退役均未开始 |

当前通用 Native observer 只供已声明的观察程序使用。Anthropic 预置观察程序不补建
旧实现未维护的混合流状态；不能将通用机制测试通过当成已批准修复该旧行为。

本阶段还补齐保留预算：槽位、索引、工具正文、身份别名及 tracker/call ID 键共同计入
现有 BufferBytes。新增反例发现原草稿错用了临时表达式工作预算（原值的八倍），因此
修正为编译修订的原始保留上限；没有调高测试限额。组合超限不提交事务，工具完成释放
正文，最终 Release 归零。每次增量只调整分类计数，不遍历已有表重新计算。

阶段验证记录：

- `closure-dev93-protocol-confirm`：协议全量通过，包含 finalDocument、自定义 Native
  约束、模型策略、四协议错误包络、JSON 证据、流状态和保留预算。
- `closure-dev93-go-all`：Go 工程全量仅 `TestCLIReferenceUpToDate` 失败，原因是之前
  管理端改为 v3 后未重新生成 CLI 文档；其余测试通过。使用现有生成器补齐文档后，
  `closure-dev93-cli-reference-confirm` 不带更新开关验证通过。
- `closure-dev93-vet`、`closure-dev93-race`：全量 vet 与相关流/模型/最终文档 race 通过。
- `closure-dev93-staticcheck`：v0.8.2 仍以既有 45 条诊断退出 1；按诊断内容和数量比较，
  新增 0、移除 0，明细保存在同名 `-diff.json`。

本阶段没有新的正式性能结果。最终审计、页面验收及六目标新版本产物仍随完整迁移验收，
不能用开始时的基线六目标产物替代。

## 提交边界复核

本轮自己的 `de405b9e` 与其编译修补 `19948419`、`77715520` 已整理为可独立构建的
`6e7ad73c`，之后 Responses 错误包络提交为 `6035284f`，第九阶段为 `25e20735`。
整理前后的阶段末尾源码树均为 `2ea0f29e29659b86e88b7769cca4f03a12140073`。
原提交保存在忽略目录的 `before-history-fold.bundle`，历史检查日志不改写。
`closure-dev93-committed-protocol` 在独立检出的 `25e20735` 上执行协议全量通过；
本轮之前的提交未重写，没有推送。

## 第十批：真实 Chat 流、最终交付合同与证据分层（dev.94）

Spec 增加有序数组动作、结构选帧、隐式开始、稀疏工具身份及声明的终态关闭。
当前成员与根帧的读取分开，终态缺失名称/ID、非法参数仍定位原 `/choices/...` 路径。
缺失可选数组与 null 不混为一谈；前置失败、空主计数 usage 都必须显式声明，默认
合同继续拒绝。Wire 观察器身份/诊断路径与未完成工具正文共用原始字节上限，原身份
自身超限的错误链和计量名保留。

Chat 目前通过四个原源样例、三个 metadata/晚到 usage/晚到工具身份补充序列，
目标三个原样例分别比较四种 ClientOutput 意图；四条原生流逐帧原字节重放一次，
Finish 不补帧。其目标 choice/message metadata 仍显式拒绝，尚未迁完，不能默认切换。

Anthropic 目标已迁入当前帧 metadata、终态 metadata、usage metadata 的覆盖与冲突
处理；不额外积累整轮帧。两种 source/target 身份合同都只从真实 `message_start.message.id`
取身份；未知同名 `response_id` 扩展不再干扰独立校验。`finalDocument` 扩展到事件方向，
构造合同和最终生命周期须类型匹配；最终文件还检查初始 message/usage、终态 stop_reason
以及帧索引，保持裸组件编码与发送检查的原有区别。

四协议 JSON 响应的最终结构与首错已由独立 Spec 声明。完整对照清单及四个旧片段的
去向见 [最终响应合同说明](../backend/protocol/builtin/packages/json-final-response-contracts.md)。
旧片段仍由原 Definition 原样对照，并检查新旧最终拒绝相同；线上样例换为四个独立新 ID
的完整响应，原输入/预期未修改，JSON 证据仍为 119 项。组件执行器不负责授予激活证据；
Verify 在输出匹配、往返和最终文档检查全部通过后才计入方向与能力覆盖。

实际缺口带来的通用构件包括数组 `length`、显式最后值覆盖的 `from_entries`、读取
原诊断位置的 `reject.source`、仅失败时求值的 `reasonExpression`，以及当前调用的
`runtime /clientOutput`。没有引入供应商专用执行器；未给 missing/null 增加隐式默认。

验证：`closure-dev94-protocol`、`closure-dev94-go-all`、`closure-dev94-vet`、
`closure-dev94-race` 均通过；staticcheck v0.8.2 仍以原 45 条诊断退出 1，归一化后新增/
移除均为零。定向过程中的 nil ClientOutput、组合预算错误链和新样例 null 来源差异均已
修正并保留原始失败日志，未通过放宽断言处理。

现阶段的主要未完成项：

- Chat 目标 logprobs/annotations/native_finish_reason、源端多故障首错与细节预算。
- Anthropic 源端跨帧签名的可信来源保存、完整 metadata/usage 和多容器未知字段来源。
- Responses/Gemini 的真实 SSE 双向声明，以及原有任务/会话/Agent/续传的完整组合。
- 请求最终 wire 的完整角色、工具关联、参数与媒体约束。不能用再次调用公开 DecodeRequest
  代替它，因为该入口还有额外能力/scope 行为并物化完整 Request。
- 所有新功能版本的正式性能测量与火焰图、等价优化、预置切换及旧生产引擎退役。

代码审查另发现 wire assertions、Mapping 和生命周期仍存在重复读取/计量，以及旧
Chat logprobs 保留预算的检查时点差异。它们分别是待测性能线索与待差分的行为边界，
不是已经证实的收益，也不能据此提前删除校验或放宽预算。

## 第十一批：来源保留、工具输入切片与请求最终结构（dev.95）

`with_input` 复用已声明的 JSON 解码程序处理原帧子包络，input/root 的位置和核心类型
分别绑定，不物化中间正文。Responses JSON 工具流用该能力承接六帧源生命周期，
有序号和缺失序号的核心事件及 Chat 输出对照通过；原始 item ID 拼写、唯一性、冲突及
错误分类保持。终态 Node.Source 改为真实 `/response/output/i`，每份 Native 原片段
逐项核验，容器规范化差异单列；尚无 Responses 目标编码器，未宣称顶层原帧重放通过。

仅有工具的流要显式声明 `itemKinds`，并与动作及方向能力一致。原默认文本生命周期
保护保留；Responses 当前只标明工具输入切片，原全局 text 能力没有删除。
新增 `tool_snapshot` 复用既有参数快照规则，缺参数仍拒绝；原“动作尚不支持”的静态
测试改成缺参测试，供应商请求接受边界未因此放宽。

`retainSource` 表只保留真实捕获的 Source 凭据，普通 JSON 覆盖会清掉旧权限，Native
包络权限不保留。它复用独立拥有的索引 JSON，不再借用初始大帧。普通表、伪造值、
覆盖、scope/修订和释放边界经测试及独立代码审查；逻辑字节计量不重复计入共享 raw，
额外 Go 视图容器不能被称为零物理占用。Anthropic 用它保持初始块来源并解出分片签名，
完整核心事件及增量签名输出与原实现一致。

Chat 目标补齐 annotations 原始 JSON 字节去重及 native_finish_reason 的即时辅助帧/
最终附载顺序。完整交付样例先发实际文本，原裸组件 `1/0/1/0` 去重基线原样保留。
logprobs 仍未接入：已实证旧 BufferBytes=512 时，纯增量到完成可接受 1235 字节累计
metadata，随后若出现 Item 快照却会在 `/items` 拒绝。后续计量必须保留这两个检查时点，
不能新增提前拒绝，也不能靠保存整个前缀回避分析。

四协议新增请求最终结构文档，保留原构造与独立 operation 约束；Anthropic 最终文档
只附加必要的局部 thinking/output_config 断言，不二次验证整份构造文档。
工具邻接、全树关联、隐式 ID 与旧深层解码约束尚未闭合，见
[请求最终合同说明](../backend/protocol/builtin/packages/json-final-request-contracts.md)。
对应未迁移的旧拒绝明确列账，不能据此默认切换预置。

`closure-dev95-protocol-confirm`、`closure-dev95-vet`、`closure-dev95-race` 通过；
staticcheck v0.8.2 仍为原 45 条诊断，新增/移除均为零。工程全量最近完整通过记录为
dev.94；dev.95 尚未重复工程全量、最终构建或正式性能测量。

用户已批准把 Anthropic 原生前缀后修改文本增量的旧失败作为独立修复，允许该类旧失败
变为成功。本阶段尚未启用该变化，后续单独提交并记录；旧失败基线仍保留。

## 第十二批：Chat 请求最终工具邻接（dev.96）

Chat 最终文档现在按原顺序完成工具调用邻接扫描：逐消息对象读取、待完成调用对角色的
限制、结果消费、调用数组形状与所属角色、非空唯一 ID，最后检查是否全部获得结果。
扫描成功后才继续原角色与孤立 thinking 校验。它只保留未完成调用及末次调用位置，
不再解码完整 Request，也不保存逐消息结果数组；成功扫描的 map 成员全部显式省略。

通用 `forget` 和 `index_size` 只操作词法索引，删除归还逻辑预算，计数不枚举条目。
原始 JSON 键的比较方式保持不变。WireAssertion 的 `check` 可以用 `reject` 明确给出
失败位置与归因；其他失败仍使用外层断言，原始错误链保留。诊断分类补齐
`invalid_association`，没有增加供应商专用 Go 校验器。

原未排空调用案例已从缺口账本移入完整新旧差分；两轮各自匹配、但解码后 ID 重复的
案例继续登记为尚未闭合的全树关联。全树的字符串比较不能替换此处的原始 JSON 比较。
同时覆盖并行结果换序、相同转义与不同拼写、空/null 字段，以及多个错误竞争时的原首错。

`closure-lexical-index-removal`、`closure-wire-explicit-rejection-confirm`、
`closure-chat-final-adjacency` 定向验证通过。最初诊断编译失败的记录保留；修复允许的
分类后重新验证，没有改动旧期望。尚未正式测量此阶段性能，也未切换生产预置。

阶段提交另在独立源码检出中通过协议全量 `closure-dev96-isolated-protocol` 和相关
race `closure-dev96-isolated-race`。工作区同名首次全量尝试遇到并行编写的 Responses
合同尚未补齐方法，日志原样保留；该临时状态不计作本提交的验证结果。

## Anthropic 原生文本接续：首版未通过资源验收

用户已允许修复原生前缀后修改文本增量的旧失败。首版 observer 能保留真实块索引、
inline 文本摘要和终态编码事实，新旧文本回归及独立协议全量通过，见
`closure-anthropic-native-transition`、`closure-dev97-isolated-protocol`。

进一步对照发现原生流新增了 renderer item、索引标记及两个常数登记项。真实网关的
源和目标共用 EvaluationState，这些记录会与源状态一起占用原条目预算：

| StateItems | 原生文本块数 | dev.96 | 首版 observer |
|---:|---:|---|---|
| 64 | 21 | 成功 | state_items 65，拒绝 |
| 128 | 43 | 成功 | state_items 129，拒绝 |
| 256 | 85 | 成功 | state_items 257，拒绝 |
| 4096 | 1365 | 成功 | state_items 4097，拒绝 |

证据为 `closure-anthropic-native-shared-budget`；其探针退出 0 只代表成功收集到差异，
不代表修复验收通过。独立 State 的较早探针也观察到回退；小 BufferBytes 两组因配置
本身超过编译上限而失败，不能据此判断请求保留量。

此首版没有提交，已还原工作区中的 Anthropic 变化；完整补丁保存在忽略目录
`anthropic-native-eager-rejected.patch`。下一实现需复用已有交付状态，不能通过调高
预算、忽略观察错误或把原成功变拒绝当作获批例外解决。当前该独立修复仍未完成。

## 第十三批：Responses 文本容器及完成快照（dev.97）

真实 Responses 源流新增普通文本、refusal、多消息、多片段、空消息和终态补完成。
同一 JSON 消息解码程序也补齐 phase 与已知轮次标签，终态复用它，不另写流专用的
语义替代物。来源继续定位原始帧，Native 片段仍逐项保留；既有工具切片与样例字节未改。

Spec 声明父子关系、子位置、文本封口、完成快照核验及终态的完整容器覆盖。普通文本
允许不单独发送 text.done；显式 item.done 重复仍拒绝，终态对已完成项只核验、不再次
发 ItemFinished。文本沿用摘要，不保存累计正文；属性独立持有并计入既有共享字节预算。

独立审查发现并修正了两处问题：不带 ordinal 的完成核验原来漏比 parent；Mapping
原来将开始和完成状态各存一条记录，可能提前耗尽条目预算。现在 items/parts 每项
只有一条记录，原位更新完成状态；message 与工具开始共用重复登记检查。
既有少量轨迹继续逐帧比较完整核心事件，另以相同 StateItems=64 对照 48 个空消息。

本切片没有闭合 reasoning、annotation.added、完整 logprobs/usage 尾帧、metadata/
turn-state、心跳或 Responses 目标编码。异常字段、多故障首错也未完全闭合：例如
终态同时修改 phase 与 text，旧实现先核对 message metadata，新合同先核对子片段。
不能将正常轨迹的精确对照当成异常归因全部一致，更不能据此切换生产预置。

最终独立源码的 `closure-dev97-stream-final-protocol` 协议全量、
`closure-dev97-stream-final-race` 相关 race 通过，包含合并后的 48 消息容量对照。
合并状态时一次声明把诊断 path 放到了错误层级，编译器明确拒绝；已修正声明并保留
`closure-dev97-stream-review-confirm` 原失败日志，没有放宽编译检查或测试期望。

## 第十四批：Chat 根控制字段最终校验（dev.98）

Chat 最终文档新增 stream_options 及 include_usage 的已有类型边界，并保留两个
max-token 字段不能同时存在的规则（包括两者都为 null）。顺序仍为消息邻接/角色、
stream_options、include_usage、max 字段冲突，之后才进入尚待迁移的内容解码检查。
未知 options 成员、单独的 null 限额等原接受边界没有收紧。

options 诊断保留原 DecodeRequest / decode 归因；冲突保留 EncodeRequest / runtime。
这不是新一轮诊断重构，仅让声明能够表达已有 decode 阶段。复用最终请求差分入口，
`closure-chat-final-controls` 通过，包含坏 options 与限额、消息历史之间的首错对照。
这一批不构造额外请求树，也不扩大为完整工具关联已闭合的声明。

本提交独立源码的协议全量 `closure-dev98-isolated-protocol` 通过。

## 独立修复：Anthropic 从已交付文本接续（dev.99）

获批的旧失败现在通过按需接续处理：首次修改时，Mapping 从已成功交付的最终 wire
状态读取真实块位置，并克隆一次文本摘要；此前的原生流不建立第二套逐项渲染记录。
字段关系和仅允许文本接续的选择仍在 Anthropic 配置中，通用 Go 原语只读取已交付项、
复制摘要和维护事务。

读取要求校验器绑定同一编译目标、映射与 scope，且当前接受的帧全部完成交付；任何
校验失败后不再提供接续状态。克隆不共享可变 hash 或工作区，失败事务不推进前缀。
新增事实读取不触发原生复合帧重渲染，也不以读取事实代替最终 wire 校验。
最终客户端合同补记实际存在的 inline 文本，继续保留缺失/null 字段的原接受边界。

接受边界只在具备匹配交付校验器的文本混合路径扩展。不绑定校验器的独立调用仍保留
原失败，旧不完整前缀及其失败断言未修改。原本可成功的“稀疏原生块后另起生成项”
继续使用旧编号；成功进入新接续后，才沿已交付编号继续生成。工具与 thinking 混合
接续仍未启用，接近 int64 极限后的新增块也没有额外成功保证。

`closure-native-delivered-text-confirm`、`closure-native-index-delivery-guards` 通过，
包括 inline 前缀、修改→原生增量→完整快照、编号 7 到 8、终态不重复和错误状态隔离。
`closure-anthropic-demand-shared-budget` 与 dev.96 配置比较：原提前失败的边界消失；
默认预算下 4095/4096 块均成功，4097 块新旧同在源解码的 state_items 4097/4096 拒绝。
正式回归另在原 Native-only 场景固定 StateItems=64，消费 64 个完整文本块。

持久表的 `index_size` 复用既有事务更新槽保存新增键计数，覆盖和失败事务不增加数量，
不枚举条目，也不增加独立计数数组。尚无本轮功能完整版本的正式性能或峰值结论，
不能把消除条目重复登记直接换算成延迟改善或物理内存节省。

最终 `closure-dev99-go-all` Go 工程全量、`closure-dev99-vet`、`closure-dev99-race`
通过。staticcheck v0.8.2 仍以原 45 条诊断退出 1，与 dev.95 按内容及数量比较新增/
移除均为零，明细见 `closure-dev99-staticcheck-diff.json`。
独立审查另补齐实际读取交付状态时的 Native 协调依赖标记，定向回归
`closure-native-delivered-reconcile` 通过；静态原生快速路径仍不强制重渲染。

## 第十五批：Responses 普通进度与独立来源状态（dev.100）

普通 `response.in_progress`、模型 usage 更新、托管工具 usage 及晚到 usage 迁入两源声明。
复用现有 JSON usage/metadata 表达式；输出仍是当前帧的观察值，累积账本只用于旧有
算术检查。created/completed 不为该账本填初值，普通进度中的 output/error 不被误当成
完整响应。无载荷进度不补终态；metadata-only 帧与 usage 尾帧维持各自的生命周期。

Spec 新增独立编译的状态槽及有序 `assert` / `state_update` 动作，复用 Mapping 的
槽位求值和所有权处理。两侧不共享可变状态；断言可以读取本帧暂存值，失败不提交槽位
或序号。`check_usage` 复用核心计数算术，不加供应商分支；显式检查保留原诊断错误链。
动作的位置拒绝使用其已声明的诊断，其他未声明动作仍沿用原 wire.stream 归因。

精确对照发现并修复两处空值问题：JSON 响应的空 content 为 `[]`，直接流终态中的
空 content 为 null；共享 JSON helper 不再为流专属表示改变 JSON 结果。另一个空事件
差异来自测试辅助函数的 append 克隆把非 nil 空数组变成 nil，现使用 `slices.Clone`
保留真实旧值，没有为此修改生产解码或放宽比较。

去掉了仅为判断有无载荷而重复构造 metadata 的两套表达式，按前置类型检查后的剩余
字段是否为空分派 observe/control。原生帧与未知扩展仍保留，不靠丢弃未知值减小状态。
覆盖同名大 usage 字段时，旧值与新值不能先进入同一临时数组或 merge 操作数预算。
声明现按键排除被覆盖旧成员，再组装结果；子对象仍只合并一层。没有调整 BufferBytes。
两个约 4 MiB 字段连续覆盖的旧源链与新独立合同对照通过。

已通过的定向记录为 `closure-responses-progress-exact`、
`closure-progress-overlay-budget`；精简之前的协议全量为 `closure-progress-protocol-all`。
中间失败日志保留，尤其 `closure-progress-compact-and-budget` 表明仅把 concat 换成
merge 仍会重复计量操作数，不能作为修复后的结果。

本批仍不覆盖 safety_buffering、派生 output_text 的完整响应检查、billing/attribution、
全部 metadata/turn-state、reasoning、annotation/logprobs 或 Responses 目标编码。
资源预算仍须独立对齐：旧引擎分别限制身份与流正文，新合同合计身份和槽位；旧累计
未知 usage 仅限字节，新槽还检查累计节点。其组合边界尚未闭合，不能宣布完整接受边界
或性能等价，更不能据此切换预置。相关代码推断与后续实测必须分别记录。

`closure-progress-identity-budget-probe` 已确认其中的字节差异：相同 8 MiB 限额下，
2 MiB 合成响应 ID 加 5 MiB 未知 usage 值，旧解码、EventReplay、真实终态和 EOF 均成功，
新独立 wire 合同在 `/state` 提前拒绝。探针退出成功仅表示采集完成；这是切换阻断项，
不是通过的回归。首版探针另外错用了尚未声明的 EncodeEvent 目标能力，导致旁路
Mapping 的 replay 报 `/unmapped`；这不是已证实的配置缺口。真实来源入口使用
DecodeEvent 能力并设置 EncodeEvent 的校验方向，修正版探针沿用这一构造；旧探针与
结果均保留。单独绕过 wire 观察 Mapping 不代表完整成功。累计节点差异暂为代码推断。

尚未接入任何预置的 Chat logprobs 目标计量草稿、测试及审查笔记完整归档于
`.tmp-dev/protocol-dsl-closure-2026-10-31/unwired-trackers/`。它们尚未证明容量与旧
checkpoint 一致，因此没有随本阶段注册为 DSL 原语；后续从实际目标映射继续接入。
这不是该功能已完成或已取消。当前生产预置继续使用旧执行链，本批没有正式性能结论。

`closure-dev100-go-all` 全后端测试通过（约 104 秒），`closure-dev100-vet` 通过。
staticcheck 首轮指出本次提取后已无调用的旧 predicate 包装方法；确认唯一声明后删除，
`closure-dev100-staticcheck-clean` 仍以既有 45 条诊断退出 1，与 dev.99 按内容及数量
对比新增/移除均为零。结果见 `closure-dev100-staticcheck-diff.json`，未把退出 1 写成通过。
`closure-dev100-race` 的状态槽、有序断言、Responses 进度、Anthropic 原生接续及事件
运行期定向 race 通过；本阶段没有改动 WebUI，也没有把以前的跨平台产物当成本版本构建。

## 第十六批：Responses 普通心跳与错误源流（dev.101）

`keepalive`、`response.failed`、扁平/嵌套 `error` 现由 Spec 与 Mapping 声明。心跳
不补响应或终态；带额外字段的心跳仍产生未映射证据，包括其他事件已认识的 metadata
字段。失败只解码错误载荷，不以完整响应规则增加 output、status、usage 要求，可先于
created，也可结束尚未闭合的工具参数。失败后的 usage 尾帧保留原规则。

错误载荷表达式从既有 JSON 实现提取，HTTP 上下文、类别、别名、未知错误字段和原
JSON 的可选 error 判断不变。复用原两种包络与七种拒绝样例，比较完整核心事件及四个
旧目标的错误重建字节；这验证了新源的语义输出，不是尚未迁入的新 Responses 目标。
生命周期测试保留重复终态、带载荷的终态后心跳、只有心跳的提前 EOF 及部分工具失败。

新增严格比较发现序号错误原因仍是通用文字，与旧 Responses 的统一错误句不同。
Spec 新增限定的 `sequenceReason`，要求同时有 `sequenceDiagnostic`；类型/null/负数
与重复/倒序均使用同一配置文字，原默认合同不变。没有新增另一份序号账本，也没有
在 Go 中写供应商判断。原 `closure-responses-control-first` 失败保留；
`closure-responses-control-sequence` 定向通过，`closure-dev101-go-all` 全后端通过
（约 97 秒），`closure-dev101-vet` 通过。

修正 source target 后的 `closure-progress-identity-source-probe` 再次确认上一批
`/state` 预算阻断。新旧单独的来源语义 replay 均可完成 EOF，首版探针的 `/unmapped`
错误已消失；没有用这个旁路代替独立 wire 验收。探针源码及前版均在日期目录保留。

仍未覆盖 `SSE-Keep-Alive:true` 空 delta 哨兵的显式消费及 normalization 诊断。
普通 keepalive 不能代替该分支；Responses 剩余源事件、目标编码、Gemini 双向 SSE、
全部策略/资源边界及正式性能验收仍未完成。生产预置和旧执行链继续保留。

新增[能力迁移清单](protocol-dsl-capability-status.md)连接旧入口、两源声明、现有测试
和剩余缺口。独立阅读还定位到来源入口重复静态校验、节点扫描及解析缓存创建，记录于
`.tmp-dev/protocol-dsl-closure-2026-10-31/source-read-reuse-review.md`；它是待 profile
验证的代码线索，没有测得绝对成本，不能称为这批修改的性能收益。

`closure-dev101-race` 的流状态、有序断言、进度及错误源流回归通过。
`closure-dev101-staticcheck` 仍是原 45 条诊断，内容/数量比较新增与移除均为零；
`closure-dev101-audit-self` 的 161 项审计自测通过，无跳过。这是本阶段开发验证，
不替代四协议完整迁移、真实客户端、正式性能或最终六目标验收。
