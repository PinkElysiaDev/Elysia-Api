# Agent Note: 弱引用编译缓存与单次解码

Status: implemented

## Problem

独立的注册表、校验和管理重载会反复重建相同的四个内置协议执行图。服务器测试夹具还会完整解码再编码这些包，制造另一种源码拼写。重复编译和离线验证主导启动分配；原本彼此独立的白盒编译器夹具，在生产不可变图开始共享之后还可能互相污染。表达式字段存在性检测再次遍历所有子树，重复的内联 schema 则分别调用 JSON Schema 编译器。

## Decision

`protocol` 持有进程级缓存，覆盖 `Compiler.Compile`、`CompilePackage`、`CompileSources`、`Service.Validate`、编译会话和持久化修订重载。键由 `CompilerVersion`、完整引擎 `Limits`、排序后的已安装 feature 身份，以及**精确原始定义字节**的 SHA-256 组成，不做 JSON 归一化。Mapping 自带的 limits 和全部样例也包含在这些字节中。不同拼写、limits、features 或编译器版本不能共用一个执行图。

条目保存 `weak.Pointer[Compiled]`。同一键的并发未命中共享一次编译；错误不缓存，每个等待方拥有独立诊断。`runtime.AddCleanup` 仅在弱引用身份仍匹配时删除失效元数据，因此旧清理任务不会误删替代条目。仅显式准入且与内嵌包字节完全相同的源码被强引用保留：最多四个图，每个 ID 一个，读取时核对完整准入键。其他拼写、配置和作者变体不会被进程级固定。生产注册表本来也会持有这些预置图。编译会话保留首次准入和发布契约，但移除了冗余的线性去重遍历。历史加载在引擎字节数和条目数限制内保留进程缓存的图，同时仍重新读取持久化定义和报告。修正后的固定规则、context 输入和保留决策由[编译缓存修正](2026-11-16-compile-cache-corrections.zh.md)记录。

执行图复用不是授权。重载和发布仍读取修订，核对 identity/hash，读取权威数据库报告并执行 `CanActivate`。缺失、失败、过期或不匹配的数据库证据不能被缓存的离线证据替代。原子快照、请求 pin、回滚和修复隔离保持不变。

### Immutability audit

全部可执行 `Compiled` 字段均在缓存发布前初始化：definition/identity/hash、limits/capabilities、native 策略、mapping/output plan、wire contract/validator、operation/task/model/Agent plan、token-count 子图和 package plan。编译期表达式事实先被释放。公开的 package、operation 和 capability 访问器返回独立值或副本。JSON Schema 验证会创建自己的局部 validator。遍历索引和惰性状态、预算、native replay workspace 与流事务仍属于请求（`valueReader`、evaluation/input view、evaluation state）。未发现生产运行期修改执行图的路径。

离线证据是刻意保留的例外：独立分配、互斥保护的 memo，而不是执行惰性状态。故意修改私有 limits 或替换 mapping/contract 的测试，通过 `_test.go` helper 分离图头和外层 map；Agent output slice 也被分离。审计发现的修改目标是图头字段、外层 map 条目和 Agent slice 元素，没有透过保留的运行期 plan 指针写入。现有独立 plan 和资源限制断言保持不变。protocol 的 `TestMain` 哨兵在测试结束后重新编译进程缓存中每个仍存活的条目（包括未固定的自定义图和源文件对图），比较 limits 和执行图结构指纹，仅排除已同步的证据 memo。固定预置也必须仍有对应缓存条目；[审计缺口恢复](../testing/2026-11-16-audit-gap-restoration.zh.md) 记录了哨兵的范围与负向测试。缓存测试直接测试实际共享结果，不使用分离夹具。

### Offline evidence inputs

`Verify` 并非字面意义的纯函数：它填写 `VerifiedAt`，观察取消和 deadline，并继承 `RequestBodyLimit`。其**完整成功证据**对下列枚举输入是确定的：

- 编译源码身份、编译器版本、引擎/定义 limits 和已安装 features。
- 普通样例、事件序列、model、token-count、task、session 和 Agent 的全部夹具，包括样例 ID/顺序、scope、context、input、期望输出/诊断和 capability 声明；这些都包含在完整定义字节中。
- 有效请求体预算和调用方持有的文档 metrics。仅默认预算且没有传入 metrics owner 时使用 memo；其他输入绕过 memo，而不是扩张每个图的选项 map。完整 context 读取清单见[编译缓存修正](2026-11-16-compile-cache-corrections.zh.md)。

离线执行没有 provider/network 输入，使用显式的合成 operation 身份和时间。组合/profile 验证是独立路径，不缓存。传入的 context metrics 会保守地绕过 memo，尽管其只存储与策略无关的事实。取消、deadline 超时、失败和部分执行永不填充 memo。等待者取得 memo 锁后再次检查取消。每次调用获得 checks、嵌套 capabilities、coverage、issues、target scope 的深拷贝，并填写自己的 `VerifiedAt`。给定相同时间戳，缓存和不缓存的报告字节一致。**命中缓存意味着本进程内本次调用没有重新执行这些夹具**；它不是新的上游证据，也不允许忽略持久化报告。

### Decoding and schema reuse

`CompileSources` 校验每个独立 JSON 边界，按原始字节/token 计量重复键及包的额外嵌套，然后对整个 package 做一次类型化解码。`ParseValue` 是语法校验和不可变字节复制，并不构建 DOM；原始 token 计量必须发生在类型归一化之前。即使命中缓存也检查独立边界，避免无效的源码切分借用一个有效拼接包的缓存键。超限输入在分配拼接缓冲前被拒绝。

仅类型化解码失败会进入小型的、只用于诊断的独立源码回退路径。它保留原有源码 identity、code、path 和 codec 消息，不能执行编译，也不能把拒绝改成接受。如果回退没有发现源码错误，仍保留原始带归属的拒绝诊断。Fail-closed 回退测试和既有拒绝测试保护该边界。这比引入第二个支持源码偏移归属的解析器更小。

`Expression.UnmarshalJSON` 在同一次字段遍历中完成解码并记录存在性，利用 decoder position 而不是重新扫描子树。仅格式错误、未知或大小写不匹配的输入保留旧的严格解码/存在性路径，以保持接受范围和诊断兼容。旧实现测试 oracle 覆盖重复字段、转义键、null/zero/false、混合大小写、错误 JSON 和精确错误消息。

内联 wire schema plan 通过每次编译的内容 memo 在 Spec 和 Mapping 表达式编译器之间共享。键是确定性的 schema 编码；本次编译 limits 固定，schema 没有依赖表达式 scope 的选项。成功 plan 和 JSON Schema validator 被共享；错误永不记忆，各调用点仍附加自己的源码位置。

服务器预置夹具现在保留内置源码字节，在不变的数据库写入和运行期初始化之前经过真实 `CompilePreset` 准入。它们不再制造类型化重编码后的第二种拼写。这不是共享或预先填充的注册表，不跳过样例、证据、重载或授权检查。

## Alternatives considered

**仅测试使用的预填充/共享注册表。** 用户否决：它掩盖生产重载和编译成本，且可能跳过证据边界。复用由生产缓存提供；夹具只保留生产源码拼写。

**通用强引用 LRU。** 不采用：它保留任意昂贵自定义图，并引入容量和调参策略。弱引用条目加每个内置预置恰好一个准入图，在不增加这些策略的前提下限定强引用保留。

**不缓存 Verify。** 时间戳和 context 依赖使直接缓存整个报告不正确。当前仅记忆完整成功、默认预算的证据，复制后逐次填写时间戳；失败和其他预算继续正常执行。

**归一化定义键或跨编译共享 schema。** 定义不采用归一化：拼写、重复字段和原始资源消耗属于准入契约。Schema 复用仅限同一次编译及其固定选项。

**支持源码偏移的第二解析器。** 选择更小的只诊断回退方案；它失败关闭，并保留现有严格 decoder 消息。

## Consequences

不变的注册表和测试服务器共享不可变可执行图，不共享请求状态，也不授予激活权限。首次准入后，热服务器启动不再执行预置编译或验证。自定义图及其证据仍可回收，弱引用元数据异步清理。最多四个与内嵌字节完全相同的预置图保留至进程结束；已验证历史图另有 Service 持有的有界保留。编译入口仍需对精确字节做 SHA-256；源码对入口还检查独立边界。缓存 Verify 分配独立报告 slice，而不是返回可修改的共享证据。源码诊断回退有意保留冷兼容路径。

## Measurements

环境：Apple device、macOS、Go 1.27.3。另一个 agent 同时运行，wall/RSS 是单次有界观察，不声称隔离系统测量。每条 Go 命令均使用 `GOMEMLIMIT=1GiB GOMAXPROCS=2 GOFLAGS=-p=1`；测试使用 `-parallel=2 -count=1`。忽略目录内 `.tmp-dev/testperf/fable-b/watch.py` 汇总进程组 RSS，超过 2 GiB 就终止整个进程组。`/usr/bin/time -l` 提供 wall 和最大单进程 RSS；watchdog 独立采样进程组总 RSS。没有运行完整 server 包、完整 Go suite 或无界命令。

以下命令从仓库根目录执行，均由上述 watchdog 和 `/usr/bin/time -l` 包裹：

```sh
go -C backend test ./protocol -parallel=2 -count=1
go -C backend test ./protocol/builtin -parallel=2 -count=1
go -C backend test ./server -run '^TestChatCompletions' -parallel=2 -count=1
go -C backend test ./protocol/builtin -run '^$' -bench 'BenchmarkPackageCompile|BenchmarkVerifyPreset' -benchmem -parallel=2 -count=5
```

| 命令 | 前 wall s | 后 wall s | 前 max RSS MiB | 后 max RSS MiB |
| --- | ---: | ---: | ---: | ---: |
| `./protocol` | 26.17 | 12.16 | 946.8 | 957.6 |
| `./protocol/builtin` | 48.11 | 34.34 | 545.8 | 482.3 |
| 聚焦 ChatCompletions | 45.85 | 15.62 | 850.4 | 546.7 |

以下是五次运行的中位数。`BenchmarkPackageCompile` 保留既有循环：因为迭代间不 pin 结果，GC 后弱缓存可能未命中。因此它测量的是重复 API 成本，不是隔离的冷编译成本，也不是保证全热的成本。验证中位数包含独立报告复制。

| CompileSources 预置 | 前 ms/op | 后 ms/op | 前 B/op | 后 B/op | 前 allocs/op | 后 allocs/op |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| Chat | 66.796 | 7.029 | 80,990,534 | 11,785,869 | 260,924 | 14,402 |
| Responses | 148.747 | 13.122 | 200,977,274 | 24,977,023 | 1,191,024 | 86,760 |
| Anthropic | 60.975 | 7.210 | 67,033,714 | 16,619,300 | 273,878 | 27,617 |
| Gemini | 62.781 | 4.584 | 70,237,544 | 10,723,865 | 257,120 | 13,838 |

| Verify 预置 | 前 ms/op | 后 microseconds/op | 前 B/op | 后 B/op | 前 allocs/op | 后 allocs/op |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| Chat | 100.033 | 1.690 | 68,799,366 | 8,048 | 416,020 | 43 |
| Responses | 112.697 | 2.668 | 74,731,136 | 11,024 | 631,697 | 59 |
| Anthropic | 149.825 | 2.211 | 120,938,165 | 10,784 | 796,912 | 50 |
| Gemini | 108.250 | 2.071 | 92,339,668 | 10,320 | 630,305 | 47 |

临时、不交付的 Go overlay 分别计数 `Compiler.Compile` API 入口、实际 `compilePackage` 构建、`Verify` 入口和实际未缓存验证。一个进程中连续运行五个 `newTestServer` 子测试，每个服务器均在下一个启动前清理。测量命令如下：

```sh
go -C backend test -overlay=../.tmp-dev/testperf/fable-b/overlay/overlay.json ./server -run '^TestSequentialStartupCacheMeasurement$' -parallel=2 -count=1 -v
```

| 启动 | 初始化 wall ms | Compile API 次数 | 实际编译 | Verify API 次数 | 实际 Verify 执行 |
| --- | ---: | ---: | ---: | ---: | ---: |
| 首次 | 966.532 | 16 | 4 | 4 | 4 |
| 热 1 | 202.031 | 16 | 0 | 0 | 0 |
| 热 2 | 215.095 | 16 | 0 | 0 | 0 |
| 热 3 | 240.470 | 16 | 0 | 0 | 0 |
| 热 4 | 218.955 | 16 | 0 | 0 | 0 |

保留夹具原始源码之前，首次 overlay 测得首启 12 次实际构建、热启 7–8 次，由此定位到人为引入的第二种拼写。最终 overlay 的零热执行断言通过。计数不是生产日志或交付 hook。

最终有界 `go test ./protocol/...`、`go test ./internal/architecture`、聚焦 server 的 `-run 'TestProtocol|TestProductionStartup|TestProductionReload|TestPreset'` 和 `go vet ./protocol/... ./server ./storage` 均通过。完整非 race suite 的五分钟目标与全套峰值 RSS 仍**未验证**：完整 server/Go suite 不在本任务内存安全运行授权范围内。未执行 race 测试和隔离冷编译 profiling。
