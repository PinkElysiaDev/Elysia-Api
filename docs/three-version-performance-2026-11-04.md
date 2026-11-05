# 三版性能对照与分配归因：2026-11-04

本轮完成同期测量、profiling 和优化计划，没有修改生产执行实现。

**A 不是正式 1.7.0。** 正式 `v1.7.0` 对应 `9b72829a`；历史 A 是后来的 `b2844846 / dev.101`。当前固定为 `d7e29817`，执行实现对应 `f2b298f8`。

当前默认链与 A 的 CPU 差异在本次八个场景中均未达到统计确定性。三分包相对正式 1.7.0 的 CPU/请求为 **1.24～3.55 倍**，八个区间均高于 1；分配字节相对 1.7.0 同工具链重建版为 **1.33～2.64 倍**。三分包仍未通过性能切换门槛。

最大的新增发现是 **continuation 历史摘要成本**：64 条交替角色消息触发 1,120 次节点摘要；媒体节点的摘要走通用 JSON 序列化，生成多份大正文缓冲。字段表和 Core 投影仍然重要，但不是大正文场景的首要分配根因。

## 证据与身份

- [188 张交互火焰图及差分图](../.tmp-dev/three-version-2026-11-04/profiles/index.html)
- [五轮比较、绝对值和置信区间](../.tmp-dev/three-version-2026-11-04/comparison.json)
- [全部指标中位数表](../.tmp-dev/three-version-2026-11-04/measurement-tables.md)
- [分配类别及完整调用栈](../.tmp-dev/three-version-2026-11-04/profiles/attribution.json)
- [构建命令与二进制身份](../.tmp-dev/three-version-2026-11-04/builds.json)
- [配置、修订和测量文件哈希清单](../.tmp-dev/three-version-2026-11-04/evidence-manifest.json)
- [微基准、逃逸分析和运行图诊断](../.tmp-dev/three-version-2026-11-04/micro/manifest.json)
- [验证命令及结果](../.tmp-dev/three-version-2026-11-04/checks/results.json)

| 条件 | 源码身份 | 二进制 SHA-256 |
|---|---|---|
| R-release | 官方 v1.7.0 / 9b72829a | `06b878052d2cd9a16f1b7a0be2359257116f6d318945b1fd912645db48644970` |
| R-build | 9b72829a 重建 | `245487370f393fe7a82f7f9c6681700acf3bb635c088a3fe52ba8824873f32c9` |
| A | b2844846 重建 | `031ac0e526ab8a761d9965629820b38011bdfdf6cd9367874681c66dc555685f` |
| C-default / C-package | d7e29817，同一二进制 | `fa489051be7898d06c8b16c14a6967f21841be2acfd7c43b2c4e372bfe994948` |

官方资产来自 [v1.7.0 Windows amd64 发布地址](https://github.com/PinkElysiaDev/Elysia-Api/releases/download/v1.7.0/elysia-api-windows-amd64.exe)。构建信息为 Go 1.27.2，内嵌完整修订 `9b72829a40a6f73cbe82604a6d04e397d8bdf634`，`/health` 返回 `1.7.0 / 9b72829`。发布资产的 `vcs.modified=true` 与生成的嵌入资产一并记录。没有使用实际提交为 `d273ce96` 的本地 dist 文件。

重建关闭 CGO、PGO、自动 Git stamping，显式写入源码提交。重建的健康接口版本字符串统一设为 `1.7.0`，**不是版本证明**；身份以提交、文件哈希及活动协议修订为准。后端归档保留跟踪的 embed 占位文件，官方二进制保留发行 UI/源码资产；R-release/R-build 不是字节级等价构建。本轮未请求 UI 或源码导出。

生产管理 API 不允许覆盖内置预置。C-package 将四个 Spec/Mapping 以 `measure-*` ID 保存、验证、激活并绑定，只改 `spec.id` 和样例对应的 `definitionId`。进程保留 **四个旧预置＋四个自定义包**；C-default 使用默认旧链。全部条件经 `/gateway/:id/*path`：旧 Chat 声明 `/chat/completions`，包声明 `/v1/chat/completions`。源地址遵守声明，不裁剪自定义 URL。测量前、重载后及完成后的修订与绑定均一致。

## 方法与工作量

Windows amd64，Intel Core Ultra 7 270K Plus，24 个逻辑 CPU，约 31.7 GiB RAM；Go 1.27.2，`GOMAXPROCS=2`、`GOGC=100`、`GOMEMLIMIT=2GiB`。客户端、网关、模拟上游独立进程，各版本依次运行。正式计时期间没有并行构建、测试或 profiling。

上游固定为 `.tmp-dev/fixed-provider-build/server.test.exe`，每批记录哈希、归档源码和返回身份。数据库、日志、密钥和端口隔离；目录同步、Agent remote 和正文日志关闭，正常用量写入保留。未调用真实供应商。

| 场景 | 请求/轮 | 内容 |
|---|---:|---|
| short | 4,096 | Chat 短请求 |
| history | 256 | 64 条交替角色消息，每条 4 KiB |
| tools | 1,024 | 32 个函数工具及 schema |
| burst | 4,096 | 四并发短请求 |
| stream | 128 | 256 个文本增量帧，每帧 256 字节，另含生命周期帧 |
| image | 128 | 工具结果携带 2 MiB base64 媒体控制夹具 |
| cross-json | 512 | Chat → Anthropic JSON |
| cross-stream | 128 | Chat → Anthropic 256 帧 SSE |

每场景五轮循环换序。200 个正式进程批次、160 个采集批次全部通过，合计 **466,560 个测量请求**，不含预热和诊断。完整消费并检查正文、usage、终止及上游调用次数。额外 15 组严格审计覆盖五个条件下历史角色/消息边界、完整工具 schema、媒体工具参数和结果关联；该审计使用代理，耗时不参与比较。

发行运行表使用正式进程 OS CPU 和 20 ms 工作集采样；精确分配表使用同源码测试构建的 `TotalAlloc/Mallocs` 批次差值，采集器运行正常 `New/ListenAndServe` 生命周期。正式二进制没有新增诊断接口或每请求观测。

延迟截至完整消费响应；吞吐还包括独立客户端的语义检查，不能解释为网关理论最大吞吐。CPU/分配在用量写入排空后取值，排空等待另存。计时不强制 GC；恢复内存取空闲一秒的值，GC 后存活堆另采。`PeakHeap`、`GCCPUSeconds` 不可用，不把结构中的零当测量结果。

使用现有固定种子 bootstrap 比较器，区间估计的是**独立中位数比值**，不是配对估计。5% 仅用于排查排序，小于 5% 的稳定变化仍保留。发行资产分配仅有 pprof 采样估计，不能用 R-build 精确值冒充发行资产。

## 同期结果

CPU，毫秒/请求，五轮中位数：

| 场景 | R-release | R-build | A | C-default | C-package |
|---|---:|---:|---:|---:|---:|
| short | 0.637 | 0.633 | 0.645 | 0.645 | 1.163 |
| history | 7.507 | 7.568 | 7.690 | 7.690 | 26.611 |
| tools | 2.350 | 2.213 | 2.258 | 2.411 | 4.089 |
| burst | 0.504 | 0.519 | 0.519 | 0.507 | 1.087 |
| stream | 12.573 | 12.817 | 13.428 | 12.695 | 36.987 |
| image | 18.921 | 18.677 | 18.921 | 18.188 | 35.278 |
| cross-json | 0.641 | 0.702 | 0.763 | 0.702 | 0.793 |
| cross-stream | 16.479 | 16.479 | 15.015 | 16.846 | 35.889 |

三分包相对正式 1.7.0、A 的 CPU 比值，及相对 1.7.0 重建版的分配比值：

| 场景 | CPU C/R-release [95%] | CPU C/A [95%] | 分配 C/R-build [95%] |
|---|---:|---:|---:|
| short | 1.826 [1.753, 2.118] | 1.805 [1.605, 1.932] | 1.721 [1.707, 1.722] |
| history | 3.545 [3.463, 3.825] | 3.460 [3.381, 3.703] | 2.616 [2.614, 2.617] |
| tools | 1.740 [1.652, 1.795] | 1.811 [1.716, 1.887] | 2.639 [2.633, 2.641] |
| burst | 2.159 [2.096, 2.215] | 2.096 [2.059, 2.165] | 1.705 [1.695, 1.724] |
| stream | 2.942 [2.405, 3.932] | 2.755 [1.968, 3.682] | 2.303 [2.297, 2.308] |
| image | 1.865 [1.778, 2.007] | 1.865 [1.832, 1.927] | 2.397 [2.393, 2.404] |
| cross-json | 1.238 [1.048, 1.381] | 1.040 [0.880, 1.733] | 1.328 [1.252, 1.346] |
| cross-stream | 2.178 [2.100, 2.252] | 2.390 [2.056, 2.557] | 1.584 [1.580, 1.588] |

精确分配，KiB/请求：

| 场景 | R-build | A | C-default | C-package |
|---|---:|---:|---:|---:|
| short | 154.35 | 157.13 | 148.32 | 265.59 |
| history | 9,675.83 | 9,684.96 | 9,681.59 | 25,310.09 |
| tools | 568.92 | 571.92 | 556.44 | 1,501.45 |
| burst | 155.41 | 158.58 | 148.71 | 265.04 |
| stream | 6,766.69 | 6,770.05 | 6,839.63 | 15,581.41 |
| image | 18,077.76 | 18,024.72 | 18,038.87 | 43,328.44 |
| cross-json | 164.73 | 165.66 | 158.23 | 218.69 |
| cross-stream | 9,306.18 | 9,386.30 | 9,122.70 | 14,743.87 |

吞吐，请求/秒，展示三个主要对象：

| 场景 | R-release | A | C-package |
|---|---:|---:|---:|
| short | 1,366.2 | 1,353.6 | 815.2 |
| history | 109.1 | 110.4 | 49.8 |
| tools | 370.4 | 368.2 | 242.2 |
| burst | 3,848.0 | 3,772.8 | 1,814.2 |
| stream | 53.1 | 52.0 | 25.5 |
| image | 42.7 | 42.7 | 28.4 |
| cross-json | 1,243.3 | 1,225.2 | 1,018.7 |
| cross-stream | 45.4 | 45.5 | 25.8 |

全部条件的 p50/p95/p99、SSE 首帧、GC 次数/暂停、峰值工作集及恢复内存见完整指标表。短请求完整消费 p50 为 0.514/0.514/1.028 ms；SSE p50 为 9.778/10.255/28.801 ms，首帧 p50 为 1.023/1.023/1.535 ms，顺序均为 R-release/A/C-package。

三条解释链：

1. **R-build → A：** 八个 CPU 比较均不确定；分配存在小幅稳定增长，如短请求约 1.8%。A 不能代替 1.7.0。
2. **A → C-default：** 八个 CPU 比较均不确定；短请求分配减少约 5.6%、工具减少约 2.7%，同协议 SSE 增加约 1.0%。公共路径并非所有指标都相同。
3. **C-default → C-package：** 七个 CPU 场景稳定回退；cross-json CPU 比值约 1.13，但区间跨 1，保留不确定。八个场景的分配均稳定增加。

R-release/R-build 及所有区间单独保留在 comparison.json。没有拼接旧报告或小批次百分比，没有为不确定结果反复补测。

## 分配归因

短请求、工具、SSE、媒体及补充的 history 均采集 CPU、alloc_space、alloc_objects、inuse_space。CPU 与内存采样分开，CPU 时间窗均覆盖完整固定请求批次。HTTP 图按每 1,000 请求归一化；其原始 Duration 没有缩放，不能用 samples/Duration 推导 CPU 利用率。

精确增量 C-package − C-default：

| 场景 | 额外 B/请求 | 额外对象/请求 |
|---|---:|---:|
| short | 120,091 | 1,344 |
| history | 16,003,590 | 33,387 |
| tools | 967,683 | 9,054 |
| stream | 8,951,586 | 126,573 |
| image | 25,896,520 | 2,752 |

同协议 SSE 摊到每个文本增量帧，额外约 **34.15 KiB、494.43 个对象**。这是请求开销摊入 256 个文本帧的结果；实际 wire 控制帧数另存于 profile measurements。

以下为互斥调用栈类别的采样估计，类别可能包含子分配，并非某一个切片的 flat 值。调用计数来自独立 overlay，不用其耗时/分配总量作验收。

| 路径/场景 | 额外 B/请求，估计 | 额外对象/请求，估计 | 独立调用计数 |
|---|---:|---:|---|
| continuation / history | 17,651,474 | 20,312 | 33 次历史摘要、1120 次 Node 摘要，默认链均为 0 |
| continuation / image | 28,337,780 | 12 | 2 次历史摘要、3 次 Node 摘要；少量大缓冲主导字节 |
| 字段表 / tools | 511,610 | 3,183 | 1893.69 次/请求，默认链 234 次 |
| Core 投影 / short | 50,831 | 505 | 170 个容器；字段容量 922、使用 479；Node 整体投影 28 次 |
| 字段表 / stream | 1,626,465 | 10,626 | 7828 次/请求，默认链 3643 次 |
| Native 子作用域 / stream | 823,495 | 3,697 | 2855 次，其中 2595 次子树无捕获需求 |
| 状态相关子树 / stream | 1,413,928 | 16,297 | 提交 518 个状态对象；分配类别也含事务子操作 |

正增量可能大于净增量，因为其他路径减少。例如 history 摘要增加约 17.65 MB，但最终精确净增量是 16.00 MB。互斥分组后才可求和，不能叠加累计百分比。排除 profiler 后，采样净增量/精确值分别为 short 124,026/120,091 B、history 16,006,757/16,003,590 B、tools 1,011,384/967,683 B、stream 9,529,828/8,951,586 B、image 26,314,660/25,896,520 B。对象采样在少量大分配场景误差更大，总量以精确计数为准。

### 首要根因：continuation 摘要

调用链：`prepareGatewayPlan → newContinuationOwner/restoreGatewayContinuation → historyDigest → ContinuationNodeDigest → EncodeValue/Value.MarshalJSON → Value.Bytes`。

`restoreGatewayContinuation` 为每个 assistant 轮次重新计算历史 parent。64 条交替消息的前缀访问数 `1+3+...+63=1024`，加初始完整历史 64 个节点和恢复遍历 32 个节点，得到实测 **1120 次摘要**。本夹具没有输入 carrier 和 session，仍进入恢复遍历。

`ContinuationNodeDigest` 清理 provenance 等字段后调用通用 `EncodeValue(node)`。Value 经 MarshalJSON/Bytes 复制，Sonic 再 compact、HTML escape，大字符串经 `ownEncodedString` 分离容量。所有权机制有各自用途，但只消费摘要的中间步骤生成多份正文。媒体主要位点是 `prim.Compact`、`rt.GrowSlice`、`Value.Bytes`、`ownEncodedString` 和 `jsontext.reformatValue`。

旧注入式夹具没有相同的启动合同：`newTestServerWithStore → newUnboundTestServer` 使用空 Config，没有 SecretKeyPath；没有环境密钥和 key path 时，GetDBEncryptionKey 返回 nil，codec 无法建立。这说明旧夹具可能跳过历史摘要，不能用其结果排除本轮发现的成本。本轮正常启动创建隔离密钥，并用计数证明该路径实际执行。另一个不可混用的条件是四预置替换与四预置＋四自定义包的区别。

### 小对象和保留堆

预检复用未失效：五个重点场景中两条当前链都只调用一次 EncodeRequestValue。valueReader.fragment 的复制字节在包链反而更少，例如 history 约 1.05 MB 对默认链 2.37 MB；不能把全部增长归因于该 Clone 路径。

短请求已有 504 次字段视图命中，仍构建 73 张表。Core 生成器给稀疏 Node 预留全部 17 个字段槽位，简单文本仅用 2 个。逃逸分析确认 json_output.go 的 `[64]string` 和 nativeCaptureState.forItem 的 child 进入堆。SSE 每请求构造操作数切片 3958 次，总容量 12121；子树成本不能全部当作切片成本。

十轮固定迭代微基准中位数：

| 形状 | ns/op | B/op | allocs/op |
|---|---:|---:|---:|
| 4 字段，独立片段 / 立即借用 | 228.75 / 198.45 | 400 / 336 | 6 / 2 |
| 16 字段，独立片段 / 立即借用 | 1255.50 / 1122.50 | 2392 / 2136 | 23 / 7 |
| 完整稀疏 Node / 仅 payload | 216.50 / 28.06 | 776 / 0 | 3 / 0 |
| 8/32/128 项编码 | 471.15 / 1730.50 / 5834.50 | 1312 / 4096 / 11008 | 2 / 3 / 4 |
| 引用改写 | 2862782 | 5074801 | 22620 |
| Native 接续 | 337743.5 | 248964 | 3149 |

这些形状提供的合同不同，不能作为已实现优化收益。引用和 Native 微基准计时排除夹具构造，独立 profile 包含编译；未与无法完成同样语义的旧版比较速度。

短请求 GC 后存活堆采样：R-release 约 4.71 MiB、R-build 3.58 MiB、A 6.07 MiB、C-default 6.11 MiB、C-package 53.20 MiB。包链的 compileOutputPlan/newCompiledExpression 两个 flat 位点约占 59.22%。另一个诊断只保留四包和十六策略：100631 个运行表达式、100631 个输出计划，浅层空间合计约 34.55 MiB，没有保留的 expressionCompileFacts。

不能据此删除全部编译图：运行计划、原始配置、组合声明和在途快照应分别审查；采样分配来源不能独立证明当前持有者。自然空闲一秒的工作集还包含空闲堆页，不能当泄漏，也不能把常驻图当每请求分配。独立 CPU 诊断中的 GC 路径，history 为默认链/包链 1.68/7.97 ms/请求，媒体为 2.19/10.47 ms；这些不是正式计时值。

## 下一轮优化计划

保持 DSL/Spec/Mapping、公开配置与 API、wire 合同和资源限额。以下均待实现，不承诺按采样占比取得等量收益，不新增 VM、供应商 Go 特例或全局缓存，不关闭 Native、引用、权限或交付检查。

每项独立提交，对直接父版本至少十轮微基准、五轮 HTTP；任务 1 首先对本报告 C。任何稳定回退都阻止采纳，5% 仅用于排序。保留失败证据，功能合同变化立即撤回。

| 顺序/提交边界 | 具体工作及证据 | 必须覆盖的回归与额外撤回条件 |
|---|---|---|
| 1. continuation 历史工作，server/continuation*.go | 无 carrier 且无可用持久恢复 session 时避免无结果的恢复遍历，保留输入接受边界；parent 延迟到真正需要时计算，固定原始历史和 scope。真实恢复复用同一不可变历史的节点摘要，消除重复前缀正文序列化。证据为 1120 次摘要和大正文新增分配。 | 空/合法/伪造 carrier、session、持久恢复、并行工具、role/ordinal/parent、嵌套消息、取消；8/32/64/128 条历史计数。凭据关联变化或新增长时保留即撤回。 |
| 2. 摘要私有编码，protocol/continuation.go | 复用现有结构输出及有界哈希写入，避免多份完整 JSON；保持摘要字节兼容，不改变公开 Value.Bytes 所有权。媒体的 Compact、HTML escape、MarshalJSON 和字符串分离是具体证据。 | 精确整数/指数、重复键、空/缺失、HTML/Unicode/非法 UTF-8、reasoning、嵌套工具媒体及被排除/保留字段；直接对照旧摘要。另测真正有凭据的路径，不能只测任务 1 已跳过的情况。 |
| 3. 字段视图，value_container/plan_object_reader | 评估小对象紧凑视图及只读取所需字段，不扩大根/子对象缓存。tools 额外约 512 KB，SSE 约 1.63 MB。 | 重复/转义键、动态指针、嵌套 item/binding；进入 Core/Native/状态/诊断/公共返回值继续独立拥有。小片段钉住大正文、缓存增长即撤回。 |
| 4. Core 投影，从 internal/coreplan 生成器修改 | 缩减稀疏结构预留槽位，在真正不可变的单次执行段复用投影，或让立即消费者遍历已有结构。short 额外约 50.8 KB，tools 约 268.9 KB。不复用已失败的广泛 selector 方案。 | 密集/稀疏 Node、Request/Response/Event、重复 struct 合并、来源证明、引用、规则写入失效、逻辑出现次数预算。密集对象或大正文稳定回退即撤回。 |
| 5. Native 子作用域，plan_native_capture/迭代/过滤 | 先证明需求标记覆盖递归、binding/source capture，再避免无消费者 child 的创建；局部位置按作用域传值。2855 次中 2595 次无需求，相关增量约 823 KB。 | Native→重建→Native、with_input/item/recur、仅 source capture、跨帧保存、取消/失败；不通过共享对象覆盖/恢复作用域。任何身份或所有权变化即撤回。 |
| 6. 工作区和操作数，json_output/plan_container | 针对真实逃逸评估局部工作区、准确容量、不可变片段直接消费。区分最终输出和临时列表，不能把 operand 子树全算收益。 | 8/32/128 项、小/大操作数、eager 求值、首个错误顺序、逐节点取消、准确字节预算、空容器、转义。短请求/大正文/Native 任一稳定回退即撤回。 |
| 7. 编译持有关系，先诊断后删减 | 区分原始配置、组合声明、运行图及旧快照，只释放已证明无用对象；不清空所有 model 声明，不删除运行表达式，不换 VM。现有 compile facts 已释放，不重复列作可回收目标。 | 来源—目标组合、导出/预览/指纹、失败编译、热更新及在途快照；冷启动与稳定编译分别测量，复测 HTTP GC/CPU，不改 GC 参数。持有关系证据不足时不进入删除阶段。 |

流状态、转换和交付证明中的剩余临时分配保留为待细分项。当前证据不足以认定这些检查或事务冗余，不能为了消除 other 类别删除它们。

## 验证、失败与复现

Go 全量 `go test ./... -count=1 -timeout=8m`、vet、staticcheck v0.8.2 通过；server 全量耗时 168.657 s，包含 16 方向本地 JSON/SSE。协议、三个 builtin Native/引用用例及六个 server 身份/预检/交付/Native 用例的 race 通过。9 项测量自测、161 项审计自测通过，无跳过。已有测试覆盖所有权、精确数值、Unicode、引用、状态预算、慢消费、取消、部分交付和重载；正确性通过不等于全部路径性能验收。

188 个 HTML 均存在；Chromium headless 抽查普通、差分及 Native 图的加载、搜索与点击，无页面脚本错误。本轮未进行完整 WebUI、Agent 或其他平台性能验收。

保留的准备失败包括只读预置写入拒绝、旧端点路径错误、初次归档构建继承外层 Git 身份；纠正后才正式测量。还保留首次字段借用形状修正、pprof Duration 解析适配，以及追加摘要探针时同名文件 overlay 冲突日志；改为保留目录结构后重建采集。失败未当作通过。

standalone 长任务启动后曾编辑 runner 源码。真正执行的是 `891a1a4e` 启动快照，SHA-256 `1cd4b64542b6617607b7731466a19a5205818c2b28aebc383b0002154fba740f`，保存在 standalone-runner-executed.py / standalone-launch.json。后来逐批读取的 runner 文件仅是工作区观察。新 runner 在模块加载时冻结源码字节。正式 driver 原件为 driver-stage1.test.exe，后续 driver 的差异和哈希在 builds.json 中。

本地提交：`891a1a4e` 固定身份/采集适配；`274570cc` 归因基准、工具、源码清单；本报告为第三笔文档提交，未推送。源码清单还补入前一轮遗漏的六个测试文件，未修改其内容。

保留本轮证据目录后，在独立检出中复现，或为各阶段指定新输出路径。prepare 不覆盖现有目录。保留固定 provider 的二进制、report.json 和源码；不能替换 provider 后仍沿用此身份。

```powershell
python scripts/performance-versions.py prepare
python scripts/performance-versions.py run --phase=standalone --label=standalone-measure
python scripts/performance-versions.py run --phase=counters --label=counters-measure
python scripts/performance-versions-audit.py
python scripts/performance-attribution.py
python scripts/performance-versions.py run --phase=attribution --versions=C-default,C-package --scenes=short,history,tools,stream,image --rounds=1 --requests=64 --label=attribution
python scripts/performance-versions.py run --phase=standalone --mode=cpu --rounds=1 --scenes=short,history,tools,stream,image
python scripts/performance-versions.py run --phase=standalone --mode=memory --rounds=1 --scenes=short,history,tools,stream,image
python scripts/performance-versions-micro.py
node scripts/performance-versions-compare.mjs
python scripts/performance-profile-summary.py
```

原始计数在 attribution，新增摘要计数在 attribution-digest。旧探针构建归档为 attribution-smoke-build / attribution-before-digest-build，最终构建在 attribution-build。不要使用这些 atomic 构建的耗时或总分配作比较。微基准/profile 次数、命令、cwd 和二进制哈希见 micro/manifest.json；各批 run.json 含配置、策略/修订、请求、进程身份和退出码。

现有数据库、日志、Swift、生产预置保持不变；未发布、未调用真实供应商。本轮 Windows 结果不扩展为跨平台验收。
