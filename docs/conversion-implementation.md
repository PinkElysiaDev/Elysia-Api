# 可配置跨协议转换

实现保留公共语义模型，在 HTTP 普通／流式网关中使用独立的 ConversionPolicy。配置不修改预置定义或模型工具权限。编译器为 `2.0.0-dev.25`，特征为 `conversion.policies.v1`、`conversion.continuation.v1` 和 `conversion.client_output.v1`。本轮实现与验收见 [四协议修复记录](four-protocol-repair-dev25.md)。

## 默认行为与契约

模型原始绑定、分组／手动权限和组合证据分开处理。上游响应按照真实模型契约检查，转换后的内容再由目标协议检查。组合的较窄能力集只选择请求路径，不覆盖模型绑定。真正超出模型／分组权限的输出仍返回 `upstream_contract_violation`。

默认规则按各方向实际使用的编解码模块选择，预置和旧自定义协议同等生效；模块后的 `after` 映射仍先执行。完全手写的目标编码器不因名称或 family 相似而自动获得字段省略规则。

| 规则 | 行为 |
|---|---|
| `client-stream-options` | 分离 `include_usage` 偏好；跨族未知子字段逐项诊断；非流式选项兼容忽略、严格拒绝 |
| `responses-include` | 保留同线格式的 include；跨协议将 `reasoning.encrypted_content` 作为客户端续传选择，不发送上游；未知选择项拒绝 |
| `responses-storage` | Responses 入口向无响应对象保存语义的已知编码器转换时，false 转为无状态；true／缺省／null 在兼容模式诊断降级，严格模式拒绝 |
| `gemini-tool-results` | 文本工具结果明确包装为 `{"result":原文}`，记录兼容降级；字段名可改；严格模式拒绝此包装 |
| `text-tool-results` | 对象工具结果通过 JSON 文本投影到 Chat／Responses／Anthropic，保留数字精度和调用关联；严格模式拒绝未包装的类型改变 |
| `system-instruction-hoist` | Anthropic／Gemini 目标上提 system/developer，保持文本顺序；中段作用域或 developer 优先级改变时诊断，严格模式拒绝 |
| `history-response-metadata` | 第二轮历史中的已知响应元数据按目标投影；空 annotations 不再阻断工具续轮，非空引用按同一映射处理 |
| `response-metadata` / `event-metadata` | 已知响应附加字段逐项映射、规范化或诊断省略；Chat↔Responses URL 引用及 token 概率保留所属文本；未知扩展不自动放行 |
| `response-shape` / `event-shape` | 转换有序输出项，保留工具调用 ID；消息边界及交错损失有诊断；多个候选不混成一轮 |
| `response-envelope` / `event-envelope` | 构造稳定响应 ID、创建时间及项目状态；仅客户端格式补齐，不改供应商用量 |
| `request-signatures` | 投影无法原生表达的外族签名；已认证恢复到目标协议的资源保留 |
| `response-signatures` / `event-signatures` | 只处理签名，保留其他资源和正文；恢复通道成功与信息丢失分别记录 |
| `response-usage-projection` / `event-usage-projection` | 只省略列举的目标不可表达用量字段；兼容模式诊断，严格模式拒绝；内部计量保持完整 |
| `response-anthropic-usage-envelope` / `event-anthropic-usage-envelope` | Anthropic 必填用量缺失时，兼容模式补客户端专用零占位并诊断；严格模式拒绝；原始计量不变 |

新版 Chat 预置声明 `requires: ["conversion.client_output.v1"]`，`stream_options` 不再属于通用生成参数。true 输出一次 Chat 结束用量块；false、缺省、null、空对象默认不输出该块。`usage.defaultIncludeUsage` 恢复旧显示默认。`usage.collectUpstreamUsage` 独立决定是否向 Chat 上游请求用量；内部计量不受客户端显示偏好影响。没有实际计数时不会伪造观察值。

未声明该特征的旧自定义 Chat 定义保留用户映射所依赖的解码形状、Agent 参数及样例；映射执行后，默认策略自动规范化旧参数，无需重建或手动开启。回归使用提交 `82406b9` 的原始定义，仅更改副本 ID。

Responses 的 include 接受缺失、null 和字符串数组，错误类型返回准确的 `/include` 路径。同线格式保留未知字符串、顺序及空值；跨协议目前只实现 `reasoning.encrypted_content`。它复用 Elysia 认证载体，不能冒充供应商原生加密思考内容。有状态才保存和包装；用户关闭客户端载体时，服务端副本不等于已向客户端返回所选字段。跨协议分离输出偏好记录 `conversion_normalized / info / preserved`，不提前宣称载体已生成或信息损失；后续载体交付失败有独立诊断。`store` 由独立保存规则处理；上下文约束不随之省略。

`responses_context` 是请求阶段的具名规则，按实际目标模块继承。仅将跨协议的 `previous_response_id:null` 规范化为无引用，并记录信息级诊断；同线格式保留原值。关闭规则后编码器仍拒绝未映射的空引用。`truncation` 接受 auto、disabled 或 null，`previous_response_id` 接受非空字符串或 null；入口和用户映射后均校验类型。跨协议非空引用及截断要求（包括显式 null）继续拒绝，需要完整历史恢复或经过验证的等价截断行为，不能自动清空。实际 Responses 预置未声明所需会话能力，非空原生引用也在路由前以 `/previous_response_id` 明确拒绝；本轮未扩大能力声明。`conversation` 和生成约束保持原边界。

诊断按协议身份、阶段、规则、策略、严重程度及保真分类等完整内容去重，流式重复记录折叠，不合并同路径不同阶段的结果。调用日志页面区分信息、警告和错误。dev.24 重新生成当前语义的验证证据，不重写用户定义或草稿。

`responses_storage` 仅默认匹配实际 Responses 解码模块。参数为 `{"targetCodec":"gemini","onUnsupported":"degrade"}`，可将 `onUnsupported` 改为 `reject`；严格模式始终优先。原生 Responses 保存路径保留原值。其他已知目标：显式 false 不要求保存，允许转换；true／null／缺省按保存意图处理，兼容模式记录损失并在响应 JSON、`response.created` 和终止对象中返回 `store:false`。错误类型返回 `invalid_input at /store`，不调用上游。本项不提供 Responses 检索、删除或会话存储 API；签名副本不是响应对象数据库。`store:false` 不承诺供应商零留存，也不关闭调用日志。

Anthropic 客户端输出必须有合法的消息外壳及用量。先取得同一上游帧、同一响应的已知用量，再渲染首帧；不等待下一帧，不默认缓冲整轮。缺失必填计数的零占位只存在于客户端副本（`origin:placeholder`），不进入 `ProtocolUsage`、计费或数据库；真实零值仍是 `observed`。已知缓存拆分照常保留，可选缓存字段不自动补零。尾部真实计数替换占位，快照不累加；终止消息在用量尾帧消费完后生成。缺少身份时使用本次响应稳定 ID 和已选模型。一轮结束、取消或错误均不重新提交上游生成。

合法的同线格式、同映射原生帧原样保留，仍通过最终必填字段检查；错误原生帧不会因“旧模块兼容”绕过校验。不同模块的自定义定义不因 family 相同跳过修复。网关、协议编码预览和组合验证在 `after`、线格式规则及客户端载体处理后检查四协议输出，并用目标模块独立解码器检查事件关联。Anthropic 缺 usage、Chat 缺 created、Responses 缺消息身份或 annotations 等不能记为转换成功。

dev.30 的 Responses 消息使用独立的 `item.started` / `item.finished`；内容事件通过 `parentId` 引用已开始的消息语义 ID。`index` 仍是整轮内唯一的项目关联，编码器分别维护顶层 `output_index` 和消息内 `content_index`。正文立即发送，消息 ID、边界、空消息、phase 和完成状态保留到终止快照。父消息缺失、关联改变、内容尚未结束或终止快照删除已发送消息均拒绝；内容结束后不能通过消息快照改写正文或引用元数据。

跨协议展开消息由现有 `event-shape` 规则处理，记录身份、状态及边界表达损失，严格模式拒绝；关闭规则不由编码器兜底展开。收集器和组合验证保留显式容器关联，不能仅因文本相同而接受消息拆分。手写事件编码器须显式转发 `parentId` 或声明投影；不按 family 猜测。两个精确匹配的旧内置解码样例会经验证生成新修订，原修订和草稿保留。

严格策略的条件拒绝记录在组合报告 `checks[].rejected`，不算成功样例，也不贡献能力覆盖。仍须有独立的正向请求、响应及完整流证据；预置新增显式 `store:false` 和首帧完整用量样例。不存在正向覆盖的旧自定义严格配置继续显示具体阻塞原因，不能借负向样例宣称支持。兼容模式的旧自定义修复无需改写原文。

客户端实验：`@ai-sdk/anthropic` 2.0.33 与 4.0.78 均完整消费经过修复的流，并检查所有 `error` 事件。首帧未知输入填 0 时，2.0.33 的最终展示仍可能保留 0；4.0.78 可用尾帧更新。因此无法承诺所有客户端都显示最终正确输入计数。用户实际客户端仍需单独复测；用户已明确未使用 Cherry Studio。

用量投影范围：Anthropic 目标省略 `output.reasoning_tokens`；非 Gemini 目标省略 `toolUsePromptTokenCount`；非 Anthropic 目标省略两个 TTL 桶；Gemini 目标省略 `CacheCreation` 及依赖它的 `uncached_input_tokens`。投影前验证原始算术关系，输出总量不重复加入思考计数，未知明细仍拒绝。流式的顶层用量和终止响应用量使用同一动作，内部计量只消费原始上游事件。

编码器没有这些字段的隐藏省略分支，组合比较也不再按 family 忽略它们。运行、预览和验证比较同一份投影；预览及离线签名验证仅使用临时认证状态，不写持久副本。报告区分原生保留、可恢复包装、有损兼容和拒绝。

升级自动重验原自定义修订；失败证据保留，相关协议隔离，健康预置继续加载。绑定直接使用最终继承策略验证。正常重启复用当前证据；原文、草稿、手动能力和显式未绑定状态不变。

## 配置和管理

页面为 `/protocols/conversions`。模型源和模型绑定页面提供覆盖入口。优先级是引擎默认、协议对、模型源、模型；同 ID 完整覆盖，禁用规则停止继承。执行顺序必须明确，同阶段的重复顺序拒绝编译。未完成的规则仍可保存为草稿。

阶段为 `ingress`、`request`、`response`、`event`、`wire`。动作注册表另增加 `responses_include` 和 `usage_projection`；schema 返回适用阶段、目标编码器枚举及说明，页面提供对应选择器。其 `value` 为实际目标模块名：`openai-chat`、`responses`、`anthropic` 或 `gemini`。原有 `set`、`remove`、`transform`、`warn`、`reject`、`signatures`、`stream_options`、`tool_result_object`、`buffer_node`、`provider_signature` 继续使用。`transform` 使用受限表达式引擎，不执行 JavaScript。普通规则不能创建或重新关联来源、作用域和签名资源。

`dev.23` 增加 `responses_storage`（请求阶段，以上对象参数）和 `anthropic_usage_envelope`（响应／事件阶段，无参数）。现有规则表单提供保存降级选择，完整 JSON 仍可编辑；不新增页面或存储表。禁用补齐规则后，最终格式检查仍会拒绝不完整输出。

`buffer_node` 等待匹配节点完成，保持原事件顺序，受引擎资源上限约束。严格签名投影自动等待完整节点；组合验证也使用同一等待逻辑。原始上游事件序列独立验证，保存失败不能掩盖生命周期错误。Anthropic `signature_delta` 累积为完整签名，重复语义快照不会重复发送签名片段；Gemini `thoughtSignature` 按完整字段处理，不猜测任意字符串是增量还是快照。已写出流式内容后不会重提生成请求。

所有管理接口沿用管理员鉴权：

| 接口（公共前缀 `/api/admin/protocols`） | 用途 |
|---|---|
| `GET /schema` | 策略 schema、阶段、动作和限制 |
| `GET /conversion-policies` | 草稿及当前激活修订 |
| `PUT /conversion-policies/:id/draft` | 携带 `expectedHash` 保存草稿 |
| `POST /conversion-policies/:id/verify` | 验证指定草稿 hash 并生成修订 |
| `GET /conversion-policies/:id/revisions` | 修订与验证报告 |
| `POST /conversion-policies/:id/activate` | 指定 hash、expectedActive、selector 激活或回滚 |
| `POST /conversion-policies/preview` | 只读阶段预览、规则来源和诊断 |
| `POST /conversion-policies/:id/probe-signature` | 显式向已有模型发送兼容值验证请求 |
| `GET /continuations` | 数量、字节、命中、未命中、过期和淘汰计数 |
| `DELETE /continuations?session=...` | 按会话清理副本 |

原 `/preview` 支持 `mode=conversion`，原 `/combinations` 支持 `conversionPolicy`。绑定中的 `conversion` 可固定 `policyId/revisionHash` 并增加 `overrides`；固定修订不随全局激活漂移。

策略、绑定和派生证据同事务更新；请求通过一次数据库读事务固定策略及绑定。提交时检查配置代数和读取集合，变化返回 409。证据关联双方定义／样例 hash、编译器、完整能力契约及最终策略 hash；条件规则另外绑定模型／操作／传输上下文。

## 签名恢复

载体前缀为 `elysia-continuation.v1.`。使用主密钥派生独立用途密钥，通过 AES-GCM 认证加密原片段。Messages 使用专用 thinking 载体，Responses 使用 opaque reasoning 载体，Chat 使用 `elysia_continuation` 扩展。载体只由 Elysia 解封，不发送给供应商。

不存整段用户历史，只存需要恢复的签名节点。鉴权主体、源／账号、实际模型 ID、会话、父轮次摘要和内容摘要共同限制恢复。工具按包含调用 ID 与完整参数的摘要匹配，客户端补入 `content:null` 不改变关联；文本及媒体另需原节点序号。修改内容、压缩历史、合并原节点、跨会话／模型或歧义匹配都可能使精确关联失败；兼容模式诊断，严格模式拒绝，不按工具名或相似文本猜测。

无载体查找必须有稳定会话标识，例如 `x-elysia-session-id`。默认保留 7 天、每会话 64 轮、全局 512 MiB，单记录受 8 MiB 上限约束。过期记录立即不参与查找，写入时分批清理。容量由事务内计数器维护。缺少主密钥不阻断普通无签名文本请求；需要恢复时明确诊断，绝不降级为明文。

`provider_signature` 没有默认魔法值。规则须指定目标族、工具节点及具体值，支持 raw/base64 编码。用户点击验证后才发送合成工具历史，返回的工具不执行。上游接受且响应通过契约检查后才保存证据；证据绑定最终策略、协议修订、账号、实际模型和编译器，30 天到期。该动作是有诊断的兼容替代，不是恢复原签名。

多密钥源可以指定源配置中的密钥序号（界面从 1 开始，接口 `keyIndex` 从 0 开始）；仅允许已启用且具备该模型权限的密钥。留空使用首个可用密钥，验证结论不推广至其他账号。添加载体后的普通响应及单个流帧仍须满足目标缓冲上限。

## 迁移与验收边界

新增结构接入当前仓库 `storage.OpenWithKey` 的版本化步骤 `2026100801`、`2026100802`。数据和完成标记事务提交，已完成步骤不重建表或扫描历史正文；策略验证结果另行追加到证据表。当前检出的代码仍使用既有启动迁移体系，本次没有重新实现此前另一计划中的统一迁移执行器。

打包重启检查发现旧启动别名表仍包含 `openai-responses → responses-api → openai-responses` 及 Anthropic 的同类循环，会破坏验证报告。本轮改为历史别名直接指向最终 ID，并加入十次重放后协议修订、激活和证据摘要不变的回归。该修复不等于完成全部历史数据库迁移重构。

自动化覆盖两个原故障、usage 偏好、加密数据库重开后的无载体恢复、会话／内容隔离、并行同名工具第二轮、严格模式签名晚到、规则顺序、限制、兼容值验证成功／失败、CAS 和容量淘汰。浏览器用例连接真实后端验证草稿、预览只读、验证、激活与 409。

已使用 Claude Code 2.1.293 和 OpenAI Node SDK 7.30.1，连接本地真实网关及模拟 Gemini 上游完成工具第二轮。Claude Code 在 `--bare --restricted`、短系统提示词、仅 Read 工具及关闭提示缓存／thinking 的条件下实际回传了载体。测试显式配置丢弃 Anthropic metadata／output_config 的具名兼容规则，不代表 Gemini 与 Anthropic 的 effort 等价，也不代表 Claude Code 全部默认配置都已验收。Chat SDK 验证普通请求保留载体，以及流式请求只保留标准工具字段、删除载体后依靠稳定会话恢复。另有常规 HTTP 回归在关闭数据库及重建协议注册表后验证无载体恢复。

客户端验收可通过 `ELYSIA_TEST_CLAUDE_BIN` 和 `ELYSIA_TEST_OPENAI_MODULE` 指向隔离安装，再运行 `go test ./server -run '^TestConversionClientToolRoundTrip$' -v -count=1`。不设置变量时仅运行不依赖外部客户端的 HTTP 回归。测试不使用真实供应商凭据。

远端脱敏结构重放、供应商原故障重试，以及真实客户端完整配置、历史压缩等场景仍需独立验收。当前证据支持本地修复通过，不能据此宣称远端问题已经关闭或整个发布验收完成。
