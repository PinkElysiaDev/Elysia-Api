# 输出约束：大自在天核心合同与协议配置

生成请求使用 `parameters.outputConstraint` 表达输出约束。DSL 负责通用结构遍历、预算和诊断；Spec 描述各自外部字段；每份 Mapping 只实现自身 wire 与大自在天 core2 的互转。没有直接读取其他协议私有字段的路径。

```json
{
  "version": 1,
  "kind": "json-schema",
  "enforcement": "required",
  "dialect": "json-schema",
  "name": "answer",
  "schema": {
    "type": "object",
    "properties": {"answer": {"type": "string"}},
    "required": ["answer"],
    "additionalProperties": false
  }
}
```

`version` 必须是整数 1。`kind` 为 `text`、`json`、`json-object` 或 `json-schema`。`json` 表达任意 JSON 值的格式意图，`json-object` 保留对象形式的意图，不能无声互换。`enforcement` 明确区分 `required` 与 `preferred`；后者只用于 Schema 提示意图，不能在目标中自动升级为必需约束。Schema 格式必须声明 `dialect:"json-schema"` 并携带对象形式的 `schema`。`name` 与 `description` 是可选的模型可见提示，只用于 Schema 格式。

这是一份有限合同，不声称实现整个 JSON Schema 标准。首批精确子集包括：

- 对象必须明确给出 `properties`、`required` 和 `additionalProperties:false`，且 `required` 恰好包含全部属性名，没有重复。字段顺序保持原始 Schema，不重写 required 顺序。
- 数组必须有单一对象形式的 `items` Schema；递归只进入 `properties` 的值与 `items`。
- 基础类型为 `string`、`number`、`integer`、`boolean`、`null`；`type` 是一个字符串。
- Schema 内的 `title` 与 `description` 可选，必须是字符串，并原样保留。
- 显式 `number`／`integer` 类型可带非空数值 `enum`，单元素 enum 表达固定值。成员必须是数值；integer 成员按数学整数判断，接受整数形式的小数／指数写法和超出 int64 的数值。不会转换为 float64、截断或改写数值字节。枚举顺序与重复成员保持；`1`、`1.0`、`10e-1` 是同一数值的不同写法，重复项不会产生不同候选值。无效数组定位到 `/enum`，不匹配成员定位到具体 `/enum/索引`，嵌套属性名按 JSON Pointer 转义。
- Chat、Responses、Gemini 的显式 `string` 类型可带非空字符串 `enum`，每个成员必须是字符串。单元素枚举、空字符串成员、Unicode、大小写、空白、成员顺序和重复项均保持原样；不会折叠大小写、归一化 Unicode、裁剪或去重。缺少 enum 表示未声明枚举约束，显式 null、空数组或非数组定位到 `/enum`，混合类型定位到首个不匹配成员的 `/enum/索引`。未知相邻 Schema 关键字仍先于枚举成员校验拒绝，不会因支持 enum 而被忽略。

enum 使用各自 Mapping 的共享命名表达式检查，通用 `is_integral` 只提供数学整数谓词，原 `is_integer` 的 int64 解码合同不变。字符串枚举支持在 Chat、Responses、Gemini 三份 Mapping 中扩展，Anthropic Mapping 增加自身目标限制的声明拒绝，无需新增 DSL 算子、Spec 字段或核心参数。没有增加通用数值幅度上限或提高资源限额；现有 Schema 校验的数值资源边界继续生效。供应商自身 Schema／枚举数量限制仍然存在，原样传递合法 JSON 数值不代表每个渠道都会接受任意幅度。OpenAI 官方另有整个 Schema 总计1000个枚举值上限。

const 仍不在本次扩展内，原生 const 继续保持来源所有权。后续可由核心保留 const，再由目标 Mapping 原样表达或等价表达为单元素 enum；只有先归一成核心 enum、又要求恢复原 const 关键字时，才需要额外来源布局。本次没有验收该后续方案。

Anthropic 的字符串 enum 保持独立边界：已固定的官方合同明确警告字符串 enum／const 的大小写可能改变，因此来源原生配置继续保持 Anthropic 所有权，清除 Native 后仍可由自身 Mapping 重建；不自动提升为中立的精确值承诺。目标 Anthropic 对核心中的字符串 enum 在对应 `/schema/.../enum` 路径拒绝。这个目标限制不会阻止 Chat、Responses、Gemini 各自与核心表达精确字符串枚举，也不会改变 Anthropic 数值 enum 的支持。不能通过大小写折叠或只看一次正确回答来声称精确保真。

Anthropic Mapping 的默认请求规则 `output-constraint-schema` 使用同一 Schema 校验器，明确拒绝目标不能精确表达的中立 Schema，保留原有嵌套错误路径；不修改约束值。规则随现有默认转换配置自动启用，顺序为146，先于名称147／描述148规则；不支持的 Schema 在 compatible 与 strict 模式都先按其准确位置拒绝。组合验证将当前已启用规则的拒绝记录为负面证据：`Rejected:true`、`Passed:false`，不提供正面能力覆盖；普通 Schema 和数值 enum 仍需自己的成功样例。这样字符串 enum 的不支持不会使合法普通／数值约束路线失效，调用者无需另选联合策略。禁用规则后，直接编码仍拒绝不可表达的 Schema，组合报告也不会把未经声明的失败当成成功；资源、结构和求值错误始终是验证失败。修改规则或绑定后必须重新验证匹配当前 policyHash／bindingHash 的证据。

可缺省属性、nullable／type 数组、布尔或 null 等其他类型的 enum、const、数值上下界、字符串和数组长度、format、oneOf／anyOf／allOf、引用、定义及其他关键字不在首批子集。它们不会被删掉或替换：来源原生合法格式保持完整的来源所有权，只能由自身 Mapping 重建；手工编辑后的中立 Schema 若带入这些约束，目标在实际嵌套路径明确拒绝。例如属性 `a~/b` 的 `minimum` 会定位到 `/parameters/outputConstraint/schema/properties/a~0~1b/minimum`。描述字符串和属性名不会被当成 Schema 控制关键字遍历。

| 外部配置 | 中立表示及边界 |
| --- | --- |
| Chat `response_format` | `json_schema` 的 name／schema／description／显式 strict 进入核心；同源从核心重建。 |
| Responses `text.format` | 处理格式自身；text 的 verbosity 和未知相邻成员继续由 Responses 所有，不整体丢弃 text。响应正文的 text 元数据不受此变更影响。 |
| Anthropic `output_config.format` | 支持的子集进入 required 的 Schema 约束；字符串 enum 保持来源所有权。effort 仍是独立的 reasoning 意图，其余成员保留来源所有权。 |
| Gemini `generationConfig.responseJsonSchema` | 配合 application/json MIME 进入 required 的 Schema 约束。未知 generationConfig 成员保持自身所有权。 |
| Gemini `generationConfig.responseSchema` | OpenAPI 形式在首版保持来源所有权；不改名成 JSON Schema，不假定大写 type、nullable、oneOf 等具有相同语义。 |

缺失输出约束表示没有声明该意图；核心中的 null、空对象、未知版本或未知成员会明确报错。OpenAI 原生 strict 缺失与显式 null 各自保留在来源对象中，首版不把未知的隐式默认变成 false 或 true。显式 true 对应 required，显式 false 对应 preferred。原生 schema／description 的允许缺失和 null 由各自 Spec 决定；未满足中立合同的合法配置保留来源对象，不产生第二份语义所有者。

OpenAI 目标要求已有的显式 name；Anthropic／Gemini 来源没有该信息时，目标拒绝 `/parameters/outputConstraint/name`，不会创造名字。Anthropic 与 Gemini 无法承载顶层 name／description；默认兼容模式通过两个可独立禁用的 Mapping 规则 `output-constraint-name` 与 `output-constraint-description` 省略它们，每次产生有损诊断。strict 模式或禁用相应规则时拒绝。Schema 内的描述不会由这些规则删除。删除提示后的核心对象、schema 与原始来源请求具有独立所有权。

Chat／Responses 的名称合同由各自 Spec 声明：只允许 ASCII 大小写字母、数字、下划线和短横线，最多 64 个字符。来源非法名称定位到原生 name 字段；手工编辑的中立名称由目标 Mapping 在 `/parameters/outputConstraint/name` 拒绝。两处使用通用 `matches_charset` 与长度比较，供应商的字符集合和上限不进入 Go 核心。名称不会被清洗、截断或替换。已固定的字段文档未声明最小长度，因此本实现保留显式空字符串与缺失的区别，不自行补充非空限制；这不代表对任意模型或渠道额外限制的验证。缺失与 null、strict 的缺失／null／false／true、来源所有权及 count_tokens 的独立拒绝规则保持原合同。

同源重建以可编辑核心为准。已进入核心的格式不再留在 wire 余项中；同时提供核心格式和来源格式会作为双重所有权拒绝，即使成员值相同。对不在首批范围的原生格式，去掉 Native 仍可从自身 wire 所有者重建。旧 `response_format`、`gemini_response_mime`、`gemini_response_schema` 核心参数不再被目标消费。

`output.constraints` 是独立的请求能力。四份 Mapping 通过各自的 decode 与 encode 样例证明它；模型绑定必须显式允许该能力，既有文本权限不会自动扩展。组合验证仍保存完整失败报告，同时独立产生普通文本的较窄报告；缺少 name 的结构化输出失败不会使无格式要求的文本请求失去路线。不同报告的能力不能拼接。修改 Schema、Mapping、模型合同或转换规则后，应重新保存绑定生成证据。

`count_tokens` 的程序与证据保持独立。生成能力和生成样例不会自动授权计数请求中的输出约束。用户自己显式补充合法 name 后还必须重新验证目标组合；一份符合 Schema 的模型回答不能替代出站合同与约束保真证明。

发布验收覆盖真实网关 JSON／SSE、共享编译结果的竞态、配置刷新，以及标准资源限额下的完整包和自定义包。每项结论以实际候选修订和保存的验证记录为准；组件通过不替代完整回归或正式性能验收，当前状态见[三分切换记录](protocol-three-part-cutover-2026-11-14.md)。
