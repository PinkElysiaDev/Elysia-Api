# Agent 工具全量目录（31 个，历史基线）

> **历史基线**：现行工具面已收敛为 `bash`（elysia CLI）+ `ask_user` + `update_plan`，本文所列 31 个工具不再直接暴露给模型，其实现保留为 CLI 命令的内部处理器（命令对照见 [agent-cli.md](agent-cli.md)）。本文是重设计前的注册表转储快照，供查阅各内部处理器的定义/权限/元数据。
> 权限三键：`save`（持久化写入）、`live_test`（真实出站）、`delete`（不可逆删除），均可按会话设 ask/always/never；未门控工具随时可用。
> 权限三键：`save`（持久化写入）、`live_test`（真实出站）、`delete`（不可逆删除），均可按会话设 ask/always/never；未门控工具随时可用。

## 总览

| # | 工具 | 用途（审批卡一句话） | 权限 | 元数据 |
|---|------|------|------|------|
| 1 | `update_protocol_draft` | 写入/更新协议配置草稿（立即校验并离线验证） | 不门控（随时可用） | 不可并行 · 风险 medium · 超限截断保 头部 |
| 2 | `preview_request` | 离线渲染草稿请求（不发送） | 不门控（随时可用） | 并行安全 · 风险 low · 超限截断保 头部 |
| 3 | `test_upstream` | 向真实上游发送一次测试请求 | 门控 live_test（真实出站确认） | 不可并行 · 风险 high · 超时 120000ms · 超限截断保 尾部 |
| 4 | `test_model_list` | 试拉上游模型列表（按草稿 models 发现配置） | 门控 live_test（真实出站确认） | 不可并行 · 风险 high · 超时 60000ms · 超限截断保 头部 |
| 5 | `save_protocol` | 把当前草稿保存为正式协议 | 门控 save（写入确认） | 不可并行 · 风险 high · 超限截断保 头部 |
| 6 | `read_protocol` | 读取已保存协议的完整配置 | 不门控（随时可用） | 并行安全 · 风险 low · 超限截断保 头部 |
| 7 | `list_sources` | 列出全部模型源（密钥脱敏） | 不门控（随时可用） | 并行安全 · 风险 low · 超限截断保 头部 |
| 8 | `list_model_groups` | 列出全部模型组及成员 | 不门控（随时可用） | 并行安全 · 风险 low · 超限截断保 头部 |
| 9 | `query_usage_stats` | 查询用量统计汇总与模型分布 | 不门控（随时可用） | 并行安全 · 风险 low · 超限截断保 头部 |
| 10 | `query_usage_trend` | 查询用量按日趋势 | 不门控（随时可用） | 并行安全 · 风险 low · 超限截断保 头部 |
| 11 | `query_usage_logs` | 查询调用日志（可过滤错误） | 不门控（随时可用） | 并行安全 · 风险 low · 超限截断保 头部 |
| 12 | `get_usage_log_detail` | 读取单条调用日志详情（含捕获体） | 不门控（随时可用） | 并行安全 · 风险 low · 超限截断保 头部 |
| 13 | `query_system_logs` | 查询系统日志 | 不门控（随时可用） | 并行安全 · 风险 low · 超限截断保 头部 |
| 14 | `create_model_source` | 创建模型源（需审批） | 门控 save（写入确认） | 不可并行 · 风险 high · 超限截断保 头部 |
| 15 | `update_model_source` | 修改模型源（需审批） | 门控 save（写入确认） | 不可并行 · 风险 high · 超限截断保 头部 |
| 16 | `refresh_model_source` | 拉取模型列表（真实出站，需审批） | 门控 live_test（真实出站确认） | 不可并行 · 风险 high · 超时 60000ms · 超限截断保 尾部 |
| 17 | `create_model_group` | 创建模型组（需审批） | 门控 save（写入确认） | 不可并行 · 风险 medium · 超限截断保 头部 |
| 18 | `update_model_group` | 修改模型组（需审批） | 门控 save（写入确认） | 不可并行 · 风险 medium · 超限截断保 头部 |
| 19 | `update_outbound_policy` | 查询或修改出站禁止 IP 段（需审批） | 门控 save（写入确认） | 不可并行 · 风险 high · 超限截断保 头部 |
| 20 | `delete_model_source` | 删除模型源（不可逆，需审批） | 门控 delete（删除确认） | 不可并行 · 风险 high · 超限截断保 头部 |
| 21 | `delete_model_group` | 删除模型组（不可逆，需审批） | 门控 delete（删除确认） | 不可并行 · 风险 high · 超限截断保 头部 |
| 22 | `list_models` | 查询模型列表（可按源过滤） | 不门控（随时可用） | 并行安全 · 风险 low · 超限截断保 头部 |
| 23 | `update_model` | 修改单个模型（需审批） | 门控 save（写入确认） | 不可并行 · 风险 medium · 超限截断保 头部 |
| 24 | `delete_model` | 删除单个模型（不可逆，需审批） | 门控 delete（删除确认） | 不可并行 · 风险 high · 超限截断保 头部 |
| 25 | `list_api_keys` | 查询 API Key（访问令牌）列表 | 不门控（随时可用） | 并行安全 · 风险 low · 超限截断保 头部 |
| 26 | `create_api_key` | 创建 API Key（需审批） | 门控 save（写入确认） | 不可并行 · 风险 high · 超限截断保 头部 |
| 27 | `update_api_key` | 修改 API Key（需审批） | 门控 save（写入确认） | 不可并行 · 风险 high · 超限截断保 头部 |
| 28 | `delete_api_key` | 删除 API Key（不可逆，需审批） | 门控 delete（删除确认） | 不可并行 · 风险 high · 超限截断保 头部 |
| 29 | `update_plan` | 更新工作方案清单（侧边栏实时展示） | 不门控（随时可用） | 不可并行 · 风险 low · 超限截断保 头部 |
| 30 | `ask_user` | 向用户提出一个需要选择的问题 | 不门控（随时可用） | 不可并行 · 风险 low |
| 31 | `update_title` | 用一句话概括当前任务并设为会话标题 | 不门控（随时可用） | 不可并行 · 风险 low |

## 协议设计（agent_protocol_tools.go）

### 1. `update_protocol_draft`

- **权限**：不门控（随时可用）；**元数据**：不可并行 · 风险 medium · 超限截断保 头部
- **给模型的描述**：提交完整的自定义协议配置 JSON（整份覆盖当前草稿）。服务端会做声明式校验并用样例请求离线渲染；校验或渲染问题会原样返回，需修复后重新提交。文档中有响应示例时一并传 exampleResponse 以检验映射。
- **参数**：

| 字段 | 类型 | 说明 |
|------|------|------|
| `config` | object **必填** | 完整的 CustomProtocolConfig 对象（含 id 与 request） |
| `exampleResponse` | object | 可选：上游响应示例（JSON 对象），用于离线检验 response 映射 |

### 2. `preview_request`

- **权限**：不门控（随时可用）；**元数据**：并行安全 · 风险 low · 超限截断保 头部
- **给模型的描述**：用样例 Maheshvara 请求离线渲染当前草稿的出站请求（method/path/query/headers/body/凭证注入形态）。不发起真实上游请求。用于提交草稿后自查请求形状。
- **参数**：

| 字段 | 类型 | 说明 |
|------|------|------|
| `sampleRequest` | object | 可选：自定义样例 Maheshvara 请求；缺省用内置默认样例 |

### 3. `test_upstream`

- **权限**：门控 live_test（真实出站确认）；**元数据**：不可并行 · 风险 high · 超时 120000ms · 超限截断保 尾部
- **给模型的描述**：把当前草稿渲染成请求并发送到真实上游（用户审批后执行），返回 HTTP 状态、原文、映射结果；stream=true 时采样 SSE 事件与解码结果。用户在对话中给出 baseUrl / API key 时作为参数传入；已提供过的凭证本会话会自动记住，无需重复索要。
- **参数**：

| 字段 | 类型 | 说明 |
|------|------|------|
| `apiKey` | string | 可选：用户提供的 API key（缺省用会话已记住的） |
| `baseUrl` | string | 可选：用户提供的上游 baseUrl（缺省用会话已记住的） |
| `sampleRequest` | object | 可选：自定义样例请求 |
| `stream` | boolean | 可选：是否按流式（SSE）测试，默认 false |

### 4. `test_model_list`

- **权限**：门控 live_test（真实出站确认）；**元数据**：不可并行 · 风险 high · 超时 60000ms · 超限截断保 头部
- **给模型的描述**：按当前草稿的 models 发现配置向真实上游（用户审批后执行）请求模型列表并解析，用于验证发现端点配置。草稿未声明 models 配置时会报错。用户在对话中给出 baseUrl / API key 时作为参数传入；已提供过的凭证本会话自动记住。
- **参数**：

| 字段 | 类型 | 说明 |
|------|------|------|
| `apiKey` | string | 可选：用户提供的 API key |
| `baseUrl` | string | 可选：用户提供的上游 baseUrl |

### 5. `save_protocol`

- **权限**：门控 save（写入确认）；**元数据**：不可并行 · 风险 high · 超限截断保 头部
- **给模型的描述**：把当前草稿保存进协议注册表（用户审批后执行，写入即热生效）。编辑模式必须保持原协议 id；新建模式若 id 与现有协议冲突会被拒绝（换一个 id 再试）。建议在真实测试通过后再请求保存。
- **参数**：

| 字段 | 类型 | 说明 |
|------|------|------|
（无参数）

### 6. `read_protocol`

- **权限**：不门控（随时可用）；**元数据**：并行安全 · 风险 low · 超限截断保 头部
- **给模型的描述**：按 id 读取一条已保存协议（含内置预置协议）的完整配置 JSON，作为写法参考或编辑基准。
- **参数**：

| 字段 | 类型 | 说明 |
|------|------|------|
| `id` | string **必填** | 协议 id，如 anthropic-api |

## 模型源 / 模型组 / 模型 / 出站运维（agent_ops_tools.go）

### 7. `list_sources`

- **权限**：不门控（随时可用）；**元数据**：并行安全 · 风险 low · 超限截断保 头部
- **给模型的描述**：列出全部模型源：平台、baseUrl、启停、自动拉取、密钥策略（脱敏）、模型数与最近拉取状态。不显示任何密钥明文。
- **参数**：

| 字段 | 类型 | 说明 |
|------|------|------|
（无参数）

### 8. `list_model_groups`

- **权限**：不门控（随时可用）；**元数据**：并行安全 · 风险 low · 超限截断保 头部
- **给模型的描述**：列出全部模型组：名称、启停、策略、重试、并发/限额与成员（sourceId:modelId 引用，也可能只显示模型 id）。
- **参数**：

| 字段 | 类型 | 说明 |
|------|------|------|
（无参数）

### 9. `query_usage_stats`

- **权限**：不门控（随时可用）；**元数据**：并行安全 · 风险 low · 超限截断保 头部
- **给模型的描述**：查询网关用量统计：请求量/成功失败/token 消耗/缓存命中/平均耗时汇总 + 按模型分布。时间窗用 days（默认 7）或 from/to（RFC3339）；可按模型名、key 名、模型组过滤。
- **参数**：

| 字段 | 类型 | 说明 |
|------|------|------|
| `days` | integer | 最近 N 天（默认 7，最大 366） |
| `from` | string | 起始时间（RFC3339），与 to 搭配 |
| `groupName` | string | 按模型组过滤 |
| `keyName` | string | 按 API token 名过滤 |
| `modelName` | string | 按模型名过滤 |
| `to` | string | 结束时间（RFC3339），默认现在 |

### 10. `query_usage_trend`

- **权限**：不门控（随时可用）；**元数据**：并行安全 · 风险 low · 超限截断保 头部
- **给模型的描述**：查询用量按日趋势（请求数/成功失败/token），适合生成趋势图表。窗口与过滤参数同 query_usage_stats。
- **参数**：

| 字段 | 类型 | 说明 |
|------|------|------|
| `days` | integer | 最近 N 天（默认 7） |
| `from` | string |  |
| `groupName` | string |  |
| `keyName` | string |  |
| `modelName` | string |  |
| `to` | string |  |

### 11. `query_usage_logs`

- **权限**：不门控（随时可用）；**元数据**：并行安全 · 风险 low · 超限截断保 头部
- **给模型的描述**：查询调用日志列表（新→旧）：状态码、错误与错误类别、模型、key、耗时、token。status=failed 只看失败请求；需要深入某个请求时用 get_usage_log_detail 取捕获的请求/响应体。
- **参数**：

| 字段 | 类型 | 说明 |
|------|------|------|
| `days` | integer | 最近 N 天（默认 7） |
| `from` | string |  |
| `groupName` | string |  |
| `keyName` | string |  |
| `limit` | integer | 返回条数（默认 20，最大 100） |
| `modelName` | string |  |
| `status` | string | success | failed |
| `statusCode` | integer | 精确状态码过滤 |
| `to` | string |  |

### 12. `get_usage_log_detail`

- **权限**：不门控（随时可用）；**元数据**：并行安全 · 风险 low · 超限截断保 头部
- **给模型的描述**：按 requestId 读取单条调用日志的完整记录：四段捕获体（入站/出站/上游响应/回给客户端的响应）、重试链、错误详情。用于深入分析失败请求。
- **参数**：

| 字段 | 类型 | 说明 |
|------|------|------|
| `requestId` | string **必填** | 调用日志的 requestId |

### 13. `query_system_logs`

- **权限**：不门控（随时可用）；**元数据**：并行安全 · 风险 low · 超限截断保 头部
- **给模型的描述**：查询系统运行日志（info/warn/error 级别），排查网关自身问题时使用。
- **参数**：

| 字段 | 类型 | 说明 |
|------|------|------|
| `level` | string | info | warn | error（缺省全部） |
| `limit` | integer | 返回条数（默认 30，最大 100） |

### 14. `create_model_source`

- **权限**：门控 save（写入确认）；**元数据**：不可并行 · 风险 high · 超限截断保 头部
- **给模型的描述**：创建模型源（用户审批后生效）。platform 支持 openai/anthropic/gemini/responses 或 custom:<协议ID>；autoFetchModels=true 自动拉取模型列表（创建后需另经 refresh_model_source 拉取，或用户在页面手动拉取）；手动模型用手动列表或 manualModels。创建前先向用户确认 baseUrl、平台与密钥来源。
- **参数**：

| 字段 | 类型 | 说明 |
|------|------|------|
| `apiKey` | string | API key（加密存储） |
| `autoFetchModels` | boolean | 是否自动拉取模型列表 |
| `baseUrl` | string **必填** | 上游 baseUrl（http/https） |
| `fetchBaseUrl` | string | 模型列表拉取地址（缺省同 baseUrl） |
| `manualModels` | array | 手动模型名列表 |
| `name` | string **必填** | 源名称（显示用） |
| `platform` | string | openai（默认）/anthropic/gemini/responses/custom:<id> |

### 15. `update_model_source`

- **权限**：门控 save（写入确认）；**元数据**：不可并行 · 风险 high · 超限截断保 头部
- **给模型的描述**：修改已有模型源（用户审批后生效）：启停、改名、换 baseUrl/平台、更换 API key（留空=保留原 key）、调整自动拉取或手动模型列表。source 用源 id 或名称指定。
- **参数**：

| 字段 | 类型 | 说明 |
|------|------|------|
| `apiKey` | string | 新 API key；留空表示保留原 key |
| `autoFetchModels` | boolean |  |
| `baseUrl` | string |  |
| `enabled` | boolean |  |
| `manualModels` | array | 整体替换手动模型列表 |
| `name` | string |  |
| `platform` | string |  |
| `source` | string **必填** | 源 id 或名称 |

### 16. `refresh_model_source`

- **权限**：门控 live_test（真实出站确认）；**元数据**：不可并行 · 风险 high · 超时 60000ms · 超限截断保 尾部
- **给模型的描述**：从上游拉取某模型源的模型列表（用户审批后执行，真实出站请求）。手动源则会同步手动模型列表。新建自动拉取源后用这个工具取回模型。
- **参数**：

| 字段 | 类型 | 说明 |
|------|------|------|
| `source` | string **必填** | 源 id 或名称 |

### 17. `create_model_group`

- **权限**：门控 save（写入确认）；**元数据**：不可并行 · 风险 medium · 超限截断保 头部
- **给模型的描述**：创建模型组（用户审批后生效）：模型引用列表（sourceId:modelId 或模型名）、调度策略、重试。名称需唯一；创建前先 list_sources / list_model_groups 确认可用模型与重名。
- **参数**：

| 字段 | 类型 | 说明 |
|------|------|------|
| `dailyLimitMaxRequests` | integer | 每日请求上限（0=不限） |
| `dailyLimitMaxTokens` | integer | 每日 token 上限（0=不限） |
| `enabled` | boolean | 默认 true |
| `maxConcurrency` | integer | 并发上限（0=不限） |
| `maxRetries` | integer | 失败重试次数（默认 3） |
| `models` | array | 成员模型引用（sourceId:modelId 或模型名） |
| `name` | string **必填** | 组名（客户端调用时用的模型名） |
| `strategy` | string | round-robin（默认）/random/sequential |

### 18. `update_model_group`

- **权限**：门控 save（写入确认）；**元数据**：不可并行 · 风险 medium · 超限截断保 头部
- **给模型的描述**：修改已有模型组（用户审批后生效）：启停、策略、重试、并发/限额、成员增删。group 用组名或 id 指定。
- **参数**：

| 字段 | 类型 | 说明 |
|------|------|------|
| `addModels` | array |  |
| `dailyLimitMaxRequests` | integer |  |
| `dailyLimitMaxTokens` | integer |  |
| `enabled` | boolean |  |
| `group` | string **必填** | 组名或 id |
| `maxConcurrency` | integer |  |
| `maxRetries` | integer |  |
| `removeModels` | array |  |
| `strategy` | string |  |

### 19. `update_outbound_policy`

- **权限**：门控 save（写入确认）；**元数据**：不可并行 · 风险 high · 超限截断保 头部
- **给模型的描述**：查询或整体替换出站禁止 IP 段列表（SSRF 防护）。不传参数 = 只读返回当前列表与预置默认；ranges = 整体替换（CIDR 数组，空数组 = 全放行）；resetDefault = 恢复预置默认。上游是本机/内网地址（如 127.0.0.1）被 "refused to dial denied IP" 拦截时，从 ranges 中去掉对应段（环回 127.0.0.0/8、私网 10.0.0.0/8、172.16.0.0/12、192.168.0.0/16）即可放行。修改全列表为高影响操作，先向用户说明改动范围再调用。
- **参数**：

| 字段 | 类型 | 说明 |
|------|------|------|
| `ranges` | array | 整体替换后的禁止段 CIDR 列表（空数组 = 放行所有地址） |
| `resetDefault` | boolean | 恢复预置默认禁止段（忽略 ranges） |

### 20. `delete_model_source`

- **权限**：门控 delete（删除确认）；**元数据**：不可并行 · 风险 high · 超限截断保 头部
- **给模型的描述**：删除模型源（用户审批后执行，不可逆）：源下全部模型与组内成员引用一并级联删除。source 用源 id 或名称指定。删除前先向用户核对对象与影响面（模型数、受影响的组）。
- **参数**：

| 字段 | 类型 | 说明 |
|------|------|------|
| `source` | string **必填** | 源 id 或名称 |

### 21. `delete_model_group`

- **权限**：门控 delete（删除确认）；**元数据**：不可并行 · 风险 high · 超限截断保 头部
- **给模型的描述**：删除模型组（用户审批后执行，不可逆）：客户端将无法再按该组名调用。若某些 API Key 只授权了这一个组，会随删除级联禁用（名单在结果里返回）。group 用组名或 id 指定。
- **参数**：

| 字段 | 类型 | 说明 |
|------|------|------|
| `group` | string **必填** | 组名或 id |

### 22. `list_models`

- **权限**：不门控（随时可用）；**元数据**：并行安全 · 风险 low · 超限截断保 头部
- **给模型的描述**：查询模型清单（默认全部源）。source 传源 id 或名称可按源过滤；search 按名称模糊匹配。建模型组前用它确认可用的模型 id。只给前 limit 条（默认 50、上限 200），总量在 summary 里。
- **参数**：

| 字段 | 类型 | 说明 |
|------|------|------|
| `limit` | integer | 返回条数（默认 50，上限 200） |
| `search` | string | 名称模糊匹配（可选） |
| `source` | string | 源 id 或名称（可选） |

### 23. `update_model`

- **权限**：门控 save（写入确认）；**元数据**：不可并行 · 风险 medium · 超限截断保 头部
- **给模型的描述**：修改单个模型（用户审批后生效）：启停、改名、类型、maxTokens、能力标记（视觉/工具/结构化）、思考模式。能力字段被修改后刷新不再覆盖（capability_source=manual）。source 用源 id 或名称，model 是模型 id（先 list_models 查）。
- **参数**：

| 字段 | 类型 | 说明 |
|------|------|------|
| `enabled` | boolean |  |
| `maxTokens` | integer |  |
| `model` | string **必填** | 模型 id |
| `name` | string |  |
| `source` | string **必填** | 源 id 或名称 |
| `structuredOutput` | boolean |  |
| `thinkingMode` | string |  |
| `toolsCapable` | boolean |  |
| `type` | string |  |
| `visionCapable` | boolean |  |

### 24. `delete_model`

- **权限**：门控 delete（删除确认）；**元数据**：不可并行 · 风险 high · 超限截断保 头部
- **给模型的描述**：删除单个模型（用户审批后执行，不可逆）：组内引用一并清理。自动拉取的模型下次刷新可能重新出现；想临时下线优先用 update_model 的 enabled=false。source 用源 id 或名称，model 是模型 id。
- **参数**：

| 字段 | 类型 | 说明 |
|------|------|------|
| `model` | string **必填** | 模型 id |
| `source` | string **必填** | 源 id 或名称 |

## API Key 管理（agent_token_tools.go）

### 25. `list_api_keys`

- **权限**：不门控（随时可用）；**元数据**：并行安全 · 风险 low · 超限截断保 头部
- **给模型的描述**：查询 API Key（访问令牌）列表。token 脱敏显示；allowedGroups 为空表示可访问全部模型组。带 agent 作用域的是远程访问 Key（驱动 AI 助手专用，不参与推理），由用户在运行配置页管理——不可对其做写操作。
- **参数**：

| 字段 | 类型 | 说明 |
|------|------|------|
（无参数）

### 26. `create_api_key`

- **权限**：门控 save（写入确认）；**元数据**：不可并行 · 风险 high · 超限截断保 头部
- **给模型的描述**：创建 API Key，即客户端调用 /v1 接口用的推理访问令牌（用户审批后生效）。secret 留空则自动生成随机明文，完整明文只在本次结果里返回一次，请提醒用户立即保存。allowedGroups 为空表示可访问全部模型组（扩权面大，创建前先向用户确认授权范围）。远程访问 Key（驱动 AI 助手的那类）由用户在运行配置页管理，不由此工具创建。
- **参数**：

| 字段 | 类型 | 说明 |
|------|------|------|
| `allowedGroups` | array | 允许访问的模型组名称；空=不限制 |
| `enabled` | boolean | 默认 true |
| `name` | string **必填** | Key 名称（主键，创建后不可改） |
| `secret` | string | Key 明文；留空自动生成随机值 |

### 27. `update_api_key`

- **权限**：门控 save（写入确认）；**元数据**：不可并行 · 风险 high · 超限截断保 头部
- **给模型的描述**：修改已有 API Key（用户审批后生效）：启停、调整可访问的模型组、更换明文（newSecret 留空=保留原值）。名称是主键不可修改；远程访问 Key（agent 作用域）由用户在运行配置页管理，此工具不可修改。allowedGroups 为空表示不限制（可访问全部模型组），调整前先向用户确认。
- **参数**：

| 字段 | 类型 | 说明 |
|------|------|------|
| `allowedGroups` | array | 整体替换允许访问的模型组；空=不限制 |
| `enabled` | boolean |  |
| `name` | string **必填** | Key 名称 |
| `newSecret` | string | 新明文；留空保留原值 |

### 28. `delete_api_key`

- **权限**：门控 delete（删除确认）；**元数据**：不可并行 · 风险 high · 超限截断保 头部
- **给模型的描述**：删除 API Key（用户审批后执行，不可逆）：使用该 Key 的客户端将立即无法调用。远程访问 Key（agent 作用域）请在运行配置页删除。删除前先向用户核对名称。
- **参数**：

| 字段 | 类型 | 说明 |
|------|------|------|
| `name` | string **必填** | Key 名称 |

## 会话流程（计划 / 提问 / 标题）

### 29. `update_plan`

- **权限**：不门控（随时可用）；**元数据**：不可并行 · 风险 low · 超限截断保 头部
- **给模型的描述**：更新工作方案（analysis 摘要与步骤清单都是整体替换）。方案分两部分：analysis 归纳已完成探索/查询/测试得到的结论与关键约束（不是步骤，执行中发现新结论就更新它）；plan 只列**尚未执行**的动作步骤（动宾短语、到对象、关键参数），把已完成的查询/分析列为步骤是错误用法。执行推进时把完成步骤标 done，新增发现只调整剩余步骤与 analysis。通常 3-7 条。
- **参数**：

| 字段 | 类型 | 说明 |
|------|------|------|
| `analysis` | string | 分析摘要：已做工作的结论归纳与约束（1-5 句话） |
| `plan` | array **必填** | 待执行步骤列表（整体替换；已完成步骤标 done 保留） |
| `ready_for_approval` | boolean | 方案已定稿、等待用户确认时置 true（仅计划模式）。定稿要求：analysis 已归纳结论，plan 全部为待执行动作 |

### 30. `ask_user`

- **权限**：不门控（随时可用）；**元数据**：不可并行 · 风险 low
- **给模型的描述**：信息不足以继续、且有明确选项时，向用户提问并暂停本轮。用户作答后你会拿到 answer 继续。不要用它做开放式寒暄。
- **参数**：

| 字段 | 类型 | 说明 |
|------|------|------|
| `allow_custom` | boolean | 是否允许用户输入选项之外的答案 |
| `options` | array | 预设选项 |
| `question` | string | 要问用户的问题 |

### 31. `update_title`

- **权限**：不门控（随时可用）；**元数据**：不可并行 · 风险 low
- **给模型的描述**：把会话标题改写成对任务目标的简洁概括（动宾短语，不超过 16 个字，不要复述用户原话）。理解任务后调用一次；任务目标变化时再更新。
- **参数**：

| 字段 | 类型 | 说明 |
|------|------|------|
| `title` | string **必填** | 新标题，不超过 16 个字 |
