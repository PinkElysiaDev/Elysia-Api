# Go 后端测试

使用 Go 1.27.2 或更新版本。日常命令在**仓库根目录**运行，避免裸 `go test ./...` 按机器默认并发同时构建和运行重型夹具：

```sh
# 快速层：除 server 外的全部 Go 包，包含现有 fuzz 种子。
npm run test:protocol -- --suite=fast

# 完整普通层：全部包和全部 server 测试，包含现有 fuzz 种子。
npm run test:protocol -- --suite=all

# 完整 race 层：按包及 server 互斥分片分别保留证据；不再使用单个 65 分钟进程。
npm run test:protocol -- --suite=all --race
# CI 的等价入口：node scripts/verify-protocol.mjs race

# 原有定向层保持不变；不传 suite 时仍默认 conversion，而非完整层。
npm run test:protocol -- --suite=conversion
npm run test:protocol -- --suite=gemini-stream
npm run test:protocol -- --suite=output
```

Runner 固定 `GOMAXPROCS=2`、`GOMEMLIMIT=2GiB`、`GOFLAGS=-p=2`、
`-parallel=2 -count=1 -timeout=15m`；每个任务的外部超时为 16 分钟。
`--jobs=1..4` 仅控制 runner 的任务槽位，不随之提高 Go 的包并发。
普通完整层先运行非 server 包，再运行 server，避免重型进程叠加；race 的
`protocol`、`protocol/builtin` 和每个 server 分片占满槽位。其他 race 包任务在槽位内并行。
超时或中断会终止整个测试进程组（Windows 使用进程树），不遗留测试子进程。
GOMEMLIMIT 是 Go 软限制，并非整个进程树的 RSS 硬上限；受限机器可使用外部 RSS watchdog。

server race 分片为 Gateway/PackageGateway、Agent、Protocol/Preset/RecoveredPresets，
以及使用 `-skip` 的补集。所有顶层 Test/Fuzz 恰好落入一个分片，包括未来新增的名称；
`scripts/verification/backend.test.mjs` 直接扫描当前声明验证这个分区，不维护易过期的测试名单。
不会删除样本、子测试或断言，也不会扩大超时或协议资源预算。

其余示例在 `backend/` 下执行；定向命令也应显式约束资源：

```sh
export GOMEMLIMIT=1GiB GOMAXPROCS=2 GOFLAGS=-p=1
go test ./server \
  -run '^TestGatewayUsageBodyPolicy$' -parallel=2 -count=1 -timeout=15m
# 顺序依赖检查使用同样的限制，增加 -shuffle=on；不要并行启动多个重型测量。
```

Hertz 在支持的平台上默认使用 Sonic，测试、构建脚本、Docker 和 CI 均无需额外 JSON build tag。

## 性能基线与分配归因

在仓库根目录运行 `node scripts/benchmark-performance.mjs --output=.tmp-dev/performance-baseline`。
工具串行执行微基准、三进程隔离 HTTP 测量、独立 CPU/分配 profile 及取消诊断；
包含分配次数、累计字节、GC、逐请求延迟、采样峰值和自然空闲恢复曲线。
场景、参数、测量边界与证据说明见[性能基准](../docs/backend-performance.md)。
迁移结果见 [Hertz 简报](../docs/backend-hertz-migration.md)。原始测量与 profile
保存到源码树外；旧轮次报告可从 Git 历史查询。后续优化应重新采集对应源码的基线。

## 后端结构与组件边界

```sh
go test ./internal/architecture
go test -race -shuffle=on ./internal/routing ./internal/outbound ./config ./storage ./relay ./server
go vet ./...
```

架构测试检查包依赖方向、Hertz 的 HTTP 边界以及命令行/进程退出归属，并禁止重新引入 Gin 或 HTTP handler 兼容适配器。
路由组件测试覆盖加载失效代际、并发合并、失败重试、模型/Key 游标、跨午夜配额与幂等释放；
现有 server 集成测试继续覆盖真实存储、凭据撤销、故障转移、鉴权与关停。
配置导入本身不创建文件；启动仍通过 `go run . --config <path>` 显式执行。

性能路径的回归按所属组件维护：语义克隆/归一化由 `semantic_document.go` 提供，
typed mapping 和转换规则共享该实现；文档预算检查集中在 `document_metrics.go`。
普通 HTTP 与流式响应均通过 `gatewayResponsePlan` 接收交付所需信息。

测试优先验证外部行为、资源预算、所有权和快慢路径等价性。分配次数由 benchmark
报告，不在普通单测中固定为零；不为内部优化阈值或调用同一实现的包装函数重复建立
等价性测试。摘要、缓存和流状态回归分别归入对应组件测试，性能采集入口继续保留。

后台组件定向回归：

```sh
go test -race -shuffle=on -count=1 -timeout=5m ./server \
  -run '^Test(Health|SourceRefresh|Retention|UsageWriter|UsageQueue)'
```

健康调度的阈值、无证据、恢复与路由失效共用一条轻量 SQLite 行为测试；
禁用后热启用使用 `testing/synctest`，不再用无数据库提前返回冒充默认禁用测试。
协议线格式、鉴权与关停取消仍保留真实 HTTP 回归；通用非 2xx 错误不枚举重复状态码。

## 并行与隔离

`-parallel` 限制每个测试进程内的并行用例数，默认使用 `GOMAXPROCS`。
Runner 固定 `-parallel=2 -p=2`；`-p` 控制包级并发，任务槽位控制进程之间的并发。
测量耗时时不要同时运行其他测试或构建，也不要把 `(cached)` 结果当作执行耗时。

## 刷新任务与异步任务等待

```sh
go test -race -shuffle=on -parallel=4 -count=1 -timeout=5m ./server \
  -run '^Test(SourceRefresh|GatewayJobWaitReadOutcomes|GatewayJobExplicitWaitTimeoutPolicies)'
```

刷新调度用例采用轻量文本协议和真实模型发现操作，仍经过协议验证、源保存与 SQLite 提交；
请求屏障期间不重复验证全部预置协议组合。完成检查等待已注册任务及其后续刷新结束，
夹具清理先取消、等待后台任务，再关闭存储。生产发现请求的 30 秒期限保持不变。

`gateway_job_wait_test.go` 使用 `testing/synctest` 虚拟时间控制到期时机，覆盖读取中到期、
轮询间到期、调用者取消、普通读取错误和取消写入失败，验证 HTTP 状态、取消意图与任务身份。
真实异步提交、查询及恢复仍由 `gateway_job_test.go` 的集成用例覆盖。

## MCP 定向验证

```sh
go test -race ./server -run '^Test(MCP|AgentRemote|A2A|RemoteThinkingEffortWhitelistAndSchema)' -count=1 -timeout=3m
```

`mcp_sdk_test.go` 使用官方 Go SDK 客户端经过真实 Hertz HTTP/鉴权层验证发现、
工具调用、进度和 Key 撤销；`mcp_cli_tool_test.go` 保留无状态批处理、部分失败、
脱敏及实际 SSE 断开取消的回归。传输层兼容性变化见
[远程 Agent 接入](../docs/remote-agent-api.md#取代手写传输层的破坏性迁移)。

## A2A 定向验证

```sh
go test -race ./server -run '^Test(A2A|AgentRemote|Remote|MCP)' -count=1 -timeout=3m
```

`a2a_sdk_test.go` 使用官方 0.3 / 1.0 客户端验证真实 HTTP/SSE、鉴权撤销、
断开后继续执行、并发重复消息、任务重放、订阅广播及分页淘汰。
`agent_remote_test.go` 保留审批恢复、显式裁决、取消终态和旧任务取代的集成回归。
版本与响应差异见[远程 Agent 接入](../docs/remote-agent-api.md#sdk-线协议迁移)。

## Hertz 原生 HTTP 验证

```sh
go test -race -shuffle=on ./server ./relay ./internal/architecture \
  -run '^Test(Hertz|MCP|A2A|GatewayWebSocket|GatewaySSEAndNDJSON|GatewayFailureEncoding|Shutdown|RequestedShutdown|Preparation|StreamCancellationUsage)' \
  -count=1 -timeout=5m
```

原生 listener 的测试覆盖未完成上传时拒绝、首帧前断连取消、流错误 trailer、
流式 panic、chunked keep-alive、WebSocket 会话清理及强制关停（包括 handler 返回后的 BodyStream）；
MCP/A2A 用官方客户端验证 SDK 消息与原生传输的互操作。
测试输出默认在请求完成后解析一次；仅取消时序用例开启逐 Flush 快照，不对第三方 JSON 库或内部指针装配重复建测。
迁移边界与网络库选择见 [Hertz 迁移说明](../docs/backend-hertz-migration.md)。

## CLI 语法与命令框架定向验证

```sh
go test -race ./server ./agent ./internal/clishell \
  -run '^Test(CLI|MCPCLI|MaskSecret|RedactCommand|MaskedPending)' -count=1 -timeout=3m
```

`cli_framework_test.go` 覆盖 mvdan/sh 支持子集、拒绝 shell 求值、Cobra help 与审批一致性、
请求间 flag 状态隔离及转义/拼接引号密钥的脱敏；现有 CLI 测试保留业务映射、批处理与真实执行回归。
中文 CLI 参考由运行时 Cobra help 生成：

```sh
UPDATE_AGENT_CLI_DOCS=1 go test ./server -run '^TestCLIReferenceUpToDate$' -count=1
```

## SSE 库迁移定向验证

```sh
go test -race ./internal/eventstream ./protocol ./relay ./server \
  -run '^Test(SSE|HTTP|StreamIdle|OpenAIStreamNotCut|GatewaySSEAndNDJSON|GatewayBrokenStream|GatewayResponsesNativeFailure|GatewayFailureEncoding|ProviderFailureRetains|CustomProtocolStream|TransportFailure|FailureEvidence|AgentCaller|AgentFullTurn|AgentGatedToolApproval|AgentStopTurn|AgentKeyCreateSecret|OpenAIChatPassthroughStream|ResponsesPassthroughStream|StreamCancellationUsage|ToolOnlyStreamWithoutUsage|CanceledConnection)' \
  -count=1 -timeout=4m
```

覆盖库适配的完整帧 EOF、LF/CRLF/CR 分隔、BOM、事件 ID、控制帧、整帧预算、写失败及取消，
并验证真实 HTTP 桥接、尾部用量入账、Agent 事件和上游错误归属。
选型与兼容性差异见 [SSE 迁移说明](../docs/backend-sse-migration.md)。

## JSON Schema 反射迁移定向验证

```sh
go test -race ./protocol ./server \
  -run '^Test(Schema|ProtocolSchemaLibrary|ProtocolRevisionAdmin|ProtocolAdminRejects|AgentProtocolV2|CLIReferenceUpToDate|RemoteThinkingEffortWhitelistAndSchema|OperationSchema|CompileChecksNestedOutputSchema)' \
  -count=1 -timeout=3m
```

`protocol/schema_test.go` 使用生产运行时的 santhosh-tekuri/jsonschema 编译器验证 invopop 生成结果，
覆盖全部根节点、递归引用、严格字段、Value 任意 JSON、空值/缺失、命名 map 及结果隔离。
`server/protocol_schema_library_test.go` 验证 HTTP 与 CLI schema 查询；现有 authoring 测试覆盖编辑、验证及启用流程。
输出表示差异见 [JSON Schema 参考](../docs/protocol-definition-reference.md#schema-表示迁移)。

## Sonic JSON Pointer 定向验证

```sh
go test -race ./protocol ./protocol/builtin ./server \
  -run '^(Test(JSONPointer|RequestViews|RequestResources|Value|Expression|CompileChecksNestedOutputSchema|Native|MutationTransaction|TypedMeter|RequestWireSchemaTypedParity|JSONModuleFallbackMatchesShippedSamples|HTTP|GatewaySSEAndNDJSON|GatewayBrokenStream|AgentProtocolV2)|FuzzJSONPointerObjectLookup)' \
  -count=1 -timeout=3m
go test ./protocol -run '^$' \
  -fuzz '^FuzzJSONPointerObjectLookup$' -fuzztime=10s -parallel=2
GOMAXPROCS=1 go test ./protocol -run '^$' \
  -bench '^BenchmarkJSONTraversal/pointer' -benchmem -benchtime=500ms -count=5
```

覆盖重复键、转义/Unicode 键、精确数值、缺失与 null、索引校验、深层兄弟节点及内存所有权。
选型与前后基准见 [JSON 遍历评估](../docs/backend-json-traversal-benchmarks.md)。
本次采用 Sonic Pointer 定点读取；全量 AST 容器替换因小对象、宽对象和数组场景回退而未采用。

## 运行时 Schema 校验迁移

```sh
go test -race ./protocol ./protocol/builtin ./server \
  -run '^Test(RuntimeSchema|Expression|Compile|Operation|Model|Request|JSONModule|Native|Mutation|Typed|Verification|Schema|AgentProtocolV2|ProtocolRevision|ProtocolAdmin|CLIReference)' \
  -count=1 -timeout=3m
go test -race ./protocol/builtin \
  -run '^Test(ShippedDefinitionsVerify|BuiltinVerifiedProfilesKeepTextRoutesAndFullDiagnostics)$' \
  -count=1 -timeout=3m
GOMAXPROCS=1 go test ./protocol -run '^$' \
  -bench '^BenchmarkRuntimeSchema' -benchmem -benchtime=200ms -count=3
```

覆盖精确数字、nullable/enum、缺失、对象与数组约束、资源边界、并发复用和操作元数据隔离。
运行时与编译成本分别测量，含回退结果；见[迁移报告](../docs/backend-runtime-schema-migration.md)。

## Expr 表达式执行评估

结论为暂不采用 Expr：原生语义存在差异，适配层仍需保留精确值、预算及诊断逻辑，且多数已测路径回退。
一次性原型已删除；历史代码和复现步骤见[评估报告](../docs/backend-expr-evaluation.md)。

## sqlc 增量查询验证

```sh
go test -race ./storage ./server \
  -run '^Test(ListGroups|Group|CreateGroup|AddGroup|RemoveGroup|DeleteGroup|SourceModelsGroupsTokensAndUsage|ModelGroup|UpsertGroup|RuntimeConfig|AdminListGroups)' \
  -count=1 -timeout=3m
GOMAXPROCS=1 go test ./storage -run '^$' \
  -bench '^BenchmarkListGroups$' -benchmem -benchtime=200ms -count=3
```

固定的模型组和 settings 查询交给 sqlc，`ListGroups` 消除 N+1；运行时和生成器共享一份 schema，业务测试覆盖查询结果。基准和范围见 [sqlc 迁移报告](../docs/backend-sqlc-migration.md)。

## 原子文件写入验证

```sh
go test -race ./internal/atomicfile ./config ./storage ./server \
  -run '^Test(WriteFile|Config|AgentSettingsSave|EnsureConfig|Bootstrap|DBEncryption|Secret|ModelCatalogCache|Catalog|ProtocolSnapshot|SnapshotCleanup|RuntimeConfigAtomic)' \
  -count=1 -timeout=3m
GOOS=windows GOARCH=amd64 go test ./internal/atomicfile \
  -run '^$' -c -o /tmp/opencode/atomicfile-windows.test.exe
GOMAXPROCS=1 go test ./internal/atomicfile -run '^$' \
  -bench '^BenchmarkAtomicWrite$' -benchmem -benchtime=200ms -count=3
```

验证配置、密钥、模型目录缓存和协议快照发布的完整替换、失败保留、权限及临时文件清理；不宣称父目录 fsync 耐久性。详见[原子写入迁移报告](../docs/backend-atomic-file-migration.md)。
