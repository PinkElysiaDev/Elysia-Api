# Agent Note: 有界后端测试分层

Status: implemented

## Problem

重复的 server 夹具反复编译、验证相同的内置协议和源绑定组合。Go 默认的包级与测试级并发放大这项工作；完整 race 检查此前使用单个 60 分钟测试进程及 65 分钟命令超时。已报告的无界运行占用 7–12 GB、耗时 20–40 分钟以上。仅限制资源既不能消除重复夹具工作，也无法定位单体 race 运行中哪个部分失败。

## Decision

- `scripts/verification/backend.mjs` 保留现有 JSON/证据 runner。`fast` 选择所有非 server 包；`all` 增加所有 server 测试。现有 `conversion`、`gemini-stream`、`output` 名称及默认 `conversion` 选择不变。通过 Go 发现包，不维护包白名单。
- 普通层默认使用 `GOFLAGS=-p=1`、`GOMAXPROCS=2`、`GOMEMLIMIT=1GiB` 及 `-parallel=2 -count=1 -timeout=15m`；每项任务的外部超时为 16 分钟。`--jobs=1..4` 控制任务槽位，而非 Go 包并发。普通完整检查将非 server 包与八个不相交的 server 分片独立调度，默认共享四个槽位。race 保留 `GOFLAGS=-p=2`、`GOMEMLIMIT=2GiB` 及默认两个槽位。当前集成策略和测量见[内存上限与测试分片](../performance/2026-11-16-memory-limit-and-test-shards.zh.md)。
- 完整 race 检查采用独立包任务和四个不相交的 server 任务：Gateway/PackageGateway、Agent、Protocol/Preset/RecoveredPresets，以及 `-skip` 补集。protocol、builtin 和 server race 任务占满槽位，其他包任务在有界槽位内并行。每项任务保留独立日志和证据；超时或中断终止整个进程组/树，POSIX 下进一步升级为 KILL。
- 预置夹具保留内嵌包的精确原始字节，只读取 `spec.id`。完整包反序列化后重编码会实体化省略的默认字段，导致启动为证明等价性再次编译存储中的修订/草稿。播种并发布使用现有生产 `CompilationSession`，发布后关闭；紧接初始化的播种跳过多余 reload。这不是测试自建的编译缓存或 registry。
- Agent 执行夹具复用现有 `fixtureBindingReports` 证明，同时使用真实 SQLite 源、模型、绑定并失效路由。源作者路径仍由 `TestSourceSaveIsolatesUnrelatedInvalidBindings`、`TestSourceSaveKeepsIndependentModelProtocolOverride`、`TestSourceBindingUpdatePersistsInheritedEvidence` 运行生产验证；部分 Agent 用例也保留 `saveSource`。
- 用量正文策略按每种策略共享一个 server/store，而非按端点/结果创建。保留全部 18 个叶子用例和断言。每例结束后排空并停止 usage writer、清除用量记录和媒体文件，再运行下一例。下一例重启 writer，保留异步路径和精确的一条记录断言。
- 两个顺序执行的 Agent 思考矩阵只共享不可变运行时，每例完整替换源/模型/绑定状态。恢复缺陷和会修改状态的工具探针保留独立 store/server。恢复初始夹具复用已验证报告；修复及第二次幂等性初始化仍走真实路径。期望预置哈希复用损坏前已验证的 view，不再仅为断言额外编译。
- 新增回归覆盖非隔离的自定义刷新失败后预置仍被发布，以及缓冲源事件展开为多个目标事件时的语义序列冲突。不删除或放宽任何样本、缺陷用例、子测试、断言、协议限制或生产超时。

## Alternatives considered

**测试专用播种 registry。** 用户已否决，选择单独实现的生产编译协议缓存。本变更独立于该缓存，只使用正常持久化的证据和单次发布范围内的生产 session。

**gotestsum。** 否决，因为它重复现有 runner 的 JSON 解析和每任务证据/报告处理。

**仅限制资源。** 否决，因为重复夹具工作仍然存在，race 任务仍是单体，无法准确归因超时。

**共享可变夹具。** 对恢复损坏和工具探针冲突用例否决。这些用例继续保留新状态；只有能证明可以完整替换的顺序夹具共享 server。

## Consequences

`backend/TESTING.md` 的日常命令使用有界分层，CI 同时限制 vet/build 并发。server race 选择通过补集涵盖现有 fuzz 种子及未来名称。Node 分区测试扫描当前顶层声明；另一次有界 `go test -list` 检查观察到 612 个名称各落入一个分片：gateway 98、Agent 49、protocol 34、其他 431。

共享夹具测试在正常用例表顺序和临时反转的用例表顺序下均通过 `-shuffle=on -count=3`。路由失效和存储转换代际刷新替换后的契约；调用同步完成、预留被释放、不复用会话/continuation，策略用例间排空用量队列。专门的源验证测试、两个新增边界回归、runner 测试及 `go vet ./server` 均通过。

GOMEMLIMIT 是 Go 运行时软限制，不保证整个进程树的 RSS。下方独立测量早于生产缓存的集成；当时没有运行整个 server 包或 `go test ./...`。当前完整普通层结果和剩余 race 验证限制记在上述集成说明中。没有增加测试清理阶段的主动内存回收。

## Measurements

基于 `39b5a3fd`，在运行 macOS 的 Apple device 上单次观察，**不包含**并行实施的生产缓存。其他工作同时进行，因此不是统计比较。wall 时间包括编译；下表 RSS 是 `/usr/bin/time -l` 的最大 RSS，单位 MiB。watchdog 另行汇总整个进程组的 RSS，并在 2 GiB 时终止。

| 定向 `-run` | 之前 wall | 之后 wall | 之前峰值 RSS | 之后峰值 RSS |
| --- | ---: | ---: | ---: | ---: |
| `^TestGatewayUsageBodyPolicy$` | 48.81 s | 13.73 s | 1038.7 MiB | 886.7 MiB |
| `^TestPresetRecoveryDefectsAndIdempotence$` | 43.33 s | 25.33 s | 711.0 MiB | 718.3 MiB |
| `^TestAgent` | 186.74 s | 89.00 s | 1908.6 MiB | 1812.6 MiB |
| `^TestChatCompletions` | 27.27 s | 19.29 s | 672.4 MiB | 546.2 MiB |

最终进程组峰值依次为 940.8、747.5、1832.8、581.2 MiB；所有最终运行均通过且未触发 watchdog。用量策略 JSON 中的测试执行时间从 36.57 s 降至 5.09 s。后台生命周期/传输定向组耗时 85.98 s，最大 RSS 为 1448.5 MiB。共享夹具三次 shuffle 重复耗时 34.62 s；反转用例表后三次重复耗时 39.86 s。最终 `go vet ./server` 在相同内存/并发限制下用时 2.64 s 并通过。

精确字节播种修复前的一次中间完整 Agent 运行触发 watchdog 上限。同一子组 `^TestAgent(Thinking|ToolProbe)` 的 `-memprofile` 对比显示，采样分配空间从 24040.73 MiB 降至 15508.67 MiB，结束时存活空间从 44.22 MiB 降至 41.39 MiB。存活内存主要是 Sonic decoder 池，而非被保留的编译图；记忆化的绑定报告约占 4 MiB。最终精确字节/session 播种及两个 Agent 源夹具 helper 在不主动回收内存的情况下解决完整组退化。

精确测量形式如下，从仓库根目录执行（忽略目录内的 watchdog 调用 `/usr/bin/time -l`，设置 `GOMEMLIMIT=1GiB GOMAXPROCS=2 GOFLAGS=-p=1`，采样进程组 RSS，并在 2 GiB 时终止）：

```sh
python3 -I .tmp-dev/testperf/fable-a/watch.py shipped-usage go test ./server -run '^TestGatewayUsageBodyPolicy$' -parallel=2 -count=1 -timeout=15m -json
python3 -I .tmp-dev/testperf/fable-a/watch.py shipped-recovery go test ./server -run '^TestPresetRecoveryDefectsAndIdempotence$' -parallel=2 -count=1 -timeout=15m -json
python3 -I .tmp-dev/testperf/fable-a/watch.py shipped-agent go test ./server -run '^TestAgent' -parallel=2 -count=1 -timeout=15m -json
python3 -I .tmp-dev/testperf/fable-a/watch.py shipped-chat go test ./server -run '^TestChatCompletions' -parallel=2 -count=1 -timeout=15m -json
python3 -I .tmp-dev/testperf/fable-a/watch.py shipped-lifecycle go test ./server -run '^Test(Health|SourceRefresh|Retention|UsageWriter|UsageQueue|Shutdown|RequestedShutdown|Preparation|StreamCancellationUsage|GatewayJob|GatewayWebSocket|Hertz|MCP|A2A)' -parallel=2 -count=1 -timeout=15m -json
node --test scripts/verification/backend.test.mjs scripts/verification/runner.test.mjs
```

变更后的定向 JSON 耗时仍包括夹具工作，因为此处没有生产缓存。剩余最大的固定传输等待是 `TestShutdownForceClosesStuckHTTPConnection` 中两次 5 秒生产 HTTP 关停期限；没有可注入的测试专用超时，因此保留真实行为和断言。其余 Agent、健康、恢复和源刷新热点主要在编译/验证协议或绑定矩阵，而非可避免的 sleep。全局最慢测试排名属于集成完整测试证据，而不是这些定向组。
