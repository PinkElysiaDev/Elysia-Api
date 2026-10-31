# DSL 功能闭合与性能验收记录

基线为 `d273ce96 / 2.0.0-dev.88`，分支 `refactor/protocol-dsl`。本轮先完成能力迁移，
再对功能等价的完整链路测量并优化；未通过门槛前不切换四预置，不移除生产旧入口。
仅本地提交，使用合成数据和模拟上游。

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
