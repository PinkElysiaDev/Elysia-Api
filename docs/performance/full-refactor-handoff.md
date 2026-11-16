# 交接文档：转换层完整重构

Status: design, not implemented. Written 2026-11-16. 本文档中的任何内容都尚未实现。

## 问题

网关通过在一份语义文档上执行编译后的输出计划，完成协议 A → Core2 → 协议 B 的转换。
性能工作的第二回合（分支 `perf/round-2`）在**同一台机器、同一 harness、同一运行时设置**
下，相对 dev.173 达到**几何平均 1.06x 吞吐量**。峰值堆下降 15–37%，分配次数最多下降
38%，但延迟几乎没有变化。4x–13x 的目标未达成，且分配层面的优化已接近枯竭：
`stream256` 每请求仍分配 177k 项，仅改善 4.7%。

本文档描述达到该目标所需的重构。这是项目历史上最大的一次改动，在没有下方所述黄金
语料存在之前不应启动。

## 为什么显眼的目标是错的

一份 profile 将约 34% 的累计耗时归给 `outputPlan.build`。那是累计值，不是自身耗时。
在 `GOMEMLIMIT=1GiB GOMAXPROCS=2 GOFLAGS=-p=1` 下实测：

| 函数 | flat | 累计 |
| --- | ---: | ---: |
| `compiledOutputPlan.build` | 0.13% | 38.5% |
| `compiledOutputPlan.buildNode` | 2.74% | 38.4% |
| `compiledOutputPlan.buildOperation` | 1.90% | 38.1% |
| `evaluation.advance` | 1.93% | 3.87% |

分发本身约 4.6% 自身耗时，其中字符串 switch 只占约 1.5ns。Go 1.27 把这个约 100 个
分支的字符串 switch 编译为按长度分桶的间接跳转表加首字节二分查找，而不是比较链。
单节点开销为 5.8ns（`literal`）到 8.3ns（`read`），已包含函数序言、`advance` 与错误
处理。随规模增长的是调用量——短请求约 1,400 次，64 消息历史约 40,000 次——但即使
按 40,000 次计算，整个节点边界也只是 5.8ms 请求中的约 232µs。

把解释器分发替换为闭包或整数操作码，实测可回收 **1–2%**，不会更多。基于这一算术，
该方案被否决。另一次独立调查确认 `buildOperation` 在运行时从不修改 plan 或
expression，且 plan 在并发请求间共享——因此该设计对未来改动本已安全，它只是不构成
耗时所在。

**耗时来自对文档的反复遍历。** 单个非流式请求执行 12–15 次全文遍历，外加 2–3 次编码。
那才是目标。

## 文档的代价

使用 `unsafe.Sizeof` 在 darwin/arm64 上实测的结构体大小：

| 类型 | 字节 |
| --- | ---: |
| `Node` | 296 |
| `Request` | 264 |
| `Event` | 408 |
| `Response` | 216 |
| `Tool` | 120 |
| `Provenance` | 160 |
| `Native` | 192 |
| `Value` | 16 |

一份 64 消息的会话在负载字节之前就占用约 39.7 KB 结构体足迹。`Node` 占主导：role、
ID、call ID、name、status、payload `Value`、`*ToolInput`、三个切片头、一个 map、两个
指针以及三个未导出字段。`Value` 刻意是一个指向不可变原始 JSON 的 16 字节句柄，这是
好设计，应当保留。

## 共同根因

三项独立的优化被同一个缺失机制阻断：**`Request` 与 `Event` 上没有变更纪元（mutation
epoch）**。写入散落在 `conversion_request_fields.go`、`core_control_*.go`、
`package_model_controls.go` 与 `conversion_apply.go` 中，没有单一收口点。因此除了完整
结构比较之外，没有任何手段能证明「该文档自上次检查以来未改变」，于是每个消费者都做
一次完整比较：

- `equalCoreSnapshot` 每请求被作为根调用触达 **109 次**。`requestContextUnchanged` 被
  调用 9 次（解码 1 次，`conversion.Request` 内 7 次——每条已应用规则一次，编码 1 次）。
  `requestCheckUnchanged` 2 次。
- `digestSemanticEvents` 每个流帧**完整序列化事件批次两次**，因为解码侧摘要覆盖的切片
  与编码侧不同，且除重算外无法证明两者相等。

实测代价：`equalCoreSnapshot` 累计 4.40%，`digestSemanticEvents` 的逐帧代价未测量，
但相当于两次完整序列化。

在文档上维护一个单调递增的代数计数器，并在每个写入点递增，可使两种比较在常见情况下
降为 O(1)。这正是它属于新表示、而非事后附加的原因。

## 第一步——替换文档层

约束：随程序发布的 `spec.json`/`mapping.json` 包与用户自定义包完全不动。Schema 4 /
DSL 2 保持原样。这仅是内部 Go API 的改动。

设计目标，按优先级：

1. **缩小 `Node`。** 它是主要分配单元，也是递归单位。目标压到 128 字节以下：把罕用
   字段移入由节点索引的旁表，并把三个切片头替换为共享 arena 中的单一子节点区间。
2. **连续存储。** 节点放入每文档 arena，用父/子区间而非嵌套切片表示，使一次遍历成为
   线性扫描且子节点相邻，缓存友好。正是这一点让 12 次遍历在删除任何一次之前就变得
   可负担。
3. **保持 `Value` 不变。** 不可变原始 JSON 句柄是正确的，它保留了 absent／null／zero
   的区别，且约 343 处测试字段读取与 `spanIdentity` 缓存依赖它。不要改动它。
4. **加入变更代数计数器。** 每文档一个 `uint32`，由每个变更辅助函数调用的单一
   `touch()` 递增。所有既有写入点都必须转换，且必须有测试断言不存在 `touch()` 之外的
   导出字段赋值。
5. **保留承重的未导出回执。** `verdict`、`continuationInput`、`rangeOrigin`、
   `rangeAssociation`、`auxiliary`、`controlReceipt`、`requestWorkspace` 是安全与正确性
   状态，不是附带物。多个测试断言它们的**指针身份**
   （`hotpath_verdict_test.go:145,161,192`、`package_cache_native_test.go:26-27,187`）。
   它们随其余部分一起迁入 arena，不会消失。

代码生成自动跟随：`internal/coreplan` 与 `internal/semanticmeter` 在构建期反射这些
结构体，写出五个生成文件（`plan_core_generated.go`、`semantic_clone_generated.go`、
`semantic_normalize_generated.go`、`typed_meter_generated.go`，另加与文档无关的
`definition_encode_generated.go`）。执行 `cd backend && go generate ./protocol`。
它们的 oracle 测试将生成结果与手写反射实现比对，若表示改动破坏某个遍历器会明确失败
——这是预期的安全网。`typed_limits_test.go:238` 已在 CI 中强制 meter 新鲜度。

范围代价，直说：`protocol.Request`/`Node`/`Value` 在 `protocol/` 之外有**约 380 处
非测试调用点**——`server` 338 处、`agent` 36 处。`agent` 直接构造 `[]protocol.Node`
字面量（`agent/history.go:31,42,67,143`、`agent/context.go:131`）。测试中约有 240 处
`reflect.` 使用与 343 处直接字段读取。这是我们自己拥有的内部代码，但不是一个小 diff。

## 第四步——削减冗余遍历

按实测价值与是否真正可靠排序。证据来自对 `BenchmarkRequestMemoryPipeline` 的插桩运行，
所有探针事后均已移除。

### 不可移除——不要尝试

**`preserveMappedNative` 内的第二次编码（同协议编码的约 17%）。**
`canReuseOutput`（`request_workspace.go:45`）的设计意图是通过证明当前请求等于解码基线
来跳过基线重编码。它对四个内置协议全部失败，且实测失败点不在 plan 标志上（所有
`encode_request` plan 上这些标志均为 false），而在 `equalRequestSnapshot`：转换依法
设置了 `ClientOutput.CollectUsage` 与 `ResponsesStorage`，gemini 还设置 `Parameters`。
随程序发布的 openai-chat 程序在条件分支内读取 `/clientOutput/collectUsage`、
`/clientOutput/rawStreamOptions`、`/clientOutput/responsesStorage`，因此这些差异字段在
语义上是活跃的。输出在基准夹具上恰好相同，只因该夹具是非流式——那是数据相关的运气，
不是不变式。移除第二次编码需要在 `compiledOutputPlan` 上做编译期读集分析，证明 plan
无法观测任何差异字段，且需覆盖枚举根的算子（`keys`、`type`、`entries`、`project`、
`walk`、`visit`、空指针 `read`）。对 openai-chat 该分析仍然失败。**这道门在做真正的工作。**

注意此处暴露的测量缺口：`scripts/benchmark-performance.mjs` 会清除 `Native`，因此这
17% 对端到端 harness 不可见，但对同协议流量真实存在。修复 harness 是独立工作，且应在
采信任何同协议端到端数字之前完成。

**最终边界的线协议重复校验。** `ValidateRequestOutput` 使用 `upstream-request` 校验，
而非 `generation-request`。校验器不同，因此尽管作用于相同字节也并非冗余。
`plan_encode.go:109-112` 的 `mapping.output.validate` 对 package 方向为 nil——那是空操作，
不是一次遍历。唯一候选是 `finishPackageExecution` 的校验，移除它会改变错误优先级
（schema 失败 vs `InvalidMutation`），且该函数与非 preserve 路径共享。无法证明安全；
保持不变。

### 仅在变更纪元落地后移除

**校验比较（每请求 109 次根遍历）。** 有了代数计数器后，当文档自见证取得后未被触碰时，
每次 `requestContextUnchanged`/`requestCheckUnchanged` 降为一次计数器比较；仅当被触碰
时才回退到完整的 `equalCoreSnapshot` 遍历。这完全保留了现有安全姿态——
`request_verdict.go:10-12` 的注释（「宁可多走一次也不接受陈旧的、与安全相关的校验」）
仍然成立，因为被触碰的文档依然走完整遍历。

**逐帧摘要（每帧两次序列化）。** 一旦 `convertEvent` 能通过同一计数器报告「无导出字段
变更」，且 `len(acceptedEvents) == len(frame.Events)` 且顺序一致，解码侧摘要即可替代
编码侧重算。

### 现在即可移除，收益小

**`canReuseOutput` 的 warn 效应检查**在 `options.Diagnostics == nil` 时可以放宽，因为
warn 的重数是它唯一守护的可观测量。收益小、风险低，需要一个测试证明该配置下诊断路径
确实未被使用。

## 第三步——生成执行器

**在第一步与变更纪元落地并完成测量之前不得启动。** 在当前指针密集的文档上生成直筒式
Go 代码，只会产出仍在十几次遍历中追逐指针的代码。收益只在这一顺序下复合。

具体含义：不再由运行时解释 `compiledOutputPlan` 树，而是按编译后的 Mapping 为
`encode_request`／`encode_response`／`encode_event` 程序各生成一个 Go 函数，与既有编译
计划一同缓存，并以相同的 revision 身份为键。生成函数必须精确复现：每条错误消息及其
路径、首错顺序、所有资源限制检查（`Nodes`、`BufferBytes`、`walkNodes`）、`advance()`
步数限制、absent／null 语义，以及 `Value` 原始字节的保留。

正确性负担就是全部风险。上述每一项行为目前都由一处的通用解释器代码产生；生成代码则
按程序分别产生，意味着生成器的缺陷会表现为静默的语义差异，而不是编译错误。这正是黄金
语料必须先存在的原因。

## 黄金语料——先建这个

仓库当前**没有 `.golden` 文件**。最接近的现有材料是 `protocol/testdata/`（会话夹具）、
`protocol/builtin/testdata/`（流布局、Agent 发现契约、归档夹具），以及执行完整
解码→规范化→编码的编译器夹具测试。其中没有一项是逐字节 oracle。

所需：从当前工作树捕获的语料，覆盖每个内置包、每个方向，以及每一类重要情形——短文档
与长文档、工具、图像、流帧、跨协议对、保留 Native 与不保留 Native，以及错误路径
（包括每个预期 `ConversionIssue` 代码与路径，而不只是成功）。捕获必须记录完整线协议
字节，而非解析后的结构。此后对语料的任何改动都是行为变更，必须按行为变更评审。

## 迁移顺序

每次提交保持绿色，且不依赖后续步骤：

1. 从当前工作树捕获黄金语料，并在当前工作树上通过。
2. 在既有 `Request`/`Event` 上加入变更纪元，附带证明不存在 `touch()` 之外导出字段写入
   的测试。修复端到端 harness，使其不再清除 `Native`。
3. 校验比较与摘要携带优化，并测量。
4. 文档表示变更，同步重新生成全部生成文件。
5. 测量。重新判断第三步（生成执行器）是否仍值得其风险。

无论第四、第五步是否发生，第 1–3 步都值得做。

## 风险清单

- **生成代码的语义漂移**（第五步）：主要风险，只能靠黄金语料与 `typed_limits_test.go`
  式的新鲜度强制来缓解。
- **约 380 处外部调用点**：机械但量大；遗漏一处仍按旧布局读取字段的调用点可能通过编译
  却是错的。可通过尽量保持字段名稳定来缓解。
- **oracle 测试固化了旧表示**：`semantic_normalize_parity_test.go`、
  `semantic_clone_oracle_test.go`、`typed_limits_test.go` 与手写反射 oracle 比对。它们是
  第四步的安全网，同是第四步的重写负担。
- **指针身份测试**：`hotpath_verdict_test.go`、`package_cache_native_test.go`、
  `package_history_mapping_test.go` 断言回执指针的身份。arena 分配会改变这些指针的含义。
- **本计划不承诺 4x–13x。** 第 1–4 步没有任何实测投影；只有第五步的收益可能触达该区间，
  且尚未建模。

## 未测量

- 第 1、3、4、5 步之后吞吐量的任何投影。本文档中唯一的实测数字是第二回合的 A/B 结果、
  profile 占比、调用次数、结构体大小与校验比较的调用次数。
- `digestSemanticEvents` 的逐帧代价。
- 语料捕获是否覆盖了足够的真实流量分布。
- 非 Apple 硬件上的生产行为，以及最初取得交接基线的 Windows 上的行为。

## 产生数据的命令

Profile 与调用计数通过以下命令取得：

```sh
env GOMEMLIMIT=1GiB GOMAXPROCS=2 GOFLAGS=-p=1 go -C backend test ./protocol \
  -run '<benchmark>' -bench=. -count=1 -cpuprofile=.tmp-dev/cpu.out
go tool pprof -top -cum <test-binary> .tmp-dev/cpu.out
env GOMEMLIMIT=1GiB GOMAXPROCS=2 GOFLAGS=-p=1 go -C backend build \
  -gcflags=-m=1 ./protocol
```

第二回合的 A/B 使用 `scripts/benchmark-performance.mjs --execution=production
--mode=measure --repeats=5`，在 `bfe7638f` 与第二回合 HEAD 两个工作树上交错执行，环境为
`GOMAXPROCS=2 GOGC=100 GOMEMLIMIT=2GiB`。
