# DSL 批次 2：认证续传实施记录

基线为 `e97d2ce6 / dev.108`，当前功能批次为 `dev.109`，在 `refactor/protocol-dsl` 本地实施。包、DSL、核心和策略版本分别保持 4、2、2、2。生产预置尚未切换。

## 配置与宿主边界

- include 原表示由 `ClientOutput.RawResponsesInclude` 独占；`RequireContinuationState` 是来源要求，目标规则只能写 `ResponsesIncludeResolved`。API 输入类型由 Spec 检查，核心预览和 Agent 直接构造的值由目标规则处理。
- 四包声明目标恢复规则；Chat、Responses、Anthropic 声明客户端载体位置，Gemini 没有虚构载体能力。规则可以整体替换或禁用。
- Mapping 提取 token 和真实原文位置，`is_carrier` 在原数组遍历时消费载体，不提前删除数组成员或改变后续来源下标。宿主认证 Owner、Scope、历史和节点摘要后，才确认请求。
- 未认证的载体不能通过移除 Native 绕过检查；已认证请求可删除 Native 重建，但替换根 Native 原文会使认证上下文不匹配。
- 加密、Seal/Open、持久化和作用域校验仍归宿主。配置或普通 JSON 不能创建恢复证明；仅 Persist 不满足明确的客户端续传要求。
- 输出载体经最终 Spec 与只读 `emitted` 检查，JSON 在 HTTP completion 成功后确认交付，SSE 在写入及 Flush 成功后确认。不增加逐帧 goroutine。

## 真实调用暴露并修复的缺口

组件成功不能代替真实网关。定向 HTTP 检查暴露了载体约束误用于错误包络、目标必填模型或响应 ID 缺失、结构化 reasoning 的单段可见投影、以及签名到达早于实体完成的问题。

模型与目标 ID 由目标规则明确生成或规范化，仅处理真正缺失的值；null、空字符串及已有值不改。生成的目标 ID 使用宿主可信 operation，诊断说明它不是观测到的供应商身份。禁用规则后不由编码器补做。

Anthropic 支持本批所需的“单段可见 reasoning → 普通消息”顺序输出，复用现有 thinking 状态和空 signature 客户端兼容表示。空 signature 不生成供应商资源或恢复证明。summary、多段 structured reasoning 尚未作为这一投影的支持范围，保持明确拒绝，后续布局批次继续处理。reasoning 显示 ID 的省略由目标规则诊断，严格模式不隐式降级。

流中的受保护资源通过收集器已有实体关联到完成内容的摘要。只有实际需要恢复的资源才触发现有有界事件缓冲；不会将中途文本前缀密封成完整状态。普通增量比较不额外编码完整正文。实际用户修改内容或关联后，不得继续使用来源完整内容的证明。

等待期间发生失败或取消时释放等待内容，保留失败终态，不将部分内容冲成成功，也不让等待未完成错误覆盖原终态。

离线组合验证改为逐事件收集、完成封装和投影，不再提前给整个流授予恢复证明；输出验证仍使用既有离线编码阶段，不能声称它完全模拟实时交付。单事件预览缺少完整生命周期，不授予虚假的客户端交付证明。

## 验证与范围

检查命令、退出状态和原始日志位于忽略目录 `.tmp-dev/protocol-three-part-2026-10-31/checks/`。实施中的失败保留原始记录；以最终复核结果判断验收，不删除旧断言或批量更新快照。

已验证的代表性真实调用包括 Responses→Anthropic JSON/SSE 的两轮恢复、篡改与规则禁用拒绝、Gemini→三个客户端协议的载体输出、仅 Persist 无法满足客户端要求，以及 Agent 两轮工具调用。严格模式下原始正文与晚到签名成功；兼容模式允许用户修改正文后，晚到签名仍按 `/continuation` 拒绝，且只调用一次上游。严格模式会先拒绝修改本身，不能拿它代替后一条认证边界检查。

Responses 的 visible reasoning 兼容组合证据通过；完整 summary 范围仍拒绝。Gemini→Anthropic 的计量及签名严格组合证据通过。新增首帧计量齐全的文本和晚到签名样例均手写预期，原首帧缺计量样例保留。

生成目标响应 ID 曾暴露来源帧计数错配：规则执行前固定来源计数观察，后续模型／身份投影不再改变查询键。定向测试覆盖无 ID、改名及多个来源响应的隔离；不增加第二次帧扫描。终态资源与已收集节点的一致性已由现有 Collector 保证，没有增加重复兜底。

四包 JSON token 数分别为 Chat 94,622、Responses 99,866、Anthropic 87,712、Gemini 68,483，仍使用 100,000 上限；Responses 后续扩展需继续提取真正重复的声明。

| 检查记录 | 结果 |
|---|---|
| `phase-eight-generate-confirm` | 核心字段与资源生成通过 |
| `phase-eight-live-fifth` | 除最后 strict late 子用例外，真实两轮、篡改／禁用、三种客户端及 Agent 全部通过；后者的修复由下列复核和最终全量覆盖 |
| `phase-eight-strict-seventh`、`phase-eight-edited-continuation` | 原正文 strict late 通过；编辑子用例改为真正执行编辑的兼容策略后，认证边界拒绝通过 |
| `phase-eight-go-all` | Go 全量通过；约 459.9 秒，server 约 385.7 秒，protocol 7.2 秒，builtin 38.1 秒 |
| `phase-eight-continuation-race` | protocol 与 builtin race 通过；server 达默认 10 分钟超时，未报告数据竞争。堆栈仍在配置解析／组合证据验证；未完成的三个网关用例单独复核，不能把本次退出 1 写成通过 |
| `phase-eight-continuation-race-remaining` | 上述三个未完成网关用例 race 复核通过，server 约 329.8 秒；`GOMAXPROCS=4`、`GOGC=100`、`GOMEMLIMIT=4GiB`、超时 30 分钟 |
| `phase-eight-vet` | Go 全量 vet 通过 |
| `phase-eight-staticcheck` | 固定 v0.8.2，24 条存量诊断，与上一批记录逐行相同；退出 1，无新增诊断 |

全量设置为 Windows amd64、Go 1.27.2、`GOMAXPROCS=2`、`GOGC=50`、`GOMEMLIMIT=1GiB`、PGO 关闭、`-p=1` 与相关包编译并发 1。此次功能套件比上一阶段耗时更长，包含新增组合证据及真实多轮检查；不能据此归因为线上性能下降，也不隐去该测试成本。

## 性能与后续

本批只有功能数据，尚无正式性能结论。临时测试使用受限 Go/GC 设置，不能冒充计划要求的配对数据。无待发 token 时不运行载体输出程序，删除了未消费的 hash 与重复编码。

新包启用恢复时同协议请求仍可能创建历史摘要和收集器，实体证明也有请求级小状态；这些成本需要在功能闭合后纳入正式 profile 和三版本比较。不得以绕过配置的供应商分支、省略认证或扩大缓存来消除成本。

工具处置、一般历史布局、缓存与错误配置化、完整入口及资源契约、正式性能、生产切换和旧链退役仍属于后续批次。不推送、不调用真实供应商，不提交私人正文或大体积诊断资产。
