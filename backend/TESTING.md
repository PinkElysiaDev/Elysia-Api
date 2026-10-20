# Go 后端测试

在 `backend/` 下使用 Go 1.27.2 或更新版本运行：

```sh
# 日常回归：Go 默认并行运行各包，包内隔离用例通过 t.Parallel 并行。
go test -tags=sonic ./...

# 实际耗时（禁用测试结果缓存，但保留编译缓存）。
go test -tags=sonic -count=1 -timeout=30m ./...

# 检查共享状态、执行顺序依赖和数据竞争。
go test -tags=sonic -race -shuffle=on -count=1 -timeout=30m ./...

# 对照包内串行执行；与默认并行命令使用相同机器、环境和编译缓存。
go test -tags=sonic -parallel=1 -count=1 -timeout=30m ./...
```

`-tags=sonic` 启用 Gin 的 Sonic 编解码，使测试与构建脚本、Docker 和 CI 使用相同实现。

`-parallel` 限制每个测试进程内的并行用例数，默认使用 `GOMAXPROCS`。
资源受限的 CI 可以设置 `-parallel=4 -p=2`；`-p` 控制包级并发。
测量耗时时不要同时运行其他测试或构建，也不要把 `(cached)` 结果当作执行耗时。

## MCP 定向验证

```sh
go test -tags=sonic -race ./server -run '^Test(MCP|AgentRemote|A2A|RemoteThinkingEffortWhitelistAndSchema)' -count=1 -timeout=3m
```

`mcp_sdk_test.go` 使用官方 Go SDK 客户端经过真实 HTTP/Gin 鉴权层验证发现、
工具调用、进度和 Key 撤销；`mcp_cli_tool_test.go` 保留无状态批处理、部分失败、
脱敏及实际 SSE 断开取消的回归。传输层兼容性变化见
[远程 Agent 接入](../docs/remote-agent-api.md#取代手写传输层的破坏性迁移)。

## A2A 定向验证

```sh
go test -tags=sonic -race ./server -run '^Test(A2A|AgentRemote|Remote|MCP)' -count=1 -timeout=3m
```

`a2a_sdk_test.go` 使用官方 0.3 / 1.0 客户端验证真实 HTTP/SSE、鉴权撤销、
断开后继续执行、并发重复消息、任务重放、订阅广播及分页淘汰。
`agent_remote_test.go` 保留审批恢复、显式裁决、取消终态和旧任务取代的集成回归。
版本与响应差异见[远程 Agent 接入](../docs/remote-agent-api.md#sdk-线协议迁移)。

## CLI 语法与命令框架定向验证

```sh
go test -tags=sonic -race ./server ./agent ./internal/clishell \
  -run '^Test(CLI|MCPCLI|MaskSecret|RedactCommand|MaskedPending)' -count=1 -timeout=3m
```

`cli_framework_test.go` 覆盖 mvdan/sh 支持子集、拒绝 shell 求值、Cobra help 与审批一致性、
请求间 flag 状态隔离及转义/拼接引号密钥的脱敏；现有 CLI 测试保留业务映射、批处理与真实执行回归。
中文 CLI 参考由运行时 Cobra help 生成：

```sh
UPDATE_AGENT_CLI_DOCS=1 go test -tags=sonic ./server -run '^TestCLIReferenceUpToDate$' -count=1
```

## SSE 库迁移定向验证

```sh
go test -tags=sonic -race ./internal/eventstream ./protocol ./relay ./server \
  -run '^Test(SSE|HTTP|StreamIdle|OpenAIStreamNotCut|GatewaySSEAndNDJSON|GatewayBrokenStream|GatewayResponsesNativeFailure|GatewayFailureEncoding|ProviderFailureRetains|CustomProtocolStream|TransportFailure|FailureEvidence|AgentCaller|AgentFullTurn|AgentGatedToolApproval|AgentStopTurn|AgentKeyCreateSecret|OpenAIChatPassthroughStream|ResponsesPassthroughStream|StreamCancellationUsage|ToolOnlyStreamWithoutUsage|CanceledConnection)' \
  -count=1 -timeout=4m
```

覆盖库适配的完整帧 EOF、LF/CRLF/CR 分隔、BOM、事件 ID、控制帧、整帧预算、写失败及取消，
并验证真实 HTTP 桥接、尾部用量入账、Agent 事件和上游错误归属。
选型与兼容性差异见 [SSE 迁移说明](../docs/backend-sse-migration.md)。

## JSON Schema 反射迁移定向验证

```sh
go test -tags=sonic -race ./protocol ./server \
  -run '^Test(Schema|ProtocolSchemaLibrary|ProtocolRevisionAdmin|ProtocolAdminRejects|AgentProtocolV2|CLIReferenceUpToDate|RemoteThinkingEffortWhitelistAndSchema|OperationSchema|CompileChecksNestedOutputSchema)' \
  -count=1 -timeout=3m
```

`protocol/schema_test.go` 使用已有的 google/jsonschema-go 依赖独立解析和验证生成结果，
覆盖全部根节点、递归引用、严格字段、Value 任意 JSON、空值/缺失、命名 map 及并发结果隔离。
`server/protocol_schema_library_test.go` 验证 HTTP 与 CLI schema 查询；现有 authoring 测试覆盖编辑、验证及启用流程。
输出表示差异见 [JSON Schema 参考](../docs/protocol-definition-reference.md#schema-表示迁移)。

## Sonic JSON Pointer 定向验证

```sh
go test -tags=sonic -race ./protocol ./protocol/builtin ./server \
  -run '^(Test(JSONPointer|RequestViews|RequestResources|Value|Expression|CompileChecksNestedOutputSchema|Native|MutationTransaction|TypedMeter|RequestWireSchemaTypedParity|JSONModuleFallbackMatchesShippedSamples|HTTP|GatewaySSEAndNDJSON|GatewayBrokenStream|AgentProtocolV2)|FuzzJSONPointerObjectLookup)' \
  -count=1 -timeout=3m
go test -tags=sonic ./protocol -run '^$' \
  -fuzz '^FuzzJSONPointerObjectLookup$' -fuzztime=10s -parallel=2
GOMAXPROCS=1 go test -tags=sonic ./protocol -run '^$' \
  -bench '^BenchmarkJSONTraversal/pointer' -benchmem -benchtime=500ms -count=5
```

覆盖重复键、转义/Unicode 键、精确数值、缺失与 null、索引校验、深层输入回退及内存所有权。
选型与前后基准见 [JSON 遍历评估](../docs/backend-json-traversal-benchmarks.md)。
本次采用 Sonic Pointer 定点读取；全量 AST 容器替换因小对象、宽对象和数组场景回退而未采用。

## 运行时 Schema 校验迁移

```sh
go test -tags=sonic -race ./protocol ./protocol/builtin ./server \
  -run '^Test(RuntimeSchema|Expression|Compile|Operation|Model|Request|JSONModule|Native|Mutation|Typed|Verification|Schema|AgentProtocolV2|ProtocolRevision|ProtocolAdmin|CLIReference)' \
  -count=1 -timeout=3m
go test -tags=sonic -race ./protocol/builtin \
  -run '^Test(ShippedDefinitionsVerify|BuiltinVerifiedProfilesKeepTextRoutesAndFullDiagnostics)$' \
  -count=1 -timeout=3m
GOMAXPROCS=1 go test -tags=sonic ./protocol -run '^$' \
  -bench '^BenchmarkRuntimeSchema' -benchmem -benchtime=200ms -count=3
```

覆盖精确数字、nullable/enum、缺失、对象与数组约束、资源边界、并发复用和操作元数据隔离。
运行时与编译成本分别测量，含回退结果；见[迁移报告](../docs/backend-runtime-schema-migration.md)。

## Expr 表达式执行评估

从仓库根目录运行隔离原型（不改变 backend 依赖或生产执行器）：

```sh
python3 tools/expr-evaluation/run.py -race \
  -run '^(Test(ExprEvaluation|Expression|CompileChecksNestedOutputSchema)|FuzzExprEvaluationSubset)' \
  -count=1 -timeout=2m
python3 tools/expr-evaluation/run.py -run '^$' \
  -fuzz '^FuzzExprEvaluationSubset$' -fuzztime=15s -parallel=2
GOMAXPROCS=1 python3 tools/expr-evaluation/run.py -run '^$' \
  -bench '^BenchmarkExprEvaluation' -benchmem -benchtime=200ms -count=3
```

结论为暂不采用 Expr：原生语义存在差异，适配层仍需保留精确值、预算及诊断逻辑，且多数已测路径回退。
测试包含明确断言已知不兼容行为的探针；通过不代表完整 DSL 等价。详见[评估报告](../docs/backend-expr-evaluation.md)。

## sqlc 增量查询验证

```sh
go test -tags=sonic -race ./storage ./server \
  -run '^Test(SQLC|ListGroups|Group|CreateGroup|AddGroup|RemoveGroup|DeleteGroup|SourceModelsGroupsTokensAndUsage|ModelGroup|UpsertGroup|RuntimeConfig|AdminListGroups)' \
  -count=1 -timeout=3m
GOMAXPROCS=1 go test -tags=sonic ./storage -run '^$' \
  -bench '^BenchmarkListGroups$' -benchmem -benchtime=200ms -count=3
```

本次只把固定的模型组和 settings 查询交给 sqlc，`ListGroups` 消除 N+1；生成文件与运行时 schema 通过定向测试校对。基准和范围见 [sqlc 迁移报告](../docs/backend-sqlc-migration.md)。

## 测试组织与去重

缓存的定向 benchmark、Otter 替换前后结果及容量语义差异见
[缓存性能对比](../docs/backend-cache-benchmarks.md)。

具体删除依据、保留的行为验证位置和矩阵缩减数量见
[测试裁剪记录](../docs/backend-test-review.md)。

- `protocol/builtin` 负责预置协议编译、验证及编解码矩阵。
  `TestShippedDefinitionsVerify` 是四个预置协议的完整离线验证入口。
  `TestBuiltinVerifiedProfilesKeepTextRoutesAndFullDiagnostics` 在同一份 4×4 组合报告上
  检查文本、缓存能力、完整诊断及可见推理/摘要边界，避免分别生成两遍相同报告。
- `server` 负责 HTTP/SSE/WebSocket、授权、持久化、协议发布与恢复等集成行为。
  同样的业务输入在协议层和 HTTP 层出现，并不意味着断言重复。
- 加密推理的首次投影、恢复载荷、日志证据和后续轮次合并在
  `TestGatewayResponsesEncryptedReasoningContinuation`，共用同一套服务器和响应数据。
- 重启幂等性测试执行首次初始化及两次重复重启/刷新，分别检查状态与证据不变；
  压力重复使用 `-count`，不在默认回归里硬编码十次相同操作。

## 测试准备数据

- `protocol/builtin/fixture_test.go` 通过 `sync.OnceValues` 复用只读的编译结果。
  `Compiled.Definition()` 返回可独立编辑的副本；每次请求和流仍创建自己的状态。
- `server/gateway_protocol_test.go` 按协议内容哈希复用真实生成的预置离线报告。
  批量写入四个预置后只发布一次运行时。
- `server/routing_fixture_test.go` 先从当前测试数据库解析策略、绑定和模型上下文，
  再按源协议、目标协议、策略、上下文和能力契约哈希复用离线组合报告。
- 数据库、配置文件、服务器、请求和可编辑定义均由各测试独立持有。
  编译器、验证器、启动恢复、作者工作流等针对验证过程本身的测试仍直接调用生产流程。

## 并行与时间控制

- 在拥有独立状态的顶层用例调用 `t.Parallel()`；矩阵只在子用例自己创建全部资源时并行。
  共用请求计数器、当前响应模式或同一数据库的内层子用例顺序执行。
- Gin 模式及私网测试拨号开关在 `server/main_test.go` 的 `TestMain` 中统一初始化。
- `config.Load` 返回独立配置；启动入口所用的 `GlobalConfig` 只在包初始化时发布，
  加载测试配置或重新读取文件不会覆盖它。
- 修改环境变量、全局代理/禁止地址、调用 `server.New` 的测试继续串行；
  它们会在顶层并行测试恢复执行前完成。测量进程内存、分配或性能的用例也保持串行。
- `TestSourceRefreshCoalescesSavesAndSharesSyncGate` 也保持串行：它需要在真实模型发现
  的截止时间内完成两次带协议验证的配置保存。通道控制旧请求的释放，测试传输层不再额外
  施加更短的 10 秒超时，模型发现自身的截止时间仍然有效。
- 内存内并发测试使用 `testing/synctest` 和通道屏障。例如缓存合并测试先等待
  所有 goroutine 阻塞，再确认只有一个请求进入 handler，最后释放请求。
- 涉及真实网络/数据库的测试使用通道通知开始与释放，并保留失败超时。
  真正验证网络超时的测试保留短时间窗口，不用任意长的 `Sleep` 模拟业务同步。

保留的真实上游、客户端 SDK、负载与 HTTP profiling 用例使用各自的 `ELYSIA_*` 显式开关。
一次性旧实现对拍、扫描原型调查和重复堆快照入口已删除；历史审计报告中的命令对应原提交。
