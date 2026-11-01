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
