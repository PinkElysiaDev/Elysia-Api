# 自定义协议定义参考（v2）

[English](protocol-definition-reference.en.md) · [使用指南](protocol-guide.md) · [实现参考](protocol-definition-v2.md)

当前格式是 `schemaVersion: 2`，语义文档为版本 1；作者 `version`、内容哈希和编译器版本独立。旧 request/response/shape 模板只可导入迁移，不再执行。实时事实来源为 `GET /api/admin/protocols/schema` 或 `elysia protocol schema`，对应 [Definition](../backend/protocol/definition.go)、[语义模型](../backend/protocol/model.go)、[编译器](../backend/protocol/compiler.go)。

## 运行时值 Schema 校验

编译器将受限的 `ValueSchema` 转换为 draft 2020-12 Schema，使用 **santhosh-tekuri/jsonschema/v6 v6.0.3** 预编译。映射、操作输入及模型页复用协议修订持有的校验器。`nullable` 仍允许 null 绕过 enum，精确数字及重复键后值优先的语义保持；应用层通过 Sonic `UseNumber` 解码。

**迁移兼容性：** 编译器版本 `2.0.0-dev.73` 要求重新生成验证证据。进入 Schema 校验的值（包括未知子字段与 enum）统一限制数字转换规模：数字原文最多 4096 字节，指数范围 -4096…4096；解码前检查完整值的字节、节点及深度预算。诊断采用库的错误树及转义后的实例路径，消息措辞与错误顺序可能变化。范围、基准与验证结果见[迁移报告](backend-runtime-schema-migration.md)。

**请求预算修正：** 编译器版本 `2.0.0-dev.74` 使 operation 输入校验使用固定到请求上下文的请求体预算（默认 32 MiB），与请求解码、编码保持一致。协议显式配置的更小上限仍然生效，响应与事件预算保持独立。已有验证证据需要按修正后的校验行为重新生成。

## 生成的 JSON Schema

实时 catalog 使用 **`github.com/invopop/jsonschema` v0.14.0** 将 Go 模型反射为 JSON Schema draft 2020-12，覆盖协议定义、绑定、转换策略及语义模型。结构体拒绝未知属性，JSON 的 `omitempty` 和 `omitzero` 标签决定字段是否必需。协议 `Value` 类型显式映射为不限制类型的 JSON schema，保留任意 JSON 值，包括 null、false、零与大整数。缓存意图字段说明使用依赖的 `jsonschema_description` 标签。

### Schema 表示迁移

直接检查 schema JSON 的客户端需要支持以下标准、校验等价的表示：

- 不限制类型的 schema 可以是布尔值 `true`，不一定是 `{}`。
- `Object`、`CapabilitySet` 等命名 map 使用本地 `$defs` / `$ref`，不再始终内联。
- 空 `required` 数组省略；包括 `omitzero` 在内的可选字段仍为可选。
- 绑定和转换策略根节点也声明 `$schema`；关闭自动生成的包路径 `$id`，保持本地引用解析。

catalog 包装及语义根节点 `request`、`response`、`event`、`task`、`models`、`agent` 继续可用。`semanticSchema` 是共用 `$defs` 的根节点集合；验证某个根时，应组合其 `$ref`、共用 `$defs` 和 dialect。CLI 的 `--section` / `--type` 查询继续返回命名定义，也能查询新增的命名 map。

归一化上述等价表示后，全部现有 catalog 约束与迁移前一致。每次查询返回独立对象；应用层序列化使用 Sonic，依赖自行管理其自定义 schema 序列化。运行时协议检查、资源预算及动作专用约束继续由编译器执行。

## 完整样例

从零定义的 [text-alpha](../backend/protocol/testdata/text-alpha.json) 与 [text-beta](../backend/protocol/testdata/text-beta.json) 可直接录入编辑器。工具及完整多轮示例见 [编译器回归](../backend/protocol/compiler_test.go)；WebSocket、异步任务使用同目录的 session 和 job 样例。不要将不完整片段当作可启用协议。

## 定义组成

| 字段 | 约束 |
| --- | --- |
| `id` / `name` / `version` | 身份、显示名和作者版本；ID 不等于语义来源身份 |
| `family` / `wireVersion` | 原生内容兼容身份；仍受方向和资源作用域检查 |
| `requires` | 所需已安装引擎功能；不能靠声明安装新机制 |
| `capabilities` | 可验证能力；方向可进一步收窄 |
| `directions` | 独立解码/编码映射，不能机械反转 |
| `operations` | 相对路径、方法、传输、鉴权位置、输入约束和会话/任务/目录配置 |
| `expressions` | 编译时复用的声明式表达式；拒绝循环引用 |
| `native` | 原生保留策略及需要时的数组稳定身份 |
| `limits` | 只能在引擎上限内收窄深度、节点、状态及缓冲限制 |
| `samples` | 映射样例与预期结果、失败诊断、事件序列及能力证据 |
| `sessionSamples` / `taskSamples` / `modelSamples` | 对应工作流证据 |
| `agent` | Agent 参数映射、工具结果格式、思考档位及样例 |
| `extensions` | 明确允许的惰性元数据；其它未知配置键报错 |

方向：`decode_request`、`encode_request`、`decode_response`、`encode_response`、`decode_event`、`encode_event`、`decode_client_event`、`encode_upstream_event`。定义可以只实现部分方向，但使用场景必须匹配。

## 映射表达式

一个方向选择 `module`、`transform` 或事件 `rules`。内置模块为 `openai-chat`、`responses`、`anthropic`、`gemini`。`after` 对模块输出执行明确的后映射；其 `input` 为模块结果、`root` 为原输入。`initial` 在首个非空事件前输出一次声明的事件前缀。`input`/`output` schema 校验类型，操作级 `input` 另检查最终 HTTP 正文。

字段读取使用 JSON Pointer：

```json
{"op":"object","fields":{"deployment":{"op":"read","path":"/model"},"history":{"op":"read","path":"/content"}}}
```

这只是编码方向片段。表达式目录包含 `read`、`literal`、`omit`、`object`、`array`、`map`、`flatmap`、`filter`、`choose`、`if`、`enum`、`cast`、`merge`、`concat`、`join`、`sort`、`associate`、`exists`、`equal`、`all`、`any`、`not`、`parse_json`、`stringify_json`、`strip_prefix`、`ref`。取值来源为 `input`、`item`、`root`、`context`。准确参数从 schema 读取。

缺失、null、false、0、空字符串/数组/对象均不同。`exists` 包含明确零值和 null；`join(source)` 只接受字符串数组；`cast` 不截断数值。未知分支报错，非法 function JSON 不用 `{}` 代替。循环只能遍历输入，禁止任意脚本和递归。

## 缓存、错误和事件

缓存断点为 `{"kind":"breakpoint","location":"block","value":{"type":"ephemeral"},"ttl":"1h"}`；`value` 不再含 ttl。请求、工具、内容块分别映射；不自动透传未知字段或增加缓存策略。资源引用保存供应商/账号/模型/会话范围。

usage 的 `input/output/total/cacheRead/cacheCreation` 是带 `count` 和 `origin` 的可缺失计数器。`origin` 为 `observed` 或 `inferred`。例如显式零：`{"count":0,"origin":"observed"}`。自定义别名通过 `exists` 和条件映射确定优先级。

内置错误语义使用 `message/category/code/param/details`，不能表达的专有细节明确报错。失败响应映射可读取 `context.httpStatus`。流结束后允许 usage 尾帧；未知事件默认拒绝，需要忽略的帧必须明确映射为空数组。HTTP stream 的 `framing.idleMillis` 缺省或为 0 时使用 600000（10 分钟），显式范围 1—3600000（最多 1 小时）。它限制等待响应头及每次读取上游数据的空闲时间，不限制总流时长，也不受非流式 `httpTimeout` 控制。已有显式值保持不变；自定义协议可通过此字段调整等待时间，修改后需验证并激活修订。

## 管理 API

以下路径都在 `/api/admin/protocols` 下，使用既有管理鉴权：

| 方法与路径 | 用途 |
| --- | --- |
| `GET /schema` / `/enabled` | 当前目录、已启用修订 |
| `POST /validate` / `/preview` / `/test` | 编译、共享预览、独立真实验证 |
| `GET` / `PUT /:id/draft` | 草稿；原始 JSON 保留精度 |
| `POST /:id/verify` | 保存修订及离线证据 |
| `GET /:id/revisions` / `/:id/revisions/:hash` | 不可变修订 |
| `POST /:id/revisions/:hash/verify` | 当前引擎重新验证 |
| `POST /:id/activate` / `/:id/rollback` | `{ "revisionHash":"...", "expectedActive":"..." }`，须匹配有效报告 |
| `GET /:id/diff` | 比较两个修订 |
| `POST /combinations`，`GET` / `PUT /bindings` | 组合验证与源/模型组能力绑定 |
| `POST /reload` | 编译成功后原子重载 |
| `GET /migration`，`POST /migration/preview` / `/migration/apply` | 升级预演与事务切换 |

预览支持 `mapping/session/task/models/agent`。接口正文以 [处理器](../backend/server/protocol_revision_admin.go) 与 [PreviewInput](../backend/protocol/authoring.go) 为准。样例覆盖不充分、哈希或编译器过期均阻止启用；上传 `passed:true` 不能绕过验证。
