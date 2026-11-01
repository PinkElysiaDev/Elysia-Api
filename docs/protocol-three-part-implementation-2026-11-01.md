# 三部分架构实施记录

基线：`refactor/protocol-dsl`，`b2844846 / dev.101`。第一阶段提交为 `acc97556 / dev.102`，第二阶段为 `1841b04b / dev.103`，第三阶段为 `0c749dd1 / dev.104`；第四阶段为本次 `dev.105`。后续目标处置仍在开发，不能将组件闭合当作全部迁移完成。新验收依据见[契约](protocol-three-part-contract.md)。

## 基线留档

`.tmp-dev/protocol-three-part-2026-10-31/baseline/` 保存源码归档、独立工作树、六目标产物、SHA256 和构建信息。Windows amd64 已用独立新数据库完成启动、四预置、health 和内嵌界面冒烟；另外五目标仅构建验证。所有构建使用 Go 1.27.2，PGO 关闭。

## 当前实现

- 包格式 4、DSL 2、核心 2、转换策略 2；管理模板及验证入口使用对应版本，历史身份保持可读。
- Spec 独立命名表达式；命名程序按静态上下文共享编译结果，保留权限、深度和引用指纹约束。
- 单次输入视图共享资源检查、Spec 成功检查和读取索引；源流入口同时执行独立 wire 生命周期与 Mapping，复用当前帧读取结果。
- `check_links` 接入四协议来源与出站请求工具历史；Gemini 显式结果消费后可缩小剩余隐式关联集合。check-only assertion 共用规则，不附加整树 schema 扫描。
- Spec 有序状态更新、计算工具身份/参数及仅快照工具生命周期，服务真实 Gemini 配置。
- Gemini 双向流已通过文本、thinking 签名、工具参数、晚到 usage、原生字节回放和工具后续轮次的组件验收；无 ID 工具可生成独立核心身份，隐式结果仅匹配唯一待处理调用。
- 固定状态槽不计为动态关联条目；每个新物化槽独立校验 Nodes/Depth，保留字节统一计量。未变化且已经验证的同一槽值不重扫。
- 模型绑定编辑器从 v4 的 Spec 读取 operations；空协议目录也不能绕过转换策略格式验证。

验证日志位于 `.tmp-dev/protocol-three-part-2026-10-31/checks/`。当前已有：核心测试、源流网关定向测试、CLI 参考生成检查、前端类型/lint/构建、真实隔离后端的 23 项浏览器回归、161 项审计自测通过。staticcheck v0.8.2 仍以 45 条既有诊断退出 1，对比基线新增/移除均为零。

首次后端全量中，除四项旧错误树断言外其余包通过。这四项改为实际拒绝契约：空媒体 URL、歧义工具结果、冲突 reasoning 和非法 owner 仍拒绝；不更新旧错误树快照，也不保留展开式表达式的冗长路径。最终 `phase-one-go-all-confirm` 全量、`phase-one-vet-final` 及 `phase-one-race` 均通过。race 覆盖命名程序、输入视图、关联、流状态、Gemini 流、源流网关及管理格式门槛。

`phase-one-staticcheck-final` 仍为既有 45 条诊断，命令退出 1；这不是静态检查全净。首次浏览器运行未连接后端时跳过的 10 项，已由 `frontend-real` 的隔离真实后端运行补齐，23 项全部通过。一次前端构建命令误用了不存在的脚本，改用项目 `build:webui` 后通过；该命令错误不属于产品失败。

## 本阶段行为说明

- 歧义、重复及孤立工具关联采用 `invalid_association`；来源关联诊断指向原请求路径。
- Chat 来源允许最后一个尚未返回结果的调用，最终出站仍要求完整工具回复。两者共用关联事实，完成要求分别声明。
- 命名表达式失败定位到声明位置 `/mapping/expressions/...`，不再展开整条调用路径；错误原因仍保留。
- 旧格式4之前的两源包与版本1转换策略不满足新格式执行门槛；历史身份仍可读取。旧模块格式尚未最终退役。
- Gemini 仍使用已有单 candidate 能力范围，本阶段不扩大供应商 API。

## 第二阶段：外部流实体与 Responses 重建

- Spec 使用外部实体、命名空间、父子关系及生命周期；不再通过构造核心事件执行外部流验证。来源外部状态与 Mapping 状态独立，当前帧字段读取共享。
- `base` 复用已编译文档，并仅允许附加断言；Responses 来源与最终输出共用结构，最终输出额外要求序号。未提高配置节点上限。
- Responses 目标流接入 reasoning 两类集合、annotation／logprobs 增量、JSON／文本工具参数、晚到工具身份及 usage 终态。工具用量复用 JSON Mapping 的配置片段，不重复累计为模型 token。
- 原生复合帧通过显式 observer 同步状态，只回放一份原帧。缺少目标必需序号的合法来源帧由 `nativeWhen` 选择语义重建；输出序号按已交付位置与当前批次偏移计算。
- 通用 `control.updated` 保留续传状态载荷，辅助 metadata 与纯传输控制独立分类。成功来源校验产生绑定方向、Scope 和帧的私有凭据；兼容省略由目标规则授权，严格模式拒绝损失。
- `buffer_text`／`buffer_array` 支持事务式增量、快照前缀校对、读取及释放。收集器的显式 metadata append 采用分段累积，最终输出清除增量指令。
- `core_controls` 与字段观察接通三类配置默认策略：Responses 上下文引用、Gemini safety 取舍、Chat 缺失响应模型。用户同 ID 替换后重新编译，不沿用被替换规则的执行计划。
- 网关、收集入口、会话及离线验证绑定一轮执行 ID 和时间。普通 context JSON 不能改写这些运行事实；没有 DSL 随机值或缺失身份常量兜底。

`phase-two-responses-arrays` 已通过六类往返（reasoning、文本阶段、函数参数、增量 metadata、文本工具、工具用量）、原生字节保真、晚到身份和缺失序号重建。它是组件验收，不代表四协议 HTTP 组合或性能验收已完成。

第二阶段扩大回归后发现：旧测试缺少独立流状态、部分断言比较旧内部 Native/错误树、外部事件配置与动态 StateItems 预算混用，以及空 metadata 增量的计费问题。已按新契约分别修复实现或测试，保留实际内容、身份、用量、终态和拒绝边界验证。

控制事件凭据已经脱离整份原帧：只保留独立载荷、来源事实、映射指纹和帧摘要；原帧仍由 EventFrame 持有。既有测试用 2 MiB 前导空白与小 headers 检查小载荷不共享大输入，以及 Scope／映射变化拒绝。

本阶段的表示与接受边界变化：

- 同一身份字符串的不同 JSON 转义写法相等；不同 ID、null、重复身份和乱序帧继续拒绝。
- Anthropic 流的缺失或 null block index 不再默认为 0，按外部合同拒绝。
- 纯传输 keepalive 产生零个核心事件；空终态不要求内部 content 使用非 nil 空切片。最终 wire 结构仍由目标合同验证。
- 结构和生命周期错误按实际源／目标边界归因，不再复刻旧 `unsupported_capability` 覆盖或旧错误树。
- 来源位置和原文片段以实际输入为准，逐项目不再重复保存仅为旧表示对照所需的 Native 副本；原帧保真由 EventFrame 负责。
- JSON 工具用量错误使用真实 `/tool_usage/...`，流帧嵌套字段使用 `/response/tool_usage/...`；不再把 JSON 断言标成 `decode_event`。DSL `check_usage` 的核心约束使用 `core.usage` 阶段，由实际调用补齐方向，保留原始错误链。

第二阶段复核记录（Windows amd64、Go 1.27.2）：

| 记录 | 结果与范围 |
|---|---|
| `phase-two-go-all-final` | `go test -p=1 ./... -count=1` 全部通过；147.7 秒。包括核心、新包组件、网关、Agent、存储及既有旧预置路径 |
| `phase-two-vet-final` | `go vet ./...` 通过 |
| `phase-two-race` / `phase-two-race-builtin-serial` | 核心和网关相关 race 通过；builtin 初次编译因系统提交内存不足中断，单包 `-p=1` 复核通过。没有观察到数据竞争报告 |
| `phase-two-staticcheck-final` | 固定 v0.8.2 仍因 24 条存量诊断退出 1；无新增诊断 |

新核心类型最初使计量生成器多产生一条无用局部赋值。修正生成器后，原有 21 条同模式 `SA4006` 也消除，静态诊断从基线 45 条降至 24 条；没有忽略规则或手改生成产物。完整静态差异位于 `checks/phase-two-staticcheck-final-diff.json`。

race 编译失败时系统可用物理内存约 8.7 GiB，提交额度占用约 98%。这是工具执行环境的记录，不是网关运行内存结论。后续 31 MiB 四并发性能测试仍须重新检查既定环境门槛。

本阶段沿用第一阶段已通过的前端与审计结果；最终新引擎切换仍需重新执行完整页面、审计、六目标构建和实际调用矩阵。性能收益等待功能完整后的正式测量。

## 第三阶段：元数据、计量处置与真实接续

- 四份 Mapping 接入 typed `core_controls` 的 metadata／usage 遍历、目标处置及 Anthropic 计数包络。普通元数据与辅助帧共享稳定的 `event-metadata` 规则 ID；严格拒绝、兼容告警和用户整条替换仍使用原策略入口。
- `/frame/usage` 来自原始事件批次的数字观察。未知与零计数分开处理，按原顺序合并，复制前约束规模；不保存原始事件、attribution 或 billing。没有数值的帧不建立响应 map。目标包络只取必需主计数，不恢复先前明确省略的细分；原始上游记账独立保留。
- Chat JSON／SSE 的 logprobs 统一为核心 token 数组，保留正文／拒绝节点归属、choice index 和 finish reason。未知 choice／概率成员继续受扩展保护；新增测试移除所有 Native 后重建，避免仅靠原样转发掩盖映射遗漏。
- `track_array` 保存固定前缀摘要，`resume_tool` 接续已交付工具参数；Anthropic 工具与 thinking 原生帧后的语义修改已接通。占位 JSON 的替换在帧事务内提交，失败不污染后续状态。
- Responses 文本 part、delta、done 与完整快照统一保存概率字段的存在性。之前 delta／done 无条件补 `[]`、最终快照却省略字段，会正确触发 Spec 的不一致拒绝；本轮修正 Mapping，没有放松外部校验。
- 原始核心 JSON 的 `core_controls` 入口使用严格解码，防止规则重新编码时静默删除未知核心字段；typed 路径不增加这次解码。
- 网关入站、候选准备、after 流校验、任务、health 与签名 probe 补齐可信执行事实。持久任务复用本地 ID／创建时间；Task 仅获准读取 operation 的 ID／开始时间，命名程序缓存同步隔离权限。
- 新 Spec 统一完整官方端点与服务器 origin 的组合。显式迁移旧 baseURL 的说明已写入契约；旧配置与通用 URL 构造器没有自动改写。

本阶段的表示与诊断说明：

- Anthropic 响应块的 `cache_control` 没有官方输出字段证据，按未知扩展保护，不假造响应缓存能力；请求缓存语义保留。官方 SDK 类型及下载哈希保存在本地调查目录。
- Responses 输出 logprobs 缺失属于现有客户端兼容范围；官方参考将该字段描述为数组。重建区分未出现、显式空数组和实际概率，正常原生帧仍保留原字节。
- 最终请求的 thinking／output_config 错误指向真实 `/thinking`、`/output_config`；Chat 双上限冲突指向 `/max_tokens`。最终校验测试改为检查明确的拒绝码、方向与来源位置，不再比较旧错误文字、伪造核心路径或 `request.validation` 阶段。
- Responses 既有 `response.failed.error` 对象／字符串兼容保留。对象成员检查改为显式条件分支，避免对字符串执行嵌套字段读取。

验证证据仍位于 `.tmp-dev/protocol-three-part-2026-10-31/checks/`：

| 记录 | 结果与范围 |
|---|---|
| `phase-three-protocol-all` | 核心协议包全量通过；builtin 发现字符串 error 条件错误及旧诊断断言，均已修正 |
| `phase-three-builtin-confirm` | builtin 全量运行只剩一项新测试的错误码期望；按已声明的语义约束／外部结构边界分别固定期望 |
| `phase-three-boundary-confirm` | 修正后的最终响应／请求／事件合同、签名 probe 定向复核全部通过；没有批量刷新输出快照 |
| `phase-three-vet` | `go vet -p=1 ./...` 通过；采用同一组受限编译设置 |
| `phase-three-server-constrained` | health、任务、收集／预览、网关源流定向测试通过；签名 probe 的直接 handler 夹具缺中间件 ID，已补齐并在上一行复核 |
| `phase-three-activation-audit` | 四包正式 Verify 均完成；Gemini 全部通过，其余存在下表所列能力证据缺口。命令退出 0 仅表示审计正常执行，不表示四包均可激活 |
| `phase-three-matrix-activation-gate` | 网关矩阵在正式激活验证阶段拒绝不完整的 Chat 能力证据，尚未进入上游调用 |

系统剩余提交额度约 0.7 GiB 时，普通协议编译与网关测试编译分别发生内存不足／Windows errno 1455。核对后未发现本轮遗留 Go 进程，也没有终止用户进程或调整分页文件。测试最终使用 `-p=1`，编译器 `-c=1`，`GOMAXPROCS=1/2`、`GOGC=10/20`、`GOMEMLIMIT=256/384MiB` 完成相应复核；具体设置逐命令记录。这些结果只验证功能，不能用于计划要求的正式性能比较。

正式激活审计的剩余项：

| 默认包 | 检查数 | 未闭合证据 |
|---|---:|---|
| Chat | 44 | JSON 响应双向的 images、audio、documents |
| Responses | 58 | JSON 响应双向的 tools.server、images、documents |
| Anthropic | 55 | 请求双向的 reasoning.history_retention |
| Gemini | 50 | 无；尚未替代四协议组合验收 |

完整报告为 `phase-three-activation-reports.json`。已准备的 16 方向 JSON／SSE 网关矩阵暂存于 `pending-tests/protocol_package_matrix_test.go`，待上述能力实现和真实证据闭合后接入；不会伪造报告绕过激活，也不将这个门槛失败写成已完成网关验收。

## 第四阶段：服务端执行、官方响应结构与约束复用

`dev.105` 的本阶段实现如下；最终切换仍受下文完整调用和性能门槛约束：

- 新 `ServerToolNode / server_tool` 表示上游负责执行的活动，复用现有字段与资源 Scope。Responses 的 web search、file search、image generation、code interpreter 接通 JSON、请求历史与双向 SSE。代码、日志和检索摘录不当作助手答案，也不登记本地函数调用。
- 完整产物快照替换结构结果；进度只更新小型头信息。代码保持增量，不为了进度物化全文；partial image 替换当前观察，不积累多份图片。进度 completed 不结束 item。nullable、缺失及空数组分别保留，容器资源与文件资源保持独立类型和作用域。
- `json_digest` 复用精确 JSON 摘要算法，供 Spec 核对大产物快照而不再保存完整副本。Spec 的具名 constraints 绑定当前输入、原文 root、权限和真实来源路径；可在不同调用边界复用，循环／不可达／深度超限在编译时拒绝，引用闭包进入文档哈希。
- Chat 已声明真实 `message.audio`，映射数据、转录、过期时间及带 Scope 的 audio ID，不猜测缺失的音频格式。请求 images/documents 保留；响应中从输入块复用而误带的两项声明已纠正。官方响应 content 是 string/null，原先接受 image_url/input_file 等输入块数组属于本轮明确纠正的偶然行为，不称为无接受边界变化。
- Anthropic 补齐 keep=all 的正式双向样例。四份包均通过完整 Verify，包括能力证据；测试不再只检查已写样例而遗漏整份报告的失败。四包额外提供纯文本 SSE 组合证据，原生未知扩展样例仍独立保留。
- 服务端工具次数由 Mapping 推断到 Usage.Details；JSON 观察实际执行项，流在真正 item done 时更新固定计数槽，终态按累计快照合并。普通 in_progress 只读取报告计数，不扫描输出推断执行次数。显式 observed 计数（含零）优先，不把 inferred 计数伪装成上游 tool_usage，也不累计到模型 token。
- 网关和 Agent 的新包不再调用旧 raw 工具计量器；迁移中的旧预置仍沿用原路径。Agent 的 JSON／SSE 实际两轮均已验证加密 reasoning、上游 search、本地函数和工具结果历史，一轮模型调用对应一条最终记录。
- 只有工具次数时保留原有模型 token 缺失估算；真正模型计数到达后再切换为 protocol_observed。不会将工具执行次数当成模型实际用量，也不提前清除估算标记。

开始实施时，验证进程曾在启动阶段失败，系统仅剩约 73 MiB 提交额度，物理内存仍有约 9.4 GiB 可用。恢复到约 2.7～3.0 GiB 剩余提交额度后，已恢复受限串行功能验证；没有终止用户程序或调整系统内存设置。该环境仍未达到正式大正文四并发测量的提交额度门槛。

Responses 包按实际 JSON token 计量，包含对象键和容器边界。通过具名约束、命名程序及外部实体复用，补齐能力后的包约 99,987 token，仍在 100,000 限额内；未提高限额。该配置余量很少，后续扩展必须继续整理真实重复。编译链的过深分支按真实责任拆分，没有隐藏引用深度。

本阶段明确纠正的边界：Chat 响应 content 只接受 string/null，未知扩展不豁免已知字段类型；Responses in_progress 已出现的 output 必须是对象数组。两者原先接受数值属于旧偶然行为，不作为兼容要求保留。只有工具计数而没有模型用量时，重建省略 usage，不再制造空 usage 对象。

完整网关验收尚未通过：新矩阵已能通过四包正式激活，但组合报告揭示 Chat 裸助手历史分组、响应 item ID／status、响应属性及客户端 store 意图处置尚未闭合。最初夹具错误地承诺整个协议的能力，已改为实际模拟模型的 text/usage 能力及 JSON/SSE 传输；真实失败仍保留，不伪造组合报告。模型范围缩小不代表工具、媒体或其它复杂能力已通过跨协议验收。

阶段性记录包括 `phase-four-core-first`、`phase-four-wire-constraints`、`phase-four-protocol-all`、`phase-four-responses-confirm-two`、`phase-four-server-ledger-confirm`。`phase-four-http-agent-matrix` 中 Agent 两轮通过，但整个命令因网关矩阵失败退出 1；不能将其描述为 HTTP 矩阵通过。

本阶段最终复核：

| 记录 | 结果与实际范围 |
|---|---|
| `phase-four-go-except-pending-matrix` | `go test ./... -skip '^TestDefaultPackagesGatewayMatrix$' -count=1` 通过，461.6 秒；明确排除已记录失败的待完成矩阵，不能写成无例外 Go 全量通过 |
| `phase-four-vet` | `go vet -p=1 ./...` 通过 |
| `phase-four-staticcheck` | 固定 v0.8.2 因 24 条存量诊断退出 1；逐条比较新增／移除均为零，未设置忽略规则 |
| `phase-four-race` | 约束复用、精确摘要、server 生命周期／所有权、真实服务端流和 Agent 两轮的相关 race 全通过，271.2 秒；仅 Windows amd64 运行验证 |
| `phase-four-accounting-final` | 工具次数与模型估算隔离、显式零、晚到模型计数、旧计量路径及 Agent JSON／SSE 两轮复核通过；这是完整检查后的小型计量修正验证 |
| `phase-four-boundary-final` | 共用结构断言在 HTTP 及双向会话的真实输入／输出边界归因通过；包括 EncodeUpstreamEvent 的出站拒绝分类 |

以上均为受限内存设置的功能检查，不是性能测量。最终前端页面、审计、六目标构建及正式配对性能等待完整目标处置与生产切换阶段，未使用旧构建产物冒充本版本验收。

## 尚未完成的门槛

- 四协议已接入双向流，Chat 目标 logprobs、Anthropic 混合接续和 Responses 服务端产物已有组件验收；跨协议父子输出组织及全部 HTTP/Agent 组合仍需闭合。
- 四份默认包的完整独立激活证据已齐；组合证据及目标取舍仍需完善。metadata／usage、模型归一化、Gemini historyRetention/safety 已配置化；错误、签名恢复、缓存提示、工具结果、历史布局及客户端交付意图尚需接线。
- 新资源契约尚未覆盖所有保留状态；默认转换策略仍有供应商 Go 分支。
- 生产预置仍走旧加载器；旧执行路径、自动修复绑定及旧格式执行尚未退役。
- 四协议 16 方向实际调用、最终全量/静态/race/页面/六目标验收，以及三版本正式配对性能与火焰图尚未完成。

共享读取与编译复用目前属于实现变化，不能在正式测量前写成性能收益。旧调查和能力清单保留为历史材料，不再要求复制其中的旧错误树或资源计费缺陷。
