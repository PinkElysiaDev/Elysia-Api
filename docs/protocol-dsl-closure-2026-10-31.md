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
