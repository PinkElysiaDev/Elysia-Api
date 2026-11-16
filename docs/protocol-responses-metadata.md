# Responses 响应元数据约束

`openai-responses/spec.json` 声明响应 `instructions` 与 `tool_choice` 的字段合同。约束依据 [OpenAI Python SDK 固定版本](https://github.com/openai/openai-python/blob/3dae6090cc31cc64a437fe0bcee740b6d81ed2cc/src/openai/types/responses/response.py)，覆盖该版本声明的嵌套联合、字段类型和必需字段。Spec 的 `response-instructions-metadata` 与 `response-tool-choice-metadata` 约束在来源及最终客户端 JSON 响应中复用；SSE 创建、进度、完成、未完成、失败事件也验证其 response 容器。

失败事件将已有 `response-metadata-types` 的选择路径修正为 `/response`。这也修正 `previous_response_id`、数值及 `text` 等旧元数据约束的容器位置；这些字段保留原有类型、null 和错误分类规则。最终客户端 JSON 只增加上述两个共享联合约束，不引入整组旧字段规则。

DSL 提供通用类型、条件与约束引用能力；Spec 定义本协议合法结构；Mapping 负责本协议与大自在天核心之间的元数据提取、还原和中立 Meaning。响应中的 `instructions` 是信息，不附加请求历史关联或核心消息投影能力限制。请求及上游请求保留各自独立合同。

`instructions` 接受字符串、null 或 `ResponseInputItem` 数组。数组覆盖固定 SDK 的 33 个分支，包括输入与输出消息、工具调用及输出、工具声明与配置更新、推理、压缩、MCP、项目引用和程序项目。嵌套联合也必须有效。例如，独立 `function_call_output` 无需对应历史调用；未知显式 `type`、缺少 `function_call.name`、缺少 JSON Schema 配置的 `schema`、非法消息内容类型都会拒绝。可选字段的缺失、null 与错误类型按各自声明区分。

`tool_choice` 接受 `auto`、`none`、`required` 及 SDK 声明的对象分支。项目另保留已经观察到的响应 `tool_choice: null` 兼容边界；这不是对请求合同的放宽。`allowed_tools` 要求 `mode` 为 `auto` 或 `required`，`tools` 为对象数组。该 SDK 将成员声明为开放字典，所以成员的未知字段有效，非对象成员无效。

联合类型采用整项匹配。空内容数组可能同时满足多种消息分支，只需其中一个完整分支有效；不能把多个分支的必需字段合并。SDK 允许的对象邻接字段保持开放，未知顶层响应邻接字段沿用已有所有权规则。未知显式联合判别值不会因邻接字段开放而被接受。

新的 `instructions` 与 `tool_choice` 检查在来源不合法时返回 `upstream_contract_violation`，客户端最终输出不合法时返回 `conversion_rejected`。失败事件中 `previous_response_id`、`temperature`、`text` 等旧检查仍保留原有显式 `invalid_input` 分类。错误定位到所属断言，嵌套约束保留数组索引和 SSE 的 `/response` 前缀。单个 Schema 内的字段错误、消息内容联合或动态字典错误可能定位到所属对象，对外原因使用断言声明的说明，不承诺所有结构化路径均指向叶子字段。编辑核心元数据或移除 Native 后重建仍执行同一最终合同。

此项完成之前明确延后的元数据联合收紧，不代表已确定历史 F01 的触发字段。固定 SDK 的结构类型声明也不等同于其全部自然语言业务约束。现场请求、SDK 来源与回归证据分别记录于三分切换记录。
