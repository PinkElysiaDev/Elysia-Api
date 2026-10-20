# 后端性能基准

[English](backend-performance.en.md) · [开发指南](development.md)

本工具用于性能优化 issue [#65](https://github.com/PinkElysiaDev/Elysia-Api/issues/65) 的基线采集与后续对照。第一阶段记录分配次数、累计分配量、内存峰值和空闲恢复行为，并定位热点；之后分别推进局部优化、执行计划与 typed 流水线、所有权与池化。

实测版本对照：[v1.6.2 与 `5a8115e` 的 profiling 对比](backend-performance-comparison-v1.6.2.md)（同机重测、原始证据、内存回退及热点分析）。

后续定向优化：[issue #77 三批次优化与最终 profiling 对照](backend-performance-targeted-77-final.md)（局部验收、签名 PR、累计分配及峰值改善、计时波动和未完成项）。

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

## 局部复用与分配预算

局部优化使用资源所属的请求、流或 writer 管理生命周期：

- SSE 完整性检查只持有四个尾部字节；分片读取和 EOF 检查不创建尾部字符串。
- 每个上游流复用一个 idle timer 和通知 channel。每次 Read 结束后停止 timer；若回调已触发则先等待回调通知，再允许下一次使用。消费处理时间不计入上游空闲，Close 通过取消请求中断正在等待的 Read。
- usage writer 复用容量为 64 的批次存储和 timer，在提交完成（包括失败）后清除批次引用，维持原有 50ms 收集窗口和 payload 预算。
- 最终 wire 校验对 typed event decoder 直接读取结构化事件，同时保留语义预算、JSON 规范化和可变容器隔离；最终字节检查和生命周期 replay 仍执行。JSON-only decoder 保留通用路径。
- 语义深克隆只为可变容器及目标结构申请存储，标量通过容器字段赋值复制；不可变 Value 字节仍可共享。

下面的 helper 基准分别测量分片 SSE 尾部检查、已建立流的单次读取，以及 256 个增量加起止帧的最终校验。前两者的稳定路径目标为 `0 B/op, 0 allocs/op`，不包含流的构造、网络传输或任意 Reader 的内部成本。

```sh
cd backend
GOMAXPROCS=4 go test -tags=sonic -pgo=off \
  ./internal/eventstream ./relay ./protocol/builtin -run '^$' \
  -bench '^(BenchmarkSSECompleteReader|BenchmarkStreamBodyRead|BenchmarkFinalWireValidation)$' \
  -benchmem -benchtime=300ms -count=5
```

`TestFinalWireValidationTypedParity` 使用相同的内置协议样本比较 typed 和 JSON 最终校验，包括失败位置和终态。定向回归还覆盖 timer 重用、消费者暂停、并发 Close、候选请求隔离以及 writer 的关闭和失败释放。
## 版本化执行计划与 typed 流水线

网关读取 SQLite 已提交的 conversion generation，并为整个请求固定已解码的策略/绑定快照。绑定、激活、模型、源和模型组写入通过现有数据库触发器更新 generation；新请求使用新快照，在途请求保留旧快照。读取失败会返回错误。

快照按源/目标协议 revision hash、源/模型 ID、模型标识、operation 和 transport 惰性编译并复用转换计划。编译受锁保护，每个快照最多缓存 256 个计划；达到上限后清空索引，已固定的执行实例仍有效。协议热更新即使未改变 conversion generation，也会使用新的 revision key。规则按阶段预先索引，typed 路径资格在编译时确定。

共享计划只包含只读策略及编译程序，每个候选获得独立执行外壳。provider evidence 仍按当前账户/模型 scope、上游 revision 和时间逐次读取；诊断、delivery、scope 和 continuation 状态属于请求。Operation 选择只遍历标量路由元数据，选定后才深复制对应的 transport 元数据。

内置事件转换在独占且已归一化的语义容器上执行，依赖 JSON 的自定义规则保留通用路径。Typed 事件编码保留语义预算、mapping 预算、上下文验证、取消和输出检查；带 schema、后置 transform 或 initial 输出的 mapping 继续使用通用路径。最终 wire 校验始终保留。

`EncodeConvertedFrame` 用解码时的 native digest 检查转换后事件，随后立即编码，省去单独序列化原始批次及调用内重复比较。比较结果不会缓存到公开可变的事件对象；普通 `EncodeFrame` 每次仍检测外部修改。修改过或经过缓冲的网关批次显式重新编码。

回归覆盖四种内置协议的文本/工具流 typed/JSON 差分、归一化与资源/取消边界、native 修改检测、并发执行实例隔离及 generation/revision 更新。第三阶段性能对照使用第二阶段的干净提交及相同 HTTP 矩阵、环境。

## 独占存储与有界工作区

最后阶段减少大载荷处理，并将复用限制在明确的所有者内：

- JSON 语法校验使用标准库扫描器，不再解码临时对象树。入口仍验证并复制输入，数字保留原拼写，各协议资源预算继续独立强制检查。原有严格 Sonic 解码器保留为差分 fuzz 对照。
- 通用 Value 与严格语义契约使用单次字符串解码，保留数字精度、字符串复制、字符串校验、未知字段拒绝和尾随文档拒绝。短字符串不会借用并长期保留整个输入。
- JSON 字符串定位改用批量引号搜索和反斜杠奇偶判断；资源计量直接统计 HTML/行分隔符转义膨胀，保留无效 UTF-8 替换语义。不可变载荷即使共享，每次出现仍完整计入预算。
- 深克隆直接写入目标结构体与切片元素，只分配目标可变对象图。Reasoning 验证使用局部请求头，因为 normalizer 自己复制参数后才修改；候选恢复只在启用恢复时复制输入，转换结果仍独立拥有可变容器。
- JSON 容器在 16 个键/元素以内使用栈内临时数组，结果字符串精确预分配；更大的临时数组只属于本次调用。普通键直接写入，需要转义的键继续使用标准配置编码器。4/16 字段辅助基准目标为返回字符串的 **1 次分配**，不是整个 HTTP 编码零分配。
- 小编码结果直接使用不可变字符串 API；超过 64 KiB 的编码结果分离存储，避免长期保留编码器扩容后的多余容量。
- 每条网关流复用容量不超过 16 的 accepted-event 切片；包括失败在内的每次返回都会清理载荷引用，更大的 backing array 会丢弃。输出帧不借用该存储。

本阶段不新增应用级跨请求缓冲池：常见容器的临时空间在栈上，事件空间归单条流所有，而返回的不可变 JSON 必须独占字节。池化这些输出需要额外复制或改变生命周期契约。编码库及标准库扫描器继续使用自身的内部复用策略，冷启动和池未命中成本仍纳入测量。

```sh
cd backend
GOMAXPROCS=4 GOGC=100 GOMEMLIMIT=2GiB go test -tags=sonic -pgo=off \
  ./protocol ./internal/jsonutil -run '^$' \
  -bench '^(BenchmarkValueDecodeLarge|BenchmarkValueContainerEncoding|BenchmarkSemanticClone|BenchmarkStrictJSONValidation|BenchmarkTypedResourceCheck)$' \
  -benchmem -benchtime=300ms -count=5
```

回归测试在实际 replay 失败路径检查事件载荷引用清理和超限容量丢弃；克隆隔离遍历嵌套 map、slice、pointer；fuzz 覆盖严格语法、转义引号边界和精确资源计量。最终 HTTP 结果分别与第三阶段、第一阶段对照，并如实记录仍存在的时延及峰值回退。
