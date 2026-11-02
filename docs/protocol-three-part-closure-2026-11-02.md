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

## 批次 3C：完整响应多根与 Gemini 单候选（dev.124）

Anthropic／Gemini response-layout／240 复用 group_runs，将完整响应的有序 message、text、reasoning、tool_call 放进单个 assistant 包络；无需新增 Go 原语。容器语义及独立候选属性不能被合并，结构变化显式诊断，严格模式拒绝。编码 effect 要求零个或一个 assistant message，禁用后拒绝未解决的裸根，不从编码器重复布局。错误响应的 content=null 与无内容不被误当数组；该边界通过已有错误回归发现并修正。

Gemini Spec 新增 single-candidate 具名约束，同时检查来源与最终客户端候选数。当前产品能力支持零个或一个候选，多候选明确拒绝；不取第一个或串联独立回答。Mapping 不再把多个同轮核心根直接编码为多个候选。

必要组件回归检查两个可见 thinking 块、文本和工具的顺序与完整重建，strict、禁用、独立候选拒绝和输入隔离。既有 32 场景真实网关矩阵在 Responses→Anthropic／Gemini 的 JSON 响应中使用两个消息根，保留 output_text 一致性、完整消费、usage、终态和一次上游调用断言。试验中实际复现 Chat 多根输出仍不完整，明确登记为下一批缺口，未放宽断言；本批仅对已接通的两个目标使用复杂夹具。

| 检查 | 结果 |
|---|---|
| closure-response-layout-boundary | 双目标布局、单候选和四包正式证据 3.937 秒，通过 |
| closure-response-layout-gateway-fixed | 错误边界及组件 1.080 秒、32 场景真实网关 10.827 秒，通过 |
| closure-response-layout-all-final | builtin 最终全量 43.655 秒，通过；此前 protocol 全量 6.834 秒通过，Go 核心未修改 |
| closure-response-layout-race | builtin 44.107 秒、32 场景网关 123.306 秒，通过，无 race 报告 |
| closure-response-layout-vet | protocol 全树和 server 通过 |
| closure-response-layout-staticcheck | v0.8.2，24 主诊断＋1 关联位置，多重集与 dev.123 一致 |

token：Anthropic 95,920、Gemini 77,634、Chat 99,569、Responses 99,841，限额保持。前端本批未变。复杂 SSE、Chat 多根／混合布局、UTF-16、全部入口所有权与 A/B/C 性能仍待完成，未切换生产默认。

## 新计划批次 0：dev.124 恢复点与配置复用

dev.124 源码、四包、已有检查和匹配的 Windows amd64 可运行产物归档在忽略目录 dev124，清单保存 SHA256。性能参考 A 的六目标哈希重新核验通过，仍为 b2844846 / dev.101；恢复点不替代 A，也不是功能完整版本 B。

Chat 提取重复响应身份、终态批次、reasoning 模式和模型能力程序；Responses 共享目标 item 扩展程序。命名程序保持编译复用，未提高配置限额。生产调用关系仍为 relay 注册 builtin.Modules、server 加载旧默认定义和 storage 预置修复；独立新包尚未替代这些入口。

closure-dev124-build-final 构建通过；closure-dev124-shared-programs-final 两个受影响包的编译和正式激活样例通过，3.044 秒。仅配置整理，编译器版本未递增。

## 新计划批次 1：Chat 文本、引用与混合历史（dev.125）

受限布局新增 merge_text 和 unfold_children。前者只处理 message.children，预先计量后一次分配拼接；无命中和单节点不复制正文。后者按声明展开工具结果，保留其余连续内容的角色和顺序。容器身份、状态、资源、缓存、未知属性和未声明 metadata 不默认取第一项或丢弃。utf16_length 直接遍历字符串，引用偏移使用累计长度，不创建 rune／UTF-16 数组或重复拼接前缀。编译绑定作用域、写权限和上下文依赖；新增能力已进入默认配置、schema、操作文档和编辑器帮助。

Chat Mapping 明确声明 tool-result-history／235、assistant-history／240、history-text／250，以及 response-layout／240、response-text／250；它们在 metadata、资源和形状处置后运行。删除编码器的隐式分组、flatten、reasoning 文本 join 和首个 refusal 选择。JSON 多候选保持独立，空响应仍输出一个空 assistant choice。多块正文输出字符串；同类 reasoning／refusal 独立合并。annotations 的来源位置、null／空数组、文本及消息所有者均进入核心，引用按 UTF-16 累计偏移，logprobs 保持所属通道与顺序。实际块边界损失由规则诊断，严格或禁用相应规则时拒绝；删除根 Native 后仍可重建。

已有真实网关矩阵的 Responses→Chat JSON 使用两个消息根 h／i，客户端完整消费 hi。工具历史夹具增加相邻助手文本／调用及 user 容器中的结果／文本，实际模拟上游断言三条消息、关联及恰好一次调用。组件补充 emoji 引用、logprobs、空内容、多候选、输入隔离与未处理资源拒绝。原先要求隐式分组或非法响应数组的测试改为显式运行目标规则和手写合法预期；没有刷新快照或放宽关联断言。

| 检查 | 结果 |
|---|---|
| closure-chat-layout-all-final | protocol 全量 3.258 秒、builtin 全量 54.690 秒，通过 |
| closure-chat-layout-contracts | 正式合同及真实网关／工具／Agent 续轮通过，server 82.578 秒 |
| closure-chat-layout-race-final | 最终源码 protocol 4.277 秒、builtin 16.693 秒、server 406.267 秒，通过，无竞争报告 |
| closure-chat-layout-evidence-final | 最后配置共享与顺序调整后的 Chat 正式激活证据通过，1.143 秒 |
| closure-chat-layout-vet | protocol 全树及 server 通过 |
| closure-chat-layout-staticcheck | 固定 v0.8.2：24 主诊断及 1 关联位置，与 dev.124 去行列后的多重集相同；退出 1 |
| closure-chat-layout-types／lint | 本地前端类型和编辑器 ESLint 通过 |

初次 server race 使用 2 核／1GiB 时在组合验证中达到默认 10 分钟超时；日志保留，不列为通过。最终按常规 4 核／2GiB、20 分钟超时运行通过，没有修改 DSL 资源上限。全量测试后的调整由最终相关 race 和正式证据复核。上述耗时均为功能检查，不作为正式性能结论。

Chat 继续共享真实重复的 metadata 聚合程序，整包 token 为 98,724；Responses 为 99,815，Anthropic 95,920，Gemini 77,634。100,000 token／64 depth 限额不变。已修正上一条恢复点记录的文本编码损坏。复杂 SSE 的跨 item 位置及 Native 接续、全入口所有权、版本 B／C、正式性能和旧链退役仍未完成，生产默认尚未切换。

## 新计划批次 2A：增量位置区间与顺序引用（dev.126）

新增 track_position／project_range，复用现有流编译通道、事务和 StateItems 预算。前者按声明长度追加来源到输出区间，后者以二分查找换算范围；不识别供应商、引用字段或字符单位。连续区间合并，失败事务不修改已提交末尾；提交只扩大尾段或追加新段，不复制完整区间前缀。动态身份与实际区间分别计量，零长度首次观察保留空区间，后续空观察不移动起点。范围跨其他 item 输出或超过已观察正文时拒绝。

Chat Mapping 使用 utf16_length 度量 track_text 返回的新后缀，原生 observer 与重建共用同一程序。引用字段与换算后的写回均由配置指定。初次接线将换算放在去重之前，现有 Native 回归发现重复输出；修正为先按原 metadata 去重，再为实际输出换算。偏移不变的引用保留原条目表示。没有放宽混合流的直接断言或重写旧期望。

现有 tracker 身份表增加独立持有的 key，文本、数组、工具、缓冲及区间状态共同引用；每次提交均使用该 key，避免更新时重新持有当前大帧中的小字符串。固定通道不新增语义实体。保留已有缓冲释放及分类预算，不另建所有权框架；其他状态及日志所有权仍待整体闭合。

同一 Responses 双 content-part 夹具同时用于组件和真实网关：😀＋hi，第二段引用从 0..2 换算为 2..4。组件检查完整引用、终态和 usage；真实网关复用原矩阵，完整消费正文、计量及恰好一次上游调用。定向区间测试覆盖失败回滚、跨 item 拒绝、非法范围、空区间、资源限额，以及连续 1,024 次追加不增加区间或逻辑保留量。

| 检查 | 结果 |
|---|---|
| closure-stream-positions-all-owned | 最终核心全量 6.685 秒、builtin 全量 41.389 秒，通过 |
| closure-stream-positions-gateway | 位置／Native 组件及 Responses 真实网关方向通过，server 4.462 秒 |
| closure-stream-positions-race | 最终核心状态、默认流和真实网关：protocol 3.827 秒、builtin 13.927 秒、server 59.954 秒，通过 |
| closure-stream-positions-vet | protocol 全树及 server 通过 |
| closure-stream-positions-staticcheck | 固定 v0.8.2：24 主诊断及 1 关联位置，与前批多重集相同；退出 1 |
| closure-stream-positions-types／lint | 前端类型和编辑器 ESLint 通过 |

操作目录、生成 schema、原语说明和编辑器帮助同步，修正前批合同说明中的文本编码损坏。Chat 整包 token 99,040，其他三包不变，限额未提高。没有正式性能结论。本切片修正已有顺序 item 输出；交错 item、独立 message 的布局损失授权、复杂 thinking／工具、最终交付失败组合和全入口所有权仍待实施。版本 B 尚未形成，默认预置、旧链退役和 A／B／C 验收未完成。

## 闭合计划批次 0：dev.126 恢复点

`e7e6e802 / dev.126` 的源码、四包、已有验证记录和匹配的 Windows amd64 产物已归档至忽略目录 `.tmp-dev/protocol-three-part-closure-2026-11-02/dev126/`。`manifest.json` 记录 42 个文件的 SHA256、构建信息和参考 A 的六目标哈希核验结果；`closure-dev126-build` 通过。参考 A 仍为 `b2844846 / dev.101`，六目标产物全部与原清单一致。恢复点不视作功能完整版本 B。

当前新链使用 package 编译、声明式映射和流事务；生产默认仍经 `relay/protocol_module.go` 注册旧 `builtin.Modules()`，`server/protocol_refresh.go` 加载旧定义并修复绑定，`storage/protocol_recovery.go` 保留旧预置恢复。这些路径在最终候选切换批次删除。下一功能批次接通流事务内的显式损失授权及 Chat 复杂流；四包配置限额和既有测试结论不变。

## 闭合计划批次 1：流式布局授权与 Chat 复杂流（dev.127）

新增 `conversionDefaults[].streamDisposition`、`require_disposition` 和宿主 `WithConversion`。声明固定目标事件程序指纹；原语只在目标 program／observe／finalize 可用，不能进入来源解码或只读效果检查。表达式在现有事务登记实际损失，输出计划构造后、编码前只查询请求的规则并按有效顺序处理。兼容降级的诊断随成功事务提交；strict、禁用、替换动作、未匹配、目标不一致和缺少绑定均拒绝，不提交状态或成功诊断。原生 observer 同样执行授权，不新增引擎或全策略逐帧扫描。

Chat `event-layout / 310` 接管多个 message、同类内容块及 message 与独立工具项的边界损失。原有 targetItems 与通道索引继续持有身份；已完成的通道仍保留最后身份，不缓存正文。首次边界变化请求授权，delta 不重复登记。父子关联、容器 metadata／缓存／资源及独立候选的拒绝边界保留。网关、组合验证、流预览和 Agent 收集上下文固定已解析策略；Agent 继续直接收集核心内容，不做无必要的目标重建。

新 Responses 夹具组合两个 message、同 message 两 part、工具插入、emoji、晚到引用、logprobs 快照和 usage。组件直接断言发送顺序 `😀 → lookup → 参数 → hi → !`，引用 0..2 → 2..4、概率顺序、一次工具及终态；strict／禁用在第一次实际损失拒绝，并验证同一状态可在失败后重新接受该工具开始。真实网关在隔离测试数据库中完整消费同一夹具，只调用上游一次。初次网关失败来自测试模型和模型组未启用工具能力，补齐声明后通过，未放宽运行授权。最初 logprobs 夹具缺少已注册子集合的最终快照，按真实合同补齐，未放宽 Spec。

| 检查 | 结果 |
|---|---|
| closure-stream-layout-core-all | protocol 全量 5.908 秒、builtin 全量 38.168 秒，通过 |
| closure-stream-layout-all | 处置、Chat、真实四协议 JSON／SSE 矩阵通过；server 10.109 秒 |
| closure-stream-layout-race | 核心事务／区间、Chat Native 与复杂流、网关交付失败及 Agent 续轮：protocol 4.266 秒、builtin 32.348 秒、server 194.657 秒，通过 |
| closure-stream-layout-vet | protocol 全树和 server 通过 |
| closure-stream-layout-staticcheck | v0.8.2：24 主诊断＋1 关联位置；与 dev.126 去行列后的多重集一致，无新增／消失，退出 1 |
| closure-stream-layout-types／lint | 前端类型与编辑器 ESLint 通过 |

原语目录、反射 schema、编辑器帮助及合同说明同步。Chat 两源包装后的原始 JSON token 为 99,097，解码成包结构再序列化为 97,081；配置限额仍为 100,000／64，没有扩容。其他三包源未变。检查记录在忽略目录 checks；没有正式性能结论。其余目标复杂流、最终 wire 文本改写与引用证明、全入口所有权、版本 B／C、生产切换及旧链退役仍未完成。

## 配置空间整理（仍为 dev.127）

Chat 共享 44 处相同的非空字段选择程序和 4 处事件 metadata 汇集程序；Responses 共享跨合同判断、当前 item 来源／Native 捕获、reasoning form 与 between-tools 判断。仅替换完全相同的表达式树，命名程序继续按静态作用域编译复用，不重新展开执行。Chat／Responses 原始整包 token 分别降为 96,909／99,653。

初稿另提取 Responses 的 target-part 文本判断，编译发现最深引用达到 65／64；撤回这一项提取，没有提高深度限额。最终 `closure-stream-shared-programs-depth` 通过两个包编译、正式激活样例及 Chat 复杂流／Native 回归，2.271 秒。mapping 源 SHA256 前缀分别从 `4e13ed6d0c910e1a`／`4a5e566b1015f4de` 变为 `31135c6d43d035dd`／`a5c12f8df3481b07`，需使用新修订证据。没有 Go 执行语义变化，不升级编译器，也不重复未变化的全量检查。

## 闭合计划批次 2A：其余目标的复杂流证据（仍为 dev.127）

实际检查表明，下列组合已能由现有 Spec／Mapping 完成，因此本切片补真实调用缺口，不添加新的执行分支，也不升级编译器。

- Anthropic 新夹具包含两个独立 thinking 块及各自签名、两个文本块、分段工具参数和尾部 usage。组件同时断言原帧不重复、删除事件根 Native 后的五块顺序、签名 Scope、精确工具参数及终态。相同夹具进入真实网关矩阵。
- Responses 将 dev.127 的双 message／多 part／工具／引用／logprobs 夹具用于自身目标，复用现有逐节点重建检查；专门移除事件与 response 根 Native 后仍保持完整语义。真实网关分别检查 Chat 和 Responses 目标，保留一条实际引用；Responses 额外明确保留两个空 annotations 所有者。
- Gemini 把已有 thinking→文本→工具及独立签名、复合帧和重复尾部 usage 夹具移到共享文件，原有重建及原帧字节断言保持。真实网关使用同一文件，检查正文、工具、两个签名资源、reasoning 分项及总量。

三条真实调用均显式启用对应模型／模型组的工具能力，绑定声明 reasoning／signature 能力；测试客户端用自身 Scope 读取签名资源。初次 Anthropic 客户端收集因 Scope 为空拒绝，修正测试调用参数后通过，没有省略资源检查。首次 Responses 同目标检查暴露原断言把 metadata 容器数量当成引用数量；现分别断言三个所有者、一条引用和两个空数组，未忽略 metadata。

`closure-complex-stream-evidence` 通过组件及完整 32 场景网关矩阵，builtin 0.819 秒、server 11.350 秒。`closure-complex-stream-evidence-race` 通过新增重建、签名及三个上游的真实网关，builtin 12.326 秒、server 123.901 秒。相关 vet 通过；固定 staticcheck v0.8.2 仍为 24 主诊断＋1 关联位置，与前批完全相同，无新增／消失。前端和生产 Go 执行代码未变，不重复其全量检查。

本切片不等于批次 2 全部完成：最终 wire 实际改写文本后的旧引用失效证明、其余 Native／交付失败组合仍需闭合。特别是 Chat 没有全文终态快照，不能仅凭最终索引仍在长度范围内就接受对已改写文本的旧引用。版本 B 尚未归档；全部入口及所有权、正式 A／B／C 性能、生产默认切换、旧链删除和最终平台检查仍待实施。

## 最终闭合批次 0：dev.127 恢复点与采集入口

`90767d7e / dev.127` 恢复点位于忽略目录 `dev127/`，清单记录源码、四包、已有检查及匹配 Windows amd64 产物共 58 个文件的 SHA256。`closure-dev127-build` 通过；参考 A 的六目标产物重新核验全部一致。恢复点不等于功能完整版本 B。

采集器要求显式选择 `legacy`、`packages` 或 `production`。`packages` 在隔离数据库激活四包并重新验证测试绑定，客户端和模拟上游经 `CompileSources` 读取两源；`production` 不安装测试包，实际默认仍为 codec 时启动失败。活动四包、绑定修订、转换策略和编译器在计时前检查并写入 `execution.json`。正式 runner 另要求完整 `--expect-commit`，将提交编入测试产物；legacy 仅允许参考 A 的提交。隔离 HTTP／取消循环改用 `reset-without-gc`，回收诊断仍单独记录。

参考 A 用 Go test overlay 加入相同的采集身份检查，overlay 只覆盖两个测试文件，随证据保留；归档生产源码和原产物不变。A 的短 JSON／SSE 启动自检通过。四个新包的短 JSON／SSE 自检分别通过：Responses 9.098 秒、Gemini 8.058 秒、Chat 8.090 秒、Anthropic 9.308 秒；相关 `go vet` 通过。夹具现在使用所选核心版本、配置目标规则、宿主 operation 与实际交付确认；没有修改生产执行语义，编译器保持 dev.127。这些调用只验证采集接线，单次时间不形成性能结论。

复现入口（使用新的空输出目录，A 另传归档 `--source`）：

```powershell
node scripts/benchmark-performance.mjs --execution=packages --expect-commit=<完整提交> --mode=measure --cases=chat-short,stream16 --requests=1 --repeats=1 --idle-ms=1 --output=.tmp-dev/<新目录>
```

当前仍需：改写引用的最终边界、交付失败组合、全入口和早期错误、所有权、B 归档、完整配对性能及生产 C。`packages` 只是显式选择新链，不自动把当前源码认定为 B；正式五对交替运行及性能门槛留到功能完整之后。

## 最终闭合批次 1A：完整响应引用证明（dev.128）

新增 `encode_response.deliveryRanges` 及 `ValidateResponseOutput(before, after, options)`。Spec 保留最终外部形状；Mapping 指定文本、引用身份、路径和计量；Go 只执行只读选择及区间证明。Chat／Responses 默认包均已使用该字段。生成 schema 随 Go 合同反射同步，编辑器帮助与原语文档同时更新。事件方向尚未接线，因此编译器明确拒绝在事件方向声明此字段。

实际改写保留相同 Unicode 字符前缀和后缀；等长替换、跨改写区、未知文本关联及未修正索引均拒绝。未改写后缀在长度变化后保留确定偏移；不会自动改正 wire 规则的旧索引。无引用时跳过正文观察；正常文本不建立显式区间表，分歧后才创建必要区间。默认 UTF-16 度量直接绑定已有原语，借用字符串只在同步比较中存在，未做临时观察 JSON 往返或整份正文复制。

新包网关 JSON 出口移除 wire 规则之后的完整语义再解码，最终 Spec、引用和续传载体共用输入视图。组合验证和 wire 响应预览调用同一边界。自定义 reply 合同验证仅调整配置即可改变引用取舍，不依赖协议名称；真实 Chat 网关分别接受未改写区间、拒绝被改写引用，正文和 usage 完整消费、每次恰好一次上游调用。

首次全量检查发现纯映射样例被误加最终交付合同；恢复其只验证所选方向构造合同的职责，不改样例或放宽最终交付检查。失败日志保留。Responses 新声明曾触发 token 上限，删除 429 处等价的显式 `from: input`，使用既有默认输入作用域；没有调整 100,000 token／64 depth 限额。四包正式样例和最终全量均通过。

| 检查 | 结果 |
|---|---|
| closure-response-delivery-ranges-all-final | protocol 全量 6.272 秒、builtin 全量 35.411 秒，通过 |
| closure-response-delivery-ranges-gateway | 32 场景矩阵、JSON 引用和 storage／续传交付，15.197 秒，通过 |
| closure-response-delivery-ranges-fuzz | 4 workers，98,399 次区间 oracle 执行，通过 |
| closure-response-delivery-ranges-race | protocol 3.102 秒、builtin 8.967 秒、server 100.188 秒，通过，无竞争报告 |
| closure-response-delivery-ranges-vet | protocol 全树及 server 通过 |
| closure-response-delivery-ranges-staticcheck | 固定 v0.8.2：保留 24 主诊断＋1 关联位置，新增 0、消失 0；退出 1 |
| closure-response-delivery-ranges-types／lint | 前端类型及编辑器 ESLint 通过 |

这只是批次 1 的连续可构建切片。仍需 `ConsumeTransformed`、SSE 区间预算与交付提交、可信语义事件修改事实、静态 wire 影响路径、Native／交付失败组合、早期错误配置化和全入口所有权。没有正式性能结论，未形成 B，未切换默认或删除旧链。

## 最终闭合交付切片：失败后关闭输出（dev.129）

为后续 SSE 引用提交先闭合已确认的交付缺口：原实现 Write／Flush 失败后未提交位置，但仍保留可提交的暂存状态。现在 wire validator 与认证续传 binding 均有宿主 `AbortDelivery`；失败关闭后拒绝消费、恢复及提交，原成功批次的位置和持久化事实保留。网关记录首次类型化传输失败，后续 emit 不再次写入或 Flush；终止标记和最后一次 Flush 也使用相同边界。没有改变发送粒度，没有引入局部 recover 或失败后重编码。

沿用已有交付测试增加第二批写失败、失败后重复 emit／Commit／Consume 拒绝，并保留 Flush 后取消仍提交的直接断言。核心另验证前一批成功位置不会被后一批失败覆盖；续传复用已有认证夹具，验证第二批失败不覆盖第一批交付或落库事实。首次检查发现既有无续传调用允许 nil binding，补回该既有接口语义；第二批测试最初错误使用了不同 responseId，按真实关联修正夹具后通过，未放宽外部合同。

`closure-delivery-abort-final` 通过组件、Native／重建、断流、storage 和 32 场景网关矩阵：protocol 0.378 秒、builtin 0.799 秒、server 13.616 秒。`closure-delivery-abort-race-final` 通过状态、续传及网关失败检查：protocol 4.092 秒、server 5.728 秒。相关 vet 通过；固定 staticcheck v0.8.2 保留 24 主诊断＋1 关联位置，新增 0、消失 0。此前 dev.128 的全量与前端结果继续复用；前端未变，不重复执行。

## 最终闭合批次 1B：SSE 引用证明与交付事务（dev.130）

`encode_event.deliveryRanges` 已接入 Chat／Responses 的可见文本及引用，沿用 JSON 的只读选择与
UTF-16 区间算法。Spec 继续独立检查事件、身份和生命周期，Mapping 决定字段、通道和引用身份；
Go 不判断供应商字段。Responses 的完整响应、message 快照和终态共享命名程序，省略等价 read
默认项以保留配置空间；100,000 token／64 depth 限额不变。

`WireStreamValidation.ConsumeTransformed` 统一最终 Spec、引用和续传载体检查。新包网关删除了
wire 规则之后的完整 DecodeFrame 及对应映射状态；组合验证和完整 wire 事件预览接入同一边界。
纯映射预览保持所选方向语义；完整 wire 事件预览要求 sequence，逐帧模拟成功交付并检查终态。

文本观察保存摘要和位置，未改写时共用摘要及隐式区间。发生改写后仅比较当前增量的相同前后
字符片段；晚到引用必须落在可证明区间且索引已正确调整。snapshot 用摘要检查旧前缀，只处理
新增后缀。事务只复制可变尾区间，不复制历史区间；1,024 帧无改写追加不积累显式区间。
引用重复观察不增加状态身份。身份和区间按实际条目计量，当前批暂存载荷受字节预算限制。

成功 Flush 后提交摘要与区间，失败关闭并清理暂存载荷，结束时 Release 释放观察。续传扩展
使用同一个只读程序、sealed batch 和已提交位置生成 before／after 配对；无修改执行一次，
实际修改不得改变载体边界或帧数，不假设原帧总在批次末尾。定向传输检查使用原帧在首位的
两帧载体批次，覆盖第二帧 Write 失败、Flush 失败与 Flush 后取消；不重发部分写入的批次。

必要回归覆盖等长替换、emoji、未改写前后区间、晚到／重复引用、旧索引拒绝、无引用改写、
snapshot 前缀改写拒绝、资源预算、暂存与已提交事实、自定义配置、组合验证及 wire 预览。
真实本地 Responses→Chat SSE 完整消费验证正文、引用、usage、终态及一次上游调用；改写后
晚到引用拒绝，不能发送失效引用或成功终止标记。

本批仍不包含语义事件规则相对于原 frame 的可信修改事实，也未实现静态 wire 影响路径分析。
不能因此宣称所有正文改写安全闭合。完整 Native 接续组合、全入口与所有权、早期错误配置化、
B／C、正式性能、生产默认切换和旧链删除继续列为未完成。

检查记录保存在忽略目录 `.tmp-dev/protocol-three-part-2026-10-31/checks/`：

| 检查 | 结果 |
|---|---|
| closure-stream-ranges-final | protocol 全量 4.837 秒、builtin 全量 36.310 秒通过，包含四包编译及正式样例 |
| closure-stream-ranges-gateway-final | 续传配对、真实 JSON／SSE 引用、32 场景矩阵、交付位置通过；builtin 0.523 秒、server 14.256 秒 |
| closure-stream-ranges-carrier-batch | 同批两帧交付、第二帧 Write 失败、Flush 失败及 Flush 后取消通过；server 0.364 秒 |
| closure-stream-ranges-fuzz | 区间事务与完整区间 oracle 对照，54,030 次输入通过 |
| closure-stream-ranges-race | 核心状态、载体配对、真实引用调用及 32 场景矩阵通过；protocol 3.843 秒、builtin 19.012 秒、server 238.566 秒 |
| closure-stream-ranges-vet | 受影响 protocol 全树及 server 通过 |
| closure-stream-ranges-staticcheck | 固定 v0.8.2：24 条主诊断＋1 条关联位置；与 dev.129 去行列后的多重集一致，新增 0、消失 0；退出 1 |
| closure-stream-ranges-types／editor-lint | 前端类型检查、协议编辑器 lint 通过 |

race 之后新增的编译期“必须有最终 stream 合同”约束由最终全量和 vet 覆盖，执行状态代码未再改变。
初次检查暴露的 Responses reasoning part 误读身份已通过配置过滤修复；测试中的模型修改按整流身份
约束同时修改起始帧，未放宽合同。中间失败日志一并保留。这里没有正式 A／B／C 性能结论，
也未重复运行尚未到最终候选阶段的六平台和真实客户端验收。
