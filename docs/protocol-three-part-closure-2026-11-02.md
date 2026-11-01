# 三部分架构剩余功能闭合记录

执行基线为 `refactor/protocol-dsl`、`a20f63ce / dev.111`。本轮只做本地提交；不调用真实供应商。新版本不承担旧格式、旧绑定及旧任务的升级兼容，使用隔离新数据库验收，不删除现有数据库或日志。包／DSL／核心／策略版本继续为 4／2／2／2。

## 批次 0：基线和配置复用

- 独立性能参考 A 仍为 `b2844846 / dev.101`。重新读取其六目标二进制并校验 SHA256，六项全部匹配已有清单；没有重新构建或替换 A。
- dev.111 的源码归档、四对配置、已有检查原文和当前 Windows amd64 可运行构建存于忽略目录 `.tmp-dev/protocol-three-part-closure-2026-11-02/dev111/`，清单记录文件哈希。它是恢复点，不是功能完整版本 B。
- Responses 提取五组确实重复两次的表达式：文本增量事件、失败事件、缓存计数读取、有效存储决定和工具结果扩展。复用已有命名程序，不改变样例、限额或编译器版本。
- `closure-baseline-responses`：Responses 正式激活样例及相关包测试通过，10.108 秒。检查实际使用 GOMAXPROCS=2、GOGC=20、GOMEMLIMIT=384MiB、PGO 关闭，仅作功能检查，不作性能数据。

## 剩余实施顺序

1. 外部校验／metadata 白名单退出共享核心；请求投影和模型规则显式声明。
2. 缓存、metadata 和错误目标处置。
3. JSON 历史布局、多根输出、字符位置。
4. 真实流布局、增量与 Native 衔接。
5. 所有入口及资源所有权闭合，归档功能完整版本 B。
6. 三版本配对性能、profile 和定向优化。
7. 四预置切换、旧生产链删除及最终验收。

当前生产预置仍加载旧定义。正式性能、最终前端／页面／审计／六目标验收未完成，不能由阶段测试通过推断总目标完成或性能无回退。

## 批次 1A：显式目标程序（dev.112）

四份 Mapping 的 conversionDefaults 通过 use 绑定 model_policy；有请求字段处置的三个包同时绑定 request_projection。规则 ID、顺序和启用状态归声明所有。删除 DefaultConversionPolicy 中对这两个程序的自动安装；来源模型规范化只接入声明的模型规则。包 schema 和两源编辑器帮助同步。

编译顺序先绑定请求处置，再编译默认规则。原有缓存／历史夹具显式声明原先依赖自动注入的规则，未放宽 Native、严格模式或禁用后的编码拒绝。新增一个回归验证省略声明不运行、声明身份和顺序、禁用、错误引用及混用拒绝。

| 检查 | 结果 |
|---|---|
| closure-declared-defaults-final | protocol 全量通过，7.097 秒 |
| closure-declared-defaults-builtin | builtin 全量及四包正式证据通过，35.861 秒 |
| closure-declared-defaults-live | 32 场景网关矩阵、工具结果、storage 及最终 wire 冲突拒绝通过，24.219 秒 |
| closure-declared-defaults-race | 显式默认、缓存／Native、历史和两源编译相关 race 通过，无竞争报告 |
| closure-declared-defaults-vet | protocol 全部 vet 通过 |
| closure-declared-defaults-staticcheck | 固定 v0.8.2 仍有 24 条存量诊断，无新增或消失；退出 1 |
| closure-declared-defaults-types / lint | 前端类型检查和修改文件 lint 通过 |

检查日志在原 checks 目录。测试初次暴露一个仍依赖自动规则的缓存夹具，补齐声明后核心全量通过；未改生产兜底。该切片不代表批次 1 全部完成，metadata 白名单、共享请求外部校验和聚合证明仍待迁移；没有正式性能结论。

## 批次 1B：有限值映射的核心位置（dev.113）

有限值映射不再把核心路径固定为 reasoning.historyRetention，或把规范值固定为 all。编译按显式核心字段、字段类型、独立余项所有者和完整分区／重建证明检查；来源、Native、资源和客户端意图不在此原语的写入范围。仍不支持任意对象替换和数组布局。类型反射仅在配置编译阶段执行，运行期沿用原映射及所有权检查。

新增定向场景以 parameters.history_mode 映射 full／complete 为规范值、未知 future 留在独立余项位置，验证双向转换确实不依赖旧路径；原 historyRetention 的配置、严格拒绝及 Native 测试保持通过。聚合对象布局和请求字段处置仍有后续通用化工作，不能将该切片称为整个聚合原语完成。

- closure-finite-values-all：protocol 全量 6.833 秒、builtin 全量 34.827 秒，四包正式证据通过。
- closure-finite-values-final：最后的标量 null／独立所有者检查后，有限值与历史定向复核通过。
- closure-finite-values-race：有限值与历史相关 race 通过，无竞争报告。
- closure-finite-values-vet：protocol 全部 vet 通过。

未采集正式性能，不把编译期结构判断外推成运行期性能收益。

## 批次 1C：metadata 外部合同与处置边界（dev.114）

核心版本 2 的普通 metadata 退出共享 Go 供应商白名单；核心 logprobs、增量形式和资源检查保留。旧核心版本的 codec 路径暂保留原检查，随旧链退役一起删除。四份 Mapping 在同协议保留、跨协议归一化或兼容省略前，显式检查字段目录与形状；未声明字段即使为 null 仍拒绝。字段目录使用无歧义的三元组，不以拼接分隔符授权字段。

enum 新增显式 otherwise，只有未命中的字符串会进入该分支。默认包已使用它，操作目录、生成 schema 和编辑器帮助同步。Responses 复用真实重复读取及状态程序，并将有限字符串比较收敛到显式表；没有提升 100,000 节点或 64 层限额。一次额外提取触及原深度上限，已撤回该提取。最终整包 token 计数：Anthropic 90,362、Gemini 71,793、Chat 98,158、Responses 99,887；后续仍需继续通过真实共享腾出声明空间。

新回归证明自定义协议只改 Spec／Mapping 即可接收并重建 trace_label；来源类型错误、用户修改后的 Mapping 及最终 Spec 均拒绝。默认四包回归覆盖错误类型、未知空字段、错误命名空间，检查实际错误码和原位置。enum 的未命中与已命中不执行 otherwise 纳入现有执行器对照。

| 检查 | 结果 |
|---|---|
| closure-metadata-all-final | protocol 全量 2.500 秒、builtin 全量 36.355 秒，通过 |
| closure-metadata-gateway | 32 场景基础矩阵、工具结果与 storage 实际网关通过，23.854 秒 |
| closure-metadata-race | metadata、执行器、Responses 流容量及选择器相关 race 通过 |
| closure-metadata-vet | protocol 全部 vet 通过 |
| closure-metadata-staticcheck | v0.8.2 仍为 24 条主诊断及 1 条关联位置，与前批逐条多重集比较无新增、无消失；退出 1 |
| closure-metadata-types-direct / lint-direct | 直接调用本地 tsc 与 ESLint，通过 |

pnpm 包装命令自动准备依赖时因未批准的 swc／esbuild 构建脚本中止；没有改变构建脚本授权，直接使用本地检查器完成类型与 lint。自动产生的 pnpm 文件不进入提交。

本批不代表批次 1 全部完成。共享请求的外部类型／枚举、对象聚合证明和 request_field 仍待迁移；缓存／一般错误、布局、真实流、入口所有权、正式性能和旧链退役仍未闭合。未进行正式配对性能，不宣称性能无回退。

## 批次 1D：请求字段处置解除历史专用路径（dev.115）

request_field 的编译及执行不再固定 historyRetention 或 all。支持独立参数和 reasoning Value 叶子；编译绑定路径及清除动作，保留权限边界。目标 default 的声明值与选中值须一致；错误的恒真条件不能删除不同值。reject 使用声明路径返回诊断。默认包已有历史处置直接使用此实现，没有新增闲置原语。

新增一个参数场景验证兼容／严格归一化、不同值拒绝、规则禁用和编码拒绝，以及受保护位置不能写。已有历史、Native 删除约束、Scope、原始执行与缓存夹具继续复用。全量检查发现初稿使空缓存路径多一次分配，恢复通用无意图快返后原零分配断言通过，没有改断言。

- closure-request-fields-final：protocol 全量 6.768 秒、builtin 全量 36.448 秒，通过。
- closure-request-fields-value-boundary：收紧声明默认值边界后的相关测试通过。
- closure-request-fields-gateway：32 场景实际网关矩阵通过，10.363 秒。
- closure-request-fields-race-final：请求字段、历史、有限值及缓存相关 race 通过。
- closure-request-fields-vet：protocol vet 通过。
- closure-request-fields-staticcheck：固定 v0.8.2 的 24 条主诊断及关联位置无新增、无消失，退出 1。

本批仍不含对象聚合通用化和共享请求外部合同迁移；未采集正式性能。

## 批次 1E：请求外部合同退出共享核心（dev.116）

四个 Spec 通过具名约束声明请求追踪、Gemini safety 和 Responses 上下文控制的外部类型；来源、重建及最终出站共用声明。Gemini 非空类别／阈值与类别唯一性使用现有表达式；未知枚举和额外字段同协议保留，跨协议目标规则继续决定是否可表达。没有增加新原语或第二个最终出站检查位置。

核心版本 2 退出共享 Go 的供应商参数检查，保留工具、reasoning、资源和关联等核心约束；旧核心路径暂留旧校验。默认 Mapping 显式拒绝会在目标编码中消失的非法追踪载体，不能用省略绕过类型合同。新增自定义包回归在 metadata 参数位置声明数值 tracking，证明接收、转换、无根 Native 重建及修改后拒绝均只取决于配置。

移除八条要求旧 raw 编码接受非法用户类型或忽略非对象 metadata 的差分项，改为明确拒绝边界；字符串、null、转义等价、冲突及未知字段测试保留。旧 Chat 全字段夹具原本因数值 metadata 提前失败，改用合法追踪值，使其实际验证其余字段的转换；非法值由直接合同回归验证。没有刷新旧差分快照或扩大忽略范围。诊断使用实际 wire 位置，不模拟旧参数路径。

Responses 共享重复输出对象／索引约束、非空文本判断及六个重复绑定读取，为声明腾出空间；不提高限额。最终整包 token：Anthropic 90,843、Gemini 72,366、Chat 98,485、Responses 99,420。其余配置保持原有未修改区域的格式。

| 检查 | 结果 |
|---|---|
| closure-request-contracts-all-final | protocol 全量 2.819 秒、builtin 全量 38.134 秒，通过 |
| closure-request-contracts-gateway | 32 场景 JSON／SSE 网关矩阵及 storage／wire 相关回归通过，14.557 秒 |
| closure-request-contracts-race | 请求合同、投影、最终输出和存储相关 race 通过，无竞争报告 |
| closure-request-contracts-vet | protocol 全部 vet 通过 |
| closure-request-contracts-staticcheck | 固定 v0.8.2：24 条主诊断及 1 条关联位置，与前批多重集比较无新增、无消失；退出 1 |

测试初稿的诊断阶段写成未支持的名称，编译器拒绝；修正为既有 evaluate。随后测试指出旧差分仍要求非法 raw 类型成功，按新版本合同改为直接拒绝断言。所有最终检查记录保存在原忽略目录。

本批不代表批次 1 全部完成：对象 aggregate 证明、output_text 修改后一致性还未闭合；缓存／错误、布局、真实流、入口所有权、正式性能及旧链退役继续待实施。未测 B／C，不宣称性能无回退。

## 批次 1F：派生文本与当前正文一致（dev.117）

四目标 Mapping 在保留或省略 Responses output_text 前，使用 equal_text 检查当前核心中的有序可见文本；比较分段字符串，不构造完整拼接正文。JSON 及含完整 response 的流快照共用对应声明，refusal、reasoning、工具参数不纳入可见文本。其余 metadata 不触发正文比较。Responses Spec 将来源的类型、显式 output 数组和正文一致性提为具名约束，同时接到重建与最终客户端输出。

新增直接回归覆盖分段 Unicode 文本、正文修改、有／无根 Native、终态事件和直接编码拒绝；最终 wire 用代表性一致、冲突、null 检查。网关基础矩阵的 Responses 上游 JSON 现在显式包含合法 output_text，验证四目标真实本地转换。未调用供应商。

来源非法 output_text 统一归因为 upstream_contract_violation，目标非法输出使用 conversion_rejected；不复刻旧 stream 分类或首错文字。已有来源失败测试按这项新合同直接断言，没有刷新快照。Responses 再共享三个真实重复的绑定读取，整包 token 为 99,911；其余三包分别为 Anthropic 91,433、Gemini 72,956、Chat 99,075，限额不变。

- closure-output-text-all：protocol 全量 6.884 秒、builtin 全量 38.999 秒，通过。
- closure-output-text-gateway：32 场景真实 JSON／SSE 网关矩阵通过，10.796 秒。
- closure-output-text-race：派生文本、最终响应和来源边界相关 race 通过，无竞争报告。
- closure-output-text-vet：protocol 及 server vet 通过。
- closure-output-text-staticcheck：固定 v0.8.2 仍为 24 条主诊断及 1 条关联位置，与前批无新增／消失；退出 1。

本批不新增执行原语，不修改核心 Go 的供应商分派；旧拼接检查仅留在旧 codec 路径。对象 aggregate、缓存／一般错误、布局、复杂流、入口所有权和最终生产／性能门槛仍待完成。

## 批次 2A：默认缓存提示处置（dev.118）

四份 Mapping 接入已有 cache_intents，Anthropic 补上显式 request_projection 引用。目标保留已支持的 key、retention、breakpoint 和 scoped resource；仅配置列出的无对应表示提示可以兼容省略，并记录 info 诊断，严格模式拒绝。null 按声明归一化；未知策略、额外成员、未声明 TTL、资源引用不进入提示省略。禁用规则后，编码器检查残留而不补做降级。不新增原语或供应商 Go 判断。

Responses 共享 23 项真实重复的绑定读取，整包 token 从缓存初稿的 100,041 降为 99,824；其余 Anthropic 91,692、Gemini 73,371、Chat 99,315。100,000 token／64 层限额保持不变；触及深度上限的两项读取提取已撤回。未修改子树继续保留原格式。

全量检查暴露原缓存检查将私有工作区误当必要语义凭据，导致序列化正式样例被拒绝。改为复用已有 requestReplay：精确工作区用于复用，其他合法 Native 经相同解码边界重建基线；缓存、字段及 Native 协调共享请求局部准备结果。准备后的资源已完成 Scope stamping，移除缓存检查内的重复 stamping。认证续传仍需宿主证明；原始缓存删除证据保留，清空当前 Cache 不能绕过它。接受边界变化仅为不再因缺失私有工作区拒绝可重新核验的合法 Native；不是放开 Scope、外部合同或认证检查。

必要回归覆盖明确提示省略、严格拒绝、null、禁用、输入隔离、Native／重建、TTL 与资源保真及未知策略拒绝。复用 32 场景网关矩阵，在 Anthropic 向其他三协议的 JSON／SSE 请求中加入缓存提示，模拟上游直接检查省略，完整响应及恰好一次上游调用断言保留。

| 检查 | 结果 |
|---|---|
| closure-cache-defaults-all-closed | protocol 全量 6.877 秒、builtin 全量 40.214 秒，通过 |
| closure-cache-defaults-gateway | 32 场景真实网关通过，10.659 秒 |
| closure-cache-defaults-race | 核心缓存、四包缓存／续传及 32 场景网关通过，无 race 报告 |
| closure-cache-defaults-vet | protocol 与 server vet 通过 |
| closure-cache-defaults-staticcheck | v0.8.2，24 主诊断和 1 关联位置；与 dev.117 多重集逐条相同，退出 1 |

原始失败和修复后的检查均留在忽略目录。前端未改，不重复其检查。尚未开展 B／C 正式性能；本批不宣称性能无回退。对象 aggregate、一般错误、布局、复杂流、入口所有权及生产切换继续待实施。

## 批次 2B：受限错误字段与目标处置（dev.119）

新增 each.select:error，复用当前控制步骤、局部读取、赋值、预算和诊断。选择完整响应错误、事件错误及事件内完整响应错误；写权限仅为 message、code、param、details、providerType，禁止 category、响应状态、事件类型、来源、扩展和整个对象替换。不认识供应商名称或类别，不调用旧错误投影。schema 的选择枚举、操作文档及编辑器帮助同步；Anthropic／Gemini 默认包实际接入该能力。

两个目标以 response-error-details／event-error-details、顺序 225 声明已知 code／param 和 new_api_error wrapper 的消息保留。兼容模式保留细节并诊断结构损失，严格模式拒绝；空字符串按显式规则归一化。其他 wrapper、非法类型和未知扩展继续拒绝。规则禁用后不运行隐式投影。规则只在现存跨协议错误上执行；同协议保留原表示，普通生成结果不解析错误对象。原始错误 category、HTTP 状态、生命周期及主因保持。

必要回归覆盖 JSON、真实源错误帧、终态响应快照、禁用／严格、null／空值、已知 wrapper、非法及未知值、源隔离和编译期写权限。本地网关分别向两个目标交付上游 429，断言状态、消息细节、目标 wire 合同和恰好一次调用；已有流失败交付断言继续验证主因。测试初稿把 HTTP 失败的可缺省核心 status 误写为必须 failed，改为与实际输入状态相等，并保留失败结果的直接断言，没有改变生产状态。

| 检查 | 结果 |
|---|---|
| closure-error-details-all | protocol 全量 6.846 秒、builtin 全量 41.150 秒，通过 |
| closure-error-details-final-guards | 最终无错误／同协议守卫后的错误回归、四包正式证据及两协议失败包络通过，4.240 秒 |
| closure-error-details-gateway | 本地 HTTP 状态／细节交付及流主因检查通过，5.970 秒 |
| closure-error-details-race | 选择权限、默认错误、真实网关及流失败相关 race 通过 |
| closure-error-details-vet | protocol／server vet 通过 |
| closure-error-details-staticcheck | v0.8.2，24 主诊断及 1 关联位置，多重集与前批相同，退出 1 |
| closure-error-details-types／lint | 前端类型检查与修改文件 ESLint 通过 |

整包 token：Anthropic 93,456、Gemini 75,135、Chat 99,315、Responses 99,824。限额不变。没有正式性能结论；未知错误语义仍是明确拒绝边界，不宣称所有错误扩展均可跨协议。聚合证明、布局、复杂流、入口所有权、性能及生产退役仍未闭合。

## 批次 1G：对象聚合按声明路径编译（dev.120）

aggregate.layout 退出 /cache/0、ttl、breakpoint 和 request 的 Go 特例。编译器按现有核心结构绑定 owner、Value 成员及标量标签，禁止来源、Native、资源和客户端意图；按已有编译预算证明拆分和无覆盖重建。命名程序／局部绑定、稳定数组位置、条件双分支及独占字段合并进入同一证明。运行期使用已编译成员目录，保留必要的标签、余项、重复字段和未表示实体检查，无反射或整份核心 JSON 往返。

默认 Anthropic 根缓存接入该声明，删去重复根对象 flatmap 和缓存数组临时绑定；缺失与 null 表示保持。TTL 删除同时覆盖 Native 协调和无根 Native 重建，未知外部成员留在余项。外部 wire 扩展不能再次声明 cache_control；这类核心双重所有者现在明确拒绝。自定义工具定义仅改变配置，即可把 name 和 schema 余项映射到 /tools/0，修改 name 后重建，并拒绝未表示的 description。标签不再被当成描述性注释：修改已声明 location 而无对应目标布局会拒绝。

| 检查 | 结果 |
|---|---|
| closure-object-layout-all-2 | protocol 全量 2.826 秒、builtin 全量 41.505 秒，通过 |
| closure-object-layout-final-cache | 最终 wire 所有者守卫、缓存及四包正式证据通过，4.569 秒 |
| closure-object-layout-resource-boundary | 最后补充的受保护单资源路径编译边界通过，0.275 秒 |
| closure-object-layout-gateway | 32 场景真实 JSON／SSE 网关矩阵通过，10.849 秒 |
| closure-object-layout-race | protocol 3.799 秒、builtin 21.446 秒、server 122.499 秒，相关 race 通过 |
| closure-object-layout-vet | protocol 与 server vet 通过 |
| closure-object-layout-staticcheck | 固定 v0.8.2：24 条主诊断、1 关联位置；与 dev.119 多重集无新增、无消失，退出 1 |
| 前端类型与 ESLint | 本地 tsc 和修改编辑器 ESLint 通过 |

初稿测试暴露重复所有者、漏掉额外数组成员、测试夹具 ID 及辅助函数引用问题，均修正后保留原直接断言。检查原文保存在忽略目录。布局、复杂流、全部入口和所有权仍待闭合；未形成版本 B，未测正式配对性能，也未切换生产加载器。

## 批次 3A：显式指令布局（dev.121）

新增受限 ProtocolControlStep.layout：stable_partition 稳定移动根节点，combine_children 合并相邻同 kind／role 普通容器的子节点。Go 不识别供应商、system 或 developer；选择、角色变化、作用范围及根边界损失由 Anthropic／Gemini Mapping 的 system-instruction-hoist／140 声明。普通容器必须没有未表示身份、状态、metadata、缓存、资源或未知属性；子节点保留自己的载荷、来源、缓存与资源。合并必须有无条件边界损失声明，严格模式拒绝；只诊断、没有实际合并时不额外报告损失。

编码器检查目标要求的布局、容器字段及单根边界，规则禁用后不再暗中上提或合并。Anthropic 已确认的内联 system 兼容以 Mapping 中真实 Native 容器位置分类保留；单独 Source 注释不授权此位置。结构变化使根 Native 失效，角色变化使该容器 Native 失效；原样移动的节点和叶子 Native 保留。重排只复制节点切片，缺省未命中不创建结果切片，没有整树 JSON 往返。

复用 32 场景网关矩阵，在 Chat→Anthropic／Gemini 的 JSON 和流请求中加入对话之后的 system/developer；模拟上游检查两条指令的实际顺序，保留完整响应、usage 和一次上游调用断言。必要组件覆盖严格／禁用、容器扩展拒绝、原生位置、根 Native 删除后拒绝伪保真，以及完整响应稳定分区的载荷所有权。

| 检查 | 结果 |
|---|---|
| closure-content-layout-combined-all | protocol 全量 6.882 秒、builtin 全量 41.983 秒，通过 |
| closure-content-layout-final-contract | 后续合同收紧的定向检查通过：protocol 0.289 秒、builtin 4.492 秒 |
| closure-content-layout-combined-race | 核心 3.352 秒、builtin 11.962 秒、32 场景真实网关 135.375 秒，无 race 报告 |
| closure-content-layout-final-race | 最终布局、Native／禁用边界和四包正式证据：核心 3.352 秒、builtin 45.142 秒，通过 |
| closure-content-layout-final-gateway | 最终配置的 32 场景真实网关矩阵通过，10.803 秒 |
| closure-content-layout-loss-order | 合并损失诊断必须位于首步的最终编译边界与正式证据通过：核心 0.246 秒、builtin 4.086 秒 |
| closure-content-layout-final-vet | protocol／server vet 通过 |
| closure-content-layout-final-staticcheck | 固定 v0.8.2，24 主诊断及 1 关联位置，多重集与 dev.120 相同，退出 1 |
| 前端 | 本地 tsc 和修改编辑器 ESLint 通过 |

最终整包 token：Anthropic 95,013、Gemini 76,038、Chat 99,315、Responses 99,824，限额保持。草稿的 Native effect 权限、测试对不存在叶子 Source 的假设，以及仅靠前缀检查仍会隐式合并根的问题已修正。全部中间失败与最终检查保留在忽略目录。一般相邻历史分组、多段 reasoning／多根输出、Gemini 混合结果、UTF-16 引用、复杂流和入口所有权仍待闭合；未归档版本 B、未测正式性能、未切换生产默认或删除旧链。

## 批次 3A：Gemini 混合工具结果包络（dev.122）

修复来源 response 同时存在 error／output 或其他成员时丢失错误状态的问题。使用现有 payload 和 status.payloadForm=result_envelope 表达完整包络；显式 null 保留，普通对象内同名键不会自动升级为语义标志。Gemini 配置按声明重建完整对象，其他三个目标通过 tool-results／150 保留序列化数据和错误标志，报告结构损失；严格拒绝，禁用后不隐式处置。未增加供应商 Go 分派、核心字段或 DSL 原语。

functionResponse 扩展成员由独占字段合并保留，不能替换或向 response 语义 owner 追加成员。测试覆盖删除 Native 并修改调用 ID 后重建、状态矛盾、未知状态、扩展冲突，以及来源输入隔离。Gemini 默认包增加独立编写的混合包络解码／编码样例，进入正式激活验证。既有真实工具历史测试增加 Gemini→Anthropic JSON／SSE，直接检查出站完整包络、错误标志、工具关联、完整响应、usage 和恰好一次上游调用。

Responses 提取五组真实重复表达式：可见文本段、output_text metadata 选择、开放目标结束、默认模式禁用判断及非空数组。保留命名程序与原词法作用域，不新增展开树或提高预算。四包 token 为 Anthropic 95,267、Gemini 77,108、Chat 99,569、Responses 99,841。

| 检查 | 结果 |
|---|---|
| closure-mixed-results-all | protocol 全量 6.995 秒、builtin 全量 43.285 秒，通过 |
| closure-mixed-results-race | 包络、工具结果和真实 32 场景网关 race：builtin 24.111 秒、server 235.078 秒，通过 |
| closure-mixed-results-final | 最终独占合并、正式样例和真实工具历史通过：builtin 4.236 秒、server 11.605 秒 |
| closure-mixed-results-final-race | 最终正式样例、包络边界及真实工具历史通过：builtin 49.307 秒、server 130.049 秒，无 race 报告 |
| closure-mixed-results-vet | protocol 全树和 server 通过 |
| closure-mixed-results-staticcheck | v0.8.2：原 24 主诊断＋1 关联位置，去行列后多重集无新增或消失，退出 1 |

前端未修改，不重复前端检查。全部原始失败及成功日志位于忽略目录。本批不宣称性能无回退；尚未形成 B，也未切换生产默认。一般布局、多段 reasoning／多根输出、复杂 SSE、全入口所有权、A/B/C 正式性能及旧链退役继续待完成。

## 批次 3B：相邻助手历史分组（dev.123）

layout 增加 group_runs 和声明容器 role；既有 when、路径绑定、诊断、编译槽位及预算继续复用。按相邻选中根构造 message，普通 message 展开子节点，其他叶子移动原节点，未选中根截断。单个 message 不变；需要展开的容器若有身份、状态、metadata、缓存、资源、payload 或未知属性则拒绝。实际分组必须首步无条件声明边界损失，不能同时修改字段。没有需要分组的根时不分配结果切片；发生分组仅复制节点切片，叶子资源、来源及 Native 保留，根 Native 失效。

Anthropic Mapping 接入 assistant-history／165：选择 assistant message、reasoning、tool_call。顺序 160 已属于 history-response-metadata，初稿冲突在编译期拒绝，未引入自动调整顺序。编码 effect 拒绝未分组的裸 reasoning／tool_call；用户禁用规则后不隐式再次布局。公共 schema 自动包含操作和 role，编辑器帮助与原语文档同步。

核心回归覆盖两个相邻段、用户边界、资源／Native 所有权、容器附属语义拒绝及编译约束。默认包回归直接检查多段 thinking、文本、工具与工具结果边界、strict／禁用和输入隔离。既有真实 Gemini→Anthropic 工具调用中增加相邻助手消息，模拟上游检查 thinking→text→tool_use 的顺序，JSON／SSE 完整消费、usage 和一次上游调用断言保留。测试夹具最初缺少声明的 reasoning 能力，修正该夹具后通过，不增加运行兜底。

| 检查 | 结果 |
|---|---|
| closure-assistant-group-contract | 编译、核心分组、正式包证据及目标回归：protocol 0.289 秒、builtin 3.604 秒，通过 |
| closure-assistant-group-gateway-fixed | 真实四目标工具历史 JSON／SSE 通过，14.893 秒 |
| closure-assistant-group-all | protocol 全量 7.069 秒、builtin 全量 43.943 秒，通过 |
| closure-assistant-group-race | protocol 3.457 秒、builtin 38.615 秒、server 154.114 秒，通过，无 race 报告 |
| closure-assistant-group-vet | protocol 全树和 server 通过 |
| closure-assistant-group-staticcheck | v0.8.2，24 主诊断＋1 关联位置，去行列后多重集与 dev.122 一致 |
| 前端 | 本地 tsc 与编辑器 ESLint 通过 |

Anthropic 整包 token 95,529，限额不变。完整响应多根、Chat 混合布局、UTF-16 引用和复杂 SSE 尚未以本提交宣称闭合；B／C 性能和生产切换仍待完成。
