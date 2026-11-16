# 默认协议源

每个子目录包含两份独立编写、共同发布的配置：

| 协议 | 外部协议定义 | 默认转换行为 |
|---|---|---|
| Chat Completions | [spec.json](openai-chat/spec.json) | [mapping.json](openai-chat/mapping.json) |
| Responses | [spec.json](openai-responses/spec.json) | [mapping.json](openai-responses/mapping.json) |
| Anthropic Messages | [spec.json](anthropic/spec.json) | [mapping.json](anthropic/mapping.json) |
| Gemini GenerateContent | [spec.json](google-generate-content/spec.json) | [mapping.json](google-generate-content/mapping.json) |

Spec 描述外部文档、端点和生命周期。Mapping 描述与大自在天的双向映射、模型策略和目标取舍。DSL 提供供应商无关的执行原语。

新预置采用供应商 origin 基地址及包含版本的完整路径，例如 `https://api.openai.com` 加 `/v1/responses`。已有包含 `/v1` 的基地址不自动迁移，具体约定见[端点与基地址](../../../../docs/protocol-three-part-contract.md#预置端点与基地址)。

`Compiler.CompileSources` 将两份源编译为 schemaVersion 4 的完整包；DSLVersion 为 2，核心版本为 2。整包编译、修订、验证及激活不可分割，请求复用已编译快照。Spec 和 Mapping 的命名表达式各自独立。

运行时预置已使用本目录的 Spec/Mapping 包，旧 Module/Codec 执行入口已经退役。**第一阶段切换验收尚未通过。** 文件存在或组件测试通过不代表全部调用、中心拓扑、资源及性能门槛已经通过。当前进度见[切换记录](../../../../docs/protocol-three-part-cutover-2026-11-14.md)，验收依据见[三部分契约](../../../../docs/protocol-three-part-contract.md)。

旧版本的差分记录用于历史调查，不再要求复制旧错误文字、多故障首错树或重复资源计费。有效功能样例继续使用；reasoning `content:null`、辅助 metadata 等确认过的客户端兼容需要具名声明与实际调用验证。不得静默丢失正文、工具关联、usage 或终态。

请求中的 item `id` 和非工具结果的 `status` 在 Chat、Anthropic、Gemini 中由 `request-shape`
规则明确降级；strict 拒绝，禁用规则后编码守卫仍拒绝未处理字段。工具结果的 `status.isError`
独立属于 `tool-results`，不能由 item 状态取舍删除。Gemini Mapping 将该状态与载荷分别编码，
不会把业务对象的 `error`、`output` 键误当状态，也不会把 JSON 外观的文本自动解析为对象。

空工具结果块数组由支持该布局的目标保留；转换成文本或错误标记时按 `tool-results` 记录布局降级。
Anthropic 的未知原生块使用受来源及作用域约束的 `replay_native` 重建，缓存标记以当前核心值为准。
缓存 TTL 的逆序、缺省 5m 与未知值检查由本协议 Mapping 声明，通用 `warn` effect 记录提醒，
不改写标记、不阻断请求。Chat 的多选择请求在来源 Mapping 拒绝，不能发出无法兑现的选择生命周期。

Gemini 响应中的消息与工具调用若均已完成，`response-shape` 可用外层 completed 响应表示其
completed 状态，并记录等价规范化；其他节点状态不据此推断。未处理的工具调用 item ID/status
仍由目标编码拒绝，工具关联的 CallID 和参数保持。禁用该规则不能绕过这项边界。

助手历史分组由目标 Mapping 的 `assistant-history` 声明。Anthropic 在 `request-shape` 和签名恢复之后分组，先处理无法表达的 item ID/status，再合并同一轮的思考、文本和工具调用。Gemini 仅在存在独立的 reasoning/text/tool_call 根节点时分组，普通消息历史保持原有边界。缓存标记、未知扩展或未处理的身份字段仍受分组守卫保护；strict 拒绝有损取舍，compatible 记录对应诊断。

失败响应只按本协议正文成员识别错误信封：Chat 检查 `choices`，Responses 检查 `output`，Anthropic 检查 `content`，Gemini 检查 `candidates`。其他协议同名字段仍是本协议的未知扩展，不参与分类。OpenAI 的 `server_error` 别名由本协议 Mapping 解释；Gemini 仅在已知类别能够确定标准数值时移除冗余错误码，未知类别的数值仍保留。

Responses Spec 将成功响应的元数据约束与纯错误信封分开。共享约束的 `when` 在调用者作用域判断，`path` 只在条件成立后选择；流帧缺少 `response` 时跳过对应约束，存在时继续校验原有计数、类型和明细边界。

Chat 的 `source-opening-identity` 通过 `sourcePrelude` 声明精确的相邻空开场帧规范化。通用 DSL
最多暂存一帧，绑定来源身份及 decoder 哈希，在来源生命周期提交前校验并替换首帧已存在的 ID／时间。
strict 拒绝实际改写，禁用规则后原身份校验继续拒绝；EOF 释放原帧，取消丢弃暂存帧。原始供应商日志保持。

Chat 完整响应的 `logprobs:null` 和空对象保留在对应选择项的自有扩展中。非空 content／refusal
概率只有在所属文本节点可唯一确定时才进入节点元数据；没有 Native 时仍能重建，不能移到响应根。

Responses 的目标 Mapping 在核心 `response.finished` 到达时完成仍开放的内容项，输出 text／part／item
结束帧；最终 `response.completed`／`response.incomplete` 在 `finalize` 中生成，以纳入后续 usage
尾帧。已完成的内容项不重复输出。概率数组在协议要求的各个结束快照中完整保留，每次输出仍受原有
整批字节、节点和深度限制；不能为了减少批次大小省略概率或放宽资源限制。

Responses 的失败帧由 Spec 独立约束，不将其中的 output／usage 作为成功生成结果校验。
来源流以声明式 `hasFailed` 状态拒绝 `error`／`response.failed` 之后的 progress 帧；成功终态之后
的 usage 尾帧仍遵循已有规则。已拒绝的来源流不能通过 EOF 恢复成成功状态。

Chat、Responses、Gemini 的 `prompt_cache_options:{}`／`null` 由来源 Mapping 保存在本协议的
自有余项中，去掉根 Native 后仍能重建。已识别的 mode／ttl 进入核心缓存意图，未知成员仍归原
协议所有。Gemini 的 `generationConfig` 自有余项与核心生成参数按声明深合并，叶子冲突（包括相等
值和 null）拒绝；`thinkingConfig` 由本协议的思考映射单独处理，不重复合并。

Anthropic 的 `thinking:{}` 与 `output_config:{}` 分别保留，即使另一个字段提供了有效推理控制，
也不能据此删掉空对象。已消费的已知控制不会保存在余项中恢复旧值。

Anthropic、Gemini 的目标编码不能直接表达核心图片 `payload.detail`，仍由 Mapping 的
`image-detail-target` 节点守卫拒绝其存在，包括 null。生成及流式生成默认启用
`image-default-preprocessing`：只对真实图片节点的显式 `auto` 采用目标默认预处理，在 compatible
模式记录具名降级后投影掉 detail；不保证来源特有的缩放或 token 预算。strict 在改写前拒绝，
单独禁用规则后由目标编码守卫拒绝。high／low／original、null 和未知值继续拒绝，detail 缺失
不产生诊断；同源去 Native 重建仍保留显式 auto。count_tokens 使用独立默认规则，仍拒绝显式 detail。

`has-automatic-image-detail` 复用 `has-image-detail` 的节点种类判断。通用 DSL 只遍历实际核心
节点及内容子节点，不检查工具参数或结果中的不透明 JSON；只投影 detail，保留 MIME、数据、
URL／文件和所有其他载荷成员，不补 MIME、不获取图片、不改图片字节。工具结果子图片可被该规则
选中，但 Gemini 原有 `tool-results` 对媒体内容的独立拒绝不变。规则目录、配置预览和网关使用
同一声明，预览的核心转换成功不代表最终目标编码成功。配置行为见[可配置协议转换](../../../../docs/conversion-implementation.md#图片默认预处理)。

Anthropic 的 usage.speed 与完整 JSON usage.iterations 经本协议 Spec／Mapping 进入中立
Usage.executionSpeed／executions。账本记录 included／excluded／unknown 包含关系，不改变父计数；
未知项成员整体保留来源所有权。Chat／Responses／Gemini 通过具名 execution disclosure 规则投影
客户端副本，strict／禁用继续拒绝，源执行记录保持。SSE 只支持 message_start 的 speed 并在后续
计数中保持；显式 iterations 仍拒绝未证明的更新语义。字段、配置和持久化边界见
[执行用量合同](../../../../docs/execution-usage-contract.md)。
