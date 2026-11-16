# Agent Note: 单次启动刷新与网关热路径

Status: implemented

## Problem

启动时反复编译四个预置协议包：服务的惰性构造可能先加载一次，随后预置恢复和运行时重载又分别创建编译会话。临时编译图使堆增长到远超存活注册表的规模，初始化后仍占据大量驻留内存。网关请求还会围绕转换代际的 SQLite 读取串行加锁，并重新构造出站输入，仅为使用 `reflect.DeepEqual` 与预检输入比较。

## Decision

启动构造协议服务时不执行惰性重载，而是使用一个 `CompilationSession` 完成一次完整的刷新与发布尝试。原有基线、证据重读仍保护发生变化的恢复计划。存储中的预置定义若与内嵌定义完全一致或 JSON 等价，不再额外编译；其他拼写及默认值表示仍遵循原有校验和修复规则。发布仍重读权威修订与验证证据。初始化失败时管理功能保持可用、生成保持禁用；若完整刷新因转换错误以外的原因失败，仅在该失败路径上补跑一次仅预置的刷新与发布，使预置仍可提交并用于修复。成功初始化后调用一次 `debug.FreeOSMemory()`；管理重载路径不执行内存回收。

`Store` 在打开时加载持久化的转换代际。涉及触发器覆盖表的写操作在事务内读取变更后的代际，提交成功后才以单调方式发布到原子计数器。源、模型、绑定的单条写操作现在也使用同一组事务辅助函数。审计覆盖源创建、设置、密钥、启停、带绑定保存、替换/合并/手动/发现模型目录、模型可用性/编辑/删除、模型组保存/删除、受管理绑定、转换策略激活、协议刷新/归档、函数工具启用及 `ExecRaw`。仅修改组成员的操作以及无关的令牌、日志、续接记录写操作不涉及触发器覆盖表。数据库代际和乐观并发检查仍是权威，不改变数据库结构。

网关转换快照在未变更路径上只读取原子指针和代际，仅重建时加锁。候选的共享准备函数为资格检查与转发构造实际出站请求和求值选项。首个可用的生成候选保留编码及其诊断收集器。其他候选或已释放候选在派发时重新编码，缓存断点合成也会对真正变化的输入重新编码。旧的深比较和编译器编码复用能力标记被移除。`finalizeRequestWire` 保持不变。

架构允许列表显式登记已有且不依赖其他内部包的 `internal/expressionencode` 生成器；初次架构检查因缺少此条目而失败。

## Measurements

环境：Apple device、macOS、Go 1.27.3 darwin/arm64。结果为单次观测，不是统计基准。临时测试打开全新的临时配置与 SQLite 存储，在测量前执行 GC/内存回收，仅计时 `initializeProtocolRuntime`。健康重启先在计时区间外初始化数据库。`TotalAlloc` 是区间增量；堆字段在初始化返回后立即采样。临时 Go overlay 在 `Compiler.Compile` 入口增加原子计数，生产代码不包含测量日志或钩子。临时测试和 overlay 在测量后删除。

以下命令以仓库根目录为起点；实际执行使用绝对 worktree backend 路径，参数一致：

```sh
go -C backend test -overlay=../.tmp-dev/startup-measurement/overlay.json ./server -run '^TestStartupMeasurement$' -count=1 -v
MEASURE_RESTART=1 go -C backend test -overlay=../.tmp-dev/startup-measurement/overlay.json ./server -run '^TestStartupMeasurement$' -count=1 -v
```

| 场景 | 耗时 ms | 编译次数 | TotalAlloc MiB | HeapSys MiB | HeapReleased MiB |
| --- | ---: | ---: | ---: | ---: | ---: |
| 全新启动，修改前 | 1759.557 | 8 | 1916.46 | 342.56 | 2.12 |
| 全新启动，修改后 | 1413.019 | 4 | 1297.24 | 251.00 | 128.58 |
| 健康重启，修改前 | 985.234 | 12 | 1650.25 | 390.41 | 46.23 |
| 健康重启，修改后 | 462.090 | 4 | 825.11 | 246.66 | 128.67 |

全新启动观测耗时下降 19.7%、分配下降 32.3%；健康重启分别下降 53.1% 和 50.0%。这些测量早于最终的精确字节比较快捷路径和存储提交辅助函数清理；最终代码未重新测量。`HeapSys - HeapReleased` 不是进程 RSS，因此这里不声称测得 RSS。

验证状态：**pending: test-suite memory fix**。以下检查在要求停止后续全部测试/基准前已经完成：

- `go -C backend vet ./...`：中间版本通过，早于最终存储辅助函数变更。
- `go -C backend test -overlay=../.tmp-dev/startup-measurement/overlay.json ./server -run 'TestGatewayOutboundEncoding|TestGatewayPreparationHandoff|TestProtocolRefreshCompilation|TestProductionStartup|TestProductionReload|TestProtocolStartup' -count=1`：通过，21.395 s，早于最终清理。
- `go -C backend test ./storage -run 'TestCachedConversionGeneration|TestConversionGenerationDiscardedWrites|TestConversionSnapshotPublishesGeneration' -count=1`：使用显式提交后发布的版本通过，0.477 s；随后扩展的写路径测试尚未执行。
- `go -C backend test ./server/... ./storage/... ./internal/... -count=1`：失败/未完成。server 测试进程在内存压力下运行 588.797 s 后被杀死；storage 在 1.769 s 内通过；架构检查报告已有的 `internal/expressionencode` 允许列表缺项。该条目现已修复，但未验证。
- 串行重试 `go -C backend test ./server/... ./storage/... ./internal/... -parallel=1 -count=1 -timeout=30m` 按要求停止，未产生结果。包装器设置 `GOMEMLIMIT=1GiB`、`GOMAXPROCS=2`、`GOFLAGS=-p=1`、`GOGC=50`，并使用 1.75 GiB 进程组 RSS 看门狗。没有可报告的受限套件结果或峰值内存结论。
- 最终 `go vet ./...`、所要求的完整套件、独立 `go test ./internal/architecture -count=1`、race 检查及最终启动/网关测量均待完成。本轮不再运行测试或基准，已停止自己启动的测试进程。`git diff --check` 通过。

## Alternatives considered

**只在两次启动刷新之间共享会话。** 用户否决：这仍保留重复的刷新/发布编排，也未解决仅启动所需的惰性重载。一次刷新/发布尝试直接移除重复工作，而不是仅掩盖其中一部分。

**TTL 缓存加请求指纹。** 用户否决：TTL 会允许路由/绑定证据过期，指纹仍需遍历/分配请求输入并引入另一套失效契约。提交后代际发布和明确拥有的出站输入不需要这两种机制。

**SQLite preupdate/commit 钩子。** 实施中否决：SQLite commit 钩子在提交最终成功之前执行，语句级或保存点回滚还可能丢弃 preupdate 事件。对这些钩子计数可能发布未提交或错误的代际。显式提交后发布保留数据库触发器语义，不在进程里重新实现它。

## Consequences

启动只保留一个已验证注册表，并将编译过程的临时内存归还操作系统，不在管理重载或请求中增加 GC/内存回收工作。未变化的网关转换快照路径既不执行 SQL，也不获取全局锁；普通首次转发不再深比较请求/选项，也不重复克隆和编码。

写操作需要在事务内额外读取一次代际；之前自动提交的覆盖表写操作现在使用显式事务。后续新增的覆盖表写操作必须使用同一组提交辅助函数。其他进程、其他 Store 实例或外部 SQLite 句柄不会通知此原子计数器；受支持的写操作必须经过所属 Store，外部修复需要重新打开存储或权威快照重载。其他候选的编码刻意不保留，以限制准备阶段的生命周期，代价是这些候选被实际派发时需要重新编码。最终验证尚未完成，恢复待办检查前必须修复/限制高内存测试套件。
