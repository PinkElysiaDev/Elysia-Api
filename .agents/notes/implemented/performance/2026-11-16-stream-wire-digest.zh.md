# Agent Note: 共享流式线格式摘要

Status: implemented

## Problem

带序号的流式事件反复编码语义 Event、解码成通用规范表示、再次编码，然后计算摘要。源端记账和目标端投影分别重复这套工作。HTTP 成帧还会复制并重新压缩已经编码的 JSON，并逐帧解析相同的事件名称指针。

## Decision

`backend/protocol/event_frame.go` 在解码帧包含序号时，直接对已有线格式 Value 计算一次 SHA-256。该帧通过 `EventFrame.ConsumeEvent` 在源端与目标端回放之间共享摘要；`SequenceTracker` 接受预计算摘要，不再重新编码 Event。复合帧成员把帧摘要与解码顺序绑定，避免复用序号时误吞另一个成员。源端不会再次投影整帧均已去重的帧。

没有可用帧的事件，包括经过缓冲或合成的目标事件，以及一个源事件展开为多个目标事件的情况，仍然使用规范化语义去重。序号校验、有界历史、资源记账和原生回放授权均保持完整。不修改 Spec/Mapping 包，不添加提供商专用转换分支，不修改断言、样本或限制。

`backend/protocol/transport_http.go` 为每条网关输出流创建一次帧写入器，仅解析一次事件名称指针。SSE 直接把 `Value.JSONString()` 交给已有 eventstream 写入器；该写入器能安全地对多行 JSON 成帧。NDJSON 使用 `io.WriteString` 替代 fmt，但保留压缩：`ParseValue` 和原生回放会保留空白，而现有 `TestHTTPFramingPreservesFragmentsAndBounds` 明确要求把读取到的多行帧写成单行 NDJSON。紧凑输出不变量适用于编码器产生的帧，而非所有可接受的 Value。删除 NDJSON 压缩会违反现有断言；不添加生产环境紧凑性回退路径。

`digestSemanticEvents` 仍作为语义修改证明。通用 Core2 解码器直接构造 Event，没有可复用的、表示最终已标注来源的语义状态的编码字节。对源线格式字节求摘要无法检测随后对公开 `EventFrame.Events` 的修改；现有原生回放修改测试要求保留该检查。因此，它不能安全地复用线格式去重摘要。

## Measurements

机器：Apple device，macOS/darwin arm64，Go 1.27.3。每项基准各测五次；表格分别取 ns/op、B/op 和 allocs/op 的中位数。这些基准使用默认的逻辑处理器数量，运行时间早于后续限制测试内存的要求。

编辑前与编辑后，均从工作树根目录执行以下命令：

```sh
(cd backend && go test ./server -run '^$' -bench 'BenchmarkPackageStreamBudget/mapped/(128|2048)$' -benchmem -count=5)
(cd backend && go test ./protocol/builtin -run '^$' -bench 'BenchmarkStreamTextDecode' -benchmem -count=5)
```

| 基准 | 修改前 ns/op | 修改后 ns/op | 修改前 B/op | 修改后 B/op | 修改前 allocs/op | 修改后 allocs/op |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| PackageStreamBudget/mapped/128 | 2,652,198 | 1,821,049 | 3,197,791 | 2,289,740 | 29,246 | 15,542 |
| PackageStreamBudget/mapped/2048 | 41,079,982 | 24,189,141 | 48,662,211 | 34,700,112 | 451,714 | 238,324 |
| StreamTextDecode | 7,017,851 | 7,608,759 | 5,585,654 | 5,538,692 | 79,349 | 79,349 |

mapped/128 耗时下降 31.3%，分配次数下降 46.9%；mapped/2048 耗时下降 41.1%，分配次数下降 47.2%。仅解码基准不经过带序号的回放，分配次数不变；其 8.4% 的耗时增长不作为优化收益。同机存在其他并发工作，耗时有噪声。表格中的修改后数据来自本任务自己的验证进程退出后的复测；较早与验证重叠的一轮不用于比较。原始日志位于被忽略的 `.tmp-dev/stream-before.txt`、`decode-before.txt`、`stream-after-repeat.txt` 和 `decode-after-repeat.txt`。

验证状态：**pending: test-suite memory fix**。停止后续运行之前已取得的结果：

- `(cd backend && go vet ./protocol/... ./server/...)`：通过。
- `(cd backend && go test ./protocol/... -count=1)`：通过；protocol 24.072s，builtin 47.880s。
- `(cd backend && go test ./server -count=1 -run 'Stream|Replay|Sequence|Frame|SSE|Gemini')`：最后一次未限内存的运行在 407.664s 后以 `signal: killed` 失败。较早的一轮定向运行曾在 185.353s 内通过，但不能替代最终验证。
- `npm run test:protocol -- --suite=gemini-stream`：通过，退出码 0；运行器只使用 Node 内置模块，本工作树无需执行 `npm ci`。
- 线格式摘要、规范化回退、事件名称和编码器紧凑性的定向回归在最后加入缺失帧与下标小型保护之前通过。不声称已经对这些最终保护完整复测。

收到内存限制要求后，以 `GOMEMLIMIT=1536MiB GOMAXPROCS=2 GOFLAGS=-p=1 go test ./server -count=1 -parallel=2 -run 'Stream|Replay|Sequence|Frame|SSE|Gemini'` 串行包方式重试，并用进程树 RSS 看门狗在超过 1900 MiB 时终止。`GOMEMLIMIT` 是运行时软目标，不是 RSS 硬上限。协调者要求本轮不再执行测试或基准后，明确停止这次重试及其看门狗。最终的有界内存验证仍待完成；不持久修改执行框架或测试套件设置。

## Alternatives considered

**规范化一次后共享。** 用户否决此替代方案。它能保留不同线格式拼写之间的语义相等性，但每个带序号的帧仍须解码并分配完整的通用语义树。对已解码帧使用线格式相等性可以完全避开这部分工作。

**字段指纹。** 用户否决此替代方案。维护另一套 Event 字段遍历容易遗漏新增字段、原生扩展、缺失/null/零值差异或关联关系。现有不可变线格式 Value 已包含完整帧身份，无需第二套转换模型。

**用线格式摘要替换原生语义摘要。** 否决，因为源字节未改变不能证明公开且可变的语义事件仍然未改变。保留语义修改证明才能维持现有原生回放安全契约。

**直接写出所有 NDJSON Value。** 否决，因为解析和原生回放的 Value 可能包含物理换行。现有从读取帧写入 NDJSON 的回归要求输出只有一条物理行，因此保留当前压缩，不削弱断言，也不添加紧凑性检测回退路径。

## Consequences

已解码的带序号回放现在使用线格式相等性：完全相同的字节会去重；复用序号时，即使映射后的语义看似相同，只要空白、键顺序、数字拼写或未映射扩展发生变化，就会冲突。测试覆盖字节身份、与投影语义独立、空白/扩展变化和复合成员冲突。没有线格式的回放保持规范化相等性和大整数精度。

映射热路径移除反复的语义规范化，回放相关分配量约减半，且去重历史不保留帧载荷。新增帧摘要元数据和按操作保存的写入器状态使代码量增加。NDJSON 压缩与独立的原生语义修改摘要有意保持未优化。完整最终验证待测试套件内存修复后执行；不创建提交。
