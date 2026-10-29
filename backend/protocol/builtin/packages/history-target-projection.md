# 历史保留意图的目标侧声明接入

本切片只把既有 `request_field` 处置接入三个未启用的真实草案。来源仍是
`protocol/testdata/history/anthropic.json` 的有限值原型；完整 Anthropic 草案尚未将
context_management 提取为核心 HistoryRetention，不能据此宣称四协议全链路已迁移。

## 声明与执行边界

| 目标草案 | `/reasoning/historyRetention = "all"` 的声明 |
|---|---|
| Gemini | `default:"all"`，目标发送已提供的历史且不执行清理 |
| Chat | `reject`，尚未声明相等的历史保留保证 |
| Responses | `reject`，尚未声明相等的历史保留保证 |

声明使用现有 `scope:"request_field"`，没有新增 Go 供应商分支。引擎从目标声明绑定
request-projection 规则，保留目标身份、映射哈希、coreVersion 及策略诊断。Gemini 的 default
在兼容与严格模式都属于 preserved normalization；它只消费历史保留意图，不能删除其他
ReasoningIntent 字段。Chat/Responses 的拒绝同样不依赖兼容或严格模式。

处置只在已选择的 request-projection 规则中执行。禁用规则后，意图留在请求中，编码器必须拒绝，
不能自行再次应用默认值。未映射的 wire 参数没有因此获得删除授权。

## 垂直验证

`TestPackageDraftHistoryTargets` 直接编译真实的三个目标草案，未安装 codec 模块。
一个来源请求分别执行三个目标、兼容/严格模式共六个子测试，检查：

- Gemini 完整渲染 contents、generationConfig，与预期正文一致。
- Chat/Responses 在核心 HistoryRetention 路径拒绝，保留规则及策略身份。
- 禁用 request-projection 后保留意图，目标编码不能重复处置。
- Gemini 处置后保留独立的 mode、effort、effortScope，并渲染 thinkingLevel。
- 来源请求的完整核心与 Native 数据不被修改。

这组测试不替代来源 whole-value 识别、所有字段 owner 覆盖和完整协议验收。生产旧规则的诊断
位于 `/wire:claude/context_management`，本声明使用 `/reasoning/historyRetention`；此处没有把
两套诊断称为字节等价，也没有更改已有差分账本。

## 已修复的基线集成回退

首次连同三个草案的既有迁移测试运行时，新增目标测试未报失败，但原有序列化根 Native 编码
样例新增拒绝：`unsupported_native /native: history dispositions require a matching private baseline
for native replay`。

原因是启用 history disposition 后，前置检查要求同协议 Native 携带匹配的私有解码基线；
既有 fixture 的 Native 来自公开序列化值，没有这份运行期基线。原有 native reconciliation
在私有基线不可复用时会受控重新解码，但尚未进入这条路径就被前置检查拒绝。

`0f0c322f` 提取原有受控基线取得流程，供 history 预检与原生协调共享一次调用期结果。
没有私有 workspace 时，仍先检查 scope 和原生正文预算，再解码原始请求；后续协调复用结果。
授权回调不重复执行，调用者 Native 不被写入新状态。原始基线确有历史控制时仍拒绝未经证明的
恢复；缓存独立的精确 workspace 规则没有放宽。

旧差分样例与六项目标矩阵已通过 protocol/builtin 全量；既有期望和差异账本未改。
原失败证据保留在 `.tmp-dev/protocol-dsl-2026-10-29/history-target-native-baseline-failure.log`。
这项基础设施修复没有把供应商策略移入 Go：Gemini 默认满足以及 Chat/Responses 拒绝仍由
三个目标 JSON 声明决定。

此外，三个目标草案尚未完整拒绝未映射的其他族 `wire:*` 字段。一个没有被来源原型识别的
context_management 不能借此 default 获得删除授权。完整源草案的 owner 提取与未映射保护
仍是后续独立工作，本切片不作为生产激活证据。
