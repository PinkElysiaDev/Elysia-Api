# Agent Note: 内存上限与独立后端测试分片

Status: implemented

## Problem

网关在请求突发后保留的 RSS 显著大于存活堆所需。仅启动时归还内存无法回收后续垃圾和保留跨度。进程级预算需要有可用的默认值，不能静默覆盖运维显式设置，也不能把回收变成逐请求 GC。

完整普通后端测试的大部分 wall 时间也集中在单个 server 进程。仅限制资源无法消除重复协议夹具构建，也无法让独立重型分组并发完成。必须保留全部现有测试、断言、用例与样本的选择。

## Decision

- `main` 在构造 server 前应用 `debug.SetMemoryLimit`。现有配置中的正数 `memoryLimitBytes` 精确生效，不做钳制。缺省／零保留运行时对显式 `GOMEMLIMIT` 的解释，包括 `off`；两者都未设置时默认 256 MiB。配置加载／重载拒绝负数。修改需重启。嵌入使用 `server.New` 不改变宿主全局上限。
- 保留 `server/protocol_runtime.go` 中启动时的 `debug.FreeOSMemory()`。生命周期拥有的后台 worker 每秒采样，要求连续空闲五秒、距上次回收至少 30 秒，且自初始基线／上次回收以来累计分配至少 32 MiB，或者未归还空闲跨度至少 32 MiB 且不少于在用堆的一半。分配触发条件也覆盖突发后仍位于在用跨度中的垃圾。
- 请求准入只在现有准入互斥锁下增加 epoch：不逐请求增加计时器、时钟读取、内存统计读取、goroutine 或 GC。worker 还检查脱离浏览器的 Agent 轮次、网关会话、源／目录刷新、用量保留清理、健康探测与任务传输；回收前立即复查空闲状态和准入 epoch，生命周期取消后退出，并在关闭依赖前等待退出。回收期间不阻止新请求准入。
- 复用 `scripts/verification/backend.mjs` 及现有有界调度器／进程组清理。普通 `all` 将每个非 server 包和八个不相交的 server 分片独立调度，默认共享四个槽位。每进程使用 `GOMEMLIMIT=1GiB GOMAXPROCS=2 GOFLAGS=-p=1`、`-parallel=2 -count=1 -timeout=15m`，外部超时仍为 16 分钟。`--jobs=1..4` 调整进程数量。最后的 server 补集自动包含新测试、fuzz 目标与 example。裸 `go test ./server` 仍不分片且完整。
- race 保留独立的 2 GiB／`-p=2` 预算及重型任务串行策略。定向 suite 和默认 conversion 选择不变。CI 在现有 race 层之前调用相同的普通完整入口。可选 `ELYSIA_VERIFICATION_DIR` 将证据保存在工作树的忽略目录，默认位置不变。
- 测试夹具仅在类型化夹具编码与解码后的默认预置逐字节相同时返回精确作者原始字节。修改／自定义包保留各自完整且不同的编码。不修改现有生产编译缓存的身份、准入、激活和验证契约。
- 12 个媒体工具结果用例共享不可变预置及一个 server／store／provider 夹具。每例替换组成员／顺序、转换选择／证据，清零计数器，忘记 round-robin 游标并失效路由。两个 include-delivery 矩阵各按严格模式共享一个加密 store，总计保留全部八个有签名／无签名叶子用例。无签名先运行且不创建 continuation；有签名仍断言恰好一条持久化 continuation。严格流式拒绝和上游调用次数断言不变。

## Alternatives considered

- **逐请求或定时无条件 `FreeOSMemory`：** 否决，因为会引入不必要的延迟和 GC 工作，包括生成期间。启动后仅后台空闲策略执行归还。
- **把预算当作 RSS 硬上限或钳制显式设置：** 否决。Go 上限是受管内存软限制，显式配置必须精确执行。允许过低预算，但可能触发 GC 节流。
- **在 `server.New` 应用进程上限：** 否决，因为测试和嵌入宿主必须保留其全局运行时策略的所有权。
- **仅限制原单体测试进程：** 否决，因为昂贵夹具工作仍串行。六分片实验通过但耗时 327.695 秒；把大补集拆成独立管理／线协议／其他分组移除了这一结构性瓶颈。
- **跳过用例、削弱断言、延长超时、伪造通过证据或增加另一套编译 registry：** 否决。夹具复用现有已验证契约，不删除或放宽任何源用例／样本／断言或 Go 测试超时。
- **无界进程重叠：** 否决。普通层共享四个有界槽位；更重视总内存而非 wall 时间时可用 `--jobs=1`。

## Consequences

默认进程拥有软预算和突发后的回收能力，不在热路径执行回收。空闲检查刻意不是准入屏障：最终快照后立即到达的请求可能遇到 GC 暂停。后台数据库 rollup／日志工作不构成全进程静止保证。预算不包含部分非 Go 分配，不能保证 RSS 硬上限。过低显式预算可能以大量 GC 工作换取内存；下方生产测量报告这一代价，而不是假设它不存在。

验收普通完整运行耗时 239.055 秒，比五分钟少 60.945 秒。这是一次观察，不是冷缓存或统计保证的 CI SLA。此前中间调度方案观察到 292.122–327.695 秒。进程重叠降低相对串行基线的单进程峰值 RSS，但提高聚合 RSS。实测 2829.03 MiB 聚合为 2.76 GiB，处于用户允许的 workflow 3 GiB 范围内；每进程软限制和这一次观察都不保证聚合 RSS 的 3 GiB 硬上限。仅把原夹具放到新调度中就已达到 254.286 秒；夹具变更减少重复工作，但不是 wall 改善的唯一原因。

权威编译清单保留全部 2,349 个原顶层 Test／Fuzz／Example 名称，新增七个测试。最终 JSON 日志对全部 2,356 个最终名称各包含恰好一个终态事件，无删除、遗漏、重复或额外名称：2,341 通过、15 跳过。相同的 15 个需显式启用的性能／在线诊断测试在原运行中也跳过，没有新增 skip。server 清单为 615 → 620，包含原有 example。Node 测试还证明源码声明和合成的未来 Test／Fuzz／Example 名称在普通与 race 计划中均恰好分区一次。

`go vet ./...`、`go test ./internal/architecture -count=1`、全部七个 Node runner 测试、最终普通完整入口和定向共享矩阵检查均通过。额外的组合 shuffled `-race -count=3 -timeout=5m` 检查在 include 矩阵期间于 301.047 秒超时；没有报告 data-race 警告，但该次调用是失败，不是通过的 race 证据。不延长其超时。缩小范围后的三次重复 race 检查中，空闲内存测试通过（1.806 秒），Agent 并发保护通过（1.448 秒）。额外媒体 race 调用在收尾时取消，排队的 include 调用未运行，均不算通过。完整 race 层、Linux CI 执行、冷缓存执行和在线提供商验证为 not measured。测量后删除临时 watchdog、二进制、配置、数据库和日志，不调用外部提供商。

## Measurements

环境：macOS、Apple device、Go 1.27.3。基线为 detached `65a1138b208d274651be07d953940da523ec80d1`。计时测试／构建与生产负载不重叠。普通观察使用上述边界。Python watchdog 约每 200 ms 用 `ps` 采样子孙进程 RSS，将同一时刻的子孙求和得到聚合峰值，在 1,100 秒或 7 GiB 时终止。RSS 是采样峰值，不是内核精确 high-water mark，可能漏掉短命小进程的峰值。测试不使用结果缓存（`-count=1`），构建缓存已热。

### 完整测试 wall 与采样 RSS

| 运行 | Wall 秒 | 单进程峰值 MiB | 聚合峰值 MiB |
| --- | ---: | ---: | ---: |
| 原非 server 包，串行 | 77.138 | 526.66 | 586.95 |
| 原完整 server 测试二进制，不分片 | 410.327 | 1442.53 | 1442.53 |
| 原串行阶段之和 | 487.465 | 1442.53 | 1442.53 |
| 原源码／夹具使用最终四槽位分区 | 254.286 | 975.84 | 2441.42 |
| 最终实际开发／CI 完整 runner | 239.055 | 1023.52 | 2829.03 |

串行总和由分别计时的连续阶段组成，不是新执行的单命令冷构建。保留的原 server 二进制不包含编译／链接；最终 runner wall 包含 Go 命令开销。相对该总和，观察到 wall 降低 51.0%（2.04×）、单进程峰值降低 29.0%，但聚合峰值提高 96.1%。相对相同分区下的原夹具，wall 降低 6.0%，进程／聚合峰值均提高。运行间波动使这些百分比不能仅归因于某个夹具编辑。用户此前提供的非 server 64 秒／server 503 秒是另一组参考，不混入此受控表。

### Server 分片 wall 与测试进程 RSS

原耗时使用保留的原二进制，最终耗时包含各自 `go test` 命令。共享槽位可能延迟任务启动；表中 wall 不含排队时间。RSS 为采样测试进程峰值，不是并发分片之和。

| 分片 | 最终名称数 | 之前秒 | 之后秒 | 之前 RSS MiB | 之后 RSS MiB |
| --- | ---: | ---: | ---: | ---: | ---: |
| Gateway／PackageGateway，除重矩阵 | 86 | 144.122 | 130.230 | not measured，分片身份不可辨 | 1023.52 |
| 管理／生命周期 | 65 | 110.297 | 107.025 | 841.88 | 860.19 |
| 线协议／转换 | 73 | 170.096 | 167.920 | 774.78 | 782.09 |
| Gateway 重矩阵 | 12 | 133.960 | 89.004 | 589.19 | 602.69 |
| Agent | 50 | 75.241 | 79.152 | 484.16 | 526.56 |
| CLI／Ops | 30 | 46.823 | 39.176 | 695.27 | 696.81 |
| Protocol／Preset／RecoveredPresets | 35 | 45.167 | 56.361 | 911.02 | 939.86 |
| 自动补集 | 269 | 52.763 | 57.984 | 932.80 | 969.92 |

原分区最大 975.84 MiB 进程的退出时命令身份被截断，因此不猜测其逐分片归属。聚合峰值仅对上方完整重叠进程树测量，没有逐分片独立隔离测量。

### 非 server 包任务 wall

两列均为相同四槽位分区下的独立包任务，耗时包含命令／构建开销，不含排队。无测试包仍被调度和检查。逐包聚合 RSS 为 not measured；上方整体聚合包含这些任务。较长运行的测试进程采样前／后 RSS 分别为 protocol 354.27／334.00 MiB、builtin 445.95／457.91 MiB、relay 263.39／261.88 MiB、storage 80.97／84.59 MiB。可靠的短命测试进程峰值为 not measured。

| 包 | 之前秒 | 之后秒 | 原始 → 最终名称数 |
| --- | ---: | ---: | ---: |
| root | 2.871 | 4.437 | 0 → 1 |
| agent | 1.552 | 2.023 | 44 → 44 |
| config | 3.500 | 3.897 | 27 → 28 |
| internal/admission | 0.675 | 1.177 | 3 → 3 |
| internal/architecture | 0.546 | 0.686 | 1 → 1 |
| internal/atomicfile | 0.562 | 0.802 | 3 → 3 |
| internal/clishell | 0.240 | 0.216 | 0 → 0 |
| internal/coreplan | 0.769 | 0.733 | 0 → 0 |
| internal/dbpath | 0.184 | 0.184 | 0 → 0 |
| internal/eventstream | 0.854 | 0.940 | 3 → 3 |
| internal/expressionencode | 0.240 | 0.211 | 0 → 0 |
| internal/jsonutil | 0.575 | 0.648 | 3 → 3 |
| internal/outbound | 0.845 | 0.864 | 11 → 11 |
| internal/routing | 1.098 | 1.231 | 16 → 16 |
| internal/semanticmeter | 0.371 | 0.354 | 0 → 0 |
| internal/storagequery | 0.185 | 0.146 | 0 → 0 |
| logging | 0.643 | 0.813 | 4 → 4 |
| protocol | 12.464 | 11.994 | 868 → 868 |
| protocol/builtin | 68.631 | 70.001 | 630 → 630 |
| relay | 24.427 | 25.497 | 15 → 15 |
| storage | 4.155 | 4.155 | 106 → 106 |
| webui | 0.116 | 0.115 | 0 → 0 |

相同分区下，媒体工具结果矩阵顶层执行耗时从 92.49 降至 34.16 秒，include delivery 从 31.41 降至 16.16 秒，package include delivery 从 24.89 降至 12.84 秒。全部 12 + 4 + 4 叶子用例与断言保留。

### 生产 RSS、延迟与 GC 代价

生产二进制通过真实管理 API 创建源、绑定、组与推理 token，使用确定性本地 HTTP 上游。禁用模型目录同步，显式允许 loopback。每个生成响应均验证，不传入继承的 `GOMEMLIMIT`。顺序为原始-1 → 默认-1 → 显式96MiB-1 → 显式96MiB-2 → 默认-2 → 原始-2，每次使用全新数据／配置。每进程执行三批并发 1、每批 2,000 请求，以及三批并发 4、每批 10,000 请求；每批另有 256 个预热请求。因此每变体在每并发下有六个批次观察，不是六台独立主机。RSS 每 100 ms 采样，包含启动／管理设置；负载前空闲 8 秒，负载后空闲 36 秒以覆盖回收冷却期。不调用强制 GC 端点或测试子进程回收器。

| 变体 | 并发 | req/s 中位数 | p50 ms 中位数 | p99 ms 中位数 | 每批 GC 次数中位数 | 每批进程 CPU 秒中位数 |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| 原始 | 1 | 661.419 | 1.508 | 3.989 | 10.0 | 3.690 |
| 默认 256 MiB | 1 | 894.791 | 0.990 | 2.748 | 10.0 | 2.580 |
| 显式 96 MiB | 1 | 774.907 | 1.067 | 4.204 | 43.0 | 3.585 |
| 原始 | 4 | 1458.477 | 2.544 | 8.166 | 37.5 | 14.400 |
| 默认 256 MiB | 4 | 1761.502 | 1.973 | 6.137 | 37.0 | 11.510 |
| 显式 96 MiB | 4 | 1106.806 | 2.769 | 11.321 | 196.5 | 17.965 |

GC 与进程 CPU 增量包含预热和驱动启动；吞吐量／分位数不含预热。CPU 来自 `ps` 进程时间，不是隔离 GC CPU 计数器。隔离的 GC-assist CPU 与节流 limiter 激活为 not measured。不过紧预算暴露了端到端 GC 压力代价：并发 4 下相对默认，吞吐量降低 37.2%，p50 提高 40.3%，p99 提高 84.5%，GC 次数为 5.31×，进程 CPU 提高 56.1%。相对原始，紧预算吞吐量降低 24.1%，p99 提高 38.6%。不能把过低显式预算宣传为免费的内存收益。

默认相对原始在并发 4 下观察到吞吐量 +20.8%、p50 −22.4%、p99 −24.9%，GC 次数相近。这些**不是已证明的提速，也不是零代价保证**：原始吞吐量范围为 987.605–1872.511 req/s、p99 为 5.615–14.770 ms；默认分别为 1458.449–1835.019 req/s 和 5.772–7.646 ms。并发 1 的吞吐量范围分别为 448.047–1040.379 与 586.926–1026.579 req/s。较大且重叠的范围与时间波动使这些观察无法隔离可靠的默认延迟／吞吐惩罚。更大请求、持续接近存活集的压力、恰好在空闲 GC 时到达请求的代价为 not measured。

| 进程 | 负载前空闲 RSS MiB | 负载后 RSS MiB | 空闲36秒后 RSS MiB | 峰值 RSS MiB | 空闲后堆 MiB |
| --- | ---: | ---: | ---: | ---: | ---: |
| 原始-1 | 366.109 | 439.531 | 439.547 | 439.547 | 102.627 |
| 默认-1 | 364.406 | 438.031 | 438.063 | 438.063 | 57.023 |
| 显式96MiB-1 | 236.938 | 213.297 | 213.297 | 258.438 | 54.716 |
| 显式96MiB-2 | 238.891 | 312.625 | 35.797 | 312.625 | 54.751 |
| 默认-2 | 311.000 | 387.281 | 219.406 | 388.516 | 56.438 |
| 原始-2 | 190.063 | 261.563 | 261.594 | 375.219 | 64.254 |

默认-2 在负载后归还 167.875 MiB RSS，但默认-1 尽管堆约 57 MiB，RSS 没有实质下降。默认峰值 388.516–438.063 MiB 并不一致优于原始峰值 375.219–439.547 MiB；两者均低于单独提供的 dev.173 约 485 MiB 参考，这不是新的直接 dev.173 对比。紧预算的一次 RSS 采样低于 Go 报告的已分配堆，两种记账不能互换。交付策略限制受管内存压力并尝试空闲回收，但这些观察没有证明 macOS 下稳定的 RSS 上限或一致的物理页归还。

### 产生数据的命令与清单证明

`R` 表示获授权的工作树 `/Users/dingyi/projects/backend-projects/Elysia-Api/.claude/worktrees/r2-runtime`。下方历史 scratch 命令说明产生数据的 harness；按要求使用后删除，不作为第二套基准框架交付。

```sh
# 原二进制在修改 Go 源码前编译，从 backend/server 执行。
GOMEMLIMIT=1GiB GOMAXPROCS=2 GOFLAGS=-p=1 "$R/.tmp-dev/runtime/server-before.test" -test.v -test.count=1 -test.parallel=2 -test.timeout=15m
# 采样器命令连续执行，不与其他构建／测试竞争。
python3 -I "$R/.tmp-dev/runtime/measure.py" baseline-packages
python3 -I "$R/.tmp-dev/runtime/measure.py" partition-before
python3 -I "$R/.tmp-dev/runtime/measure.py" acceptance
# 所有计时测试结束后构建候选生产二进制并运行配对负载。
GOMEMLIMIT=1GiB GOMAXPROCS=2 GOFLAGS=-p=1 CGO_ENABLED=0 go -C /Users/dingyi/projects/backend-projects/Elysia-Api/.claude/worktrees/r2-runtime/backend build -buildvcs=false -pgo=off -o "$R/.tmp-dev/runtime/gateway-after" .
python3 -I "$R/.tmp-dev/runtime/memory-load.py"
python3 -I "$R/.tmp-dev/runtime/summarize.py"
# 额外有界 race 检查：组合调用超时，缩小范围的检查通过。
GOMEMLIMIT=1GiB GOMAXPROCS=2 GOFLAGS=-p=1 go -C /Users/dingyi/projects/backend-projects/Elysia-Api/.claude/worktrees/r2-runtime/backend test -race ./server -run '^Test(IdleMemory|GatewayIncludeCarrierDeliveryModes|PackageGatewayIncludeRequiresClientCarrier|GatewayMediaToolResultCandidateIsolation)' -shuffle=on -count=3 -parallel=2 -timeout=5m
GOMEMLIMIT=1GiB GOMAXPROCS=2 GOFLAGS=-p=1 go -C /Users/dingyi/projects/backend-projects/Elysia-Api/.claude/worktrees/r2-runtime/backend test -race ./server -run '^TestIdleMemory' -shuffle=on -count=3 -parallel=2 -timeout=2m
GOMEMLIMIT=1GiB GOMAXPROCS=2 GOFLAGS=-p=1 go -C /Users/dingyi/projects/backend-projects/Elysia-Api/.claude/worktrees/r2-runtime/backend test -race ./agent -run '^TestRunTurn_ConcurrencyGuard$' -count=3 -timeout=2m
# 原始／最终清单，包含 example。
GOMEMLIMIT=1GiB GOMAXPROCS=2 GOFLAGS=-p=1 go -C /Users/dingyi/projects/backend-projects/Elysia-Api/.claude/worktrees/r2-runtime/backend test -overlay="$R/.tmp-dev/runtime/baseline-overlay/overlay.json" -list='^(Test|Fuzz|Example)' ./...
GOMEMLIMIT=1GiB GOMAXPROCS=2 GOFLAGS=-p=1 go -C /Users/dingyi/projects/backend-projects/Elysia-Api/.claude/worktrees/r2-runtime/backend test -list='^(Test|Fuzz|Example)' ./...
# acceptance 实际启动的命令（CI 同样使用）。
ELYSIA_VERIFICATION_DIR="$R/.tmp-dev/runtime/acceptance-runner" node "$R/scripts/verify-protocol.mjs" check --suite=all --jobs=4
node --test "$R/scripts/verification/backend.test.mjs" "$R/scripts/verification/runner.test.mjs"
GOMEMLIMIT=1GiB GOMAXPROCS=2 GOFLAGS=-p=1 go -C /Users/dingyi/projects/backend-projects/Elysia-Api/.claude/worktrees/r2-runtime/backend vet ./...
GOMEMLIMIT=1GiB GOMAXPROCS=2 GOFLAGS=-p=1 go -C /Users/dingyi/projects/backend-projects/Elysia-Api/.claude/worktrees/r2-runtime/backend test ./internal/architecture -count=1
GOMEMLIMIT=1GiB GOMAXPROCS=2 GOFLAGS=-p=1 go -C /Users/dingyi/projects/backend-projects/Elysia-Api/.claude/worktrees/r2-runtime/backend test ./server -run '^Test(GatewayIncludeCarrierDeliveryModes|PackageGatewayIncludeRequiresClientCarrier|GatewayMediaToolResultCandidateIsolation)$' -count=1 -timeout=5m
```

`baseline-packages` 使用 Go overlay，将所有已修改且已跟踪的 Go 文件恢复为原提交内容，并移除新 Go 文件；`partition-before` 对非 server 任务使用该 overlay，对 server 使用保留的原二进制及最终 run／skip 分区。修正后的原完整分区通过。此前因错误夹具工作目录或不完整 overlay 导致的环境设置失败被排除，不当作产品失败或通过证据。

原始和最终清单由有界 `go test -list='^(Test|Fuzz|Example)' ./...` 生成，原始使用相同的原源码 overlay。解析的包／名称集合必须满足原始 ⊆ 最终；最终每个包／名称必须在全部最终分片／包 JSON 日志中恰好出现一个顶层终态事件，且不能出现额外名称。不把平台排除的源文件误当作当前平台编译清单遗漏。`summarize.py` 检查这些条件，读取逐任务报告和进程树采样；上述总数为实际检查结果。
