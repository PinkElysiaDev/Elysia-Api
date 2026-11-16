# Agent Note: 编译缓存身份、context 与历史保留修正

Status: implemented

## Problem

[弱引用编译缓存](2026-11-16-compile-cache-and-single-decode.zh.md)可能仅因作者变体使用预置 ID 就将其固定在进程内，导致真正的内嵌执行图无法占据对应的有界槽位。离线 memo 关于 context 仅影响请求预算的说法缺少完整读取清单。移除历史强引用保留后，持久化任务反复加载的执行图还会在 GC 后丢失，尽管进程缓存仍有弱引用条目。入口任务处理（`backend/server/gateway_job.go`）和上游执行（`backend/server/gateway_job_executor.go`）都调用 `LoadRevision`。

## Decision

### 内嵌字节固定规则

进程缓存以**预置 ID** 索引固定条目，保存执行图及完整 `compilationKey`：编译器版本、引擎 limits、feature 身份和精确准入源码字节的 SHA-256。准入还要求该 ID 对应一个内嵌包、编译结果的定义 ID 匹配，以及源码 SHA-256 等于内嵌包 SHA-256。规范化定义 hash 不能替代源码身份；即使仅有空白差异的另一种拼写，也不得被固定。未知 ID 永不固定。

`protocol` 直接嵌入已有 Spec/Mapping 文件，独立核对字节，不信任调用方，也不反向导入依赖父包的 `protocol/builtin`。一次初始化的不可变清单按与 `builtin.Packages`、`CompileSources` 相同的方式拼接源码，不归一化 JSON，并保留 Value 和 hash。不移动或修改任何样例文件。固定条目读取同时核对完整编译键及槽位的内嵌 ID/源码身份。每个预置 ID 最多固定一个内嵌图；其他引擎配置仍可使用普通弱引用缓存。

会话仍按自己的发布契约，为每个已知 ID 保留首次成功源码。但每次成功的 `CompilePreset` 都独立尝试经过源码核对的进程级准入，即使会话首次接收的是变体。因此同一会话中的变体不会阻止随后内嵌源码的准入。

### 完整离线 context 输入清单

清单从 `Verify` / `verifyUncached` 出发，追踪普通夹具、`executeVerificationSample`、`executeEventFixture`、`executeEventFixtureWithState`、`executeEventFixtureStream`、`verifyRoundTrip`、`verifyEventRoundTrip`、`verifyNativeExtension`、`verifyTaskSamples`、`verifySessionSamples`、`verifyAgentSamples`、`verifyModelSamples`、`verifyTokenCountSamples` 及其执行 helper。全仓检索枚举 `backend/protocol` 生产代码中所有 context value 读取、context 构造及取消/deadline 读取，并检查导入的 `internal/eventstream`、`internal/jsonutil`。

| Context 输入 | 读取与传播路径 | Memo 决策 |
| --- | --- | --- |
| `requestBudgetKey{}` / 有效 `RequestBodyLimit(ctx)` | `resource_budget.go`：`RequestLimits` → `wireLimits` → `requestLimits`；`mappingLimits`；`nativeLimits`；`EncodeSemantic`；转换层的 `phaseLimits` / `ruleLimits`。请求解码/编码、operation 输入/输出校验、native replay、token-count 子请求图和 Agent 请求转换会到达这些 helper。`WithRequestBodyLimit` 也读取同一键以保留已固定预算，但离线验证不安装覆盖值。`PreviewEnvelopeLimit` 读取同一值；它属于 authoring envelope，不由 `Verify` 调用。 | 仅有效预算等于 `DefaultRequestBodyBytes` 时符合条件。不同预算绕过 memo；显式固定的默认值具有相同有效语义。 |
| `documentMetricsKey{}` | `document_metrics.go`：`withDocumentMetrics`、`measureDocumentMetrics`、`fitsDocumentMetrics`、`checkDocumentSemantic`、`checkDocumentMapping`。普通/Agent/token-count 请求解码和编码、continuation 提取及 native replay 将 context 传入这些 helper。较大的嵌套调用可创建局部 owner。`document_normalization.go` 的 `normalizeDocumentMapping` 也读取此键，但目前没有生产调用方，不引入额外输入。 | 任何调用方传入值都绕过 memo，包括预算为默认值的 metrics owner。局部创建的 owner 仅存放与策略无关的本次执行计量事实，不逃逸调用。 |
| 取消 / deadline 超时 | `Verify` 及可达 runtime、mapping、expression/evaluation、core control/validation、输出编码、流结束处理、wire contract、model policy、operation plan 路径中的 `ctx.Err()`。保存的 evaluation/validation context 继续转发同一 context。离线路径不读取 `Deadline()` 或 `Done()`，不等待真实 transport；deadline 的影响表现为 `Err() == context.DeadlineExceeded`。 | 获得 memo mutex 后及克隆热报告后再次检查。仅完整通过且 `Err() == nil` 的结果可以存储。已取消、已超时、失败和部分执行永不填充 memo。 |

检索发现的以下其他键**不由离线 `Verify` 读取**。它们属于 `VerifyCombination`、`VerifyBindingCombination`、profile 选择或其专用 helper，不在此 memo 的范围内：

- `conversionVerificationKey{}`：`verification_combination.go` 的转换选择，以及 `verification_event_combination.go` 中的 `verifyEventCombination`。
- `conversionVerificationDiagnostics{}`：`verificationDiagnostics` 及仅组合验证使用的调用方。
- `combinationTransportKey{}`：组合请求/transport 选择及 `verification_profiles.go`。
- `requestScopeVerificationKey{}`：组合/profile 的请求 scope 选择。
- `visibleReasoningVerificationKey{}`：组合可见推理报告选择。

`EvaluationContext` 是另一种类型，不是 `context.Context` 的 value bag。`newVerificationContext` 从不可变夹具派生 scope 和 Values，创建新 state，并固定合成 operation ID/时间。事件夹具增加局部流 validator/replay/budget。Task helper 使用合成选项；session helper 只共享本夹具的局部状态。Agent helper 从选定请求夹具、偏好/model 及已编译转换默认项构造选项。Model 和 token-count operation plan 使用空/默认选项或已编译子图。`WithOutputStream`、`WithConversion`、`WithOperation`、`WithValueReader`、definition/target builder 和 runtime binder 不读取额外 Go context value。Scope、偏好、参数、model、输出控制、native 策略、引擎/作者 limits 和转换选择来自编译结果或夹具，而非环境 context。离线证据不接受 provider、网络、环境变量、时钟或外部 callback 的输入；`VerifiedAt` 是逐次生成的时间戳，合成 operation 时间仍然固定。反射 `MapIter.Value` 和弱引用 `Value()` 不是 context 读取。

Memo 仍是**每个执行图一份报告**，而非按 context 组合索引的 map。非默认请求预算或传入 metrics owner 一律绕过，比为可变 metrics 状态设计键更简单，条件也保持有限且可以直接审计。在与调用方共享证据前，checks、嵌套 capabilities、coverage、issues 和 target 仍被深拷贝，每次调用仍有新的 `VerifiedAt`。

### 有界图保留，不缓存授权

历史 `LoadRevision` 在 `service.mu` 下仅保留完整验证成功后获得的进程缓存图，以 `id + "/" + hash` 为键。任意时刻，所保留规范化定义字节之和不超过 `compiler.limits.BufferBytes`，条目数不超过 `compiler.limits.StateItems`。生产默认值仍是 8 MiB 定义字节和 4096 条目。这些是记账限制，**不是**执行图堆大小的估计。如果单个图的规范化定义就超过字节限制，则不保留。按插入顺序执行 FIFO 最老优先驱逐；命中不刷新顺序。条目、顺序和字节计数一起移除，并清空腾出的顺序槽位。

保留机制不增加第二套编译实现，也不引入命中时跳过授权的路径。每次历史调用仍执行 `loadVerifiedRevision`：重新读取持久化修订，调用现有进程缓存编译器，核对定义 ID/hash，重新读取当前引擎的持久化报告，并执行 `CanActivate`。强引用使执行图保持存活，进程弱缓存即可复用同一个图。仅完整加载成功后才保留。`LoadRevision` 原有 active snapshot 快捷路径不变；激活/重载仍走各自已有的权威发布路径。

失效点明确如下：

- `DeleteRetained` 的 commit 成功后立即删除匹配条目；commit 失败则保留。已有管理 history 删除 handler 正是用此方法包裹 `Store.DeleteProtocolHistory`。
- 任何历史加载失败都会删除匹配保留项，包括持久化修订缺失、报告缺失/过期/失败、编译器/样例不匹配或定义不匹配。因此带外删除在下一次加载时被拒绝并驱逐。
- 容量压力驱逐最老条目。同一修订键成功加载出另一个已准入图时，替换旧保留图且不重复计数。

此前已由进行中请求 pin 的图仍可使用。图保留和离线 memo 均不制造持久化证据或权限。

## Alternatives considered

**信任预置 ID 或规范化 hash。** 不采用：作者变体和其他拼写可以共享这些身份，必须使用精确内嵌字节 hash，并在读取时核对完整键。

**移动内嵌 loader 或添加 provider 专属准入分支。** 不采用：在 `protocol` 中直接嵌入现有源码树，可以核对通用包身份，无需包循环、公开 loader API 或 provider 分支。

**按全部 context 组合索引 memo。** 不采用：metrics owner 是可变的执行局部状态，不是不可变语义缓存键。保守绕过既保留单报告上界，也避免共享调用方状态。

**在查询持久化证据前直接返回保留修订。** 不采用：可能服务已删除或未验证历史。强引用保留仅为现有进程缓存提供生命周期所有者，每次历史加载仍以持久化状态授权。

**LRU 或新增保留限制。** 不采用：现有引擎 `BufferBytes` / `StateItems` 和最老优先策略已经足够。不扩大生产限制。

## Consequences

内嵌预置不会被 ID 冲突挤占。调用方传入 metrics 时保守放弃 memo 加速。历史任务轮询可跨 GC 保持图身份，同时不信任缓存授权；但持久化读取、精确源码 hash 和 Service mutex 仍存在于该路径。历史强引用在原有定义字节/条目限制内延长图生命周期；这些限制不代表执行图 RSS 的硬上限。

测试覆盖变体优先和空白变体准入、同会话内嵌准入、独立于弱条目的热固定读取、未知 ID 及完整键/ID 隔离。Context 测试把其他 context 下的冷/热报告与不缓存执行比较，覆盖取消、超时及部分执行，并确保克隆后的取消不会被隐藏。保留测试在加载之间强制 GC，核对权威重读，针对十二个修订测试两种容量边界，证明 FIFO 命中不刷新年龄，并检查删除、commit 失败、无效证据及恢复。

没有放宽或删除任何既有语义断言或夹具。预置不可变性哨兵仍将完整执行图指纹与一次新的不缓存编译结果比较。参考编译改用**精确准入的内嵌字节**，而非会压缩所保留字面量拼写的规范化 `compiled.definition`。非字符串 map 键改按完整键指纹排序，而非 `reflect.Value.String()` 占位字符串；后者导致 `map[Value]` 的顺序不确定。所有键和值仍参与指纹，回归测试核对顺序稳定并能检测键/值变化。新增源码/键身份检查进一步加强哨兵。条目边界夹具使用既有四样例夹具所需的最小 `StateItems`，而非删除样例以迁就无效的更小测试配置。

## Measurements

环境：Apple device、macOS。修改前测量使用提交 `a9fc1b1d`；修改后包含新增回归测试和真实内嵌预置哨兵。这些是单次有界运行，不是隔离系统吞吐量对比。

每条 Go 命令均使用 `GOMEMLIMIT=1GiB GOMAXPROCS=2 GOFLAGS=-p=1`。测试另加 `-parallel=2 -count=1`。忽略目录中的 `.tmp-dev/testperf/fable-c/watch.py` 创建独立进程组，每 100 ms 采样组内 RSS 总和，达到 2,000,000,000 字节时终止整个进程组，并包裹 `/usr/bin/time -l`。未运行完整 server 测试包、完整模块测试或 server race 测试。

以下命令从仓库根目录执行（每条均加前缀 `python3 -I .tmp-dev/testperf/fable-c/watch.py <label>`）：

```sh
go -C backend test ./protocol -parallel=2 -count=1
go -C backend test ./protocol/... -parallel=2 -count=1
go -C backend test ./internal/architecture -parallel=2 -count=1
go -C backend test ./server -run 'TestProtocol|TestProductionStartup|TestProductionReload|TestPreset|TestGatewayJob' -parallel=2 -count=1
go -C backend vet ./protocol/... ./server ./storage
```

| 运行 | 修改前 wall s | 修改后 wall s | 修改前进程组峰值 RSS MiB | 修改后进程组峰值 RSS MiB | 修改前最大单进程 RSS MiB | 修改后最大单进程 RSS MiB |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| 完整 `./protocol` | 13.688 | 8.411 | 980.8 | 957.9 | 937.8 | 560.8 |
| 上述聚焦 server 正则 | 31.165 | 34.696 | 963.1 | 985.4 | 908.3 | 926.5 |

Go 报告的包执行时间为：`./protocol` 7.968 → 7.927 s；聚焦 server 20.947 → 26.029 s。外层 wall 时间包含编译、工具启动和 watchdog 收尾；构建缓存状态及系统活动不同，不能把这些单次差异归因为吞吐量变化。Server 观察值更慢，不声称加速。所有运行均在 watchdog 阈值以下完成。

其他检查均通过：`./protocol/...` 用时 49.675 s，进程组峰值 RSS 为 583.1 MiB（`./protocol` 5.348 s，`./protocol/builtin` 38.018 s）；architecture 用时 0.968 s、84.2 MiB；vet 用时 4.520 s、322.4 MiB。聚焦修正/指纹回归使用 `-run 'TestCompilationCache|TestCompilationSessionVariant|TestVerificationMemo|TestLoadRevision|TestCompiledFingerprint'`，7.615 s 通过，进程组峰值 1000.3 MiB。Watchdog 未触发终止。`git diff --check` 干净。

受本任务运行边界限制，完整后端 suite、完整 server 包、race 测试及真实上游/provider 验证有意保持未验证。不基于 watchdog 终止的运行提出任何性能结论。
