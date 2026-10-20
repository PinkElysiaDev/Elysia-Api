# 后端性能基准

[English](backend-performance.en.md) · [开发指南](development.md)

本工具用于性能优化 issue [#65](https://github.com/PinkElysiaDev/Elysia-Api/issues/65) 的基线采集与后续对照。第一阶段记录分配次数、累计分配量、内存峰值和空闲恢复行为，并定位热点；之后分别推进局部优化、执行计划与 typed 流水线、所有权与池化。

## 运行

在仓库根目录执行，需要 Node.js、项目要求的 Go 工具链以及本地回环网络。所有 provider、凭据和数据库都是测试夹具，无需真实供应商账户。

```sh
# 完整采集：微基准、HTTP 场景、独立 CPU/分配 profile、取消诊断。
# 输出目录必须为空；构建、压测、profile 全部串行执行。
node scripts/benchmark-performance.mjs --output=.tmp-dev/performance-baseline

# 快速验证夹具；少量请求的尾延迟不具备统计稳定性。
node scripts/benchmark-performance.mjs --mode=measure \
  --cases=chat-short,stream256-cross,candidates4 \
  --requests=2 --repeats=1 --idle-ms=100 \
  --output=.tmp-dev/performance-smoke

# 单独重复正式测量，扩大样本数和空闲观察窗口。
node scripts/benchmark-performance.mjs --mode=measure \
  --cases=chat-short,burst4,stream256,stream1024 \
  --requests=128 --repeats=5 --idle-ms=10000 \
  --output=.tmp-dev/performance-repeat

# 独立归因；每个场景分别运行 memory 和 CPU 模式。
node scripts/benchmark-performance.mjs --mode=profile \
  --profiles=chat-short,stream256,image2 --seconds=30 \
  --output=.tmp-dev/performance-profiles
```

默认固定 `GOMAXPROCS=4`、`GOGC=100`、`GOMEMLIMIT=2GiB`，允许环境变量覆盖。构建启用 `sonic` 并关闭 PGO。使用同一机器、相同参数和独立输出目录比较；测量期间避免并行构建或其他压测。

`--mode=micro` 单独运行现有 protocol/builtin 微基准，每项五次、每次 300ms。它包含短请求完整语义管线、请求负载、流解码、typed 资源检查和运行时 schema 检查。

## HTTP 场景

| 场景 | 内容 |
| --- | --- |
| `chat-short`、`anthropic-short`、`responses-short`、`gemini-short` | 四种内置协议的同协议短请求 |
| `chat-anthropic`、`chat-responses` | 跨协议短请求 |
| `prefix32k`、`history256k`、`tools32` | 长前缀、长历史、32 个工具 |
| `candidates4` | 四个候选的准备成本，仍断言每请求只有一次上游生成 |
| `reload-chat` | 重载协议运行时并失效路由缓存；单独记录重载成本和其后第一个请求 |
| `burst4`、`burst4-body` | 四 worker 的闭环并发突发，分别关闭/开启 body 日志 |
| `stream16`、`stream256`、`stream1024` | 16/256/1024 个 256 字节文本增量，另有生命周期及用量帧 |
| `stream256-cross`、`stream256-burst4` | 跨协议流、四 worker 并发流 |
| `image2`、`image2-media` | 工具结果内含 2 MiB base64 文本，分别关闭 body 日志/开启媒体外置 |

每场景默认三轮，每轮 32 个请求；图片为每轮四个，重载为每轮一个。后两项是明确的场景覆盖值，不受 `--requests` 覆盖。所有请求都校验返回正文和用量；矩阵 provider 校验历史文本和工具保留，检查上游调用数与请求数一致。

`off` 只关闭 body 捕获，usage 计量和 SQLite 持久化仍开启。`body`/`media` 使用现有夹具的 64 MiB body 上限，`media` 还启用媒体外置。它们是固定测试配置，不代表默认部署的日志大小。

取消诊断在完整模式下独立运行：2 MiB 请求、四个并发流、五批，在实际收到数据帧后取消，检查上游调用数及强制 GC 后内存。

## 测量边界

- 客户端、网关、provider 为三个进程。分配、CPU、GC 和内存指标只来自网关子进程。
- `first-request` 是已初始化测试服务的第一个业务请求，包含尚未预热的请求路径；不包含进程启动、数据库建表和协议夹具验证。
- 正式测量前执行四次预热。正式计时和空闲恢复期间不主动 GC，也不重启 usage writer。
- HTTP latency 从发送请求前到完整消费响应为止；每次完整校验仍在客户端完成。吞吐是包含客户端校验成本的闭环完成速率，不是网关最大服务能力。
- `FirstByte` 是第一个响应 body 字节；`FirstFrame` 是第一个完整 SSE 帧，JSON 则是完整正文。这些指标不等同于首个模型文本 token。
- 分配和 CPU 计数采到 usage 写入完成，HTTP 延迟在响应消费结束时停止。因此计数包含异步日志结算，延迟不包含排队日志的后续等待。
- `settle` 等待请求完成和 usage 提交，保留 writer 本身；旧 `drain/reclaim` 只用于独立强制回收诊断。
- 每轮提交完成后，在 0%、10%、50%、100% 空闲观察窗口取样。默认窗口 1 秒，可用 `--idle-ms` 延长。恢复点保留累计 GC/CPU/分配计数，能够区分自然 GC 与单纯等待。
- 20ms 采样记录 HeapAlloc、HeapInuse、HeapIdle、HeapReleased、RSS、goroutine 和活动请求数。峰值是采样峰值，可能漏掉短尖峰。时间线最多 9,000 点，丢弃数显式记录。
- HeapAlloc 包含尚未 GC 的垃圾，不等于精确存活对象量。Linux RSS 来自 `/proc/self/statm`，Windows 使用工作集；其他平台或读取失败时为不可用，不应把零解释为实测零内存。
- metrics HTTP、20ms sampler、后台运行时均在网关进程内，计数包含其小量开销。时间线只在正式计数完成后导出，避免 JSON 导出直接放大该轮结果。

百分位采用 nearest-rank，汇总显示各轮指标的中位数，保留逐请求原始观察值。默认样本用于成本定位；不要把 32 次请求的 P99 当作稳定的生产尾延迟估计。不同流长度用于比较成本随帧数增长的趋势，不能把整请求分配除以增量数称作纯 decoder 分配。

## 证据与归因

输出目录包含：

- `report.json`：commit、工作树状态、Go/CPU/环境、构建参数、二进制 SHA256、逐条命令、退出状态和汇总。
- `source.patch`、`runner.mjs`：当前差异及采集器源码。优先从干净提交采集；其他未跟踪源文件需自行纳入版本管理。
- `micro.log`：五次微基准原始结果，可交给 `benchstat`。
- `measure-*/measurements.json`：逐轮、逐请求、自然恢复点和时间线。
- `memory-*/*.pprof`：分配前后、活动时刻和排空后 heap profile。
- `cpu-*/cpu.pprof`：独立 CPU 运行的 profile。
- `alloc_space-top.txt`、`alloc_objects-top.txt`、`inuse_space-top.txt`、`cpu-top.txt`：采样归因。
- `cancellation-*.json`、`cancellation.log`：独立取消诊断。
- `manifest.json`：证据文件校验和；`summary.md`：简表。

profile 模式会在测量前主动 GC，memory 模式在结尾再次 GC 以更新 heap profile；其计时不用于正式性能结论。分配 profile 使用 before/after 差分以减少初始化干扰，但仍含采样器开销；默认 Go heap profile 是采样结果，不是逐对象精确账本。`heap-active` 是负载启动后约 250ms 的快照，短负载可能已结束，因此不将其宣称为峰值堆。

```sh
go tool pprof -top -sample_index=alloc_objects \
  -base .tmp-dev/performance-baseline/memory-stream256/alloc-before.pprof \
  .tmp-dev/performance-baseline/server.test \
  .tmp-dev/performance-baseline/memory-stream256/alloc-after.pprof

go tool pprof -http=127.0.0.1:0 \
  .tmp-dev/performance-baseline/server.test \
  .tmp-dev/performance-baseline/cpu-stream256/cpu.pprof
```

后续优化以相同场景复测：同时比较 allocs/request、B/request、CPU/request、峰值和空闲恢复曲线。减少分配不能以持续抬高驻留内存为代价。零分配预算限定在具体 helper/稳定阶段，HTTP 管线预算应在获得足够重复样本后制定。原始证据保留于 CI artifact 或本地输出目录；PR/issue 发布结果摘要与采集条件。
